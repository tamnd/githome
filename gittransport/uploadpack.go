package gittransport

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
)

// Content types from the Git Smart HTTP protocol.
const (
	ctUploadAdvert  = "application/x-git-upload-pack-advertisement"
	ctUploadResult  = "application/x-git-upload-pack-result"
	ctUploadRequest = "application/x-git-upload-pack-request"
)

// defaultMaxBody caps an upload-pack POST body when the Service leaves
// MaxBodyByte zero. The negotiation a clone sends is small; this guards against
// a client streaming an unbounded request at the subprocess.
const defaultMaxBody = 50 << 20

// Service serves git clone and fetch over Smart HTTP. It resolves and authorizes
// the repository through the domain repo service, maps it to its bare path
// through the git store, and proxies the negotiation to the system git binary.
type Service struct {
	// GitBin is the git binary to exec; empty means "git" on PATH.
	GitBin string
	// Repos authorizes the read and yields the repository's internal pk.
	Repos *domain.RepoService
	// Git maps a repository pk to its on-disk bare path.
	Git *git.Store
	// MaxBodyByte caps the request body; zero uses defaultMaxBody.
	MaxBodyByte int64
	Log         *slog.Logger
}

// Mount registers the Smart HTTP routes. Both the bare-style owner/repo.git path
// and the suffix-less owner/repo path resolve, because git clients and
// github.com tolerate both; handlers strip the optional .git. info/refs serves
// both the upload-pack (read) and receive-pack (write) advertisements; the POST
// routes carry the negotiation and pack for each.
func Mount(root *mizu.Router, s *Service) {
	root.Get("/{owner}/{repo}/info/refs", s.handleInfoRefs)
	root.Post("/{owner}/{repo}/git-upload-pack", s.handleUploadPack)
	root.Post("/{owner}/{repo}/git-receive-pack", s.handleReceivePack)
}

// handleInfoRefs answers the reference-discovery GET. It dispatches on the
// service parameter: git-upload-pack advertises for clone and fetch (read
// access), git-receive-pack advertises for push (write access). A dumb-protocol
// probe (no service=) is refused so the loose-object layout never leaks.
func (s *Service) handleInfoRefs(c *mizu.Ctx) error {
	w := c.Writer()
	switch c.Request().URL.Query().Get("service") {
	case "git-upload-pack":
		return s.advertiseUpload(c)
	case "git-receive-pack":
		return s.advertiseReceive(c)
	default:
		http.Error(w, "service not supported", http.StatusForbidden)
		return nil
	}
}

// advertiseUpload streams the upload-pack ref advertisement after authorizing
// read access. It writes the service-advertisement preamble, then the refs the
// git binary produces.
func (s *Service) advertiseUpload(c *mizu.Ctx) error {
	w, r := c.Writer(), c.Request()
	bare, ok := s.resolveRead(c)
	if !ok {
		return nil
	}

	w.Header().Set("Content-Type", ctUploadAdvert)
	noCache(w.Header())
	w.WriteHeader(http.StatusOK)

	// Preamble: PKT-LINE("# service=git-upload-pack\n") + flush-pkt, the worked
	// example from the http-protocol spec.
	if err := writePktString(w, "# service=git-upload-pack\n"); err != nil {
		return nil
	}
	if err := writeFlush(w); err != nil {
		return nil
	}

	cmd := s.gitCommand(r.Context(), r, "upload-pack", "--stateless-rpc", "--advertise-refs", bare)
	cmd.Stdout = w
	if err := cmd.Run(); err != nil && s.Log != nil {
		// Headers are already sent, so we cannot change the status; the client
		// surfaces a disconnect, which is the correct failure mode.
		s.Log.Error("advertise-refs failed", "err", err)
	}
	return nil
}

// handleUploadPack answers the negotiation-plus-pack POST. It pipes the request
// body (gunzipped when the client compressed it) to the subprocess stdin and
// streams the packfile back, flushing each chunk so side-band progress reaches
// the client live.
func (s *Service) handleUploadPack(c *mizu.Ctx) error {
	w, r := c.Writer(), c.Request()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, ctUploadRequest) {
		http.Error(w, "bad content type", http.StatusUnsupportedMediaType)
		return nil
	}

	bare, ok := s.resolveRead(c)
	if !ok {
		return nil
	}

	body, err := requestBody(r, s.maxBody())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", ctUploadResult)
	noCache(w.Header())
	w.WriteHeader(http.StatusOK)

	cmd := s.gitCommand(r.Context(), r, "upload-pack", "--stateless-rpc", bare)
	cmd.Stdin = body
	cmd.Stdout = &flushWriter{w: w}
	if err := cmd.Run(); err != nil && s.Log != nil {
		s.Log.Error("upload-pack failed", "err", err)
	}
	return nil
}

// resolveRead authorizes the request actor's read access and returns the
// repository's bare path. On failure it writes the status (404 for a missing or
// invisible repository, so a private repo's existence never leaks) and reports
// ok=false.
func (s *Service) resolveRead(c *mizu.Ctx) (bare string, ok bool) {
	ctx := c.Request().Context()
	actor := auth.ActorFrom(ctx)
	owner := c.Param("owner")
	repo := strings.TrimSuffix(c.Param("repo"), ".git")

	row, err := s.Repos.GetRepo(ctx, actor.UserID, owner, repo)
	if errors.Is(err, domain.ErrRepoNotFound) {
		http.Error(c.Writer(), "Not Found", http.StatusNotFound)
		return "", false
	}
	if err != nil {
		http.Error(c.Writer(), "Server Error", http.StatusInternalServerError)
		return "", false
	}
	return s.Git.Dir(row.PK), true
}

// gitCommand builds the git subprocess. It runs with a scrubbed environment per
// spec doc 04 section 8.3: no inherited GIT_* configuration, no user or system
// config, and no prompting, so a stray GIT_DIR or credential helper in the
// daemon's environment can never reach the subprocess. PATH is inherited (git is
// often outside /usr/bin, e.g. a Homebrew install) so git can find its helper
// programs. The client's Git-Protocol header is forwarded via GIT_PROTOCOL so
// protocol v2 negotiation works.
func (s *Service) gitCommand(ctx context.Context, r *http.Request, args ...string) *exec.Cmd {
	bin := s.GitBin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{
		"PATH=" + pathEnv(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	}
	if proto := r.Header.Get("Git-Protocol"); proto != "" {
		cmd.Env = append(cmd.Env, "GIT_PROTOCOL="+proto)
	}
	return cmd
}

// pathEnv returns a PATH for the git subprocess, inheriting the daemon's PATH
// and falling back to a sane default when it is unset.
func pathEnv() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/bin:/bin"
}

func (s *Service) maxBody() int64 {
	if s.MaxBodyByte > 0 {
		return s.MaxBodyByte
	}
	return defaultMaxBody
}

// requestBody returns the request body, transparently gunzipped when the client
// sent Content-Encoding: gzip, capped at max bytes.
func requestBody(r *http.Request, limit int64) (io.ReadCloser, error) {
	rc := r.Body
	if limit > 0 {
		rc = http.MaxBytesReader(nil, rc, limit)
	}
	switch strings.ToLower(r.Header.Get("Content-Encoding")) {
	case "gzip", "x-gzip":
		gz, err := gzip.NewReader(rc)
		if err != nil {
			return nil, err
		}
		return readCloser{Reader: gz, Closer: rc}, nil
	case "", "identity":
		return rc, nil
	default:
		return nil, errors.New("gittransport: unsupported Content-Encoding")
	}
}

// noCache sets the headers git http-backend emits so intermediaries never cache
// a Smart HTTP response.
func noCache(h http.Header) {
	h.Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
}

// flushWriter flushes the underlying ResponseWriter after each write so the
// packfile and side-band progress stream to the client incrementally.
type flushWriter struct{ w http.ResponseWriter }

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

// readCloser pairs a decoded reader with the underlying closer.
type readCloser struct {
	io.Reader
	io.Closer
}
