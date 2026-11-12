import { h, shortcuts } from "../../static/core.js";

export function noteSearch(options = {}) {
  const binding = shortcuts.key("switchNote");
  const input = h("input", {
    class: ["nb-search", options.className].filter(Boolean).join(" "),
    type: "search",
    placeholder: "Search notes",
    "aria-label": "Search notes",
    "aria-keyshortcuts": binding.words,
    autocomplete: "off",
    ...options.attributes,
  });
  const box = h("label", { class: "nb-search-box" },
    h("span", { class: "nb-search-icon", "aria-hidden": "true" }),
    input,
    h("kbd", { text: binding.hint }));
  return { box, input };
}
