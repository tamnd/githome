package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// zeroOID is the all-zero object id git uses to mean "no such object" in a
// compare-and-swap: passing it as the old value of update-ref asserts the ref
// must not currently exist, which is how CreateRef stays create-only.
const zeroOID = "0000000000000000000000000000000000000000"

// runResult carries the outcome of a git subprocess: its captured streams and
// exit code. A clean exit with a nonzero code (merge-base reporting "not an
// ancestor", cat-file reporting "absent") is a normal result, not an error, so
// callers branch on Code; err is non-nil only when the process fails to start
// or the context is canceled.
type runResult struct {
	stdout []byte
	stderr string
	code   int
}

func (s *Store) bin() string {
	if s.gitBin == "" {
		return "git"
	}
	return s.gitBin
}

// pathEnv returns a PATH for the git subprocess so git can find its own helper
// programs. It inherits the daemon's PATH (git is often outside /usr/bin, for
// example a Homebrew install), falling back to a sane default when PATH is unset.
func pathEnv() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/bin:/bin"
}

// run executes a git command against the bare repository for pk with a scrubbed
// environment: no inherited GIT_* configuration, no user or system config, and
// no prompting. It is the single exec entry point for the write path.
func (s *Store) run(ctx context.Context, pk int64, stdin io.Reader, args ...string) (runResult, error) {
	full := append([]string{"--git-dir", s.Dir(pk)}, args...)
	cmd := exec.CommandContext(ctx, s.bin(), full...)
	cmd.Env = []string{
		"PATH=" + pathEnv(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	}
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := runResult{stdout: out.Bytes(), stderr: strings.TrimSpace(errb.String())}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		res.code = ee.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// fail turns a nonzero git exit into an error that carries the subcommand and
// the captured stderr, which is what an operator needs to diagnose a refused
// ref update.
func fail(args []string, r runResult) error {
	return fmt.Errorf("git %s: exit %d: %s", strings.Join(args, " "), r.code, r.stderr)
}

// RefSHA returns the object id the fully qualified reference points at, or
// ErrRefNotFound when it does not exist.
func (s *Store) RefSHA(ctx context.Context, pk int64, ref string) (SHA, error) {
	args := []string{"show-ref", "--verify", "--hash", ref}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return "", err
	}
	if r.code != 0 {
		return "", ErrRefNotFound
	}
	return strings.TrimSpace(string(r.stdout)), nil
}

// RefSnapshot returns every reference and its current object id, keyed by the
// fully qualified ref name. It backs the post-receive sink, which diffs the
// snapshot taken before a push against the one taken after to recover the
// ref-update batch. An empty repository yields an empty map.
func (s *Store) RefSnapshot(ctx context.Context, pk int64) (map[string]SHA, error) {
	args := []string{"show-ref"}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	// show-ref exits 1 with no output when the repository has no refs yet.
	if r.code == 1 && len(bytes.TrimSpace(r.stdout)) == 0 {
		return map[string]SHA{}, nil
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	out := map[string]SHA{}
	for line := range strings.SplitSeq(strings.TrimRight(string(r.stdout), "\n"), "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		out[ref] = sha
	}
	return out, nil
}

// ObjectExists reports whether the object id is present in the repository.
func (s *Store) ObjectExists(ctx context.Context, pk int64, sha SHA) (bool, error) {
	args := []string{"cat-file", "-e", sha}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return false, err
	}
	return r.code == 0, nil
}

// ObjectType returns the git type of the object ("commit", "tag", "tree",
// "blob"), or ErrObjectNotFound when it is absent. A reference's wire model
// reports the type of the object it points at.
func (s *Store) ObjectType(ctx context.Context, pk int64, sha SHA) (string, error) {
	args := []string{"cat-file", "-t", sha}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return "", err
	}
	if r.code != 0 {
		return "", ErrObjectNotFound
	}
	return strings.TrimSpace(string(r.stdout)), nil
}

// IsAncestor reports whether ancestor is reachable from descendant, the
// fast-forward test git itself uses (merge-base --is-ancestor: exit 0 yes,
// exit 1 no, anything else a real error).
func (s *Store) IsAncestor(ctx context.Context, pk int64, ancestor, descendant SHA) (bool, error) {
	args := []string{"merge-base", "--is-ancestor", ancestor, descendant}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return false, err
	}
	switch r.code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fail(args, r)
	}
}

// CreateRef creates ref pointing at sha, failing with ErrRefExists when the
// reference already exists and ErrObjectNotFound when sha is not in the
// repository. The all-zero old value makes the update-ref create-only.
func (s *Store) CreateRef(ctx context.Context, pk int64, ref string, sha SHA) error {
	switch ok, err := s.ObjectExists(ctx, pk, sha); {
	case err != nil:
		return err
	case !ok:
		return ErrObjectNotFound
	}
	if _, err := s.RefSHA(ctx, pk, ref); err == nil {
		return ErrRefExists
	} else if !errors.Is(err, ErrRefNotFound) {
		return err
	}
	args := []string{"update-ref", ref, sha, zeroOID}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fail(args, r)
	}
	return nil
}

// UpdateRef moves an existing ref to sha. Unless force is set, the move must be
// a fast-forward: sha must be a descendant of the current target, otherwise
// ErrNotFastForward is returned and the ref is left untouched. ErrRefNotFound is
// returned when the ref does not exist and ErrObjectNotFound when sha is absent.
func (s *Store) UpdateRef(ctx context.Context, pk int64, ref string, sha SHA, force bool) error {
	switch ok, err := s.ObjectExists(ctx, pk, sha); {
	case err != nil:
		return err
	case !ok:
		return ErrObjectNotFound
	}
	old, err := s.RefSHA(ctx, pk, ref)
	if err != nil {
		return err
	}
	if !force && old != sha {
		switch ff, err := s.IsAncestor(ctx, pk, old, sha); {
		case err != nil:
			return err
		case !ff:
			return ErrNotFastForward
		}
	}
	// Compare-and-swap against the observed old value so a concurrent update
	// loses rather than silently clobbering.
	args := []string{"update-ref", ref, sha, old}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fail(args, r)
	}
	return nil
}

// DeleteRef removes ref. ErrRefNotFound is returned when it does not exist.
func (s *Store) DeleteRef(ctx context.Context, pk int64, ref string) error {
	old, err := s.RefSHA(ctx, pk, ref)
	if err != nil {
		return err
	}
	args := []string{"update-ref", "-d", ref, old}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fail(args, r)
	}
	return nil
}
