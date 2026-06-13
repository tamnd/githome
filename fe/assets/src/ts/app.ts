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

// wireFlashDismiss lets a [data-flash-close] button remove its flash banner.
// The button ships in the server markup but CSS keeps it hidden until the
// js-enhanced flag is set, so with scripting off the banner simply stays, the
// same outcome as before the button existed.
function wireFlashDismiss(): void {
  for (const btn of Array.from(document.querySelectorAll<HTMLElement>("[data-flash-close]"))) {
    btn.addEventListener("click", () => {
      btn.closest(".flash")?.remove();
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

// wireBlobLineSelection enhances the numbered code view with GitHub's line and
// line-range selection. With JS off every line number is already an anchor to
// #L{n} and the :target rule paints the one linked line, so the no-JS path is
// intact. The enhancement adds shift-click to select a span, reflects it in the
// URL fragment as #L{a}-L{b} (the form GitHub uses to permalink a range), and
// paints the whole range — which a single :target cannot do.
function wireBlobLineSelection(): void {
  const table = document.querySelector<HTMLElement>("[data-line-select]");
  if (!table) {
    return;
  }
  const rows = Array.from(table.querySelectorAll<HTMLElement>(".blob-line"));
  if (rows.length === 0) {
    return;
  }

  // lineNumber pulls the integer out of a row id like "L42".
  const lineNumber = (row: HTMLElement): number => Number.parseInt(row.id.slice(1), 10);

  // paint toggles the selected class across the inclusive [lo, hi] range; a hi
  // below lo clears every row.
  const paint = (lo: number, hi: number): void => {
    for (const row of rows) {
      const n = lineNumber(row);
      row.classList.toggle("is-line-selected", n >= lo && n <= hi);
    }
  };

  // applyHash repaints from the current fragment: #L{a} highlights one line,
  // #L{a}-L{b} the span, anything else clears.
  const applyHash = (): void => {
    const m = /^#L(\d+)(?:-L(\d+))?$/.exec(window.location.hash);
    if (!m) {
      paint(1, 0);
      return;
    }
    const a = Number.parseInt(m[1], 10);
    const b = m[2] ? Number.parseInt(m[2], 10) : a;
    paint(Math.min(a, b), Math.max(a, b));
  };

  let anchor = 0;
  for (const row of rows) {
    const link = row.querySelector<HTMLElement>(".blob-num-link");
    if (!link) {
      continue;
    }
    link.addEventListener("click", (e) => {
      const n = lineNumber(row);
      if (e.shiftKey && anchor > 0) {
        // Extend the selection from the last anchored line to this one without a
        // navigation, then repaint the span ourselves.
        e.preventDefault();
        const lo = Math.min(anchor, n);
        const hi = Math.max(anchor, n);
        window.history.replaceState(null, "", `#L${lo}-L${hi}`);
        paint(lo, hi);
      } else {
        // A plain click anchors here; the browser sets #L{n} and the hashchange
        // handler repaints.
        anchor = n;
      }
    });
  }

  window.addEventListener("hashchange", applyHash);
  applyHash();
}

// RelativeTime upgrades the <relative-time> elements the server renders with a
// machine datetime and an absolute fallback body. On connect the element swaps
// its text for a relative phrase ("3 days ago") in the viewer's locale and
// keeps the exact local timestamp in the title for hover. Anything older than
// about a month stays on the server's absolute date, the github.com behavior,
// and with scripting off the element never upgrades so the fallback stands.
class RelativeTime extends HTMLElement {
  connectedCallback(): void {
    const iso = this.getAttribute("datetime");
    if (!iso) {
      return;
    }
    const then = new Date(iso);
    if (Number.isNaN(then.getTime())) {
      return;
    }
    if (!this.title) {
      this.title = then.toLocaleString();
    }
    const seconds = Math.round((then.getTime() - Date.now()) / 1000);
    const phrased = relativePhrase(seconds);
    if (phrased !== null) {
      this.textContent = phrased;
    }
  }
}

// relativePhrase renders an offset in seconds with the largest unit that fits,
// or null when the moment is far enough away that an absolute date reads
// better than "14 months ago".
function relativePhrase(seconds: number): string | null {
  const abs = Math.abs(seconds);
  if (abs >= 30 * 86400) {
    return null;
  }
  const fmt = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  if (abs < 60) {
    return fmt.format(seconds, "second");
  }
  if (abs < 3600) {
    return fmt.format(Math.trunc(seconds / 60), "minute");
  }
  if (abs < 86400) {
    return fmt.format(Math.trunc(seconds / 3600), "hour");
  }
  if (abs < 7 * 86400) {
    return fmt.format(Math.trunc(seconds / 86400), "day");
  }
  return fmt.format(Math.trunc(seconds / (7 * 86400)), "week");
}

// registerRelativeTime defines the element once; every <relative-time> already
// in the document upgrades on definition and later ones upgrade on parse.
function registerRelativeTime(): void {
  if (!customElements.get("relative-time")) {
    customElements.define("relative-time", RelativeTime);
  }
}

function boot(): void {
  markEnhanced();
  registerRelativeTime();
  wireCopyButtons();
  wireFlashDismiss();
  wireFileFilter();
  wireBlobLineSelection();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", boot, { once: true });
} else {
  boot();
}
