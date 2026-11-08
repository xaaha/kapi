import { h, pageID, prefs, root, shortcuts } from "../../static/core.js";
import { ago, base, byFolder, matches, watch } from "./pages.js";

const OPEN_KEY = "kapi-notebook-open";
const docked = matchMedia("(min-width: 1100px)");

function link(p) {
  return h("a", {
    class: "kapi-nb-page",
    href: `${root}p/${p.id}/`,
    title: `${p.path}\n${ago(p.changed)}`,
    "aria-current": p.id === pageID ? "page" : false,
    "data-ended": p.ended || false,
    "data-missing": p.missing || false,
  }, h("span", { class: "kapi-nb-title", text: p.title }));
}

function render(list, pages, query) {
  const found = byFolder(pages.filter((p) => matches(p, query)));
  list.replaceChildren();
  if (!found.size) {
    list.append(h("p", { class: "kapi-nb-empty", text: pages.length ? "No page matches." : "No pages yet." }));
    return;
  }
  for (const [folder, items] of found) {
    list.append(h("section", { class: "kapi-nb-group" },
      h("h2", { class: "kapi-nb-folder", title: folder, text: base(folder) }),
      ...items.map(link)));
  }
}

export function showSidebar() {
  const nav = h("nav", { class: "kapi-nb", "aria-label": "Notebook", id: "kapi-notebook" });
  const filter = h("input", { class: "kapi-nb-filter", type: "search", placeholder: "Filter pages", "aria-label": "Filter pages" });
  const list = h("div", { class: "kapi-nb-list" });
  nav.append(h("div", { class: "kapi-nb-head" }, h("a", { class: "kapi-nb-home", href: root, text: "Notebook" })), filter, list);
  let pages = [];
  const draw = () => render(list, pages, filter.value);
  filter.addEventListener("input", draw);
  watch((next) => {
    pages = next;
    nav.removeAttribute("data-error");
    draw();
  }, () => nav.setAttribute("data-error", ""));

  const toggle = h("button", { class: "kapi-nb-toggle", type: "button", "aria-controls": "kapi-notebook", title: `Pages (${shortcuts.key("notebook").hint})` },
    h("span", { class: "kapi-nb-toggle-icon", "aria-hidden": "true" }));
  document.body.append(nav);
  const head = document.querySelector(".kapi-site-head");
  if (head) head.prepend(toggle);
  else document.body.append(toggle);

  const isOpen = () => document.documentElement.hasAttribute("data-kapi-notebook");
  const set = (open, remember) => {
    document.documentElement.toggleAttribute("data-kapi-notebook", open);
    toggle.setAttribute("aria-expanded", String(open));
    toggle.setAttribute("aria-label", open ? "Hide pages" : "Show pages");
    if (remember && docked.matches) prefs.set(OPEN_KEY, open ? null : "closed");
  };
  set(docked.matches && prefs.get(OPEN_KEY) !== "closed", false);
  docked.addEventListener("change", () => set(docked.matches && prefs.get(OPEN_KEY) !== "closed", false));
  toggle.addEventListener("click", () => set(!isOpen(), true));
  document.addEventListener("pointerdown", (e) => {
    if (!docked.matches && isOpen() && !nav.contains(e.target) && !toggle.contains(e.target)) set(false, false);
  });
  document.addEventListener("keydown", (e) => {
    if (shortcuts.is("notebook", e)) {
      e.preventDefault();
      set(!isOpen(), true);
    } else if (!docked.matches && isOpen() && shortcuts.is("dismiss", e)) {
      set(false, false);
    }
  });
}
