import { h, pageID, root, shortcuts } from "../../static/core.js";
import { searchDialog } from "../../static/search-dialog.js";
import { ago, markVisited, matches, newIDs, onLayout, onVisit, pageTitle, redraw, noteGroups, watch } from "./pages.js";
import { stateLabel, stateTone } from "./states.js";

const RECENT_LIMIT = 7;
const href = (p) => `${root}p/${p.id}/`;
let notesSearch;

function noteRow(p, fresh) {
  return h("a", {
    class: "kapi-switcher-row", id: `kapi-switcher-page-${p.id}`, role: "option", "aria-selected": "false", "data-key": `page\n${p.id}`,
    href: href(p), title: `${p.path}\n${stateLabel(p)} · ${ago(p.changed)}`, "data-ended": p.state === "ended", "data-new": fresh || false,
  },
  h("span", { class: "nb-dot", "data-state": p.state, "data-tone": stateTone(p), "aria-hidden": "true" }),
  h("span", { class: "kapi-switcher-copy" }, h("strong", { text: pageTitle(p) }), h("span", { text: p.summary || stateLabel(p) })),
  h("time", { datetime: p.changed, text: ago(p.changed) }));
}

function section(title, pages, fresh) {
  return h("section", { class: "kapi-switcher-section" }, h("h2", { text: title }), ...pages.map((p) => noteRow(p, fresh.has(p.id))));
}

function render(list, pages, query, fresh) {
  const available = pages.filter((p) => p.id !== pageID && matches(p, query));
  if (!available.length) {
    list.replaceChildren(h("p", { class: "kapi-switcher-empty", text: pages.length ? "No note matches." : "No notes yet." }));
    return;
  }
  const groups = query.trim() ? [["Results", available]] : noteGroups(available, fresh, RECENT_LIMIT);
  list.replaceChildren(...groups.map(([title, items]) => section(title, items, fresh)));
}

// Notes are a provider of the shared dialog, not a second search UI.
export function registerNotesSearch() {
  if (notesSearch) return notesSearch;
  const search = searchDialog();
  const current = h("span", { class: "kapi-switcher-current" });
  const list = h("div", { class: "kapi-switcher-list", id: "kapi-switcher-list", role: "listbox", "aria-label": "Notes" });
  const view = h("section", { class: "kapi-notes-search", "aria-label": "Notes search" }, current, list,
    ...(pageID ? [h("a", { class: "kapi-switcher-home", href: root, text: "Home · All notes" })] : []));
  let pages = [];
  let fresh = new Set();
  let activeID = null;
  const subscribers = new Set();
  const rows = () => [...list.querySelectorAll(".kapi-switcher-row")];
  const setActive = (row) => {
    for (const item of rows()) item.setAttribute("aria-selected", String(item === row));
    activeID = row?.id ?? null;
    if (row) {
      search.input.setAttribute("aria-activedescendant", row.id);
      if (search.isOpen() && search.scope === "notes") row.scrollIntoView({ block: "nearest" });
    } else search.input.removeAttribute("aria-activedescendant");
  };
  const draw = () => {
    const query = search.scope === "notes" ? search.input.value : "";
    redraw(list, () => render(list, pages, query, fresh));
    if (search.scope === "notes") setActive((activeID && document.getElementById(activeID)) || rows()[0] || null);
  };
  const onKey = (e) => {
    const items = rows();
    const at = items.findIndex((row) => row.id === activeID);
    const next = shortcuts.is("searchNext", e);
    const previous = shortcuts.is("searchPrevious", e);
    if ((next || previous) && items.length) {
      e.preventDefault();
      e.stopPropagation();
      setActive(items[(at + (next ? 1 : -1) + items.length) % items.length]);
    } else if (shortcuts.is("searchJump", e) && at >= 0) {
      e.preventDefault();
      e.stopPropagation();
      location.href = items[at].href;
    }
  };
  search.register("notes", { view, listID: list.id, onOpen: draw, onInput: draw, onKey });
  list.addEventListener("keydown", onKey);
  list.addEventListener("pointermove", (e) => {
    const row = e.target.closest?.(".kapi-switcher-row");
    if (row) setActive(row);
  });
  const notify = () => { for (const run of subscribers) run(pages, fresh); };
  onLayout(() => { draw(); notify(); });
  onVisit(() => { fresh = newIDs(pages, pageID); draw(); notify(); });
  watch((next) => {
    if (pageID) markVisited(pageID, next);
    pages = next;
    fresh = newIDs(pages, pageID);
    const here = pages.find((p) => p.id === pageID);
    current.textContent = here ? `Current · ${pageTitle(here)}` : "All notes";
    view.removeAttribute("data-error");
    draw();
    notify();
  }, () => view.setAttribute("data-error", ""));
  notesSearch = { onUpdate(run) { subscribers.add(run); run(pages, fresh); } };
  return notesSearch;
}

export function showSidebar() {
  document.title = "kapi";
  const notes = registerNotesSearch();
  const launcher = h("button", { class: "kapi-switcher-launcher", type: "button", title: `Switch notes (${shortcuts.key("switchNote").hint})`, "aria-label": "Switch notes" },
    h("span", { class: "kapi-switcher-launcher-icon", "aria-hidden": "true" }));
  searchDialog().bind(launcher);
  notes.onUpdate((pages, fresh) => {
    launcher.toggleAttribute("data-new", fresh.size > 0);
    const title = launcher.querySelector(".kapi-current-title");
    const here = pages.find((p) => p.id === pageID);
    if (title && here) title.textContent = pageTitle(here);
  });
  const mount = () => {
    const title = document.querySelector(".kapi-breadcrumbs > .kapi-current-title");
    if (!title) return false;
    title.replaceWith(launcher);
    launcher.prepend(title);
    return true;
  };
  if (!mount()) {
    const observer = new MutationObserver(() => { if (mount()) observer.disconnect(); });
    observer.observe(document.body, { childList: true, subtree: true });
  }
}
