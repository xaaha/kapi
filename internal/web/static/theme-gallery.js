import { h } from "./core.js";
import { choiceGallery, dialog, ready, segmented, stylesheet } from "./ui.js";

const styled = Promise.all([ready, stylesheet(new URL("./theme-gallery.css", import.meta.url).href)]);

let shared;

function preview(id) {
  return h("span", { class: "kapi-theme-preview", "data-palette-preview": id, "aria-hidden": "true" },
    h("span", { class: "kapi-theme-preview-bar" }, h("b"), h("i"), h("i"), h("i")),
    h("span", { class: "kapi-theme-preview-page" }, h("b"), h("i"), h("i"), h("u")));
}

function build() {
  const theme = window.kapiTheme;
  const groups = ["All themes", ...new Set(theme.palettes.map((p) => p.group))];
  const gallery = choiceGallery({
    label: "Themes",
    groups,
    items: theme.palettes,
    noun: ["theme", "themes"],
    text: (p) => `${p.label} ${p.note} ${p.group}`,
    card: (p) => [preview(p.id), h("span", { class: "kapi-theme-caption" }, h("strong", { text: p.label }), h("small", { text: p.note }))],
    isSelected: (p) => p.id === theme.palette(),
    onPick: (p) => theme.setPalette(p.id),
  });
  const mode = segmented({
    label: "Mode",
    options: theme.modes.map((m) => ({ value: m, label: theme.modeLabels[m], icon: m })).sort((a, b) => (a.value === "system" ? -1 : b.value === "system" ? 1 : 0)),
    value: theme.get(),
    onChange: (m) => theme.set(m),
  });
  const done = h("button", { type: "button", class: "kapi-primary", text: "Done" });
  const ui = dialog({
    title: "Choose a theme",
    description: "Pick a palette for your notebook. It applies right away.",
    actions: [mode.el],
    body: gallery.el,
    footer: [h("span", { text: "Theme and mode are separate choices, saved for every browser." }), done],
    initialFocus: () => gallery.selected() || gallery.search,
    onOpen: () => {
      gallery.search.value = "";
      gallery.refresh();
      gallery.selected()?.scrollIntoView({ block: "nearest" });
    },
  });
  ui.el.classList.add("kapi-theme-gallery");
  done.addEventListener("click", () => ui.close());
  document.addEventListener("kapi-palette", () => gallery.refresh());
  document.addEventListener("kapi-theme", () => mode.set(theme.get()));
  return ui;
}

export async function openThemeGallery() {
  await styled;
  shared ??= build();
  shared.open();
}
