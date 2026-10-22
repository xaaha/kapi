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

  dark.addEventListener("change", () => choice === "system" && apply());
  addEventListener("storage", (e) => {
    if (e.key !== KEY && e.key !== null) return;
    choice = stored();
    apply();
  });

  window.kapiTheme = { choices: CHOICES, get: () => choice, set };
  apply();
})();
