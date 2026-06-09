package main

// browse.go implements the "githome browse <path>" subcommand: a zero-config
// read-only code browser for a single local git repository. It spins up an
// in-process web front backed by a temporary SQLite database seeded with one
// synthetic user and one synthetic repository, then registers a git.Store path
// override so the real repository on disk is served at that slot.
//
// No auth, issues, pulls, reviews, or settings routes are mounted — only the
// code-browsing surface from fe/web/repo. The home redirects straight to the
// repository root so the browser lands on code immediately.

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

func runBrowse(args []string) error {
	fs := flag.NewFlagSet("browse", flag.ContinueOnError)
	port := fs.String("port", "3000", "HTTP listen port")
	siteName := fs.String("name", "", "site name in the header (defaults to repo basename)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: githome browse [--port PORT] <path>")
	}

	repoPath, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}
	repoName := filepath.Base(repoPath)
	if *siteName == "" {
		*siteName = repoName
	}

	// Ephemeral secrets — fine for a local, single-user browser.
	sessionKey := make([]byte, 32)
	tokenPepper := make([]byte, 16)
	if _, err := rand.Read(sessionKey); err != nil {
		return err
	}
	if _, err := rand.Read(tokenPepper); err != nil {
		return err
	}
	_ = tokenPepper // only session needed in read-only browse mode

	listenAddr := ":" + *port
	baseURL := "http://localhost:" + *port

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	// Temporary SQLite database for the synthetic owner+repo metadata.
	dbFile := filepath.Join(os.TempDir(), fmt.Sprintf("githome-browse-%d.db", time.Now().UnixNano()))
	st, err := store.Open(ctx, "sqlite://"+dbFile, 1)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		_ = st.Close()
		_ = os.Remove(dbFile)
	}()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Seed the synthetic owner.
	owner := &store.UserRow{Login: "local", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		return fmt.Errorf("seed user: %w", err)
	}

	// Detect the default branch from the actual git repository.
	defaultBranch := browseDetectBranch(repoPath)

	// Seed the synthetic repository (public so anonymous viewers can see it).
	repo := &store.RepoRow{
		OwnerPK:       owner.PK,
		Name:          repoName,
		DefaultBranch: defaultBranch,
		Private:       false,
	}
	if err := st.InsertRepo(ctx, repo); err != nil {
		return fmt.Errorf("seed repo: %w", err)
	}

	// Git store with a path override so pk → repoPath bypasses the managed tree.
	gitStore := git.NewStore(os.TempDir())
	gitStore.RegisterPath(repo.PK, repoPath)

	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	urls := presenter.NewURLBuilder(config.URLs{
		HTML:    parsedBase,
		API:     mustParseURL(baseURL + "/api/v3"),
		GraphQL: mustParseURL(baseURL + "/api/graphql"),
		SSHHost: "localhost",
		SSHPort: 22,
	})

	logger := slog.Default()

	renderSet, err := render.New(assets.FS(), assets.Dev())
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	markupRenderer := markup.New(markup.Config{
		BaseURL:           baseURL,
		MaxHighlightBytes: 5 << 20,
		Logger:            logger,
	})

	sessions := webmw.NewSessions(sessionKey, 0, func(_ context.Context, _ int64) (*view.Viewer, error) {
		return nil, nil // no real users in browse mode
	})

	repoSvc := domain.NewRepoService(st, gitStore)
	userSvc := domain.NewUserService(st)

	root := mizu.NewRouter()

	// In browse mode the home page redirects straight to the repo rather than
	// showing the "Sign in" landing page a real instance would show.
	homeRedirect := func(c *mizu.Ctx) error {
		return c.Redirect(http.StatusFound, "/local/"+repoName)
	}

	handler := fe.Mount(root, fe.Deps{
		Render:      renderSet,
		View:        view.NewBuilder(*siteName).WithHideAuth(),
		Repos:       repoSvc,
		Users:       userSvc,
		URLs:        urls,
		Markup:      markupRenderer,
		Sessions:    sessions,
		CSRF:        webmw.NewCSRF(renderSet),
		Flash:       webmw.NewFlash(sessionKey),
		Logger:      logger,
		HomeHandler: homeRedirect,
	})

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	repoURL := "/local/" + repoName
	slog.Info("browse mode: serving repository", "path", repoPath, "url", baseURL+repoURL)
	slog.Info("http listening", "addr", listenAddr)

	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errc:
		stop()
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// browseDetectBranch opens the git repository at path and returns its HEAD
// branch name, falling back to "main" when detection fails.
func browseDetectBranch(repoPath string) string {
	r, err := gogit.PlainOpenWithOptions(repoPath, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "main"
	}
	head, err := r.Head()
	if err != nil {
		return "main"
	}
	name := head.Name().Short()
	if name == "" {
		return "main"
	}
	return name
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
