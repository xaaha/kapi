import { h, icon, modal, shortcuts } from "./core.js";

const styles = new Map();

export function stylesheet(href) {
  if (!styles.has(href)) {
    const link = h("link", { rel: "stylesheet", href });
    styles.set(href, new Promise((resolve) => {
      link.addEventListener("load", resolve, { once: true });
      link.addEventListener("error", resolve, { once: true });
    }));
    document.head.append(link);
  }
  return styles.get(href);
}

export const ready = stylesheet(new URL("./ui.css", import.meta.url).href);

function roving(items, event) {
  const at = items.indexOf(document.activeElement);
  if (at < 0) return null;
  const to = shortcuts.is("choiceNext", event) ? at + 1
    : shortcuts.is("choicePrevious", event) ? at - 1
    : shortcuts.is("choiceFirst", event) ? 0
    : shortcuts.is("choiceLast", event) ? items.length - 1
    : null;
  if (to === null) return null;
  event.preventDefault();
  return items[(to + items.length) % items.length];
}

export function segmented({ label, options, value, onChange }) {
  const buttons = options.map((o) => h("button", { type: "button", role: "radio", "data-value": o.value, "aria-label": o.label, "data-tip": o.label },
    ...(o.icon ? [icon(o.icon)] : []), h("span", { class: "kapi-segmented-label", text: o.label })));
  const el = h("div", { class: "kapi-segmented", role: "radiogroup", "aria-label": label }, ...buttons);
  const set = (next) => {
    value = next;
    for (const b of buttons) {
      const on = b.dataset.value === value;
      b.setAttribute("aria-checked", String(on));
      b.tabIndex = on ? 0 : -1;
    }
  };
  for (const b of buttons) b.addEventListener("click", () => onChange(b.dataset.value));
  el.addEventListener("keydown", (event) => {
    const next = roving(buttons, event);
    if (!next) return;
    next.focus();
    onChange(next.dataset.value);
  });
  set(value);
  return { el, set };
}

export function choiceGallery({ label, groups, items, text, card, isSelected, onPick, noun = ["item", "items"] }) {
  const all = groups[0];
  let group = all;
  const search = h("input", { type: "search", class: "kapi-choices-search", placeholder: `Search ${noun[1]}`, "aria-label": `Search ${noun[1]}`, autocomplete: "off", spellcheck: "false" });
  const count = h("p", { class: "kapi-choices-count", role: "status" });
  const groupButtons = groups.map((name) => h("button", { type: "button", "data-group": name, "aria-pressed": "false" },
    h("span", { text: name }), h("span", { class: "kapi-choices-group-count", text: String(name === all ? items.length : items.filter((i) => i.group === name).length) })));
  const nav = h("nav", { class: "kapi-choices-groups", "aria-label": "Collections" }, ...groupButtons);
  const grid = h("div", { class: "kapi-choices-grid", role: "listbox", "aria-label": label });
  const empty = h("p", { class: "kapi-choices-empty", hidden: "" });
  const cards = new Map(items.map((item) => {
    const el = h("button", { type: "button", role: "option", class: "kapi-choices-card", "data-choice": item.id, tabindex: "-1" }, ...card(item));
    el.addEventListener("click", () => onPick(item));
    return [item, el];
  }));
  grid.append(...cards.values());

  const shown = () => [...cards.values()].filter((el) => !el.hidden);
  function refresh() {
    const words = search.value.toLowerCase().trim().split(/\s+/).filter(Boolean);
    for (const [item, el] of cards) {
      el.hidden = (group !== all && item.group !== group) || !words.every((w) => text(item).toLowerCase().includes(w));
      const on = isSelected(item);
      el.setAttribute("aria-selected", String(on));
    }
    const visible = shown();
    const focusable = visible.find((el) => el.getAttribute("aria-selected") === "true") || visible[0];
    for (const el of cards.values()) el.tabIndex = el === focusable ? 0 : -1;
    for (const b of groupButtons) b.setAttribute("aria-pressed", String(b.dataset.group === group));
    count.textContent = `${visible.length} ${visible.length === 1 ? noun[0] : noun[1]}`;
    empty.hidden = visible.length > 0;
    empty.textContent = words.length ? `No ${noun[1]} match "${search.value.trim()}".` : `No ${noun[1]} here yet.`;
  }
  search.addEventListener("input", refresh);
  for (const b of groupButtons) {
    b.addEventListener("click", () => {
      group = b.dataset.group;
      refresh();
    });
  }
  grid.addEventListener("keydown", (event) => {
    const next = roving(shown(), event);
    if (next) {
      for (const el of cards.values()) el.tabIndex = el === next ? 0 : -1;
      next.focus();
    }
  });
  refresh();
  const el = h("div", { class: "kapi-choices" }, nav, h("div", { class: "kapi-choices-main" }, h("div", { class: "kapi-choices-bar" }, icon("search"), search, count), grid, empty));
  return { el, search, refresh, selected: () => [...cards.values()].find((c) => c.getAttribute("aria-selected") === "true") };
}

export function dialog({ title, description, actions = [], body, footer = [], initialFocus, onOpen, onClose }) {
  const titleID = `kapi-dialog-${Math.random().toString(36).slice(2)}`;
  const close = h("button", { type: "button", class: "kapi-dialog-close", "aria-label": "Close" }, icon("x"));
  const scrim = h("div", { class: "kapi-modal-scrim kapi-dialog-scrim", hidden: "" });
  const el = h("div", { class: "kapi-dialog", role: "dialog", "aria-modal": "true", "aria-labelledby": titleID, tabindex: "-1", hidden: "" },
    h("header", { class: "kapi-dialog-head" },
      h("div", { class: "kapi-dialog-titles" }, h("h2", { id: titleID, text: title }), ...(description ? [h("p", { text: description })] : [])),
      ...actions, close),
    h("div", { class: "kapi-dialog-body" }, body),
    ...(footer.length ? [h("footer", { class: "kapi-dialog-foot" }, ...footer)] : []));
  document.body.append(scrim, el);
  const controller = modal({ dialog: el, scrim, initialFocus, onOpen, onClose });
  close.addEventListener("click", () => controller.close());
  return { el, ...controller };
}
