import { h, icon, modal, shortcuts } from "./core.js";

const SCOPES = Object.freeze({
  notes: Object.freeze({ label: "Notes", placeholder: "Search notes" }),
  page: Object.freeze({ label: "This page", placeholder: "Search this page" }),
});

let shared;

// One modal, input and keyboard entry point. Providers own only their results
// and scope-specific behavior; switching scope never creates another dialog.
export function searchDialog() {
  if (shared) return shared;
  const stylesheet = h("link", { rel: "stylesheet", href: new URL("./search-dialog.css", import.meta.url).href });
  document.head.append(stylesheet);
  const providers = new Map();
  const triggers = new Set();
  let scope = "notes";
  let session = 0;
  const input = h("input", { type: "search", class: "kapi-search-input kapi-switcher-search nb-search", placeholder: SCOPES.notes.placeholder, "aria-label": SCOPES.notes.placeholder, role: "combobox", "aria-expanded": "true", "aria-autocomplete": "list", autocomplete: "off", spellcheck: "false" });
  const pills = h("div", { class: "kapi-search-scopes", role: "group", "aria-label": "Search scope" });
  const body = h("div", { class: "kapi-search-body" });
  const empty = h("p", { class: "kapi-switcher-empty", text: "Loading notes…" });
  const footer = h("div", { class: "kapi-search-footer", text: `${shortcuts.key("searchJump").words} to open · ${shortcuts.key("searchPrevious").hint}${shortcuts.key("searchNext").hint} to move` });
  const scrim = h("div", { class: "kapi-modal-scrim kapi-search-scrim kapi-switcher-scrim", hidden: "" });
  const dialog = h("div", { class: "kapi-search-dialog kapi-switcher", role: "dialog", "aria-modal": "true", "aria-label": "Search", tabindex: "-1", hidden: "" },
    h("div", { class: "kapi-search-input-row" }, icon("search"), input, h("kbd", { text: shortcuts.key("dismiss").hint })), pills, body, footer);
  body.append(empty);
  document.body.append(scrim, dialog);
  const syncTriggers = () => {
    for (const trigger of triggers) trigger.setAttribute("aria-expanded", String(controller.isOpen()));
  };
  const drawPills = () => pills.replaceChildren(...Object.keys(SCOPES).filter((id) => id === "notes" || providers.has(id)).map((id) => {
    const button = h("button", { type: "button", "data-search-scope": id, "aria-pressed": String(id === scope), text: SCOPES[id].label });
    button.addEventListener("click", () => { setScope(id); input.focus(); });
    return button;
  }));
  function setScope(next) {
    if (next === "page" && !providers.has(next)) next = "notes";
    if (scope !== next) providers.get(scope)?.onClose?.();
    scope = next;
    for (const [id, provider] of providers) provider.view.hidden = id !== scope || !controller.isOpen();
    const provider = providers.get(scope);
    empty.hidden = !!provider;
    input.placeholder = SCOPES[scope].placeholder;
    input.setAttribute("aria-label", input.placeholder);
    input.removeAttribute("aria-activedescendant");
    if (provider?.listID) input.setAttribute("aria-controls", provider.listID);
    else input.removeAttribute("aria-controls");
    drawPills();
    if (controller.isOpen()) {
      provider?.onOpen?.(session);
      provider?.onInput?.();
    }
  }
  const controller = modal({
    dialog, scrim, initialFocus: () => input,
    onOpen: () => {
      session++;
      input.setAttribute("aria-expanded", "true");
      document.documentElement.setAttribute("data-kapi-switcher", "");
      setScope(scope);
      syncTriggers();
      input.select();
    },
    onClose: () => {
      providers.get(scope)?.onClose?.();
      for (const provider of providers.values()) provider.view.hidden = true;
      input.setAttribute("aria-expanded", "false");
      document.documentElement.removeAttribute("data-kapi-switcher");
      syncTriggers();
    },
  });
  input.addEventListener("input", () => providers.get(scope)?.onInput?.());
  input.addEventListener("keydown", (event) => {
    if (event.isComposing || event.keyCode === 229) return;
    providers.get(scope)?.onKey?.(event);
  });
  document.addEventListener("keydown", (event) => {
    if (event.isComposing || event.keyCode === 229) return;
    if (shortcuts.is("switchNote", event)) {
      event.preventDefault();
      event.stopImmediatePropagation();
      api.toggle("notes");
    } else if (shortcuts.is("search", event) && !event.target.closest?.("input, textarea, select, [contenteditable=true]")) {
      event.preventDefault();
      event.stopImmediatePropagation();
      api.open(providers.has("page") ? "page" : "notes");
    }
  }, true);
  const api = Object.freeze({
    input, dialog,
    get scope() { return scope; },
    isOpen: controller.isOpen,
    register(id, provider) {
      providers.set(id, provider);
      provider.view.hidden = true;
      body.append(provider.view);
      drawPills();
      if (controller.isOpen() && scope === id) setScope(scope);
    },
    open(next = "notes") {
      if (controller.isOpen()) { setScope(next); input.focus(); input.select(); }
      else { scope = next; controller.open(); }
    },
    close: controller.close,
    toggle(next = "notes") { if (controller.isOpen()) controller.close(); else api.open(next); },
    bind(trigger, next = "notes") {
      triggers.add(trigger);
      trigger.setAttribute("aria-haspopup", "dialog");
      trigger.setAttribute("aria-expanded", "false");
      trigger.addEventListener("click", () => {
        if (!controller.isOpen()) trigger.focus({ preventScroll: true });
        api.toggle(next);
      });
    },
  });
  shared = api;
  drawPills();
  return api;
}

export function searchTrigger() {
  const binding = shortcuts.key("switchNote");
  const button = h("button", { type: "button", class: "kapi-search-button", "aria-label": "Search", "aria-keyshortcuts": binding.words, "data-tip": `Search ${binding.hint}` },
    icon("search"), h("span", { class: "kapi-action-label", text: "Search" }), h("kbd", { text: binding.hint }));
  searchDialog().bind(button);
  return button;
}
