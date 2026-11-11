import { h, pageID, root, shortcuts } from "../../static/core.js";
import { ago, markVisited, matches, newIDs, onLayout, onVisit, pageTitle, redraw, stateLabel, visitTimes, watch } from "./pages.js";

const WORKING = new Set(["live", "waiting"]);
const RECENT_LIMIT = 7;

const href = (p) => `${root}p/${p.id}/`;

function noteRow(p, fresh) {
  return h("a", {
    class: "kapi-switcher-row",
    id: `kapi-switcher-page-${p.id}`,
    role: "option",
    "aria-selected": "false",
    "data-key": `page\n${p.id}`,
    href: href(p),
    title: `${p.path}\n${stateLabel(p)} · ${ago(p.changed)}`,
    "data-ended": p.state === "ended",
    "data-new": fresh || false,
  },
  h("span", { class: "nb-dot", "data-state": p.state, "aria-hidden": "true" }),
  h("span", { class: "kapi-switcher-copy" },
    h("strong", { text: pageTitle(p) }),
    h("span", { text: p.summary || stateLabel(p) })),
  h("time", { datetime: p.changed, text: ago(p.changed) }));
}

function section(title, pages, fresh) {
  if (!pages.length) return null;
  return h("section", { class: "kapi-switcher-section" },
    h("h2", { text: title }),
    ...pages.map((p) => noteRow(p, fresh.has(p.id))));
}

function render(list, pages, query, fresh) {
  const available = pages.filter((p) => p.id !== pageID && matches(p, query));
  const searching = query.trim() !== "";
  list.replaceChildren();
  if (!available.length) {
    list.append(h("p", { class: "kapi-switcher-empty", text: pages.length ? "No note matches." : "No notes yet." }));
    return;
  }
  if (searching) {
    list.append(section("Results", available, fresh));
    return;
  }
  const seen = visitTimes();
  const recentlyViewed = available.filter((p) => seen[p.id] > 0).sort((a, b) => seen[b.id] - seen[a.id]).slice(0, 3);
  const recentIDs = new Set(recentlyViewed.map((p) => p.id));
  const newPages = available.filter((p) => !recentIDs.has(p.id) && fresh.has(p.id));
  const newPageIDs = new Set(newPages.map((p) => p.id));
  const working = available.filter((p) => !recentIDs.has(p.id) && !newPageIDs.has(p.id) && WORKING.has(p.state));
  const workingIDs = new Set(working.map((p) => p.id));
  const others = available.filter((p) => !recentIDs.has(p.id) && !newPageIDs.has(p.id) && !workingIDs.has(p.id)).slice(0, RECENT_LIMIT);
  list.append(...[section("Recently viewed", recentlyViewed, fresh), section("New", newPages, fresh), section("Working now", working, fresh), section("Other notes", others, fresh)].filter(Boolean));
}

export function showSidebar() {
  document.title = "kapi";
  const launcher = h("button", {
    class: "kapi-switcher-launcher",
    type: "button",
    "aria-haspopup": "dialog",
    "aria-expanded": "false",
    title: `Switch notes (${shortcuts.key("switchNote").hint})`,
  }, h("span", { class: "kapi-switcher-launcher-icon", "aria-hidden": "true" }));
  const scrim = h("div", { class: "kapi-switcher-scrim", hidden: "" });
  const search = h("input", { class: "kapi-switcher-search", type: "search", placeholder: "Search notes…", "aria-label": "Search notes", autocomplete: "off", role: "combobox", "aria-controls": "kapi-switcher-list", "aria-autocomplete": "list" });
  const current = h("span", { class: "kapi-switcher-current" });
  const list = h("div", { class: "kapi-switcher-list", id: "kapi-switcher-list", role: "listbox" });
  const home = h("a", { class: "kapi-switcher-home", href: root }, h("span", { text: "Home · All notes" }), h("span", { "aria-hidden": "true", text: "↗" }));
  const dialog = h("div", { class: "kapi-switcher", role: "dialog", "aria-modal": "true", "aria-label": "Switch notes", hidden: "" },
    h("div", { class: "kapi-switcher-search-box" }, search, h("kbd", { text: shortcuts.key("switchNote").hint })),
    current,
    list,
    home);

  let pages = [];
  let fresh = new Set();
  let before = null;
  let activeID = null;
  const rows = () => [...list.querySelectorAll(".kapi-switcher-row")];
  const setActive = (row) => {
    for (const item of rows()) item.setAttribute("aria-selected", String(item === row));
    activeID = row?.id ?? null;
    if (row) search.setAttribute("aria-activedescendant", row.id);
    else search.removeAttribute("aria-activedescendant");
  };
  const draw = () => {
    redraw(list, () => render(list, pages, search.value, fresh));
    setActive((activeID && document.getElementById(activeID)) || rows()[0] || null);
  };
  const isOpen = () => !dialog.hidden;
  const setOpen = (open) => {
    if (open === isOpen()) return;
    dialog.hidden = !open;
    scrim.hidden = !open;
    launcher.setAttribute("aria-expanded", String(open));
    document.documentElement.toggleAttribute("data-kapi-switcher", open);
    if (open) {
      before = document.activeElement;
      search.focus();
      search.select();
    } else {
      search.value = "";
      draw();
      if (before?.isConnected) before.focus({ preventScroll: true });
      else launcher.focus({ preventScroll: true });
    }
  };

  search.addEventListener("input", draw);
  search.addEventListener("keydown", (e) => {
    const items = rows();
    const active = items.findIndex((row) => row.id === activeID);
    if ((e.key === "ArrowDown" || e.key === "ArrowUp") && items.length) {
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      setActive(items[(active + step + items.length) % items.length]);
    } else if (e.key === "Enter" && active >= 0) {
      e.preventDefault();
      location.href = items[active].href;
    }
  });
  list.addEventListener("keydown", (e) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const items = rows();
    const at = items.indexOf(document.activeElement);
    if (at < 0) return;
    e.preventDefault();
    const next = at + (e.key === "ArrowDown" ? 1 : -1);
    if (next < 0) search.focus();
    else items[Math.min(next, items.length - 1)].focus();
  });
  list.addEventListener("pointermove", (e) => {
    const row = e.target.closest?.(".kapi-switcher-row");
    if (row) setActive(row);
  });
  launcher.addEventListener("click", () => setOpen(!isOpen()));
  scrim.addEventListener("click", () => setOpen(false));
  dialog.addEventListener("click", (e) => e.stopPropagation());
  dialog.addEventListener("keydown", (e) => {
    if (e.key !== "Tab") return;
    const stops = [search, ...rows(), home];
    const at = stops.indexOf(document.activeElement);
    if ((!e.shiftKey && at === stops.length - 1) || (e.shiftKey && at <= 0)) {
      e.preventDefault();
      stops[e.shiftKey ? stops.length - 1 : 0].focus();
    }
  });
  onLayout(draw);
  onVisit(() => {
    fresh = newIDs(pages, pageID);
    launcher.toggleAttribute("data-new", fresh.size > 0);
    draw();
  });
  watch((next) => {
    if (pageID) markVisited(pageID, next);
    pages = next;
    fresh = newIDs(pages, pageID);
    const here = pages.find((p) => p.id === pageID);
    current.textContent = here ? `Current · ${pageTitle(here)}` : "Current note";
    launcher.toggleAttribute("data-new", fresh.size > 0);
    dialog.removeAttribute("data-error");
    draw();
  }, () => dialog.setAttribute("data-error", ""));

  document.addEventListener("keydown", (e) => {
    if (shortcuts.is("switchNote", e)) {
      e.preventDefault();
      setOpen(!isOpen());
    } else if (isOpen() && shortcuts.is("dismiss", e)) {
      e.preventDefault();
      setOpen(false);
    }
  });
  document.body.append(scrim, dialog, launcher);
}
