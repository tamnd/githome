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

// wireCopyButtons turns any [data-copy-target] control into a clipboard copy of
// the referenced input's value. The button enhances a readonly input the viewer
// can already select and copy by hand, so with JS off nothing is lost. The
// selector points at an input in the same disclosure; we copy its value and flash
// a short confirmation on the button without disturbing the markup.
function wireCopyButtons(): void {
  for (const btn of Array.from(document.querySelectorAll<HTMLElement>("[data-copy-target]"))) {
    btn.addEventListener("click", () => {
      const sel = btn.dataset.copyTarget;
      if (!sel) {
        return;
      }
      const field = document.querySelector<HTMLInputElement>(sel);
      if (!field) {
        return;
      }
      void navigator.clipboard?.writeText(field.value).then(() => {
        const label = btn.getAttribute("aria-label");
        btn.setAttribute("aria-label", "Copied");
        window.setTimeout(() => {
          if (label === null) {
            btn.removeAttribute("aria-label");
          } else {
            btn.setAttribute("aria-label", label);
          }
        }, 1200);
      });
    });
  }
}

// wireFileFilter narrows a list as the viewer types into a [data-filter-target]
// search box. The whole list is server-rendered and fully usable with JS off;
// this only hides rows whose data-filter-text does not contain the query, so the
// file finder degrades to a plain scrollable list. The match is a case-folded
// substring, which is enough for the F1 finder; fuzzy ranking arrives later.
function wireFileFilter(): void {
  for (const input of Array.from(document.querySelectorAll<HTMLInputElement>("[data-filter-target]"))) {
    const sel = input.dataset.filterTarget;
    if (!sel) {
      continue;
    }
    const rows = Array.from(document.querySelectorAll<HTMLElement>(sel));
    input.addEventListener("input", () => {
      const q = input.value.trim().toLowerCase();
      for (const row of rows) {
        const text = (row.dataset.filterText ?? "").toLowerCase();
        row.classList.toggle("is-hidden", q !== "" && !text.includes(q));
      }
    });
  }
}

// localizeTimes upgrades any <relative-time> the server emitted with a stable ISO
// value into a friendlier client-localized label, leaving the server's text as
// the no-JS fallback. The custom element is registered in later milestones; here
// we only confirm the bundle loads and the seam is wired.
function boot(): void {
  markEnhanced();
  wireCopyButtons();
  wireFileFilter();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", boot, { once: true });
} else {
  boot();
}
