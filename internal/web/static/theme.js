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
  const PALETTES = ["default", "paper"];

  function storedPalette() {
    try {
      const v = localStorage.getItem(PALETTE_KEY);
      return PALETTES.includes(v) ? v : "default";
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
    if (!PALETTES.includes(next)) return;
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

  window.kapiTheme = { choices: CHOICES, get: () => choice, set, palettes: PALETTES, palette: () => palette, setPalette };
  apply();
  applyPalette();
})();
