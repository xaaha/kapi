import { h, pageID, prefs, root, shortcuts } from "../../static/core.js";
import { ago, byFolder, folderCollapsed, folderTitle, markVisited, matches, movePage, newIDs, onLayout, onVisit, pageTitle, redraw, renameFolder, renamePage, setFolderCollapsed, stateLabel, watch } from "./pages.js";

const OPEN_KEY = "kapi-notebook-open";
const docked = matchMedia("(min-width: 1100px)");
let dragging = null;

function editName(container, current, save) {
  if (container.hasAttribute("data-editing")) return;
  container.setAttribute("data-editing", "");
  const input = h("input", { class: "kapi-nb-rename-input", value: current, "aria-label": "New name" });
  container.append(input);
  input.focus();
  input.select();
  let cancelled = false;
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      input.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      cancelled = true;
      input.blur();
    }
  });
  input.addEventListener("blur", () => {
    container.removeAttribute("data-editing");
    input.remove();
    if (!cancelled) save(input.value.trim());
  }, { once: true });
}

function pageRow(p, folder, fresh) {
  const title = pageTitle(p);
  const link = h("a", {
    class: "kapi-nb-page",
    "data-key": `${folder}\n${p.id}`,
    href: `${root}p/${p.id}/`,
    title: `${p.path}\n${stateLabel(p)} · ${ago(p.changed)}`,
    "aria-current": p.id === pageID ? "page" : false,
    "data-ended": p.state === "ended",
    "data-new": fresh || false,
    draggable: "false",
  }, h("span", { class: "nb-dot", "data-state": p.state, "aria-hidden": "true" }), h("span", { class: "kapi-nb-title", text: title }),
  h("time", { class: "kapi-nb-when", datetime: p.changed, text: ago(p.changed) }),
  fresh ? h("span", { class: "kapi-nb-new", text: "New" }) : null);
  const rename = h("button", { class: "kapi-nb-rename", type: "button", title: `Rename ${title}`, "aria-label": `Rename ${title}`, text: "✎" });
  const row = h("div", { class: "kapi-nb-row", draggable: "true", "data-page": p.id, title: "Drag to reorder" }, link, rename);
  rename.addEventListener("click", () => editName(row, title, (name) => renamePage(p.id, name)));
  row.addEventListener("dragstart", (e) => {
    dragging = { folder, id: p.id };
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", p.id);
    row.setAttribute("data-dragging", "");
  });
  row.addEventListener("dragend", () => {
    dragging = null;
    row.removeAttribute("data-dragging");
    document.querySelectorAll(".kapi-nb-row[data-drop]").forEach((el) => el.removeAttribute("data-drop"));
  });
  row.addEventListener("dragover", (e) => {
    if (!dragging || dragging.folder !== folder || dragging.id === p.id) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    row.dataset.drop = e.clientY >= row.getBoundingClientRect().top + row.offsetHeight / 2 ? "after" : "before";
  });
  row.addEventListener("dragleave", () => row.removeAttribute("data-drop"));
  row.addEventListener("drop", (e) => {
    if (!dragging || dragging.folder !== folder || dragging.id === p.id) return;
    e.preventDefault();
    const ids = [...row.parentElement.querySelectorAll(":scope > .kapi-nb-row")].map((el) => el.dataset.page);
    movePage(folder, ids, dragging.id, p.id, row.dataset.drop === "after");
    dragging = null;
  });
  return row;
}

function folderGroup(folder, items, fresh) {
  const name = folderTitle(folder);
  const collapsed = folderCollapsed(folder);
  const toggle = h("button", { class: "kapi-nb-folder-toggle", type: "button", "aria-expanded": String(!collapsed), title: folder, "data-key": `folder\n${folder}` },
    h("span", { class: "kapi-nb-chevron", "aria-hidden": "true" }), h("span", { class: "kapi-nb-folder-name", text: name }), h("span", { class: "kapi-nb-count", text: String(items.length) }));
  const rename = h("button", { class: "kapi-nb-rename", type: "button", title: `Rename ${name}`, "aria-label": `Rename ${name}`, text: "✎" });
  const head = h("h2", { class: "kapi-nb-folder" }, toggle, rename);
  const body = h("div", { class: "kapi-nb-folder-pages", hidden: collapsed }, ...items.map((p) => pageRow(p, folder, fresh.has(p.id))));
  toggle.addEventListener("click", () => setFolderCollapsed(folder, !collapsed));
  rename.addEventListener("click", () => editName(head, name, (next) => renameFolder(folder, next)));
  return h("section", { class: "kapi-nb-group", "data-folder": folder, "data-collapsed": collapsed || false }, head, body);
}

function render(list, pages, query, fresh) {
  const found = byFolder(pages.filter((p) => matches(p, query)));
  list.replaceChildren();
  if (!found.size) {
    list.append(h("p", { class: "kapi-nb-empty", text: pages.length ? "No page matches." : "No pages yet." }));
    return;
  }
  for (const [folder, items] of found) list.append(folderGroup(folder, items, fresh));
}

export function showSidebar() {
  const nav = h("nav", { class: "kapi-nb", "aria-label": "Notebook", id: "kapi-notebook" });
  const filter = h("input", { class: "kapi-nb-filter", type: "search", placeholder: "Filter pages", "aria-label": "Filter pages" });
  const list = h("div", { class: "kapi-nb-list" });
  nav.append(h("div", { class: "kapi-nb-head" }, h("a", { class: "kapi-nb-home", href: root, text: "Notebook" })), filter, list);
  const toggle = h("button", { class: "kapi-nb-toggle", type: "button", "aria-controls": "kapi-notebook", title: `Pages (${shortcuts.key("notebook").hint})` },
    h("span", { class: "kapi-nb-toggle-icon", "aria-hidden": "true" }));
  let pages = [];
  let fresh = new Set();
  const draw = () => redraw(list, () => render(list, pages, filter.value, fresh));
  filter.addEventListener("input", draw);
  onLayout(draw);
  onVisit(() => {
    fresh = newIDs(pages, pageID);
    toggle.toggleAttribute("data-new", fresh.size > 0);
    draw();
  });
  watch((next) => {
    if (pageID) markVisited(pageID, next);
    pages = next;
    fresh = newIDs(pages, pageID);
    toggle.toggleAttribute("data-new", fresh.size > 0);
    nav.removeAttribute("data-error");
    draw();
  }, () => nav.setAttribute("data-error", ""));

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
