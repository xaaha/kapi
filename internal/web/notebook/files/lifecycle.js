import { h, icon } from "../../static/core.js";

const MINUTE = 60000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const SOON = 6 * HOUR;
const UNITS = Object.freeze({ ns: 1e-6, us: 1e-3, "µs": 1e-3, ms: 1, s: 1000, m: MINUTE, h: HOUR });

export const ARCHIVE_NOTICE = "archive-intro";

const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

export function durationMs(text) {
  let total = 0;
  for (const [, amount, unit] of String(text ?? "").matchAll(/(\d+(?:\.\d+)?)(ns|us|µs|ms|h|m|s)/g)) total += Number(amount) * UNITS[unit];
  return total;
}

export function spell(ms) {
  const hours = ms / HOUR;
  if (hours > 24 && Number.isInteger(hours / 24)) return plural(hours / 24, "day");
  if (hours >= 1 && Number.isInteger(hours)) return plural(hours, "hour");
  return plural(Math.max(1, Math.round(ms / MINUTE)), "minute");
}

export function policyOf(settings) {
  const r = settings?.retention ?? {};
  return Object.freeze({ archiveAfter: durationMs(r.archiveAfter ?? "24h"), deleteAfter: durationMs(r.deleteAfter ?? "720h") });
}

const isArchived = (p) => Boolean(p.archivedAt);
const time = (iso) => (iso ? Date.parse(iso) : Infinity);
const soonestDeletion = (pages) => pages.map((p, i) => ({ p, i })).sort((a, b) => time(a.p.deletesAt) - time(b.p.deletesAt) || time(a.p.archivedAt) - time(b.p.archivedAt) || a.i - b.i).map(({ p }) => p);

export const VIEWS = Object.freeze([
  Object.freeze({
    name: "recent",
    label: "Recent",
    shows: (p) => !isArchived(p),
    order: (pages) => pages,
    intro: (policy) => `Everything you and your agents touched lately.${policy.archiveAfter ? ` Untouched notes archive after ${spell(policy.archiveAfter)}.` : ""}`,
    context: null,
    empty: "Nothing recent. Older notes are in Archived.",
  }),
  Object.freeze({
    name: "saved",
    label: "Saved",
    shows: (p) => Boolean(p.saved),
    order: (pages) => pages,
    intro: () => "Notes you chose to keep, in their library folders. Most recently viewed first.",
    context: null,
    grouped: true,
    empty: "Nothing saved yet. Open a note and choose Save to library in its menu to keep it.",
  }),
  Object.freeze({
    name: "archived",
    label: "Archived",
    shows: isArchived,
    order: soonestDeletion,
    intro: (policy) => (policy.archiveAfter ? `Notes nobody touched for ${spell(policy.archiveAfter)}.` : "Archiving is off, so nothing new moves here.") + (policy.deleteAfter ? ` Each one is deleted ${spell(policy.deleteAfter)} after it was archived.` : ""),
    context: (policy) => [
      policy.deleteAfter ? `Deleted ${spell(policy.deleteAfter)} after archiving` : "Kept until you delete them",
      `Edit a note to bring it back to Recent. Save it to keep it for good.${policy.deleteAfter ? " The note closest to deletion is first." : ""}`,
    ],
    empty: "Nothing archived.",
  }),
]);

function pill(kind, name, text) {
  return h("span", { class: `nb-badge nb-badge-${kind}` }, icon(name), text);
}

export function badge(p, { now = Date.now(), saved = true } = {}) {
  if (p.saved) return saved ? pill("saved", "bookmark", "Saved") : null;
  if (isArchived(p)) {
    const left = time(p.deletesAt) - now;
    return pill("archived", "archive", Number.isFinite(left) && left > 0 ? `Archived · ${plural(Math.ceil(left / DAY), "day")} left` : "Archived");
  }
  if (!p.archivesAt) return null;
  const left = time(p.archivesAt) - now;
  if (left >= SOON) return null;
  if (left <= 0) return pill("soon", "clock", "Archives soon");
  return pill("soon", "clock", left >= HOUR ? `Archives in ${Math.floor(left / HOUR)}h` : `Archives in ${Math.ceil(left / MINUTE)}m`);
}

export function bannerCopy(policy) {
  if (!policy.archiveAfter) return null;
  return [
    "Kapi now tidies up after itself",
    `Notes nobody edits for ${spell(policy.archiveAfter)} move to Archived.${policy.deleteAfter ? ` Archived notes are deleted after ${spell(policy.deleteAfter)}.` : ""} Save a note to keep it.`,
  ];
}
