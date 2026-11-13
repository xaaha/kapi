import { getJSON, prefs, root } from "../../static/core.js";
import { isWorking } from "./states.js";

const REFRESH_MS = 10000;
const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

export const base = (path) => path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || path;
const dirname = (path) => path.replace(/[\\/][^\\/]*$/, "") || path;
export const foldersOf = (p) => (p.folders?.length ? p.folders : [dirname(p.path)]);
export const folderOf = (p) => foldersOf(p)[0];
export const inFolder = (p, folder) => foldersOf(p).includes(folder);

const META_KEY = "kapi-notebook-layout";
const META_EVENT = "kapi-notebook-layout";

function metadata() {
  try {
    const value = JSON.parse(prefs.get(META_KEY));
    if (value && typeof value === "object") return value;
  } catch {}
  return {};
}

function updateMetadata(change) {
  const value = metadata();
  change(value);
  prefs.set(META_KEY, JSON.stringify(value));
  dispatchEvent(new Event(META_EVENT));
}

const friendly = (name) => name.replace(/[._-]+/g, " ").trim().replace(/\b\p{L}/gu, (c) => c.toUpperCase());

export function folderTitle(folder) {
  const custom = metadata().folders?.[folder];
  if (custom) return custom;
  const name = base(folder);
  if (name.startsWith(".")) return `${friendly(base(dirname(folder))) || "Project"} pages`;
  return friendly(name) || "Pages";
}

export const pageTitle = (p) => metadata().pages?.[p.id] || p.title;

export function renameFolder(folder, name) {
  updateMetadata((value) => {
    value.folders ??= {};
    if (name) value.folders[folder] = name;
    else delete value.folders[folder];
  });
}

export function renamePage(id, name) {
  updateMetadata((value) => {
    value.pages ??= {};
    if (name) value.pages[id] = name;
    else delete value.pages[id];
  });
}

export const folderCollapsed = (folder) => metadata().collapsed?.[folder] === true;

export function setFolderCollapsed(folder, collapsed) {
  updateMetadata((value) => {
    value.collapsed ??= {};
    if (collapsed) value.collapsed[folder] = true;
    else delete value.collapsed[folder];
  });
}

function orderPages(folder, pages) {
  const order = metadata().order?.[folder] ?? [];
  if (!order.length) return pages;
  const positions = new Map(order.map((id, i) => [id, i]));
  return pages.map((p, i) => ({ p, i })).sort((a, b) => {
    const ai = positions.get(a.p.id);
    const bi = positions.get(b.p.id);
    if (ai == null && bi == null) return a.i - b.i;
    if (ai == null) return -1;
    if (bi == null) return 1;
    return ai - bi;
  }).map(({ p }) => p);
}

export function movePage(folder, ids, moved, target, after) {
  const order = ids.filter((id) => id !== moved);
  const at = order.indexOf(target);
  order.splice(at < 0 ? order.length : at + (after ? 1 : 0), 0, moved);
  updateMetadata((value) => {
    value.order ??= {};
    value.order[folder] = order;
  });
}

export function onLayout(run) {
  addEventListener(META_EVENT, run);
  addEventListener("storage", (e) => e.key === META_KEY && run());
}

export function ago(iso) {
  if (!iso) return "";
  const secs = (Date.parse(iso) - Date.now()) / 1000;
  for (const [unit, size] of [["year", 31536000], ["month", 2592000], ["week", 604800], ["day", 86400], ["hour", 3600], ["minute", 60]]) {
    if (Math.abs(secs) >= size) return rtf.format(Math.round(secs / size), unit);
  }
  return "just now";
}

export function matches(p, query) {
  const text = [pageTitle(p), p.title, p.summary ?? "", p.path, ...foldersOf(p).map(folderTitle)].join("\n");
  return window.kapiTextSearch.test(text, query);
}

export function byFolder(pages) {
  const out = new Map();
  for (const p of pages) {
    for (const folder of foldersOf(p)) {
      if (!out.has(folder)) out.set(folder, []);
      out.get(folder).push(p);
    }
  }
  for (const [folder, items] of out) out.set(folder, orderPages(folder, items));
  return out;
}

// The unfiltered Home view uses each note's primary folder so a note appears once
// and the order chosen in the organizer remains visible outside the dialog.
export function orderForHome(pages) {
  const groups = new Map();
  for (const page of pages) {
    const folder = folderOf(page);
    if (!groups.has(folder)) groups.set(folder, []);
    groups.get(folder).push(page);
  }
  return [...groups].flatMap(([folder, items]) => orderPages(folder, items));
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
  try {
    const worker = new SharedWorker(new URL("./stream.js", import.meta.url), { name: "kapi-notebook" });
    worker.port.onmessage = load;
    worker.port.start();
  } catch {
    new EventSource(`${root}api/notebook/events`).onmessage = load;
  }
  setInterval(() => document.visibilityState === "visible" && load(), REFRESH_MS);
  document.addEventListener("visibilitychange", () => document.visibilityState === "visible" && load());
}

const VISITS_KEY = "kapi-notebook-visits";

export const onVisit = (run) => addEventListener("storage", (e) => e.key === VISITS_KEY && run());

function visits() {
  let v = null;
  try {
    v = JSON.parse(prefs.get(VISITS_KEY));
  } catch {}
  if (!v || typeof v.since !== "number" || typeof v.ids !== "object") {
    v = { since: Date.now(), ids: {} };
    prefs.set(VISITS_KEY, JSON.stringify(v));
  }
  return v;
}

export function markVisited(id, pages) {
  const v = visits();
  v.ids[id] = Date.now();
  if (pages) for (const known of Object.keys(v.ids)) if (!pages.some((p) => p.id === known)) delete v.ids[known];
  prefs.set(VISITS_KEY, JSON.stringify(v));
}

export function visitTimes() {
  return visits().ids;
}

// Shared priority for home and the note switcher. Never duplicate a note across groups.
export function noteGroups(pages, fresh, otherLimit = Infinity) {
  const seen = visitTimes();
  const recent = pages.filter((p) => seen[p.id] > 0).sort((a, b) => seen[b.id] - seen[a.id]).slice(0, 3);
  const picked = new Set(recent.map((p) => p.id));
  const take = (test) => pages.filter((p) => {
    if (picked.has(p.id) || !test(p)) return false;
    picked.add(p.id);
    return true;
  });
  return [
    ["Recently viewed", recent],
    ["New", take((p) => fresh.has(p.id))],
    ["Working now", take(isWorking)],
    ["Other notes", take(() => true).slice(0, otherLimit)],
  ].filter(([, notes]) => notes.length);
}

export function newIDs(pages, current) {
  const v = visits();
  return new Set(pages.filter((p) => p.id !== current && p.opened && Date.parse(p.opened) > Math.max(v.ids[p.id] ?? 0, v.since)).map((p) => p.id));
}
