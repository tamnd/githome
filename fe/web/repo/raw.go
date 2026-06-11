package repo

import (
	"errors"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
)

// Raw serves the raw bytes of a file: GET /{owner}/{repo}/raw/{rest}. It is the
// target of every "Raw" and "Download" link and of the README "view source"
// path. Because Githome serves the web UI and raw content from the same origin
// (github.com isolates raw on a separate domain), raw is deliberately defensive:
// it never serves a blob as text/html, and it sets X-Content-Type-Options:
// nosniff so a browser cannot be tricked into running an HTML or script payload
// stored as a file. Text is served as text/plain, a known-safe image as its
// image type, and anything else as an octet-stream download. See implementation/07
// section 5 and implementation/02 section 1.6.
func (h *Handlers) Raw(c *mizu.Ctx) error {
	repo, ok := repoFromContext(c.Context())
	if !ok {
		return h.notFound(c)
	}
	ref, path, ok := h.resolveRef(repo, h.loadRefs(repo), c.Param("rest"))
	if !ok {
		return h.notFound(c)
	}

	res, err := h.repos.Contents(repo, path, ref)
	switch {
	case errors.Is(err, domain.ErrBlobTooLarge):
		// The raw view is exactly how a viewer fetches a file too large to render
		// inline, so refusing it here would strand them. The blob ceiling is the
		// domain's; when Contents declines it, there is no smaller path, so report
		// it as not found rather than inventing a partial read.
		return h.notFound(c)
	case errors.Is(err, domain.ErrGitNotFound), errors.Is(err, domain.ErrEmptyRepo):
		return h.notFound(c)
	case err != nil:
		return err
	}
	if res.IsDir || res.File == nil {
		return h.notFound(c)
	}

	h.setRawHeaders(c)
	return c.Bytes(rawStatusOK, res.File.Content, rawContentType(path, res.File.Content))
}

// rawStatusOK is the success status for a raw read, named so the Bytes call reads
// clearly without importing net/http for one constant.
const rawStatusOK = 200

// setRawHeaders applies the defensive headers every raw response carries: no
// content-type sniffing, and a cache policy that lets a client revalidate. The
// nosniff header is the load-bearing one: it stops a browser from treating a
// text/plain HTML payload as a document.
func (h *Handlers) setRawHeaders(c *mizu.Ctx) {
	hdr := c.Header()
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "no-transform")
}

// rawContentType picks the response content type for a raw blob: a known-safe
// image keeps its image type so it renders, text is text/plain so it is shown not
// run, and anything else is an octet-stream so the browser offers a download
// rather than guessing. The charset on text is utf-8 because the blob view only
// treats valid UTF-8 as text in the first place.
func rawContentType(path string, content []byte) string {
	switch ext(path) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "ico":
		return "image/x-icon"
	case "bmp":
		return "image/bmp"
	case "svg":
		// An SVG is XML that a browser will execute scripts inside, so raw SVG is
		// served as plain text, never image/svg+xml.
		return "text/plain; charset=utf-8"
	case "pdf":
		return "application/pdf"
	}
	if isBinary(content) {
		return "application/octet-stream"
	}
	return "text/plain; charset=utf-8"
}
