(() => {
  "use strict";

  const MODES = ["light", "dark", "system"];
  const MODE_LABELS = { light: "Light", dark: "Dark", system: "System" };
  const PALETTES = [
    { id: "default", label: "Default", note: "Crisp and minimal", group: "Neutral" },
    { id: "paper", label: "Paper", note: "Warm neutral", group: "Neutral" },
    { id: "porcelain", label: "Porcelain", note: "Soft white", group: "Neutral" },
    { id: "stone", label: "Stone", note: "Natural gray", group: "Neutral" },
    { id: "fog", label: "Fog", note: "Low contrast", group: "Neutral" },
    { id: "graphite", label: "Graphite", note: "Dark ink", group: "Neutral" },
    { id: "linen", label: "Linen", note: "Soft and familiar", group: "Warm" },
    { id: "sand", label: "Sand", note: "Desert warmth", group: "Warm" },
    { id: "clay", label: "Clay", note: "Earthy rose", group: "Warm" },
    { id: "espresso", label: "Espresso", note: "Coffee and cream", group: "Warm" },
    { id: "copper", label: "Copper", note: "Burnished metal", group: "Warm" },
    { id: "solar", label: "Solar", note: "Sunlit workspace", group: "Warm" },
    { id: "ocean", label: "Ocean", note: "Coastal blue", group: "Color" },
    { id: "slate", label: "Slate", note: "Cool focus", group: "Color" },
    { id: "cobalt", label: "Cobalt", note: "Clear blue", group: "Color" },
    { id: "teal", label: "Teal", note: "Calm clarity", group: "Color" },
    { id: "forest", label: "Forest", note: "Deep greens", group: "Color" },
    { id: "moss", label: "Moss", note: "Grounded green", group: "Color" },
    { id: "rose", label: "Rose", note: "Dusty blush", group: "Color" },
    { id: "berry", label: "Berry", note: "Rich pink", group: "Color" },
    { id: "plum", label: "Plum", note: "Soft purple", group: "Color" },
    { id: "lavender", label: "Lavender", note: "Gentle violet", group: "Color" },
  ];
  const SUGGESTED = ["default", "paper", "ocean"];
  const known = (id) => PALETTES.some((p) => p.id === id);
  const paletteOf = (id) => PALETTES.find((p) => p.id === id);

  const root = document.documentElement;
  const dark = matchMedia("(prefers-color-scheme: dark)");
  const here = document.currentScript.src;
  const api = new URL("../settings", here);

  let choice = MODES.includes(root.dataset.themeChoice) ? root.dataset.themeChoice : "system";
  let palette = known(root.dataset.paletteChoice) ? root.dataset.paletteChoice : "default";
  let recent = (root.dataset.paletteRecent || "").split(" ").filter(known);
  let pending = 0;

  function apply() {
    root.dataset.theme = choice === "system" ? (dark.matches ? "dark" : "light") : choice;
    root.dataset.themeChoice = choice;
    if (palette === "default") delete root.dataset.palette;
    else root.dataset.palette = palette;
    document.dispatchEvent(new CustomEvent("kapi-theme", { detail: choice }));
    document.dispatchEvent(new CustomEvent("kapi-palette", { detail: palette }));
  }

  function adopt(next) {
    if (MODES.includes(next.mode)) choice = next.mode;
    if (known(next.theme)) palette = next.theme;
    if (Array.isArray(next.recent)) recent = next.recent.filter(known);
    apply();
  }

  async function save(patch) {
    const before = { mode: choice, theme: palette, recent };
    adopt({ ...patch, ...(patch.theme && { recent: [patch.theme, ...recent.filter((id) => id !== patch.theme)].slice(0, 3) }) });
    pending++;
    try {
      const res = await fetch(api, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(patch) });
      if (!res.ok) throw new Error(`settings: ${res.status}`);
      const saved = await res.json();
      if (pending === 1) adopt(saved);
    } catch {
      if (pending === 1) adopt(before);
    } finally {
      pending--;
    }
  }

  const set = (next) => MODES.includes(next) && save({ mode: next });
  const setPalette = (next) => known(next) && save({ theme: next });

  function listen() {
    const take = (data) => {
      if (pending === 0) adopt(JSON.parse(data));
    };
    if (window.SharedWorker) {
      const worker = new SharedWorker(new URL("settings-stream.js", here));
      worker.port.onmessage = (e) => take(e.data);
      worker.port.start();
    } else {
      new EventSource(new URL("../settings/events", here)).onmessage = (e) => take(e.data);
    }
  }

  dark.addEventListener("change", () => choice === "system" && apply());

  const recentChoices = () => [...new Set([palette, ...recent, ...SUGGESTED])].slice(0, 3);

  function el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (k === "text") node.textContent = v;
      else node.setAttribute(k, v);
    }
    node.append(...children);
    return node;
  }

  const swatch = (id) => el("span", { class: "kapi-swatch", "data-palette-preview": id, "aria-hidden": "true" }, el("span", { class: "kapi-swatch-ink" }));

  function openGallery() {
    import(new URL("theme-gallery.js", here)).then((m) => m.openThemeGallery());
  }

  function entries({ icon, close = () => {} }) {
    const item = (attrs, label, ...after) => el("button", { type: "button", tabindex: "-1", ...attrs }, icon("check"), el("span", { class: "kapi-menu-label", text: label }), ...after);
    const modes = MODES.map((name) => {
      const button = item({ role: "menuitemradio", "data-theme-choice": name }, MODE_LABELS[name], icon(name));
      button.addEventListener("click", () => set(name));
      return button;
    });
    let shown = recentChoices();
    const palettes = shown.map(() => {
      const button = item({ role: "menuitemradio", "data-palette": "" }, "", swatch("default"));
      button.addEventListener("click", () => setPalette(button.dataset.palette));
      return button;
    });
    const browse = el("button", { type: "button", tabindex: "-1", role: "menuitem", "data-theme-browse": "" }, el("span"), el("span", { class: "kapi-menu-label", text: "Browse all themes" }), icon("chevron"));
    browse.addEventListener("click", () => {
      close();
      openGallery();
    });
    const sync = () => {
      if (!shown.includes(palette)) shown = recentChoices();
      palettes.forEach((button, i) => {
        const p = paletteOf(shown[i]);
        button.dataset.palette = p.id;
        button.querySelector(".kapi-menu-label").textContent = p.label;
        button.querySelector(".kapi-swatch").dataset.palettePreview = p.id;
        button.setAttribute("aria-checked", String(p.id === palette));
      });
      for (const button of modes) button.setAttribute("aria-checked", String(button.dataset.themeChoice === choice));
    };
    document.addEventListener("kapi-theme", sync);
    document.addEventListener("kapi-palette", sync);
    sync();
    const rule = () => el("div", { class: "kapi-menu-sep", role: "separator" });
    return [...modes, rule(), ...palettes, rule(), browse];
  }

  function control({ icon, shortcuts }) {
    const toggle = el("button", { type: "button", class: "kapi-theme-button", "aria-label": "Theme", "data-tip": "Theme", "aria-haspopup": "menu", "aria-expanded": "false" }, icon("theme"));
    const list = el("div", { class: "kapi-theme-menu", role: "menu", "aria-label": "Theme" });
    list.hidden = true;
    const wrap = el("div", { class: "kapi-theme" }, toggle, list);
    let hovered = false;
    let timer = 0;
    const items = () => [...list.querySelectorAll('[role="menuitem"], [role="menuitemradio"]')];
    const open = (focus) => {
      clearTimeout(timer);
      list.hidden = false;
      toggle.setAttribute("aria-expanded", "true");
      if (focus) (items().find((b) => b.getAttribute("aria-checked") === "true") || items()[0])?.focus();
    };
    function close(refocus) {
      clearTimeout(timer);
      hovered = false;
      if (list.hidden) return;
      list.hidden = true;
      toggle.setAttribute("aria-expanded", "false");
      if (refocus) toggle.focus();
    }
    list.append(...entries({ icon, close: () => close(true) }));
    list.addEventListener("click", (event) => {
      if (event.target.closest("[data-theme-choice], [data-palette]")) close(wrap.contains(document.activeElement));
    });
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
      const all = items();
      const at = all.indexOf(document.activeElement);
      const to = shortcuts.is("menuNext", event) ? at + 1 : shortcuts.is("menuPrevious", event) ? at - 1 : shortcuts.is("menuFirst", event) ? 0 : shortcuts.is("menuLast", event) ? all.length - 1 : undefined;
      if (to !== undefined) {
        event.preventDefault();
        all[(to + all.length) % all.length].focus();
      } else if (shortcuts.is("menuBack", event)) {
        event.preventDefault();
        close(true);
      } else if (shortcuts.is("focusCycle", event)) close(false);
    });
    document.addEventListener("pointerdown", (event) => {
      if (!wrap.contains(event.target)) close(false);
    }, true);
    const tip = () => (toggle.dataset.tip = `Theme: ${MODE_LABELS[choice]}, ${paletteOf(palette).label}`);
    document.addEventListener("kapi-theme", tip);
    document.addEventListener("kapi-palette", tip);
    tip();
    return wrap;
  }

  window.kapiTheme = Object.freeze({
    modes: MODES,
    modeLabels: MODE_LABELS,
    palettes: PALETTES,
    get: () => choice,
    set,
    palette: () => palette,
    setPalette,
    entries,
    control,
    openGallery,
  });
  apply();
  listen();
})();
