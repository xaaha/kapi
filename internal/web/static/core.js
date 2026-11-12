export const root = location.pathname.match(/^\/[^/]+\//)[0];
export const pageID = location.pathname.match(/^\/[^/]+\/p\/([0-9a-f]{16})\//)?.[1] ?? null;
export const shortcuts = window.kapiShortcuts;

export async function getJSON(path) {
  const res = await fetch(root + path, { cache: "no-store" });
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return res.json();
}

export const prefs = Object.freeze({
  get(key) {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key, value) {
    try {
      if (value === null) localStorage.removeItem(key);
      else localStorage.setItem(key, value);
    } catch {}
  },
});

export function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "text") el.textContent = v;
    else if (v !== false && v != null) el.setAttribute(k, v === true ? "" : v);
  }
  el.append(...children.filter((c) => c != null));
  return el;
}

const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

export function modal({ dialog, scrim, trigger, initialFocus, onOpen, onClose, shouldDismiss = () => true }) {
  let before = null;
  let inerted = [];

  const isOpen = () => !dialog.hidden;
  const focusable = () => [...dialog.querySelectorAll(FOCUSABLE)].filter((el) => !el.hidden && el.getClientRects().length);
  const setBackgroundInert = (inert) => {
    if (inert) {
      inerted = [...document.body.children].filter((el) => el !== dialog && el !== scrim && !el.inert);
      for (const el of inerted) el.inert = true;
      return;
    }
    for (const el of inerted) el.inert = false;
    inerted = [];
  };
  const setOpen = (open) => {
    if (open === isOpen()) return;
    if (open) {
      before = document.activeElement;
      dialog.hidden = false;
      scrim.hidden = false;
      trigger?.setAttribute("aria-expanded", "true");
      setBackgroundInert(true);
      onOpen?.();
      (initialFocus?.() ?? focusable()[0] ?? dialog).focus();
      return;
    }
    dialog.hidden = true;
    scrim.hidden = true;
    trigger?.setAttribute("aria-expanded", "false");
    setBackgroundInert(false);
    onClose?.();
    if (before?.isConnected) before.focus({ preventScroll: true });
    else trigger?.focus({ preventScroll: true });
  };

  document.addEventListener("keydown", (event) => {
    if (!isOpen() || event.defaultPrevented) return;
    if (event.key === "Escape") {
      if (!shouldDismiss(event)) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      setOpen(false);
      return;
    }
    if (event.key !== "Tab") return;
    const items = focusable();
    if (!items.length) {
      event.preventDefault();
      dialog.focus();
      return;
    }
    const first = items[0];
    const last = items.at(-1);
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }, true);

  scrim.addEventListener("click", () => setOpen(false));
  trigger?.addEventListener("click", () => setOpen(!isOpen()));

  return Object.freeze({
    close: () => setOpen(false),
    isOpen,
    open: () => setOpen(true),
    toggle: () => setOpen(!isOpen()),
  });
}
