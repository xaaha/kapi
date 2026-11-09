import { getJSON, prefs, root } from "../../static/core.js";

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

const LABELS = { live: "Active · agent listening", waiting: "Active · agent not listening", idle: "Inactive", ended: "Session ended" };
const RANK = ["live", "waiting", "idle", "ended"];

export const stateLabel = (p) => (p.missing ? "File moved or deleted" : LABELS[p.state] ?? "");

export const liveliest = (pages) => RANK.find((s) => pages.some((p) => p.state === s)) ?? "idle";

export const legend = (h) => h("span", { class: "nb-legend" }, ...RANK.map((s) => h("span", {}, h("span", { class: "nb-dot", "data-state": s, "aria-hidden": "true" }), LABELS[s])));

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

export function newIDs(pages, current) {
  const v = visits();
  return new Set(pages.filter((p) => p.id !== current && p.opened && Date.parse(p.opened) > Math.max(v.ids[p.id] ?? 0, v.since)).map((p) => p.id));
}
