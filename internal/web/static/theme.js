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

  const ICONS = {
    check: "M229.66,77.66l-128,128a8,8,0,0,1-11.32,0l-56-56a8,8,0,0,1,11.32-11.32L96,188.69,218.34,66.34a8,8,0,0,1,11.32,11.32Z",
    theme: "M128,24A104,104,0,1,0,232,128,104.11,104.11,0,0,0,128,24Zm0,192V40a88,88,0,0,1,0,176Z",
    system: "M208,40H48A24,24,0,0,0,24,64V176a24,24,0,0,0,24,24H208a24,24,0,0,0,24-24V64A24,24,0,0,0,208,40Zm8,136a8,8,0,0,1-8,8H48a8,8,0,0,1-8-8V64a8,8,0,0,1,8-8H208a8,8,0,0,1,8,8Zm-48,48a8,8,0,0,1-8,8H96a8,8,0,0,1,0-16h64A8,8,0,0,1,168,224Z",
    light: "M120,40V16a8,8,0,0,1,16,0V40a8,8,0,0,1-16,0Zm72,88a64,64,0,1,1-64-64A64.07,64.07,0,0,1,192,128Zm-16,0a48,48,0,1,0-48,48A48.05,48.05,0,0,0,176,128ZM58.34,69.66A8,8,0,0,0,69.66,58.34l-16-16A8,8,0,0,0,42.34,53.66Zm0,116.68-16,16a8,8,0,0,0,11.32,11.32l16-16a8,8,0,0,0-11.32-11.32ZM192,72a8,8,0,0,0,5.66-2.34l16-16a8,8,0,0,0-11.32-11.32l-16,16A8,8,0,0,0,192,72Zm5.66,114.34a8,8,0,0,0-11.32,11.32l16,16a8,8,0,0,0,11.32-11.32ZM48,128a8,8,0,0,0-8-8H16a8,8,0,0,0,0,16H40A8,8,0,0,0,48,128Zm80,80a8,8,0,0,0-8,8v24a8,8,0,0,0,16,0V216A8,8,0,0,0,128,208Zm112-88H216a8,8,0,0,0,0,16h24a8,8,0,0,0,0-16Z",
    dark: "M233.54,142.23a8,8,0,0,0-8-2,88.08,88.08,0,0,1-109.8-109.8,8,8,0,0,0-10-10,104.84,104.84,0,0,0-52.91,37A104,104,0,0,0,136,224a103.09,103.09,0,0,0,62.52-20.88,104.84,104.84,0,0,0,37-52.91A8,8,0,0,0,233.54,142.23ZM188.9,190.34A88,88,0,0,1,65.66,67.11a89,89,0,0,1,31.4-26A106,106,0,0,0,96,56,104.11,104.11,0,0,0,200,160a106,106,0,0,0,14.92-1.06A89,89,0,0,1,188.9,190.34Z",
  };

  function icon(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 256 256");
    svg.setAttribute("aria-hidden", "true");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", ICONS[name]);
    svg.append(path);
    return svg;
  }

  function control() {
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
      if (!["ArrowDown", "Enter", " "].includes(event.key)) return;
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
      const to = event.key === "ArrowDown" ? at + 1 : event.key === "ArrowUp" ? at - 1 : event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : undefined;
      if (to !== undefined) {
        event.preventDefault();
        items[(to + items.length) % items.length].focus();
      } else if (event.key === "Escape" || event.key === "ArrowLeft") {
        event.preventDefault();
        close(true);
      } else if (event.key === "Tab") close(false);
    });
    document.addEventListener("pointerdown", (event) => {
      if (!wrap.contains(event.target)) close(false);
    }, true);
    const sync = () => {
      for (const button of items) button.setAttribute("aria-checked", String(button.dataset.themeChoice === choice));
      toggle.dataset.tip = `Theme: ${names[choice]}`;
    };
    document.addEventListener("kapi-theme", sync);
    sync();
    return wrap;
  }

  window.kapiTheme = { choices: CHOICES, get: () => choice, set, control, palettes: PALETTES, palette: () => palette, setPalette };
  apply();
  applyPalette();
})();
