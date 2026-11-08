import { getJSON } from "../../static/core.js";

const REFRESH_MS = 10000;
const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

export const base = (path) => path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || path;
const dirname = (path) => path.replace(/[\\/][^\\/]*$/, "") || path;
export const foldersOf = (p) => (p.folders?.length ? p.folders : [dirname(p.path)]);
export const folderOf = (p) => foldersOf(p)[0];
export const inFolder = (p, folder) => foldersOf(p).includes(folder);

export function ago(iso) {
  if (!iso) return "";
  const secs = (Date.parse(iso) - Date.now()) / 1000;
  for (const [unit, size] of [["year", 31536000], ["month", 2592000], ["week", 604800], ["day", 86400], ["hour", 3600], ["minute", 60]]) {
    if (Math.abs(secs) >= size) return rtf.format(Math.round(secs / size), unit);
  }
  return "just now";
}

export function matches(p, query) {
  const q = query.trim().toLowerCase();
  return !q || [p.title, p.summary ?? "", p.path].some((s) => s.toLowerCase().includes(q));
}

export function byFolder(pages) {
  const out = new Map();
  for (const p of pages) {
    for (const folder of foldersOf(p)) {
      if (!out.has(folder)) out.set(folder, []);
      out.get(folder).push(p);
    }
  }
  return out;
}

const TONES = ["green", "orange", "purple", "blue", "yellow", "red"];

const hash = (s) => [...s].reduce((n, c) => (n * 31 + c.charCodeAt(0)) >>> 0, 7);

export function tones(folders) {
  const out = new Map();
  const taken = new Set();
  for (const folder of [...folders].sort()) {
    let i = hash(folder) % TONES.length;
    for (let tries = 0; taken.has(TONES[i]) && tries < TONES.length; tries++) i = (i + 1) % TONES.length;
    taken.add(TONES[i]);
    if (taken.size === TONES.length) taken.clear();
    out.set(folder, TONES[i]);
  }
  return out;
}

export function redraw(container, build) {
  const key = container.contains(document.activeElement) ? document.activeElement.dataset.key : undefined;
  build();
  if (key) container.querySelector(`[data-key="${CSS.escape(key)}"]`)?.focus({ preventScroll: true });
}

const MINUTE = 60000;

export function watch(show, fail) {
  let loading = false;
  let again = false;
  let last = "";
  const load = async () => {
    if (loading) {
      again = true;
      return;
    }
    loading = true;
    try {
      const pages = await getJSON("api/notebook/pages");
      const seen = `${Math.floor(Date.now() / MINUTE)}:${JSON.stringify(pages)}`;
      if (seen !== last) {
        last = seen;
        show(pages);
      }
    } catch {
      last = "";
      fail();
    } finally {
      loading = false;
      if (again) {
        again = false;
        load();
      }
    }
  };
  load();
  setInterval(() => document.visibilityState === "visible" && load(), REFRESH_MS);
  document.addEventListener("visibilitychange", () => document.visibilityState === "visible" && load());
}
