package repo

import (
	"compress/gzip"
	"errors"
	"fmt"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
)

// Archive serves the source-archive downloads: GET
// /{owner}/{repo}/archive/{ref}.zip and .tar.gz, including the qualified
// /archive/refs/heads/{branch}.zip and /archive/refs/tags/{tag}.zip forms
// github.com links (spec 02 section 1.6). The greedy tail carries the whole
// ref plus the format suffix, so a branch with slashes archives like any
// other. The ref resolves before the first byte streams, so a bad ref is a
// clean soft 404 instead of a broken download; after that the bytes go
// straight from one git archive subprocess to the response.
func (h *Handlers) Archive(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	ref, format, ok := splitArchivePath(c.Param("rest"))
	if !ok {
		return h.notFound(c)
	}

	// The leading directory every entry sits under, the same shape the REST
	// zipball/tarball endpoints record: owner-repo-ref with the ref's slashes
	// flattened so the directory name stays a single segment.
	prefix := ownerLogin(repo) + "-" + repo.Name + "-" + archiveRefLabel(ref)

	w := c.Writer()
	if format == "zip" {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, prefix))
		err := h.repos.Archive(ctx, repo, ref, "zip", prefix, w)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			return h.notFound(c)
		}
		return err
	}

	w.Header().Set("Content-Type", "application/x-gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, prefix))
	// git archive emits the tar; the gzip layer stays here so the exec seam
	// never depends on a gzip binary, mirroring the REST tarball handler.
	gzw := gzip.NewWriter(w)
	err := h.repos.Archive(ctx, repo, ref, "tar", prefix, gzw)
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Disposition")
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	return gzw.Close()
}

// splitArchivePath reads the archive tail into the ref and the format. The
// format is named by the suffix: .zip or .tar.gz, nothing else. The remainder
// is the ref, which may be bare (a branch, tag, or sha) or qualified with
// refs/heads/ or refs/tags/; resolution happens in the domain read. An empty
// ref or an unknown suffix reports not-ok, the soft 404.
func splitArchivePath(rest string) (ref, format string, ok bool) {
	switch {
	case strings.HasSuffix(rest, ".zip"):
		ref, format = strings.TrimSuffix(rest, ".zip"), "zip"
	case strings.HasSuffix(rest, ".tar.gz"):
		ref, format = strings.TrimSuffix(rest, ".tar.gz"), "tar.gz"
	default:
		return "", "", false
	}
	if ref == "" {
		return "", "", false
	}
	return ref, format, true
}

// archiveRefLabel flattens a ref into the archive's directory label: the
// qualified prefix drops, a full sha abbreviates, and any remaining slashes
// become dashes so the label is one path segment.
func archiveRefLabel(ref string) string {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")
	if len(ref) == 40 && isHex(ref) {
		ref = ref[:7]
	}
	return strings.ReplaceAll(ref, "/", "-")
}

// isHex reports whether s is entirely lowercase hex digits.
func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
