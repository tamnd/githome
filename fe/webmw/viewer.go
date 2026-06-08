package webmw

import "context"

// The session middleware resolves the viewer's display model for the shell, but
// the code-browsing handlers also need the viewer's primary key to read-gate a
// repository (domain.RepoService.GetRepo takes the viewer pk, reporting a private
// repo the viewer cannot see as not found). This file carries that pk on the
// request context alongside the display model, set by the same session
// middleware. An anonymous request, or one whose session names a user the store
// no longer has, leaves the pk at zero, which GetRepo treats as anonymous. See
// implementation/06 and implementation/07 section 2.

type viewerCtxKey int

const keyViewerID viewerCtxKey = iota

// withViewerID stores the resolved viewer's primary key on the context.
func withViewerID(ctx context.Context, pk int64) context.Context {
	return context.WithValue(ctx, keyViewerID, pk)
}

// ViewerID returns the signed-in viewer's primary key, or zero when the request
// is anonymous. The repo handlers pass it straight to the domain read gate.
func ViewerID(ctx context.Context) int64 {
	pk, _ := ctx.Value(keyViewerID).(int64)
	return pk
}
