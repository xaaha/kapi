import { h, root, shortcuts } from "../../static/core.js";
import { ago, folderOf, folderTitle, inFolder, matches, newIDs, onLayout, onVisit, pageTitle, redraw, stateLabel, watch } from "./pages.js";

const href = (p) => `${root}p/${p.id}/`;
const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

function card(p, fresh) {
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
    h("span", { class: "nb-card-project", text: folderTitle(folderOf(p)) }),
    h("span", { class: "nb-dot", "data-state": p.state, title: stateLabel(p), "aria-label": stateLabel(p), role: "img" })),
  h("strong", { class: "nb-card-title", text: title }),
  h("span", { class: "nb-card-summary", text: p.summary || "No summary yet." }),
  h("span", { class: "nb-card-foot" },
    h("span", { class: "nb-card-activity", text: `${stateLabel(p)} · ${ago(p.changed)}` }),
    fresh ? h("span", { class: "nb-new", text: "New" }) : null));
}

function statusMatches(p, status) {
  if (status === "active") return p.state === "live" || p.state === "waiting";
  if (status === "inactive") return p.state === "idle";
  if (status === "ended") return p.state === "ended";
  return true;
}

export function showHome(main) {
  document.title = "kapi";
  const search = h("input", { class: "nb-search", type: "search", placeholder: "Search notes", "aria-label": "Search notes", autocomplete: "off" });
  const project = h("select", { class: "nb-select", "aria-label": "Folder" }, h("option", { value: "", text: "All folders" }));
  const status = h("div", { class: "nb-status", role: "group", "aria-label": "Status" });
  const count = h("span", { class: "nb-count" });
  const grid = h("div", { class: "nb-grid" });
  const empty = h("p", { class: "nb-empty" });
  const statuses = [["active", "Active"], ["inactive", "Inactive"], ["ended", "Ended"], ["", "All"]];
  let pickedStatus = "active";
  const statusButtons = statuses.map(([value, label]) => {
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

  const brand = h("a", { class: "nb-brand", href: root }, h("span", { class: "nb-logo", "aria-hidden": "true" }), h("span", { text: "Kapi" }));
  const top = h("header", { class: "nb-top" }, brand,
    h("label", { class: "nb-search-box" }, h("span", { class: "nb-search-icon", "aria-hidden": "true" }), search, h("kbd", { text: shortcuts.key("searchPage").hint })));
  const toolbar = h("div", { class: "nb-toolbar" }, status, h("div", { class: "nb-toolbar-right" }, project, count));
  const header = h("div", { class: "nb-title-row" }, h("div", {}, h("p", { class: "nb-eyebrow", text: "Your temporary workspace" }), h("h1", { text: "All notes" })), h("p", { class: "nb-intro", text: "Recent work from you and your agents, in one searchable place." }));
  const library = h("section", { class: "nb-library", "aria-label": "Notes" }, toolbar, grid, empty);
  main.append(h("div", { class: "nb-home" }, top, h("main", { class: "nb-main" }, header, library)));

  let pages = [];
  let fresh = new Set();
  const updateProjects = () => {
    const folders = new Set(pages.filter((p) => statusMatches(p, pickedStatus)).flatMap((p) => p.folders?.length ? p.folders : [folderOf(p)]));
    const selected = project.value;
    project.replaceChildren(h("option", { value: "", text: "All folders" }), ...[...folders].map((folder) => h("option", { value: folder, text: folderTitle(folder) })));
    if ([...folders].includes(selected)) project.value = selected;
  };
  const draw = () => redraw(library, () => {
    const shown = pages.filter((p) => matches(p, search.value) && (!project.value || inFolder(p, project.value)) && statusMatches(p, pickedStatus));
    count.textContent = plural(shown.length, "note");
    grid.replaceChildren(...shown.map((p) => card(p, fresh.has(p.id))));
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
    if (document.activeElement !== search && (shortcuts.is("searchPage", e) || (shortcuts.is("search", e) && !e.target.closest?.("input, textarea, select")))) {
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
    updateProjects();
    main.removeAttribute("data-error");
    draw();
  }, () => main.setAttribute("data-error", ""));
}
