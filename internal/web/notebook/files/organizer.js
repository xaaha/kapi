import { h, modal } from "../../static/core.js";
import { byFolder, folderCollapsed, folderTitle, movePage, onLayout, pageTitle, renameFolder, renamePage, setFolderCollapsed } from "./pages.js";
import { stateLabel, stateTone } from "./states.js";

let dragging = null;

function editName(container, current, save) {
  if (container.hasAttribute("data-editing")) return;
  container.setAttribute("data-editing", "");
  const input = h("input", { class: "kapi-organizer-rename-input", value: current, "aria-label": "New name" });
  container.append(input);
  input.focus();
  input.select();
  let cancelled = false;
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      input.blur();
    } else if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
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

function pageRow(page, folder, ids) {
  const title = pageTitle(page);
  const rename = h("button", { class: "kapi-organizer-rename", type: "button", "aria-label": `Rename ${title}`, text: "Rename" });
  const row = h("div", { class: "kapi-organizer-page", draggable: "true", "data-key": `organizer-page\n${folder}\n${page.id}` },
    h("span", { class: "nb-dot", "data-state": page.state, "data-tone": stateTone(page), "aria-hidden": "true" }),
    h("span", { class: "kapi-organizer-page-title", text: title }),
    h("span", { class: "kapi-organizer-state", text: stateLabel(page) }),
    rename);
  rename.addEventListener("click", () => editName(row, title, (name) => renamePage(page.id, name)));
  row.addEventListener("dragstart", (event) => {
    dragging = { folder, id: page.id };
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", page.id);
    row.setAttribute("data-dragging", "");
  });
  row.addEventListener("dragend", () => {
    dragging = null;
    row.removeAttribute("data-dragging");
    document.querySelectorAll(".kapi-organizer-page[data-drop]").forEach((el) => el.removeAttribute("data-drop"));
  });
  row.addEventListener("dragover", (event) => {
    if (!dragging || dragging.folder !== folder || dragging.id === page.id) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    row.dataset.drop = event.clientY >= row.getBoundingClientRect().top + row.offsetHeight / 2 ? "after" : "before";
  });
  row.addEventListener("dragleave", () => row.removeAttribute("data-drop"));
  row.addEventListener("drop", (event) => {
    if (!dragging || dragging.folder !== folder || dragging.id === page.id) return;
    event.preventDefault();
    movePage(folder, ids, dragging.id, page.id, row.dataset.drop === "after");
    dragging = null;
  });
  return row;
}

function folderGroup(folder, pages) {
  const name = folderTitle(folder);
  const collapsed = folderCollapsed(folder);
  const toggle = h("button", { class: "kapi-organizer-folder-toggle", type: "button", "aria-expanded": String(!collapsed), title: folder },
    h("span", { class: "kapi-organizer-chevron", "aria-hidden": "true" }),
    h("strong", { text: name }),
    h("span", { text: String(pages.length) }));
  const rename = h("button", { class: "kapi-organizer-rename", type: "button", "aria-label": `Rename ${name}`, text: "Rename" });
  const head = h("div", { class: "kapi-organizer-folder-head", "data-key": `organizer-folder\n${folder}` }, toggle, rename);
  const ids = pages.map((page) => page.id);
  const body = h("div", { class: "kapi-organizer-pages", hidden: collapsed }, ...pages.map((page) => pageRow(page, folder, ids)));
  toggle.addEventListener("click", () => setFolderCollapsed(folder, !collapsed));
  rename.addEventListener("click", () => editName(head, name, (next) => renameFolder(folder, next)));
  return h("section", { class: "kapi-organizer-folder", "data-collapsed": collapsed || false }, head, body);
}

export function createOrganizer() {
  const button = h("button", { class: "nb-organize", type: "button", "aria-haspopup": "dialog", "aria-expanded": "false", text: "Organize" });
  const scrim = h("div", { class: "kapi-modal-scrim kapi-organizer-scrim", hidden: "" });
  const close = h("button", { class: "kapi-organizer-close", type: "button", "aria-label": "Close organizer", text: "Close" });
  const list = h("div", { class: "kapi-organizer-list" });
  const dialog = h("div", { class: "kapi-organizer", role: "dialog", "aria-modal": "true", "aria-label": "Organize notes", tabindex: "-1", hidden: "" },
    h("header", { class: "kapi-organizer-head" }, h("div", {}, h("h2", { text: "Organize notes" }), h("p", { text: "Rename folders and notes, collapse groups, or drag notes into your preferred order." })), close),
    list);
  let pages = [];
  const render = () => {
    if (!controller.isOpen()) return;
    const key = list.contains(document.activeElement) ? document.activeElement.dataset.key : null;
    const groups = byFolder(pages);
    list.replaceChildren(...(groups.size ? [...groups].map(([folder, items]) => folderGroup(folder, items)) : [h("p", { class: "kapi-organizer-empty", text: "No notes to organize." })]));
    if (key) list.querySelector(`[data-key="${CSS.escape(key)}"]`)?.focus({ preventScroll: true });
  };
  const controller = modal({
    dialog,
    scrim,
    trigger: button,
    initialFocus: () => close,
    onOpen: render,
    shouldDismiss: (event) => !event.target.matches?.(".kapi-organizer-rename-input"),
  });
  close.addEventListener("click", controller.close);
  onLayout(render);
  document.body.append(scrim, dialog);
  return Object.freeze({
    button,
    setPages(next) {
      pages = next;
      render();
    },
  });
}
