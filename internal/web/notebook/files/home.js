import { h, root, shortcuts } from "../../static/core.js";
import { ago, base, byFolder, folderOf, inFolder, legend, liveliest, matches, newIDs, onVisit, redraw, stateLabel, watch } from "./pages.js";

const SHOWN_PER_PROJECT = 3;

const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;
const href = (p) => `${root}p/${p.id}/`;

const dot = (state, label) => h("span", { class: "nb-dot", "data-state": state, title: label, "aria-label": label, role: "img" });
const pageDot = (p) => dot(p.missing ? "ended" : p.state, stateLabel(p));
const dim = (p) => p.missing || p.state === "ended";

function sheet(p) {
  if (!p) {
    return h("div", { class: "nb-sheet nb-sheet-empty" },
      h("h2", { text: "Nothing on your desk yet." }),
      h("p", { text: "Ask an agent to make a page, or run kapi open notes.html in any folder. It shows up here." }));
  }
  const folder = folderOf(p);
  return h("a", { class: "nb-sheet", "data-key": "sheet", href: href(p), "data-state": p.state },
    h("div", { class: "nb-sheet-meta" },
      pageDot(p),
      h("span", { text: `${stateLabel(p)} \u00b7 ${base(folder)}` }),
      h("time", { datetime: p.changed, text: ago(p.changed) })),
    h("h2", { text: p.title }),
    p.summary ? h("p", { text: p.summary }) : null,
    h("span", { class: "nb-continue", text: "Continue ↗" }));
}

function projectList(groups, pick) {
  const list = h("ul", { class: "nb-projects" });
  for (const [folder, items] of groups) {
    const button = h("button", { type: "button", class: "nb-project", "data-key": `project\n${folder}`, title: `Show every page in ${folder}` },
      dot(liveliest(items), `Most active page: ${stateLabel({ state: liveliest(items) })}`),
      h("span", { class: "nb-project-name", text: base(folder) }),
      h("span", { class: "nb-project-count", text: plural(items.length, "page") }));
    button.addEventListener("click", () => pick(folder));
    const recent = h("ul", { class: "nb-project-pages" }, ...items.slice(0, SHOWN_PER_PROJECT).map((p) =>
      h("li", {}, h("a", { href: href(p), title: p.path, "data-key": `recent\n${folder}\n${p.id}` }, pageDot(p), h("span", { text: p.title }), h("time", { datetime: p.changed, text: ago(p.changed) })))));
    list.append(h("li", { class: "nb-project-item" }, button, recent));
  }
  return list;
}

function card(p, picked, fresh) {
  const folder = picked ?? folderOf(p);
  return h("a", { class: "nb-card", "data-key": `card\n${p.id}`, href: href(p), title: p.path, "data-dim": dim(p), "data-new": fresh },
    h("span", { class: "nb-card-top" },
      h("span", { class: "nb-glyph", "aria-hidden": "true", text: [...p.title.trim()][0]?.toUpperCase() ?? "?" }),
      h("span", { class: "nb-card-folder", text: base(folder) }),
      fresh ? h("span", { class: "nb-new", text: "New" }) : null,
      pageDot(p)),
    h("span", { class: "nb-card-title", text: p.title }),
    p.summary ? h("span", { class: "nb-card-summary", text: p.summary }) : null,
    h("span", { class: "nb-card-foot", text: [stateLabel(p), ago(p.changed)].filter(Boolean).join(" · ") }));
}

function heading(title, note) {
  return h("div", { class: "nb-heading" }, h("h2", { text: title }), note ? h("span", { text: note }) : null);
}

export function showHome(main) {
  document.title = "Notebook · kapi";
  const search = h("input", { class: "nb-search", type: "search", placeholder: "Search pages", "aria-label": "Search pages", autocomplete: "off" });
  const top = h("header", { class: "nb-top" },
    h("a", { class: "nb-brand", href: root }, h("span", { class: "nb-logo", "aria-hidden": "true" }), h("span", { text: "Kapi" })),
    h("label", { class: "nb-search-box" }, h("span", { class: "nb-search-icon", "aria-hidden": "true" }), search,
      h("kbd", { text: shortcuts.key("searchCommand").hint })));
  const eyebrow = h("p", { class: "nb-eyebrow" });
  const hero = h("section", { class: "nb-hero" },
    h("div", {}, eyebrow, h("h1", {}, "Pick up ", h("br"), h("em", { text: "where you left off." }))),
    h("p", { class: "nb-hero-note", text: "Every page you and your agents wrote, kept in one notebook." }));
  const desk = h("div", { class: "nb-desk-slot" });
  const projects = h("div", { class: "nb-projects-slot" });
  const chips = h("div", { class: "nb-chips", role: "group", "aria-label": "Project" });
  const grid = h("div", { class: "nb-grid" });
  const libraryNote = h("span");
  const library = h("section", { class: "nb-library", "aria-label": "Every page" },
    h("div", { class: "nb-heading" }, h("h2", { text: "Every page" }), libraryNote), h("div", { class: "nb-filters" }, chips, legend(h)), grid);
  main.append(h("div", { class: "nb-home" }, top, hero,
    h("section", { class: "nb-desk" },
      h("div", {}, heading("On your desk", "The open page that changed last"), desk),
      h("div", {}, heading("Projects", "Where your pages were opened"), projects)),
    library,
    h("footer", { class: "nb-foot" }, h("span", { text: "kapi" }), h("em", { text: "A notebook you share with your agents." }))));

  let pages = [];
  let fresh = new Set();
  let picked = null;
  const pick = (folder) => {
    picked = picked === folder ? null : folder;
    drawLibrary();
    if (picked) library.scrollIntoView({ behavior: "smooth", block: "start" });
  };
  const drawLibrary = () => redraw(library, () => {
    const groups = byFolder(pages);
    chips.replaceChildren(...[null, ...groups.keys()].map((folder) => {
      const chip = h("button", { type: "button", class: "nb-chip", "data-key": `chip
${folder ?? ""}`, "aria-pressed": String(picked === folder), text: folder ? base(folder) : "All" });
      chip.addEventListener("click", () => pick(folder));
      return chip;
    }));
    chips.hidden = groups.size < 2;
    const shown = pages.filter((p) => (!picked || inFolder(p, picked)) && matches(p, search.value));
    libraryNote.textContent = shown.length === pages.length ? plural(pages.length, "page") : `${shown.length} of ${pages.length}`;
    grid.replaceChildren(...(shown.length ? shown.map((p) => card(p, picked, fresh.has(p.id))) : [h("p", { class: "nb-empty", text: pages.length ? "No page matches." : "No pages yet." })]));
  });
  const draw = () => redraw(main, () => {
    const groups = byFolder(pages);
    if (picked && !groups.has(picked)) picked = null;
    const day = new Date().toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" });
    const live = pages.filter((p) => p.state === "live").length;
    eyebrow.textContent = [day, plural(pages.length, "page"), plural(groups.size, "project"), live ? `${live} live` : ""].filter(Boolean).join(" · ");
    desk.replaceChildren(h("article", { class: "nb-desk-card" }, h("div", { class: "nb-sheet-under", "aria-hidden": "true" }), sheet(pages.find((p) => !dim(p)) ?? pages[0])));
    projects.replaceChildren(h("article", { class: "nb-projects-card" }, groups.size ? projectList(groups, pick) : h("p", { class: "nb-empty", text: "Projects appear once a page is opened." })));
    drawLibrary();
  });
  search.addEventListener("input", drawLibrary);
  onVisit(() => {
    fresh = newIDs(pages, null);
    drawLibrary();
  });
  document.addEventListener("keydown", (e) => {
    if (document.activeElement !== search && (shortcuts.is("searchCommand", e) || (shortcuts.is("search", e) && !e.target.closest?.("input, textarea")))) {
      e.preventDefault();
      search.focus();
      search.select();
    } else if (document.activeElement === search && shortcuts.is("dismiss", e)) {
      search.value = "";
      search.blur();
      drawLibrary();
    }
  });
  watch((next) => {
    pages = next;
    fresh = newIDs(pages, null);
    main.removeAttribute("data-error");
    draw();
  }, () => main.setAttribute("data-error", ""));
}
