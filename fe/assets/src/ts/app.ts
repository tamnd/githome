// app.ts is the JS bundle entry for the Githome web front. The F0 skeleton ships
// only the progressive-enhancement seam: nothing here is required to render or
// navigate a page, and every interactive feature added in later milestones is an
// enhancement of working server-rendered HTML. See implementation/05.

// markEnhanced flips a document-level flag once scripts run, so CSS can choose to
// reveal enhancement-only affordances. With JS off the flag never sets and the
// no-JS markup stays in effect, which the behavior oracle relies on.
function markEnhanced(): void {
  document.documentElement.dataset.jsEnhanced = "true";
}

// localizeTimes upgrades any <relative-time> the server emitted with a stable ISO
// value into a friendlier client-localized label, leaving the server's text as
// the no-JS fallback. The custom element is registered in later milestones; here
// we only confirm the bundle loads and the seam is wired.
function boot(): void {
  markEnhanced();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", boot, { once: true });
} else {
  boot();
}
