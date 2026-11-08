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
