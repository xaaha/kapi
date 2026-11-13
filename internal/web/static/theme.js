(() => {
  "use strict";

  const KEY = "kapi-theme";
  const CHOICES = ["system", "light", "dark"];
  const root = document.documentElement;
  const dark = matchMedia("(prefers-color-scheme: dark)");

  function stored() {
    try {
      const v = localStorage.getItem(KEY);
      return CHOICES.includes(v) ? v : "system";
    } catch {
      return "system";
    }
  }

  let choice = stored();

  function apply() {
    root.dataset.theme = choice === "system" ? (dark.matches ? "dark" : "light") : choice;
    root.dataset.themeChoice = choice;
    document.dispatchEvent(new CustomEvent("kapi-theme", { detail: choice }));
  }

  function set(next) {
    if (!CHOICES.includes(next)) return;
    choice = next;
    try {
      if (next === "system") localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, next);
    } catch {}
    apply();
  }

  const PALETTE_KEY = "kapi-palette";
  const PALETTES = [
    { id: "default", label: "Default" },
    { id: "paper", label: "Paper" },
  ];
  const known = (id) => PALETTES.some((p) => p.id === id);

  function storedPalette() {
    try {
      const v = localStorage.getItem(PALETTE_KEY);
      return known(v) ? v : "default";
    } catch {
      return "default";
    }
  }

  let palette = storedPalette();

  function applyPalette() {
    if (palette === "default") delete root.dataset.palette;
    else root.dataset.palette = palette;
    document.dispatchEvent(new CustomEvent("kapi-palette", { detail: palette }));
  }

  function setPalette(next) {
    if (!known(next)) return;
    palette = next;
    try {
      if (next === "default") localStorage.removeItem(PALETTE_KEY);
      else localStorage.setItem(PALETTE_KEY, next);
    } catch {}
    applyPalette();
  }

  dark.addEventListener("change", () => choice === "system" && apply());
  addEventListener("storage", (e) => {
    if (e.key === PALETTE_KEY || e.key === null) {
      palette = storedPalette();
      applyPalette();
    }
    if (e.key !== KEY && e.key !== null) return;
    choice = stored();
    apply();
  });

  // Icon rendering is supplied by the shared UI core; theme state stays synchronous.
  function control({ icon, shortcuts }) {
    const names = { light: "Light", dark: "Dark", system: "System" };
    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "kapi-theme-button";
    toggle.setAttribute("aria-label", "Theme");
    toggle.setAttribute("data-tip", "Theme");
    toggle.setAttribute("aria-haspopup", "menu");
    toggle.setAttribute("aria-expanded", "false");
    toggle.append(icon("theme"));

    const list = document.createElement("div");
    list.className = "kapi-theme-menu";
    list.setAttribute("role", "menu");
    list.setAttribute("aria-label", "Theme");
    list.hidden = true;
    const wrap = document.createElement("div");
    wrap.className = "kapi-theme";
    wrap.append(toggle, list);

    let hovered = false;
    let timer = 0;
    const items = ["light", "dark", "system"].filter((name) => CHOICES.includes(name)).map((name) => {
      const button = document.createElement("button");
      button.type = "button";
      button.setAttribute("role", "menuitemradio");
      button.tabIndex = -1;
      button.dataset.themeChoice = name;
      button.append(icon("check"));
      const label = document.createElement("span");
      label.className = "kapi-menu-label";
      label.textContent = names[name];
      button.append(label, icon(name));
      button.addEventListener("click", () => {
        set(name);
        close(wrap.contains(document.activeElement));
      });
      list.append(button);
      return button;
    });
    const paletteGroup = document.createElement("div");
    paletteGroup.setAttribute("role", "group");
    paletteGroup.setAttribute("aria-label", "Palette");
    paletteGroup.className = "kapi-palette-choices";
    const paletteItems = PALETTES.map(({ id, label }) => {
      const button = document.createElement("button");
      button.type = "button";
      button.tabIndex = -1;
      button.dataset.palette = id;
      button.setAttribute("role", "menuitemradio");
      const text = document.createElement("span");
      text.className = "kapi-menu-label";
      text.textContent = label;
      button.append(icon("check"), text);
      button.addEventListener("click", () => {
        setPalette(id);
        close(wrap.contains(document.activeElement));
      });
      paletteGroup.append(button);
      return button;
    });
    list.append(paletteGroup);
    items.push(...paletteItems);
    const open = (focus) => {
      clearTimeout(timer);
      list.hidden = false;
      toggle.setAttribute("aria-expanded", "true");
      if (focus) (items.find((button) => button.getAttribute("aria-checked") === "true") || items[0])?.focus();
    };
    function close(refocus) {
      clearTimeout(timer);
      hovered = false;
      if (list.hidden) return;
      list.hidden = true;
      toggle.setAttribute("aria-expanded", "false");
      if (refocus) toggle.focus();
    }
    toggle.addEventListener("click", () => {
      if (hovered) hovered = false;
      else if (list.hidden) open(false);
      else close(false);
    });
    toggle.addEventListener("keydown", (event) => {
      if (!shortcuts.is("menuNext", event) && !shortcuts.is("activate", event)) return;
      event.preventDefault();
      open(true);
    });
    wrap.addEventListener("pointerenter", (event) => {
      if (event.pointerType !== "mouse") return;
      clearTimeout(timer);
      if (!list.hidden) return;
      open(false);
      hovered = true;
    });
    wrap.addEventListener("pointerleave", (event) => {
      if (event.pointerType !== "mouse" || !hovered) return;
      timer = setTimeout(() => close(list.contains(document.activeElement)), 250);
    });
    list.addEventListener("keydown", (event) => {
      const at = items.indexOf(document.activeElement);
      const to = shortcuts.is("menuNext", event) ? at + 1 : shortcuts.is("menuPrevious", event) ? at - 1 : shortcuts.is("menuFirst", event) ? 0 : shortcuts.is("menuLast", event) ? items.length - 1 : undefined;
      if (to !== undefined) {
        event.preventDefault();
        items[(to + items.length) % items.length].focus();
      } else if (shortcuts.is("menuBack", event)) {
        event.preventDefault();
        close(true);
      } else if (shortcuts.is("focusCycle", event)) close(false);
    });
    document.addEventListener("pointerdown", (event) => {
      if (!wrap.contains(event.target)) close(false);
    }, true);
    const sync = () => {
      for (const button of items) button.setAttribute("aria-checked", String(button.dataset.palette ? button.dataset.palette === palette : button.dataset.themeChoice === choice));
      toggle.dataset.tip = `Theme: ${names[choice]}`;
    };
    document.addEventListener("kapi-theme", sync);
    document.addEventListener("kapi-palette", sync);
    sync();
    return wrap;
  }

  window.kapiTheme = { choices: CHOICES, get: () => choice, set, control, palettes: PALETTES, palette: () => palette, setPalette };
  apply();
  applyPalette();
})();
