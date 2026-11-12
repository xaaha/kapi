import { h, root, shortcuts } from "../../static/core.js";
import { createOrganizer } from "./organizer.js";
import { ago, byFolder, folderOf, folderTitle, foldersOf, matches, newIDs, onLayout, onVisit, pageTitle, redraw, watch } from "./pages.js";
import { noteSearch } from "./searchbox.js";
import { STATUS_FILTERS, stateLabel, stateMatches, stateTone } from "./states.js";

const href = (p) => `${root}p/${p.id}/`;
const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

function card(p, fresh, folder) {
  const title = pageTitle(p);
  return h("a", {
    class: "nb-card",
    "data-key": `card\n${p.id}`,
    href: href(p),
    title: p.path,
    "data-ended": p.state === "ended",
    "data-new": fresh || false,
  },
  h("span", { class: "nb-card-top" },
    h("span", { class: "nb-glyph", "aria-hidden": "true", text: [...title.trim()][0]?.toUpperCase() ?? "?" }),
    h("span", { class: "nb-card-project", text: folderTitle(folder ?? folderOf(p)) }),
    h("span", { class: "nb-dot", "data-state": p.state, "data-tone": stateTone(p), title: stateLabel(p), "aria-label": stateLabel(p), role: "img" })),
  h("strong", { class: "nb-card-title", text: title }),
  h("span", { class: "nb-card-summary", text: p.summary || "No summary yet." }),
  h("span", { class: "nb-card-foot" },
    h("span", { class: "nb-card-activity", text: `${stateLabel(p)} · ${ago(p.changed)}` }),
    fresh ? h("span", { class: "nb-new", text: "New" }) : null));
}

export function showHome(main) {
  document.title = "kapi";
  const { box: searchBox, input: search } = noteSearch();
  const project = h("select", { class: "nb-select", "aria-label": "Folder" }, h("option", { value: "", text: "All folders" }));
  const status = h("div", { class: "nb-status", role: "group", "aria-label": "Status" });
  const count = h("span", { class: "nb-count" });
  const grid = h("div", { class: "nb-grid" });
  const empty = h("p", { class: "nb-empty" });
  let pickedStatus = "active";
  const statusButtons = STATUS_FILTERS.map(({ value, label }) => {
    const button = h("button", { type: "button", class: "nb-status-button", "aria-pressed": String(value === pickedStatus), text: label });
    button.addEventListener("click", () => {
      pickedStatus = value;
      for (const item of statusButtons) item.setAttribute("aria-pressed", String(item === button));
      updateProjects();
      draw();
    });
    return button;
  });
  status.append(...statusButtons);

  const brand = h("a", { class: "kapi-site-home", href: root }, h("span", { class: "kapi-logo", "aria-hidden": "true" }), h("span", { class: "kapi-site-name", text: "Kapi" }));
  const theme = window.kapiTheme?.control?.();
  const top = h("header", { class: "nb-top kapi-site-head" }, brand,
    h("div", { class: "kapi-site-actions nb-top-actions" }, searchBox, theme));
  const organizer = createOrganizer();
  const toolbar = h("div", { class: "nb-toolbar" }, status, h("div", { class: "nb-toolbar-right" }, organizer.button, project, count));
  const header = h("div", { class: "nb-title-row" }, h("div", {}, h("p", { class: "nb-eyebrow", text: "Your temporary workspace" }), h("h1", { text: "All notes" })), h("p", { class: "nb-intro", text: "Recent work from you and your agents, in one searchable place." }));
  const library = h("section", { class: "nb-library", "aria-label": "Notes" }, toolbar, grid, empty);
  main.append(h("div", { class: "nb-home" }, top, h("main", { class: "nb-main" }, header, library)));

  let pages = [];
  let fresh = new Set();
  const updateProjects = () => {
    const folders = new Set(pages.filter((p) => stateMatches(p, pickedStatus)).flatMap(foldersOf));
    const selected = project.value;
    project.replaceChildren(h("option", { value: "", text: "All folders" }), ...[...folders].map((folder) => h("option", { value: folder, text: folderTitle(folder) })));
    if ([...folders].includes(selected)) project.value = selected;
  };
  const draw = () => redraw(library, () => {
    const candidates = project.value ? byFolder(pages).get(project.value) ?? [] : pages;
    const shown = candidates.filter((p) => matches(p, search.value) && stateMatches(p, pickedStatus));
    count.textContent = plural(shown.length, "note");
    grid.replaceChildren(...shown.map((p) => card(p, fresh.has(p.id), project.value || null)));
    empty.hidden = shown.length > 0;
    empty.textContent = pages.length ? "No note matches these filters." : "No notes yet. Ask an agent to create one, or run kapi open notes.html.";
  });

  search.addEventListener("input", draw);
  search.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" || e.isComposing || e.keyCode === 229) return;
    const first = grid.querySelector(".nb-card");
    if (!first) return;
    e.preventDefault();
    first.focus();
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
  document.addEventListener("keydown", (e) => {
    if (document.activeElement !== search && (shortcuts.is("switchNote", e) || (shortcuts.is("search", e) && !e.target.closest?.("input, textarea, select")))) {
      e.preventDefault();
      search.focus();
      search.select();
    } else if (document.activeElement === search && shortcuts.is("dismiss", e)) {
      search.value = "";
      search.blur();
      draw();
    }
  });
  watch((next) => {
    pages = next;
    fresh = newIDs(pages, null);
    organizer.setPages(pages);
    updateProjects();
    main.removeAttribute("data-error");
    draw();
  }, () => main.setAttribute("data-error", ""));
}
