package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// archiveTimeout bounds one git archive subprocess. Archives of real
// repositories stream well under this; a walk that cannot finish in the
// window is one the server refuses to pin a request on.
const archiveTimeout = 5 * time.Minute

// ArchiveStream writes an archive of the tree at sha to w as one git archive
// subprocess, streaming blob by blob instead of materializing the repository
// in memory. format is anything git archive accepts ("zip", "tar"); prefix is
// the leading directory recorded for every entry. The caller resolves sha
// before calling, so by the time git runs the only failures left are
// infrastructure ones.
func (s *Store) ArchiveStream(ctx context.Context, pk int64, format, prefix string, sha SHA, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	args := []string{"archive", "--format=" + format, "--prefix=" + prefix + "/", "--end-of-options", sha}
	full := append([]string{"--git-dir", s.runDir(pk)}, args...)
	cmd := exec.CommandContext(ctx, s.bin(), full...)
	cmd.Env = baseEnv()
	cmd.Stdout = w
	var errb bytes.Buffer
	cmd.Stderr = &errb
	err := cmd.Run()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return fail(args, runResult{code: ee.ExitCode(), stderr: strings.TrimSpace(errb.String())})
	}
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
