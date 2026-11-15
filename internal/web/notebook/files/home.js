import { getJSON, h, icon, prefs, root, shortcuts } from "../../static/core.js";
import { createOrganizer } from "./organizer.js";
import { ago, byFolder, dirname, folderOf, folderTitle, foldersOf, newIDs, onLayout, onVisit, orderForHome, pageTitle, redraw, watch } from "./pages.js";
import { cardMenu, undoNote } from "./trash.js";
import { searchTrigger } from "../../static/search-dialog.js";
import { registerNotesSearch } from "./sidebar.js";
import { ARCHIVE_NOTICE, VIEWS, badge, bannerCopy, policyOf } from "./lifecycle.js";
import { stateLabel, stateTone } from "./states.js";

const href = (p) => `${root}p/${p.id}/`;
const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

export function savedFolders(pages) {
  const folders = new Map();
  for (const p of pages) {
    const dir = dirname(p.path);
    if (!folders.has(dir)) folders.set(dir, { dir, shown: p.savedIn || dir, pages: [] });
    folders.get(dir).pages.push(p);
  }
  return [...folders.values()];
}

function card(p, fresh, folder, menu, savedBadge = true) {
  const title = pageTitle(p);
  return h("div", { class: "nb-card-wrap" }, link(p, fresh, folder, title, savedBadge), menu.button(p, title));
}

function link(p, fresh, folder, title, savedBadge) {
  return h("a", {
    class: "nb-card",
    "data-key": `card\n${p.id}`,
    href: href(p),
    title: p.path,
    "data-ended": p.state === "ended",
    "data-archived": Boolean(p.archivedAt),
    "data-new": fresh || false,
  },
  h("span", { class: "nb-card-top" },
    h("span", { class: "nb-glyph", "aria-hidden": "true", text: [...title.trim()][0]?.toUpperCase() ?? "?" }),
    h("span", { class: "nb-card-project", text: folderTitle(folder ?? folderOf(p)) }),
    h("span", { class: "nb-dot", "data-state": p.state, "data-tone": stateTone(p), title: stateLabel(p), "aria-label": stateLabel(p), role: "img" })),
  h("strong", { class: "nb-card-title", text: title }),
  h("span", { class: "nb-card-summary", text: p.summary || "No summary yet." }),
  h("span", { class: "nb-card-foot" },
    badge(p, { saved: savedBadge }),
    h("span", { class: "nb-card-activity", text: `${stateLabel(p)} · ${ago(p.changed)}` }),
    fresh ? h("span", { class: "nb-new", text: "New" }) : null));
}

export function showHome(main) {
  document.title = "kapi";
  registerNotesSearch();
  const searchButton = searchTrigger();
  const project = h("select", { class: "nb-select", "aria-label": "Folder" }, h("option", { value: "", text: "All folders" }));
  const tabs = h("div", { class: "nb-status", role: "group", "aria-label": "View" });
  const count = h("span", { class: "nb-count" });
  const grid = h("div", { class: "nb-grid", "data-view": prefs.get("kapi-home-view") === "list" ? "list" : "cards" });
  const viewButtons = ["cards", "list"].map((view) => {
    const button = h("button", { type: "button", class: "nb-view-button", "aria-pressed": String(grid.dataset.view === view), text: view === "cards" ? "Cards" : "List" });
    button.addEventListener("click", () => {
      grid.dataset.view = view;
      for (const g of shelf.querySelectorAll(".nb-grid")) g.dataset.view = view;
      prefs.set("kapi-home-view", view);
      for (const item of viewButtons) item.setAttribute("aria-pressed", String(item === button));
    });
    return button;
  });
  const viewToggle = h("div", { class: "nb-view-toggle", role: "group", "aria-label": "Note view" }, ...viewButtons);
  const empty = h("p", { class: "nb-empty" });
  const shelf = h("div", { class: "nb-shelf", hidden: true });
  let picked = VIEWS.find((view) => location.hash === `#${view.name}`) ?? VIEWS[0];
  let settings = null;
  const contextTitle = h("strong");
  const contextDescription = h("p");
  const context = h("div", { class: "nb-filter-context", role: "status" }, contextTitle, contextDescription);
  const bannerTitle = h("strong");
  const bannerText = h("p");
  const gotIt = h("button", { type: "button", class: "nb-banner-dismiss", text: "Got it" });
  const banner = h("div", { class: "nb-banner", role: "status" }, h("div", { class: "nb-banner-copy" }, bannerTitle, bannerText), h("div", { class: "nb-banner-actions" }, gotIt));
  banner.hidden = true;
  const tabButtons = VIEWS.map((view) => {
    const button = h("button", { type: "button", class: "nb-status-button", "data-tab": view.name, "aria-pressed": String(view === picked), text: view.label });
    button.addEventListener("click", () => {
      picked = view;
      history.replaceState(null, "", view === VIEWS[0] ? location.pathname : `#${view.name}`);
      updateProjects();
      draw();
    });
    return button;
  });
  tabs.append(...tabButtons);

  const brand = h("a", { class: "kapi-site-home", href: root }, h("span", { class: "kapi-logo", "aria-hidden": "true" }), h("span", { class: "kapi-site-name", text: "Kapi" }));
  const theme = window.kapiTheme?.control?.({ icon, shortcuts });
  const top = h("header", { class: "nb-top kapi-site-head" }, brand,
    h("div", { class: "kapi-site-actions nb-top-actions" }, searchButton, theme));
  const organizer = createOrganizer();
  const toolbar = h("div", { class: "nb-toolbar" }, tabs, h("div", { class: "nb-toolbar-right" }, viewToggle, organizer.button, h("span", { class: "nb-select-wrap" }, project, h("span", { class: "nb-select-caret", "aria-hidden": "true" })), count));
  const title = h("h1");
  const intro = h("p", { class: "nb-intro" });
  const header = h("div", { class: "nb-title-row" }, h("div", {}, h("p", { class: "nb-eyebrow", text: "Your workspace" }), title), intro);
  const library = h("section", { class: "nb-library", "aria-label": "Notes" }, toolbar, banner, context, grid, shelf, empty);
  const note = undoNote();
  const menu = cardMenu(note);
  main.append(h("div", { class: "nb-home" }, top, h("main", { class: "nb-main" }, header, library)), menu.el, note.el);
  const arrived = new URLSearchParams(location.search);
  if (/^[0-9a-f]{16}$/.test(arrived.get("deleted") ?? "")) {
    note.deleted({ id: arrived.get("deleted"), title: arrived.get("title") || "the note", kept: arrived.get("kept") === "1" });
    history.replaceState(history.state, "", location.pathname);
  }

  let pages = [];
  let fresh = new Set();
  const updateProjects = () => {
    const folders = new Set(pages.filter(picked.shows).flatMap(foldersOf));
    const selected = project.value;
    project.replaceChildren(h("option", { value: "", text: "All folders" }), ...[...folders].map((folder) => h("option", { value: folder, text: folderTitle(folder) })));
    if ([...folders].includes(selected)) project.value = selected;
  };
  const draw = () => redraw(library, () => {
    const candidates = project.value ? byFolder(pages).get(project.value) ?? [] : orderForHome(pages);
    const shown = picked.order(candidates.filter(picked.shows));
    const policy = policyOf(settings);
    VIEWS.forEach((view, i) => {
      const total = candidates.filter(view.shows).length;
      tabButtons[i].dataset.count = total;
      tabButtons[i].setAttribute("aria-label", `${view.label} ${total}`);
      tabButtons[i].setAttribute("aria-pressed", String(view === picked));
    });
    title.textContent = picked.label;
    intro.textContent = settings ? picked.intro(policy) : "";
    context.hidden = !picked.context;
    if (picked.context) [contextTitle.textContent, contextDescription.textContent] = picked.context(policy);
    const copy = bannerCopy(policy);
    banner.hidden = !settings || !copy || picked !== VIEWS[0] || Boolean(settings.dismissed?.includes(ARCHIVE_NOTICE));
    if (copy) [bannerTitle.textContent, bannerText.textContent] = copy;
    count.textContent = plural(shown.length, "note");
    grid.hidden = Boolean(picked.grouped);
    shelf.hidden = !picked.grouped;
    if (picked.grouped) {
      grid.replaceChildren();
      shelf.replaceChildren(...savedFolders(shown).map((f) => h("section", { class: "nb-folder", "aria-label": folderTitle(f.dir) },
        h("h2", { class: "nb-folder-head", text: folderTitle(f.dir) }, h("span", { text: `${f.shown} · ${plural(f.pages.length, "note")}` })),
        h("div", { class: "nb-grid", "data-view": grid.dataset.view }, ...f.pages.map((p) => card(p, fresh.has(p.id), f.dir, menu, false))))));
    } else {
      shelf.replaceChildren();
      grid.replaceChildren(...shown.map((p) => card(p, fresh.has(p.id), project.value || null, menu)));
    }
    menu.redrawn();
    empty.hidden = shown.length > 0;
    empty.textContent = !pages.length ? "No notes yet. Ask an agent to create one, or run kapi open notes.html." : project.value ? "No note matches these filters." : picked.empty;
  });

  let loaded = false;
  const adopt = (next) => {
    settings = next;
    if (loaded) draw();
  };
  getJSON("settings").then(adopt, () => {});
  document.addEventListener("kapi-settings", (e) => adopt(e.detail));
  gotIt.addEventListener("click", async () => {
    const before = settings;
    adopt({ ...settings, dismissed: [...(settings.dismissed ?? []), ARCHIVE_NOTICE] });
    try {
      const res = await fetch(`${root}settings`, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ dismiss: ARCHIVE_NOTICE }) });
      if (!res.ok) throw new Error(`settings: ${res.status}`);
      adopt(await res.json());
    } catch {
      adopt(before);
    }
  });

  project.addEventListener("change", draw);
  onLayout(() => {
    updateProjects();
    draw();
  });
  onVisit(() => {
    fresh = newIDs(pages, null);
    draw();
  });
  watch((next) => {
    pages = next;
    loaded = true;
    fresh = newIDs(pages, null);
    organizer.setPages(pages);
    updateProjects();
    main.removeAttribute("data-error");
    draw();
  }, () => main.setAttribute("data-error", ""));
}
