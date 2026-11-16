import { getJSON, h, icon } from "./core.js";
import { popover, popoverContent } from "./popover.js";
import { stylesheet } from "./ui.js";

const styled = stylesheet(new URL("./save-dialog.css", import.meta.url).href);

let shared;

async function build() {
  let save = async () => null;
  let busy = false;
  const where = h("p", { class: "kapi-pop-detail kapi-save-where" });
  const list = h("div", { class: "kapi-save-folders", role: "group", "aria-label": "Folders" });
  const name = h("input", { type: "text", class: "kapi-save-name", name: "folder", placeholder: "Folder name, like Work", "aria-label": "New folder name", autocomplete: "off", spellcheck: "false", maxlength: "100" });
  const create = h("button", { type: "submit", class: "kapi-primary", text: "Create and save" });
  const form = h("form", { class: "kapi-save-new", hidden: true }, name, create);
  const newFolder = h("button", { type: "button", class: "kapi-save-folder kapi-save-add", "data-new-folder": "" }, icon("folderPlus"), h("span", { text: "New folder" }));
  const error = h("p", { class: "kapi-pop-error kapi-save-error", role: "alert", hidden: true });
  const body = popoverContent({ children: [where, list, newFolder, form, error] });
  const ui = await popover({
    title: "Save to library",
    description: "Saved notes are kept until you delete them. kapi moves the file into the folder you pick.",
    className: "kapi-save-picker",
    initialFocus: () => list.querySelector("button") || newFolder,
  });
  ui.render({ content: body });

  const showForm = (on) => {
    form.hidden = !on;
    newFolder.hidden = on;
    if (on) name.focus();
  };

  async function pick(folder) {
    if (busy) return;
    busy = true;
    error.hidden = true;
    body.setAttribute("aria-busy", "true");
    const problem = await save(folder);
    busy = false;
    body.setAttribute("aria-busy", "false");
    if (!problem) return;
    error.textContent = problem;
    error.hidden = false;
  }

  newFolder.addEventListener("click", () => showForm(true));
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    if (name.value.trim()) pick(name.value.trim());
  });

  async function load() {
    list.replaceChildren(h("p", { class: "kapi-save-empty", text: "Loading folders…" }));
    let data;
    try {
      data = await getJSON("library");
    } catch {
      list.replaceChildren(h("p", { class: "kapi-save-empty", text: "Could not list the library. Name a new folder instead." }));
      showForm(true);
      return;
    }
    where.replaceChildren("Folders in ", h("code", { text: data.library }));
    list.replaceChildren(...data.folders.map((folder) => {
      const button = h("button", { type: "button", class: "kapi-save-folder", "data-folder": folder }, icon("folder"), h("span", { text: folder }));
      button.addEventListener("click", () => pick(folder));
      return button;
    }));
    if (!data.folders.length) {
      list.replaceChildren(h("p", { class: "kapi-save-empty", text: "No folders yet. Name the first one." }));
      showForm(true);
    } else list.querySelector("button").focus();
  }

  return {
    open(options) {
      save = options.save;
      name.value = "";
      error.hidden = true;
      showForm(false);
      ui.open();
      return load();
    },
  };
}

export async function openSavePicker(options) {
  await styled;
  shared ??= build();
  return (await shared).open(options);
}
