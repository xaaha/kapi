(() => {
  "use strict";

  const main = document.getElementById("kapi-page");
  const base = location.pathname.match(/^(?:.*?\/)?p\/[^/]+(?=\/)/)?.[0];
  if (!main || !base) return;

  const STATUS = {
    connecting: "Connecting to kapi\u2026",
    watching: "Agent is watching",
    queued: "Queued for the agent",
    busy: "Agent is working",
    none: "No agent connected",
    ended: "Session ended. Send a message to start again.",
    disconnected: "Disconnected. Reconnecting\u2026",
  };

  let selected = null;
  let editing = null;
  let selectedAt = 0;
  let presence = "none";
  let lastSeq = 0;
  let readUpTo = 0;
  let readBy = "";
  let queuedUpTo = 0;
  let lastHuman = 0;
  let lastReply = 0;
  let ended = false;
  let opened = false;
  let busy = false;
  let pending = null;
  let flushTimer = 0;
  let queue = Promise.resolve();
  let noticeTimer = 0;
  let note = null;
  let retryDelay = 1000;
  let asking = null;
  let lastPoint = null;
  let textRange = null;
  let textBackward = false;
  let pointerDown = false;
  let sending = false;

  function h(tag, props = {}, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props)) {
      if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
      else if (k === "class") el.className = v;
      else if (k === "text") el.textContent = v;
      else el.setAttribute(k, v);
    }
    el.append(...kids);
    return el;
  }

  const ICONS = {
    archive: "M224,48H32A16,16,0,0,0,16,64V88a16,16,0,0,0,16,16v88a16,16,0,0,0,16,16H208a16,16,0,0,0,16-16V104a16,16,0,0,0,16-16V64A16,16,0,0,0,224,48ZM208,192H48V104H208ZM224,88H32V64H224V88ZM96,136a8,8,0,0,1,8-8h48a8,8,0,0,1,0,16H104A8,8,0,0,1,96,136Z",
    unarchive: "M208,32H48A16,16,0,0,0,32,48V208a16,16,0,0,0,16,16H208a16,16,0,0,0,16-16V48A16,16,0,0,0,208,32Zm0,16V152h-28.7A15.86,15.86,0,0,0,168,156.69L148.69,176H107.31L88,156.69A15.86,15.86,0,0,0,76.69,152H48V48Zm0,160H48V168H76.69L96,187.31A15.86,15.86,0,0,0,107.31,192h41.38A15.86,15.86,0,0,0,160,187.31L179.31,168H208v40ZM90.34,109.66a8,8,0,0,1,0-11.32l32-32a8,8,0,0,1,11.32,0l32,32a8,8,0,0,1-11.32,11.32L136,91.31V152a8,8,0,0,1-16,0V91.31l-18.34,18.35A8,8,0,0,1,90.34,109.66Z",
    chat: "M140,128a12,12,0,1,1-12-12A12,12,0,0,1,140,128ZM84,116a12,12,0,1,0,12,12A12,12,0,0,0,84,116Zm88,0a12,12,0,1,0,12,12A12,12,0,0,0,172,116Zm60,12A104,104,0,0,1,79.12,219.82L45.07,231.17a16,16,0,0,1-20.24-20.24l11.35-34.05A104,104,0,1,1,232,128Zm-16,0A88,88,0,1,0,51.81,172.06a8,8,0,0,1,.66,6.54L40,216,77.4,203.53a7.85,7.85,0,0,1,2.53-.42,8,8,0,0,1,4,1.08A88,88,0,0,0,216,128Z",
    x: "M205.66,194.34a8,8,0,0,1-11.32,11.32L128,139.31,61.66,205.66a8,8,0,0,1-11.32-11.32L116.69,128,50.34,61.66A8,8,0,0,1,61.66,50.34L128,116.69l66.34-66.35a8,8,0,0,1,11.32,11.32L139.31,128Z",
    trash: "M216,48H176V40a24,24,0,0,0-24-24H104A24,24,0,0,0,80,40v8H40a8,8,0,0,0,0,16h8V208a16,16,0,0,0,16,16H192a16,16,0,0,0,16-16V64h8a8,8,0,0,0,0-16ZM96,40a8,8,0,0,1,8-8h48a8,8,0,0,1,8,8v8H96Zm96,168H64V64H192ZM112,104v64a8,8,0,0,1-16,0V104a8,8,0,0,1,16,0Zm48,0v64a8,8,0,0,1-16,0V104a8,8,0,0,1,16,0Z",
    download: "M224,144v64a8,8,0,0,1-8,8H40a8,8,0,0,1-8-8V144a8,8,0,0,1,16,0v56H208V144a8,8,0,0,1,16,0Zm-101.66,5.66a8,8,0,0,0,11.32,0l40-40a8,8,0,0,0-11.32-11.32L136,124.69V32a8,8,0,0,0-16,0v92.69L93.66,98.34a8,8,0,0,0-11.32,11.32Z",
    image: "M216,40H40A16,16,0,0,0,24,56V200a16,16,0,0,0,16,16H216a16,16,0,0,0,16-16V56A16,16,0,0,0,216,40Zm0,16V158.75l-26.07-26.06a16,16,0,0,0-22.63,0l-20,20-44-44a16,16,0,0,0-22.62,0L40,149.37V56ZM40,172l52-52,80,80H40Zm176,28H194.63l-36-36,20-20L216,181.38V200ZM144,100a12,12,0,1,1,12,12A12,12,0,0,1,144,100Z",
    pencil: "M227.31,73.37,182.63,28.68a16,16,0,0,0-22.63,0L36.69,152A15.86,15.86,0,0,0,32,163.31V208a16,16,0,0,0,16,16H92.69A15.86,15.86,0,0,0,104,219.31L227.31,96a16,16,0,0,0,0-22.63ZM92.69,208H48V163.31l88-88L180.69,120ZM192,108.68,147.31,64l24-24L216,84.68Z",
    flip: "M224,48V96a8,8,0,0,1-8,8H168a8,8,0,0,1,0-16h28.69L182.06,73.37a79.56,79.56,0,0,0-56.13-23.43h-.45A79.52,79.52,0,0,0,69.59,72.71,8,8,0,0,1,58.41,61.27a96,96,0,0,1,135,.79L208,76.69V48a8,8,0,0,1,16,0ZM186.41,183.29a80,80,0,0,1-112.47-.66L59.31,168H88a8,8,0,0,0,0-16H40a8,8,0,0,0-8,8v48a8,8,0,0,0,16,0V179.31l14.63,14.63A95.43,95.43,0,0,0,130,222.06h.53a95.36,95.36,0,0,0,67.07-27.33,8,8,0,0,0-11.18-11.44Z",
    system: "M208,40H48A24,24,0,0,0,24,64V176a24,24,0,0,0,24,24H208a24,24,0,0,0,24-24V64A24,24,0,0,0,208,40Zm8,136a8,8,0,0,1-8,8H48a8,8,0,0,1-8-8V64a8,8,0,0,1,8-8H208a8,8,0,0,1,8,8Zm-48,48a8,8,0,0,1-8,8H96a8,8,0,0,1,0-16h64A8,8,0,0,1,168,224Z",
    light: "M120,40V16a8,8,0,0,1,16,0V40a8,8,0,0,1-16,0Zm72,88a64,64,0,1,1-64-64A64.07,64.07,0,0,1,192,128Zm-16,0a48,48,0,1,0-48,48A48.05,48.05,0,0,0,176,128ZM58.34,69.66A8,8,0,0,0,69.66,58.34l-16-16A8,8,0,0,0,42.34,53.66Zm0,116.68-16,16a8,8,0,0,0,11.32,11.32l16-16a8,8,0,0,0-11.32-11.32ZM192,72a8,8,0,0,0,5.66-2.34l16-16a8,8,0,0,0-11.32-11.32l-16,16A8,8,0,0,0,192,72Zm5.66,114.34a8,8,0,0,0-11.32,11.32l16,16a8,8,0,0,0,11.32-11.32ZM48,128a8,8,0,0,0-8-8H16a8,8,0,0,0,0,16H40A8,8,0,0,0,48,128Zm80,80a8,8,0,0,0-8,8v24a8,8,0,0,0,16,0V216A8,8,0,0,0,128,208Zm112-88H216a8,8,0,0,0,0,16h24a8,8,0,0,0,0-16Z",
    copy: "M216,32H88a8,8,0,0,0-8,8V80H40a8,8,0,0,0-8,8V216a8,8,0,0,0,8,8H168a8,8,0,0,0,8-8V176h40a8,8,0,0,0,8-8V40A8,8,0,0,0,216,32ZM160,208H48V96H160Zm48-48H176V88a8,8,0,0,0-8-8H96V48H208Z",
    stop: "M128,24A104,104,0,1,0,232,128,104.11,104.11,0,0,0,128,24Zm0,192a88,88,0,1,1,88-88A88.1,88.1,0,0,1,128,216ZM160,88H96a8,8,0,0,0-8,8v64a8,8,0,0,0,8,8h64a8,8,0,0,0,8-8V96A8,8,0,0,0,160,88Zm-8,64H104V104h48Z",
    caret: "M165.66,202.34a8,8,0,0,1-11.32,11.32l-80-80a8,8,0,0,1,0-11.32l80-80a8,8,0,0,1,11.32,11.32L91.31,128Z",
    dots: "M140,128a12,12,0,1,1-12-12A12,12,0,0,1,140,128ZM128,72a12,12,0,1,0-12-12A12,12,0,0,0,128,72Zm0,112a12,12,0,1,0,12,12A12,12,0,0,0,128,184Z",
    printer: "M214.67,72H200V40a8,8,0,0,0-8-8H64a8,8,0,0,0-8,8V72H41.33C27.36,72,16,82.77,16,96v80a8,8,0,0,0,8,8H56v32a8,8,0,0,0,8,8H192a8,8,0,0,0,8-8V184h32a8,8,0,0,0,8-8V96C240,82.77,228.64,72,214.67,72ZM72,48H184V72H72ZM184,208H72V160H184Zm40-40H200V152a8,8,0,0,0-8-8H64a8,8,0,0,0-8,8v16H32V96c0-4.41,4.19-8,9.33-8H214.67c5.14,0,9.33,3.59,9.33,8Zm-24-52a12,12,0,1,1-12-12A12,12,0,0,1,200,116Z",
    dark: "M233.54,142.23a8,8,0,0,0-8-2,88.08,88.08,0,0,1-109.8-109.8,8,8,0,0,0-10-10,104.84,104.84,0,0,0-52.91,37A104,104,0,0,0,136,224a103.09,103.09,0,0,0,62.52-20.88,104.84,104.84,0,0,0,37-52.91A8,8,0,0,0,233.54,142.23ZM188.9,190.34A88,88,0,0,1,65.66,67.11a89,89,0,0,1,31.4-26A106,106,0,0,0,96,56,104.11,104.11,0,0,0,200,160a106,106,0,0,0,14.92-1.06A89,89,0,0,1,188.9,190.34Z",
  };

  function icon(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 256 256");
    svg.setAttribute("aria-hidden", "true");
    const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
    p.setAttribute("d", ICONS[name]);
    svg.append(p);
    return svg;
  }

  function themeToggle() {
    const theme = window.kapiTheme;
    if (!theme) return [];
    const names = { system: "System", light: "Light", dark: "Dark" };
    const buttons = theme.choices.map((c) =>
      h("button", { type: "button", "data-theme-choice": c, title: `${names[c]} theme`, "aria-label": `${names[c]} theme`, onclick: () => theme.set(c) }, icon(c)));
    const sync = () => {
      for (const b of buttons) b.setAttribute("aria-pressed", String(b.dataset.themeChoice === theme.get()));
    };
    document.addEventListener("kapi-theme", sync);
    sync();
    return [h("div", { class: "kapi-theme", role: "group", "aria-label": "Theme" }, ...buttons)];
  }

  const IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"];
  const MAX_IMAGES = 4;
  const fileURL = (nameOrPath) => `${base}/files/${encodeURIComponent(nameOrPath.split("/").pop())}`;

  const button = (label, action, title = label) =>
    h("button", { type: "button", "data-action": action, title, text: label });

  const tool = (glyph, label, action, key = "", shortcut = key) =>
    h("button", { type: "button", "data-action": action, "aria-label": label, "data-tip": key ? `${label} ${key}` : label, ...(shortcut && { "aria-keyshortcuts": shortcut }) },
      icon(glyph), h("span", { class: "kapi-tool-label", text: label }));

  const iconTool = (glyph, label, action) =>
    h("button", { type: "button", "data-action": action, "aria-label": label, "data-tip": label }, icon(glyph));

  const dot = h("span", { class: "kapi-dot", "data-state": "connecting" });
  const bar = h("div", { class: "kapi-bar", role: "toolbar", "aria-label": "Block actions", hidden: "" });
  const statusText = h("span", { class: "kapi-status", text: STATUS.connecting });
  const notice = h("div", { class: "kapi-notice", role: "status", hidden: "" });
  const restartBox = h("div", { class: "kapi-restart", role: "alert", hidden: "" });
  const log = h("ol", { class: "kapi-log", "aria-live": "polite" });
  const empty = h("p", { class: "kapi-empty", text: "Messages with the agent show up here." });
  const input = h("textarea", { rows: "3", placeholder: "Message the agent", "aria-label": "Message" });
  const sendButton = h("button", { type: "submit", class: "kapi-primary", text: "Send" });
  const HINT = "Enter to send, Shift+Enter for a new line";
  const hint = h("span", { class: "kapi-hint", text: HINT });
  const queueBox = h("section", { class: "kapi-queue", "aria-label": "Queued notes", hidden: "" });
  const chatTab = h("button", { type: "button", role: "tab", "aria-selected": "true", text: "Chat", onclick: () => showTab("chat") });
  const notesTab = h("button", { type: "button", role: "tab", "aria-selected": "false", text: "Notes", onclick: () => showTab("notes") });
  const tabs = h("div", { class: "kapi-tabs", role: "tablist", "aria-label": "Panel", hidden: "" }, chatTab, notesTab);
  const notesView = h("section", { class: "kapi-notes", "aria-label": "Notes" });
  const stripSummary = h("span", { class: "kapi-strip-summary" });
  const strip = h("div", { class: "kapi-notes-strip", hidden: "" }, stripSummary,
    h("button", { type: "button", class: "kapi-btn", text: "Next change", onclick: () => stepNote(1, "changed") }),
    h("button", { type: "button", class: "kapi-btn kapi-primary", text: "Notes", onclick: () => setSheet(!sheetOpen) }));
  const pins = h("div", { class: "kapi-pins" });
  const rail = h("nav", { class: "kapi-rail", "aria-label": "Where the notes are on the page", hidden: "" });
  let sendsInFlight = 0;
  const syncSend = () => (sendButton.disabled = sending || sendsInFlight > 0 || !!composerTray.blocked());
  const composerTray = imageTray(() => syncSend());
  const composer = h("form", { class: "kapi-composer" },
    input, composerTray.row,
    h("div", { class: "kapi-composer-row" }, h("span", { class: "kapi-dot kapi-composer-dot", "data-state": "connecting" }),
      composerTray.pick, composerTray.picker, hint, sendButton));
  acceptImages(composer, composerTray);
  const undoButton = button("Undo", "undo", "Undo the last change");
  const showUndo = (can) => {
    undoButton.hidden = String(can) !== "true";
    if (undoButton.hidden) dismissNote();
  };
  const pagePath = main.dataset.path || "";
  const pageName = pagePath.split(/[\\/]/).pop() || document.title;
  const menuButton = h("button", { type: "button", class: "kapi-menu-button", "aria-label": "Page menu", title: "Page menu", "aria-haspopup": "menu", "aria-expanded": "false" }, icon("dots"));
  const EXPORTS = [
    { glyph: "download", label: "HTML", run: () => exportFile("html", () => pageName) },
    { glyph: "download", label: "Markdown", run: () => exportFile("markdown", (type) => pageName.replace(/\.html?$/i, "") + (type === "application/zip" ? ".zip" : ".md")) },
    { glyph: "printer", label: "PDF", run: () => print() },
  ];
  const MENU = [
    { glyph: "copy", id: "copy", label: "Copy path", detail: pagePath, run: copyPath },
    { id: "end", glyph: "stop", label: "End session", run: endSession },
  ];
  const menuEntry = ({ id, glyph, label, detail, run }) =>
    h("button", { type: "button", role: "menuitem", tabindex: "-1", ...(id && { "data-menu": id }), onclick: () => (closeMenu(true), run()) },
      icon(glyph), h("span", { class: "kapi-menu-label", text: label }), ...(detail ? [h("code", { class: "kapi-menu-detail", title: detail, text: `\u200e${detail}` })] : []));
  const exportButton = h("button", { type: "button", role: "menuitem", tabindex: "-1", "data-menu": "export", "aria-haspopup": "menu", "aria-expanded": "false" },
    icon("download"), h("span", { class: "kapi-menu-label", text: "Export" }), h("span", { class: "kapi-menu-caret" }, icon("caret")));
  const submenu = h("div", { class: "kapi-submenu", role: "menu", "aria-label": "Export", hidden: "" }, ...EXPORTS.map(menuEntry));
  const exportGroup = h("div", { class: "kapi-menu-group", role: "none" }, exportButton, submenu);
  const menu = h("div", { class: "kapi-menu", role: "menu", "aria-label": "Page", hidden: "" }, exportGroup, ...MENU.map(menuEntry));
  const panel = h("aside", { class: "kapi-panel", "aria-label": "kapi chat" },
    h("header", { class: "kapi-panel-head" }, undoButton, ...themeToggle(), menuButton, menu), tabs,
    restartBox, notice, h("div", { class: "kapi-scroll", tabindex: "-1" }, empty, log), notesView,
    h("div", { class: "kapi-presence", role: "status" }, dot, statusText), bar, strip, queueBox, composer);
  const ring = h("div", { class: "kapi-ring", hidden: "" });
  const copyButton = h("button", { type: "button", class: "kapi-copy-code", "aria-label": "Copy code", "data-tip": "Copy", hidden: "" }, icon("copy"));
  const root = h("div", { id: "kapi-chrome" }, pins, rail, ring, panel, copyButton);
  const phone = matchMedia("(max-width: 760px)");
  phone.addEventListener("change", () => renderBar());
  const BAR_GAP = 8;
  const POINT_PAD = 10;
  const POINT_GAP = 16;
  const SOLID = "input, img, svg, textarea, button, select, video, canvas, iframe";
  const SHORTCUT = /Mac|iPhone|iPad/.test(navigator.platform) ? "\u2318\u21b5" : "Ctrl+\u21b5";
  const DELETE_KEY = /Mac|iPhone|iPad/.test(navigator.platform) ? "\u232b" : "Del";
  document.body.append(root);
  showUndo(main.dataset.undo);
  main.before(h("header", { class: "kapi-site-head" },
    h("span", { class: "kapi-logo", "aria-hidden": "true" }), h("span", { class: "kapi-site-name", text: "Kapi" })));

  const typing = (el) => !!el && (el.matches("input, textarea, select") || el.isContentEditable);
  const closest = (node, selector) => Element.prototype.closest.call(node, selector);
  const idOf = (el) => el.getAttribute("data-kapi-id");
  const find = (id) => main.querySelector(`[data-kapi-id="${CSS.escape(id)}"]`);
  const peers = (el) => [...el.parentElement.children].filter((c) => c.hasAttribute("data-kapi-id"));

  const INLINE = /^(A|ABBR|B|CITE|CODE|EM|I|KBD|MARK|Q|S|SMALL|SPAN|STRONG|SUB|SUP|U)$/;
  const CHROME_TEXT = ".kapi-callout-label, .kapi-plan-mark, .kapi-code-lang, .kapi-diff-stat";

  function textOf(el) {
    let out = "";
    const walk = (node) => {
      for (const c of node.childNodes) {
        if (c.nodeType === Node.TEXT_NODE) out += c.data;
        else if (c.nodeType === Node.ELEMENT_NODE && !c.matches(CHROME_TEXT)) {
          const gap = INLINE.test(c.tagName) ? "" : " ";
          out += gap;
          walk(c);
          out += gap;
        }
      }
    };
    walk(el);
    return out.replace(/\s+/g, " ").trim();
  }

  function labelArchived(scope) {
    const els = [...scope.querySelectorAll('[data-kapi-state="archived"]')];
    if (scope.matches?.('[data-kapi-state="archived"]')) els.push(scope);
    for (const el of els) {
      const text = textOf(el);
      const preset = el.getAttribute("data-kapi-preset")?.replace(/^kapi-/, "");
      el.setAttribute("data-kapi-excerpt", preset && text ? `${preset}: ${text}` : preset || text);
    }
  }

  function describe(id) {
    const el = find(id);
    if (!el) return "a block no longer on the page";
    const text = textOf(el);
    const short = text.length > 40 ? `${text.slice(0, 39).trimEnd()}\u2026` : text;
    const preset = el.getAttribute("data-kapi-preset")?.replace(/^kapi-/, "");
    if (!preset) return short || "an empty block";
    const named = el.getAttribute("title") || el.getAttribute("data-file");
    return named || short ? `${preset}: ${named || short}` : preset;
  }

  const humanize = (text) => text.replace(/#([a-z2-7]{8})\b/g, (m, id) => (find(id) ? `\u201c${describe(id)}\u201d` : m));

  function flash(el) {
    select(el);
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    ring.removeAttribute("data-flash");
    void ring.offsetWidth;
    ring.setAttribute("data-flash", "");
  }

  function setStatus(state) {
    for (const d of root.querySelectorAll(".kapi-dot")) d.dataset.state = state;
    statusText.textContent = STATUS[state] || STATUS.none;
    dot.title = statusText.textContent;
  }

  function say(text, kind = "info") {
    clearTimeout(noticeTimer);
    notice.textContent = humanize(text);
    notice.dataset.kind = kind;
    notice.hidden = false;
    if (kind === "info") noticeTimer = setTimeout(() => (notice.hidden = true), 4000);
  }

  const NOTE_MS = 6000;

  function dismissNote() {
    if (!note) return;
    clearTimeout(note.timer);
    note.el.remove();
    note = null;
  }

  function placeNote() {
    if (!note || note.el.isConnected) return;
    const { prev, next, parent } = note.spot;
    const nextEl = next && find(next);
    const prevEl = prev && find(prev);
    if (nextEl) nextEl.before(note.el);
    else if (prevEl) prevEl.after(note.el);
    else ((parent && find(parent)) || main).append(note.el);
  }

  function showNote(spot) {
    dismissNote();
    const el = h(spot.list ? "li" : "div", { class: "kapi-undo-note", role: "status", onclick: (e) => e.stopPropagation() },
      h("span", { text: "Deleted" }),
      h("button", { type: "button", text: "Undo", onclick: () => action("undo") }));
    note = { el, spot, timer: setTimeout(dismissNote, NOTE_MS) };
    placeNote();
  }

  function noteSpot(el) {
    const list = peers(el);
    const i = list.indexOf(el);
    const parent = el.parentElement;
    return {
      prev: list[i - 1] && idOf(list[i - 1]),
      next: list[i + 1] && idOf(list[i + 1]),
      parent: parent === main ? "" : idOf(closest(parent, "[data-kapi-id]")),
      list: parent.matches("ul, ol"),
    };
  }

  function select(el) {
    if (editing) return;
    selected?.removeAttribute("data-kapi-selected");
    if (el !== selected) selectedAt = performance.now();
    selected = el;
    el?.setAttribute("data-kapi-selected", "");
    renderBar();
  }

  function renderBar() {
    const text = !editing && !!textRange;
    if (asking || (!text && (!selected || !selected.isConnected))) {
      bar.hidden = true;
      return placeRing();
    }
    if (editing) {
      const changed = !!editing.op();
      const primary = changed ? button("Save", "save", `Save (${SHORTCUT})`) : button("Cancel", "cancel", `Cancel (${SHORTCUT} or Escape)`);
      primary.classList.add("kapi-primary");
      primary.append(h("span", { class: "kapi-key", text: ` ${SHORTCUT}` }));
      bar.replaceChildren(primary,
        ...(changed ? [button("Cancel", "cancel", "Cancel (Escape)")] : []));
    } else if (text) {
      bar.replaceChildren(tool("chat", "Ask AI", "ask", "A"));
    } else {
      const archived = selected.getAttribute("data-kapi-state") === "archived";
      const flip = selected.matches('[data-kapi-preset="diagram"][data-kapi-dir]');
      const next = selected.getAttribute("data-kapi-dir") === "lr" ? "Flow top to bottom" : "Flow left to right";
      const shown = selected.matches("figure") && selected.querySelector(":scope > img[src]");
      const picture = !archived && shown && new URL(shown.src, location.href).origin === location.origin;
      bar.replaceChildren(tool("chat", "Ask AI", "ask", "A"), ...(archived ? [tool("unarchive", "Unarchive", "unarchive", "E")] : [
        ...(phone.matches ? [tool("pencil", "Edit", "edit")] : []),
        ...(picture ? [tool("download", "Download", "download")] : []),
        ...(flip ? [iconTool("flip", next, "flip")] : []),
        tool("archive", "Archive", "archive", "E")]),
        tool("trash", "Delete", "delete", DELETE_KEY, "Delete"));
    }
    for (const b of bar.querySelectorAll("button")) b.disabled ||= busy;
    bar.hidden = false;
    placeRing();
  }

  function dockBar(floating) {
    if (floating && bar.parentElement !== root) root.append(bar);
    if (!floating && bar.parentElement !== panel) panel.insertBefore(bar, composer);
    if (!floating) bar.removeAttribute("data-place");
  }

  function coversContent(box, target) {
    const hit = (r) => r.width && r.height && r.left < box.right && box.left < r.right && r.top < box.bottom && box.top < r.bottom;
    const range = document.createRange();
    const within = (el) => [...el.childNodes].some((n) => {
      if (n.nodeType === Node.TEXT_NODE) {
        if (!n.data.trim()) return false;
        range.selectNodeContents(n);
        return [...range.getClientRects()].some(hit);
      }
      if (n.nodeType !== Node.ELEMENT_NODE) return false;
      const r = n.getBoundingClientRect();
      if (n.matches(SOLID)) return hit(r);
      return (!r.width && !r.height) || hit(r) ? within(n) : false;
    });
    if (!target) return within(main);
    const t = target.getBoundingClientRect();
    return hit(new DOMRect(t.x - 4, t.y - 4, t.width + 8, t.height + 8)) || within(main);
  }

  function pointRect() {
    if (editing) return null;
    if (textRange) {
      const rects = [...textRange.getClientRects()].filter((r) => r.width || r.height);
      return (textBackward ? rects[0] : rects.at(-1)) ?? null;
    }
    if (lastPoint?.el !== selected) return null;
    const r = selected.getBoundingClientRect();
    return new DOMRect(r.left + lastPoint.dx, r.top + lastPoint.dy - POINT_PAD, 0, 2 * POINT_PAD);
  }

  function placeBar() {
    const target = editing?.box || selected;
    const text = !editing && !!textRange;
    if (phone.matches || bar.hidden || (!text && !target?.isConnected)) return dockBar(false);
    dockBar(true);
    const areaRight = document.documentElement.clientWidth - panel.getBoundingClientRect().width;
    const { width, height } = bar.getBoundingClientRect();
    const fits = (x, y) => x >= 4 && x + width <= areaRight - 4 && y >= 0 && y + height <= innerHeight;
    const p = pointRect();
    const py = p && Math.min(Math.max(p.top + p.height / 2 - height / 2, 4), innerHeight - height - 4);
    const right = p && ["point", p.right + POINT_GAP, py];
    const left = p && ["point", p.left - POINT_GAP - width, py];
    const above = p && ["point", Math.min(Math.max(p.right - width / 2, 4), areaRight - 4 - width), p.top - BAR_GAP - height];
    const under = p && ["point", above[1], p.bottom + BAR_GAP];
    const near = !p ? [] : text ? (textBackward ? [above, under, left, right] : [above, under, right, left]) : [right, left];
    let spot = near.find(([, x, y]) => fits(x, y));
    if (text && spot && coversContent(new DOMRect(spot[1], spot[2], width, height), null)) {
      const host = closest(textRange.endContainer.nodeType === Node.ELEMENT_NODE ? textRange.endContainer : textRange.endContainer.parentElement, BLOCK);
      const gutter = host && ["point", host.getBoundingClientRect().right + BAR_GAP, py];
      const clear = [above, under, ...(gutter ? [gutter] : [])].find(([, x, y]) => fits(x, y) && !coversContent(new DOMRect(x, y, width, height), null));
      if (clear) spot = clear;
    }
    if (!spot && target?.isConnected && !text) {
      const r = target.getBoundingClientRect();
      const left = Math.min(r.right, areaRight - 4) - width;
      spot = [
        ["outside", r.right + BAR_GAP, Math.min(Math.max(r.top, Math.min(8, r.bottom - height)), innerHeight - height - 8)],
        ["above", left, r.top - BAR_GAP - height],
        ["below", left, r.bottom + BAR_GAP],
      ].find(([, x, y]) => fits(x, y) && !coversContent(new DOMRect(x, y, width, height), target));
    }
    if (!spot) return dockBar(false);
    bar.dataset.place = spot[0];
    bar.style.left = `${spot[1]}px`;
    bar.style.top = `${spot[2]}px`;
  }

  function placeRing() {
    placeBar();
    ring.hidden = (bar.hidden && !asking) || !selected || !!editing?.area;
    if (!ring.hidden) {
      const r = selected.getBoundingClientRect();
      ring.style.left = `${r.left - 4}px`;
      ring.style.top = `${r.top - 4}px`;
      ring.style.width = `${r.width + 8}px`;
      ring.style.height = `${r.height + 8}px`;
      ring.style.borderRadius = getComputedStyle(selected).borderRadius;
      ring.toggleAttribute("data-editing", !!editing);
      if (editing) ring.removeAttribute("data-flash");
    }
  }

  let stream = null;
  let restarted = false;

  function showRestart(reopen, reopened) {
    restarted = true;
    stream?.close();
    setStatus("disconnected");
    statusText.textContent = "Kapi restarted";
    restartBox.hidden = false;
    handOff();
    if (reopened) return restartBox.replaceChildren(h("p", { text: "Kapi restarted and reopened this page in a new tab. You can close this one." }));
    const copy = h("button", { type: "button", class: "kapi-copy", text: "Copy" });
    copy.addEventListener("click", () =>
      navigator.clipboard.writeText(reopen).then(() => (copy.textContent = "Copied"), () => (copy.textContent = "Copy failed")));
    restartBox.replaceChildren(h("p", {}, "Kapi restarted. Run ", h("code", { text: reopen }), " to continue."), copy);
  }

  async function restartedReply(res) {
    if (res.status !== 403) return false;
    const data = await res.clone().json().catch(() => ({}));
    if (typeof data.reopen !== "string") return false;
    showRestart(data.reopen, data.reopened === true);
    return true;
  }

  const menuItems = (within) => [...within.querySelectorAll(':scope > [role="menuitem"]:not([hidden]), :scope > [role="none"] > [role="menuitem"]')];
  let hoverOpened = false;
  let submenuTimer;

  function openSubmenu(focusFirst) {
    submenu.hidden = false;
    exportButton.setAttribute("aria-expanded", "true");
    if (focusFirst) menuItems(submenu)[0].focus();
  }

  function closeSubmenu(refocus) {
    hoverOpened = false;
    clearTimeout(submenuTimer);
    if (submenu.hidden) return;
    submenu.hidden = true;
    exportButton.setAttribute("aria-expanded", "false");
    if (refocus) exportButton.focus();
  }

  function openMenu() {
    menu.hidden = false;
    menuButton.setAttribute("aria-expanded", "true");
    menuItems(menu)[0].focus();
  }

  function closeMenu(refocus) {
    if (menu.hidden) return;
    closeSubmenu(false);
    menu.hidden = true;
    menuButton.setAttribute("aria-expanded", "false");
    if (refocus) menuButton.focus();
  }

  menuButton.addEventListener("click", () => (menu.hidden ? openMenu() : closeMenu(true)));
  exportButton.addEventListener("click", () => {
    if (hoverOpened) hoverOpened = false;
    else if (submenu.hidden) openSubmenu(false);
    else closeSubmenu(true);
  });
  exportGroup.addEventListener("pointerenter", (e) => {
    if (e.pointerType !== "mouse") return;
    clearTimeout(submenuTimer);
    if (!submenu.hidden) return;
    openSubmenu(false);
    hoverOpened = true;
  });
  exportGroup.addEventListener("pointerleave", (e) => {
    if (e.pointerType === "mouse") submenuTimer = setTimeout(() => closeSubmenu(submenu.contains(document.activeElement)), 300);
  });
  menu.addEventListener("keydown", (e) => {
    if (e.key !== "Tab") e.stopPropagation();
    const inSubmenu = submenu.contains(e.target);
    const items = menuItems(inSubmenu ? submenu : menu);
    const at = items.indexOf(document.activeElement);
    const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: items.length - 1 }[e.key];
    if (to !== undefined) {
      e.preventDefault();
      if (!inSubmenu) closeSubmenu(false);
      items[(to + items.length) % items.length].focus();
    } else if (e.target === exportButton && ["ArrowRight", "Enter", " "].includes(e.key)) {
      e.preventDefault();
      openSubmenu(true);
    } else if (inSubmenu && (e.key === "ArrowLeft" || e.key === "Escape")) {
      e.preventDefault();
      closeSubmenu(true);
    } else if (e.key === "Escape") {
      e.preventDefault();
      closeMenu(true);
    } else if (e.key === "Tab") closeMenu(false);
  });
  document.addEventListener("pointerdown", (e) => {
    if (!menu.hidden && !menu.contains(e.target) && !menuButton.contains(e.target)) closeMenu(false);
  }, true);

  async function exportFile(format, nameFor) {
    let res;
    try {
      res = await fetch(`${base}/export/${format}`);
    } catch {
      return say("Could not reach kapi. Check that the daemon is running.", "error");
    }
    if (await restartedReply(res)) return;
    if (!res.ok) return say(`Could not export the page: ${(await res.text()).trim()}`, "error");
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const link = h("a", { href: url, download: nameFor(blob.type), hidden: "" });
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 60000);
  }

  async function endSession() {
    const res = await request("/end", {});
    if (res && !res.ok) say(res.data.error || `kapi could not end the session (HTTP ${res.status}).`, "error");
  }

  function copyPath() {
    navigator.clipboard.writeText(pagePath).then(() => say("Copied the page path"), () => say("Could not copy the page path", "error"));
  }

  let titleBeforePrint = null;
  addEventListener("beforeprint", () => {
    titleBeforePrint = document.title;
    document.title = pageName.replace(/\.html?$/i, "");
  });
  addEventListener("afterprint", () => {
    if (titleBeforePrint !== null) document.title = titleBeforePrint;
    titleBeforePrint = null;
  });

  async function request(path, body) {
    let res;
    try {
      res = await fetch(base + path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
    } catch {
      say("Could not reach kapi. Check that the daemon is running.", "error");
      return null;
    }
    if (await restartedReply(res)) return null;
    const data = await res.json().catch(() => ({}));
    return { status: res.status, ok: res.ok, data };
  }

  const STALE = "The page changed before your change landed. This is the latest version, so try again.";
  const STALE_EDIT = "The page changed while you were editing. Your text is still here: save again to replace the newer version, or press Escape to discard yours.";

  async function op(kind, fields = {}, { rev = Number(main.dataset.rev), stale = STALE } = {}) {
    notice.hidden = true;
    if (kind !== "undo") dismissNote();
    busy = true;
    opsInFlight++;
    renderBar();
    try {
      const res = await request("/ops", { rev, op: { kind, ...fields } });
      if (!res || res.ok) {
        if (res) {
          main.dataset.rev = res.data.rev;
          if (res.data.seq) ownSeqs.add(res.data.seq);
          showUndo(res.data.undo);
        }
        return res;
      }
      if (res.status === 409) {
        await refresh(null);
        say(stale, "error");
      } else if (res.status === 422) {
        say(res.data.error || "kapi refused that change.", "error");
      } else {
        say(`kapi refused that change (HTTP ${res.status}).`, "error");
      }
      return res;
    } finally {
      busy = false;
      opsInFlight--;
      renderBar();
      if (!opsInFlight) releaseRevisions();
    }
  }

  const BLOCK = '[data-kapi-id]:not([data-kapi-id] [data-kapi-preset="badge"])';
  const PART = "[data-kapi-text]";

  function partsOf(el) {
    return [el, ...el.querySelectorAll(PART)].filter((n) => n.matches(PART) && (n === el || !n.hasAttribute("data-kapi-raw")));
  }

  function islandsOf(host) {
    return [...host.querySelectorAll("[data-kapi-preset]")].filter((n) => n.parentElement.closest(`[data-kapi-preset], ${PART}`) === host);
  }

  function ownText(host) {
    const copy = host.cloneNode(true);
    for (const island of islandsOf(copy)) island.replaceWith(island.dataset.kapiId || "");
    return copy.innerHTML;
  }

  function partOf(host) {
    const part = { id: host.dataset.kapiText };
    if (host.dataset.kapiCell) [part.row, part.col] = host.dataset.kapiCell.split(",").map(Number);
    if (host.dataset.kapiAttr) part.attr = host.dataset.kapiAttr;
    if (host.hasAttribute("data-kapi-lead")) part.lead = true;
    return part;
  }

  const SINGLE_LINE = "td, th, span, dt, summary, legend, [data-kapi-attr]";
  const innerOf = (host) => (host.dataset.kapiAttr ? host.textContent : host.innerHTML);

  function caretAtEnd(host) {
    host.focus();
    const walk = document.createTreeWalker(host, NodeFilter.SHOW_TEXT, (n) =>
      n.data.trim() && n.parentElement.closest('[contenteditable="false"], [contenteditable="true"]') === host ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_SKIP);
    let last = null;
    while (walk.nextNode()) last = walk.currentNode;
    if (last) getSelection().collapse(last, last.data.length);
    else {
      getSelection().selectAllChildren(host);
      getSelection().collapseToEnd();
    }
  }

  function editInPlace(el, hosts) {
    for (const host of hosts) {
      const details = host.closest("details");
      if (details) details.open = true;
      host.contentEditable = host.dataset.kapiAttr ? "plaintext-only" : "true";
      for (const island of islandsOf(host)) island.contentEditable = "false";
    }
    const parts = hosts.map((host) => ({ host, original: host.innerHTML, own: ownText(host) }));
    const op = () => {
      const changed = parts.filter(({ host, own }) => ownText(host) !== own);
      return changed.length ? { kind: "text", parts: changed.map(({ host }) => ({ ...partOf(host), inner: innerOf(host) })) } : null;
    };
    editing = { el, rev: Number(main.dataset.rev), hosts: parts, op };
    caretAtEnd(hosts[0]);
    renderBar();
  }

  function rowsOf(table) {
    return [...table.querySelectorAll("tr")].filter((tr) => tr.closest("table") === table);
  }

  function numberRows(scope) {
    for (const table of scope.querySelectorAll('[data-kapi-preset$="table"] table')) {
      const rows = rowsOf(table);
      const head = table.tHead?.rows[0] ?? rows.find((tr) => tr.cells.length && [...tr.cells].every((c) => c.tagName === "TH"));
      if (!head) continue;
      const labels = [...head.cells].flatMap((c) => Array(c.colSpan).fill(c.textContent.trim()));
      const carry = [];
      for (const tr of rows) {
        if (tr === head || tr.parentElement === table.tHead) continue;
        let col = 0;
        const skip = () => {
          while (carry[col] > 0) carry[col++]--;
        };
        for (const cell of tr.cells) {
          skip();
          cell.setAttribute("data-kapi-label", labels[col] ?? "");
          cell.setAttribute("data-kapi-col", col);
          for (let i = 0; i < cell.colSpan; i++) carry[col++] = Math.max(cell.rowSpan, 1) - 1;
        }
        skip();
      }
    }
  }

  async function editRaw(el) {
    busy = true;
    renderBar();
    let rev = Number(main.dataset.rev);
    const src = await fetch(`${base}/source/${encodeURIComponent(idOf(el))}?part=inner`)
      .then((res) => {
        if (!res.ok) return null;
        rev = Number(res.headers.get("Kapi-Rev") || rev);
        return res.text();
      }, () => null);
    busy = false;
    if (src === null) say("Could not load the text of this block.", "error");
    if (src === null || selected !== el || editing) return renderBar();
    const kind = el.getAttribute("data-kapi-preset");
    const area = h("textarea", { class: "kapi-source", spellcheck: "false", "aria-label": `Text of ${describe(idOf(el))}` });
    area.value = src;
    area.rows = Math.min(24, src.split("\n").length + 2);
    const box = h("div", { class: "kapi-source-box" }, h("div", { class: "kapi-source-label", text: `Editing ${kind}` }), area);
    const style = getComputedStyle(el);
    box.style.marginTop = style.marginTop;
    box.style.marginBottom = style.marginBottom;
    el.before(box);
    el.setAttribute("data-kapi-editing-source", "");
    const id = el.dataset.kapiText;
    editing = { el, area, box, rev, op: () => (area.value === src ? null : { kind: "text", parts: [{ id, inner: area.value }] }) };
    area.focus();
    renderBar();
  }

  function startEdit() {
    if (!selected || editing || busy) return;
    notice.hidden = true;
    dismissNote();
    if (selected.hasAttribute("data-kapi-raw")) return editRaw(selected);
    const hosts = partsOf(selected);
    if (!hosts.length) return say("This block has no text to edit here. Ask the agent to change it.");
    editInPlace(selected, hosts);
  }

  function endEdit(restore) {
    const { el, area, box, hosts } = editing;
    if (area) {
      box.remove();
      el.removeAttribute("data-kapi-editing-source");
    }
    for (const { host, original } of hosts || []) {
      if (restore) host.innerHTML = original;
      host.removeAttribute("contenteditable");
      for (const n of host.querySelectorAll("[contenteditable]")) n.removeAttribute("contenteditable");
    }
    editing = null;
    renderBar();
    if (pending) scheduleFlush();
  }

  function cancelEdit() {
    if (editing && !busy) endEdit(true);
  }

  async function saveEdit() {
    if (!editing || busy) return;
    const session = editing;
    const change = session.op();
    if (!change) return endEdit(false);
    const id = idOf(session.el);
    const { kind, ...fields } = change;
    const res = await op(kind, { id, ...fields }, { rev: session.rev, stale: STALE_EDIT });
    if (editing !== session) return;
    if (res?.ok) {
      endEdit(false);
      await refresh([id]);
    } else if (res?.status === 409) {
      session.rev = res.data.rev;
    }
  }

  async function action(name) {
    if (name === "undo") {
      const pill = note?.el.querySelector("button");
      if (pill) pill.disabled = true;
      const res = await op("undo", { id: "" });
      if (pill) pill.disabled = false;
      if (!res?.ok) return;
      await refresh(null);
      return dismissNote();
    }
    if (name === "save") return saveEdit();
    if (name === "cancel") return cancelEdit();
    if (name === "ask") return openAsk();
    if (!selected) return;
    const id = idOf(selected);
    if (name === "edit") return startEdit();
    if (name === "archive" || name === "unarchive") return (await op(name, { id }))?.ok && refresh([id]);
    if (name === "download") {
      const img = selected.querySelector(":scope > img[src]");
      if (!img) return;
      const a = h("a", { href: img.src, download: img.src.split("/").pop() });
      root.append(a);
      a.click();
      return a.remove();
    }
    if (name === "flip") {
      const dir = selected.getAttribute("data-kapi-dir") === "lr" ? "tb" : "lr";
      return (await op("orient", { id, dir }))?.ok && refresh([id]);
    }
    if (name === "delete") {
      const spot = noteSpot(selected);
      if (!(await op("delete", { id }))?.ok) return;
      await refresh(null);
      return showNote(spot);
    }
  }

  const QUOTE_MAX = 1000;
  const CONTEXT = 32;
  const clip = (text, n) => (text.length > n ? `${text.slice(0, n - 1).trimEnd()}\u2026` : text);
  const draftsKey = `kapi-drafts:${base.split("/").pop()}`;
  let drafts = loadDrafts();

  function loadDrafts() {
    try {
      const list = JSON.parse(sessionStorage.getItem(draftsKey) || "[]");
      return Array.isArray(list) ? list.filter((d) => d && typeof d.target === "string" && typeof d.text === "string") : [];
    } catch {
      return [];
    }
  }

  const handoffKey = `kapi-handoff:${base.split("/").pop()}`;

  const handoffList = () => {
    const list = JSON.parse(localStorage.getItem(handoffKey) || "[]");
    return Array.isArray(list) ? list.filter((d) => d && typeof d.target === "string" && typeof d.text === "string") : [];
  };

  function handOff() {
    if (!drafts.length) return;
    try {
      const left = handoffList();
      const known = new Set(left.map((d) => d.id));
      localStorage.setItem(handoffKey, JSON.stringify([...left, ...drafts.filter((d) => !known.has(d.id))]));
    } catch {
      return;
    }
    const n = drafts.length;
    changeDrafts(() => []);
    say(n === 1 ? "Your queued note moves to this page's next tab." : `Your ${n} queued notes move to this page's next tab.`);
  }

  let takeOverLater = false;

  function takeOver() {
    if (restarted) return;
    if (queueBox.querySelector("textarea")) return void (takeOverLater = true);
    let left = [];
    try {
      left = handoffList();
      localStorage.removeItem(handoffKey);
    } catch {}
    const known = new Set(drafts.map((d) => d.id));
    const fresh = left.filter((d) => !known.has(d.id));
    if (!fresh.length) return;
    changeDrafts((list) => [...list, ...fresh]);
    say(fresh.length === 1 ? "Brought over a queued note from the tab kapi restarted." : `Brought over ${fresh.length} queued notes from the tab kapi restarted.`);
  }

  function changeDrafts(change) {
    drafts = change(drafts);
    saveDrafts();
  }

  function saveDrafts() {
    try {
      if (drafts.length) sessionStorage.setItem(draftsKey, JSON.stringify(drafts));
      else sessionStorage.removeItem(draftsKey);
    } catch {}
    renderQueue();
  }

  function cellPart(cell) {
    const table = cell.closest("table");
    const all = rowsOf(table);
    const head = table.tHead?.rows[0] ?? all.find((tr) => tr.cells.length && [...tr.cells].every((c) => c.tagName === "TH"));
    const header = (tr) => tr.parentElement === table.tHead || tr === head;
    if (header(cell.parentElement)) return `column ${cell.textContent.trim()}`;
    const rows = all.filter((tr) => !header(tr));
    const row = `row ${rows.indexOf(cell.parentElement) + 1}`;
    const label = cell.getAttribute("data-kapi-label");
    return label ? `${row}, ${label}` : row;
  }

  function linePart(pre, range) {
    const lineAt = (node, offset) => {
      const r = document.createRange();
      r.setStart(pre, 0);
      r.setEnd(node, offset);
      return r.toString().split("\n").length;
    };
    const first = lineAt(range.startContainer, range.startOffset);
    const last = lineAt(range.endContainer, range.endOffset);
    return first === last ? `line ${first}` : `lines ${first} to ${last}`;
  }

  function within(range, el) {
    const r = document.createRange();
    r.selectNodeContents(el);
    if (range.compareBoundaryPoints(Range.START_TO_START, r) > 0) r.setStart(range.startContainer, range.startOffset);
    if (range.compareBoundaryPoints(Range.END_TO_END, r) < 0) r.setEnd(range.endContainer, range.endOffset);
    return r;
  }

  function rangeText(range) {
    const box = document.createElement("div");
    box.append(range.cloneContents());
    return textOf(box);
  }

  function context(first, last, range) {
    const before = document.createRange();
    before.setStart(first, 0);
    before.setEnd(range.startContainer, range.startOffset);
    const after = document.createRange();
    after.setStart(range.endContainer, range.endOffset);
    after.setEnd(last, last.childNodes.length);
    return { prefix: rangeText(before).slice(-CONTEXT), suffix: rangeText(after).slice(0, CONTEXT) };
  }

  function rangeAnchor(range) {
    const common = range.commonAncestorContainer;
    const host = closest(common.nodeType === Node.ELEMENT_NODE ? common : common.parentElement, BLOCK);
    const blocks = host && main.contains(host) ? [host]
      : [...main.querySelectorAll(BLOCK)].filter((b) => !b.parentElement.closest(BLOCK) && range.intersectsNode(b) && (textOf(b) ? rangeText(within(range, b)) : !b.childNodes.length || !within(range, b).collapsed));
    if (!blocks.length) return null;
    const at = (node) => (node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement);
    const both = (selector) => {
      const a = closest(at(range.startContainer), selector);
      return a && a === closest(at(range.endContainer), selector) && blocks[0].contains(a) ? a : null;
    };
    let part = "";
    if (blocks.length === 1) {
      const cell = both("td, th");
      const pre = both("pre");
      if (cell) part = cellPart(cell);
      else if (pre) part = linePart(pre, range);
      else if (both('[data-kapi-preset="badge"]')) part = "badge";
    }
    return {
      target: blocks.map(idOf).join(","),
      part,
      quote: clip(rangeText(range), QUOTE_MAX),
      ...context(blocks[0], blocks.at(-1), range),
    };
  }

  function blockAnchor(el) {
    const cell = lastPoint?.el === el && lastPoint.cell?.isConnected && el.contains(lastPoint.cell) ? lastPoint.cell : null;
    if (!cell) return { target: idOf(el), part: "", quote: "", prefix: "", suffix: "" };
    const range = document.createRange();
    range.selectNodeContents(cell);
    return { target: idOf(el), part: cellPart(cell), quote: clip(rangeText(range), QUOTE_MAX), ...context(el, el, range) };
  }

  function aboutText(anchor) {
    const root = anchor.re?.length && noteBySeq.get(anchor.re[0]);
    if (root) return `Reply to note ${root.num}`;
    const quote = anchor.quote && `\u201c${clip(anchor.quote, 60)}\u201d`;
    if (anchor.part) return quote ? `${anchor.part}: ${quote}` : anchor.part;
    return quote || describe(anchor.target.split(",")[0]);
  }

  function highlight(range) {
    if (!window.Highlight || !CSS.highlights) return;
    if (range) CSS.highlights.set("kapi-ask", new Highlight(range));
    else CSS.highlights.delete("kapi-ask");
  }

  function openAsk() {
    if (editing) return;
    if (textRange) {
      const anchor = rangeAnchor(textRange);
      if (anchor) showAsk(anchor, textRange.cloneRange(), textRange.cloneRange());
    } else if (selected) {
      const anchor = blockAnchor(selected);
      let range = null;
      if (anchor.part) {
        range = document.createRange();
        range.selectNodeContents(lastPoint.cell);
      }
      showAsk(anchor, range, null);
    }
  }

  function thumb(nameOrPath) {
    const img = h("img", { src: fileURL(nameOrPath), alt: "Attached image", loading: "lazy" });
    const link = h("a", { class: "kapi-thumb", href: fileURL(nameOrPath), target: "_blank", rel: "noopener", "aria-label": "Open image" }, img);
    img.addEventListener("error", () => link.replaceWith(h("span", { class: "kapi-thumb kapi-expired", text: "Image expired" })));
    return link;
  }

  const thumbs = (files) => (Array.isArray(files) && files.length ? [h("div", { class: "kapi-thumbs" }, ...files.map(thumb))] : []);

  function imageTray(onChange) {
    let items = [];
    let away = 0;
    const row = h("div", { class: "kapi-tray", hidden: "" });
    const picker = h("input", { type: "file", accept: IMAGE_TYPES.join(","), multiple: "", hidden: "", "aria-hidden": "true" });
    const pick = h("button", { type: "button", class: "kapi-attach", "aria-label": "Attach image", "data-tip": "Attach image", onclick: () => picker.click() }, icon("image"));
    const changed = () => {
      render();
      onChange();
    };
    function render() {
      const focused = row.contains(document.activeElement) ? document.activeElement : null;
      const keep = focused && { item: items[[...row.children].indexOf(focused.closest(".kapi-chip"))], cls: focused.className };
      row.hidden = !items.length;
      row.replaceChildren(...items.map((it) => h("div", { class: "kapi-chip", "data-state": it.state },
        it.name ? h("img", { src: fileURL(it.name), alt: "" }) : h("span", { class: "kapi-chip-label", text: it.state === "uploading" ? "Uploading\u2026" : it.label }),
        ...(it.state === "error" ? [h("span", { class: "kapi-chip-error", text: it.error }),
          h("button", { type: "button", class: "kapi-chip-retry", text: "Retry", onclick: () => upload(it) })] : []),
        h("button", { type: "button", class: "kapi-chip-remove", "aria-label": "Remove image", onclick: () => {
          items = items.filter((x) => x !== it);
          changed();
        } }, icon("x")))));
      const chip = keep && row.children[items.indexOf(keep.item)];
      const again = chip && (chip.querySelector(`.${keep.cls.split(" ")[0]}`) ?? chip.querySelector(".kapi-chip-remove"));
      if (again) again.focus();
      else if (focused) (row.querySelector(".kapi-chip-remove") ?? pick).focus();
    }
    async function upload(it) {
      it.state = "uploading";
      changed();
      let res = null;
      try {
        res = await fetch(`${base}/files`, { method: "POST", headers: { "Content-Type": it.file.type }, body: it.file });
      } catch {}
      if (res && (await restartedReply(res))) return;
      const data = res ? await res.json().catch(() => ({})) : {};
      if (!items.includes(it)) return;
      if (res?.ok && data.name) Object.assign(it, { name: data.name, state: "ready" });
      else Object.assign(it, { state: "error", error: data.error || (res ? `Not uploaded (HTTP ${res.status})` : "Could not reach kapi") });
      changed();
    }
    function add(files) {
      for (const file of files) {
        if (!IMAGE_TYPES.includes(file.type)) {
          say("Only PNG, JPEG, GIF and WebP images can be attached.", "error");
          continue;
        }
        if (items.length + away >= MAX_IMAGES) {
          say(`At most ${MAX_IMAGES} images per note or message.`, "error");
          break;
        }
        const it = { file, label: file.name || "image", state: "uploading" };
        items.push(it);
        upload(it);
      }
    }
    picker.addEventListener("change", () => {
      add([...picker.files]);
      picker.value = "";
    });
    return {
      row, pick, picker, add,
      names: () => items.filter((i) => i.state === "ready").map((i) => i.name),
      count: () => items.length,
      blocked() {
        if (items.some((i) => i.state === "uploading")) return "Wait for the images to finish uploading.";
        if (items.some((i) => i.state === "error")) return "Retry or remove the image that did not upload.";
        return "";
      },
      take(names) {
        items = items.filter((i) => !names.includes(i.name));
        away += names.length;
        changed();
      },
      settle(names, sent) {
        away -= names.length;
        if (!sent) items = [...names.map((name) => ({ name, state: "ready", label: "" })), ...items];
        changed();
      },
    };
  }

  function acceptImages(el, tray) {
    const files = (list) => [...(list || [])].filter((f) => f.kind === undefined || f.kind === "file");
    el.addEventListener("paste", (e) => {
      const found = files(e.clipboardData?.files);
      if (!found.length || e.clipboardData.getData("text/plain")) return;
      e.preventDefault();
      tray.add(found);
    });
    el.addEventListener("dragover", (e) => {
      if (![...(e.dataTransfer?.types || [])].includes("Files")) return;
      e.preventDefault();
      el.setAttribute("data-drop", "");
    });
    el.addEventListener("dragleave", () => el.removeAttribute("data-drop"));
    el.addEventListener("drop", (e) => {
      const found = files(e.dataTransfer?.files);
      el.removeAttribute("data-drop");
      if (!found.length) return;
      e.preventDefault();
      tray.add(found);
    });
  }

  async function addImages(files, after) {
    const images = files.filter((f) => IMAGE_TYPES.includes(f.type));
    let anchor = after;
    for (const file of images) {
      say(`Adding ${file.name || "the image"}\u2026`);
      let res = null;
      try {
        res = await fetch(`${base}/images`, { method: "POST", headers: { "Content-Type": file.type }, body: file });
      } catch {}
      if (res && (await restartedReply(res))) return;
      const data = res ? await res.json().catch(() => ({})) : {};
      if (!res?.ok || !data.name) return say(data.error || (res ? `Image not added (HTTP ${res.status}).` : "Could not reach kapi."), "error");
      let done = await op("image", { id: anchor, src: data.name });
      if (done?.status === 409) done = await op("image", { id: anchor && find(anchor) ? anchor : "", src: data.name });
      if (!done?.ok) return;
      await refresh(null);
      notice.hidden = true;
      const fig = done.data.ids?.[0] && find(done.data.ids[0]);
      if (fig) {
        anchor = idOf(fig);
        flash(fig);
      }
    }
    if (images.length < files.length) say("Only PNG, JPEG, GIF and WebP images can be added to the page.", "error");
  }

  function showAsk(anchor, range, restore) {
    closeAsk();
    const about = h("span", { class: "kapi-ask-about", text: `About: ${aboutText(anchor)}` });
    const head = h("div", { class: "kapi-ask-head" }, about);
    if (anchor.part && !anchor.target.includes(",") && !anchor.re?.length) {
      head.append(h("button", { type: "button", class: "kapi-ask-widen", "aria-label": "Ask about the whole block", title: "Ask about the whole block", onclick: () => {
        asking.anchor = { target: anchor.target.split(",")[0], part: "", quote: "", prefix: "", suffix: "" };
        about.textContent = `About: ${aboutText(asking.anchor)}`;
        head.lastChild.remove();
        highlight(null);
        asking.text.focus();
      } }, icon("x")));
    }
    const text = h("textarea", { rows: "3", placeholder: "Ask the agent about this", "aria-label": "Note for the agent" });
    const queueButton = h("button", { type: "button", class: "kapi-ask-queue", title: "Queue (Enter)", onclick: () => queueAsk() },
      "Queue", h("span", { class: "kapi-key", text: " \u21b5" }));
    const sendNow = h("button", { type: "button", class: "kapi-primary kapi-ask-send", title: `Send with everything queued (${SHORTCUT})`, onclick: () => sendAsk() },
      "Send", h("span", { class: "kapi-key", text: ` ${SHORTCUT}` }));
    const cancel = h("button", { type: "button", class: "kapi-ask-cancel", title: "Cancel (Escape)", text: "Cancel", onclick: () => closeAsk(true) });
    const tray = imageTray(() => sync());
    const sync = () => {
      queueButton.disabled = sendNow.disabled = (!text.value.trim() && !tray.count()) || !!tray.blocked() || sending;
    };
    text.addEventListener("input", sync);
    text.addEventListener("keydown", (e) => {
      if (e.isComposing || e.keyCode === 229) return;
      if (e.key === "Escape") return;
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        sendAsk();
      } else if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        queueAsk();
      }
    });
    const box = h("div", { class: "kapi-ask", role: "dialog", "aria-label": "Note for the agent" }, head, text, tray.row,
      h("div", { class: "kapi-ask-row" }, tray.pick, tray.picker, cancel, queueButton, sendNow));
    acceptImages(box, tray);
    box.addEventListener("keydown", (e) => {
      if (e.key !== "Escape" || e.isComposing || e.keyCode === 229) return;
      e.stopPropagation();
      closeAsk(true);
    });
    root.append(box);
    asking = { box, anchor, range, restore, text, tray, sync, at: (range ?? selected)?.getBoundingClientRect() };
    sync();
    highlight(range);
    renderBar();
    placeAsk();
    text.focus({ preventScroll: true });
  }

  function placeAsk() {
    if (!asking || phone.matches) return;
    const live = asking.range?.getClientRects().length ? asking.range : find(asking.anchor.target.split(",")[0]);
    const r = live?.isConnected === false ? asking.at : (live?.getBoundingClientRect() ?? asking.at);
    asking.at = r;
    const areaRight = document.documentElement.clientWidth - panel.getBoundingClientRect().width;
    const { width, height } = asking.box.getBoundingClientRect();
    const x = Math.min(Math.max(r.left, 8), areaRight - width - 8);
    const below = r.bottom + BAR_GAP;
    const y = below + height <= innerHeight - 8 ? below : Math.max(8, r.top - BAR_GAP - height);
    asking.box.style.left = `${Math.max(8, x)}px`;
    asking.box.style.top = `${y}px`;
  }

  function closeAsk(restore = false) {
    if (!asking) return;
    const { box, restore: range } = asking;
    asking = null;
    box.remove();
    highlight(null);
    renderBar();
    if (restore && range && main.contains(range.startContainer) && !range.collapsed && range.startContainer.isConnected) {
      getSelection().removeAllRanges();
      getSelection().addRange(range);
      trackSelection();
    }
  }

  function queueAsk() {
    const text = asking?.text.value.trim() ?? "";
    const files = asking?.tray.names() ?? [];
    if ((!text && !files.length) || asking.tray.blocked() || sending) return false;
    const { target, part, quote, prefix, suffix, re = [] } = asking.anchor;
    const id = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
    closeAsk();
    changeDrafts((list) => [...list, { id, target, part, quote, prefix, suffix, re, ...(files.length ? { files } : {}), text }]);
    return true;
  }

  function sendAsk() {
    if (queueAsk()) sendNotes();
  }

  async function sendNotes() {
    if (sending) return;
    const text = input.value.trim();
    if (!drafts.length) return send();
    if (composerTray.blocked()) return say(composerTray.blocked());
    const files = composerTray.names();
    const batch = drafts.slice();
    const ids = new Set(batch.map((d) => d.id));
    sending = true;
    renderQueue();
    input.value = "";
    composerTray.take(files);
    const res = await request("/notes", { rev: Number(main.dataset.rev), notes: batch.map(({ target, part, quote, prefix, suffix, re, files: f, text: t }) => ({ target, part, quote, prefix, suffix,
      ...(Array.isArray(re) && re.length ? { re } : {}), ...(Array.isArray(f) && f.length ? { files: f } : {}), text: t })), text, ...(files.length ? { files } : {}) });
    sending = false;
    composerTray.settle(files, !!res?.ok);
    asking?.sync();
    if (res?.ok) {
      return changeDrafts((list) => list.filter((d) => !ids.has(d.id)));
    }
    if (text) input.value = input.value ? `${text}\n${input.value}` : text;
    renderQueue();
    if (res) say(res.data.error || `Notes not sent (HTTP ${res.status}).`, "error");
  }

  function editDraft(item, d) {
    const area = h("textarea", { rows: "2", "aria-label": "Edit queued note" });
    area.value = d.text;
    let finished = false;
    const done = (save) => {
      if (finished) return;
      finished = true;
      const next = area.value.trim();
      if (save && (next || d.files?.length) && next !== d.text) changeDrafts((list) => list.map((x) => (x.id === d.id ? { ...x, text: next } : x)));
      else renderQueue();
      if (takeOverLater) {
        takeOverLater = false;
        takeOver();
      }
    };
    area.addEventListener("keydown", (e) => {
      if (e.isComposing || e.keyCode === 229) return;
      if (e.key === "Escape") {
        e.stopPropagation();
        done(false);
      } else if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        done(true);
      }
    });
    area.addEventListener("blur", () => done(true));
    item.querySelector(".kapi-queued-text").replaceWith(area);
    area.focus();
  }

  function renderQueue() {
    const n = drafts.length;
    queueBox.hidden = !n;
    hint.textContent = n ? `Enter sends ${n === 1 ? "the note" : `the ${n} notes`}` : HINT;
    queueBox.replaceChildren(h("h2", { class: "kapi-queue-head", text: n === 1 ? "1 note queued" : `${n} notes queued` }),
      ...drafts.map((d) => {
        const item = h("div", { class: "kapi-queued-note", "data-draft": d.id },
          h("button", { type: "button", class: "kapi-ref kapi-queued-about", "data-ref": d.target.split(",")[0], text: aboutText(d) }),
          h("div", { class: "kapi-queued-text", text: d.text }),
          ...thumbs(d.files),
          h("div", { class: "kapi-queued-tools" },
            h("button", { type: "button", "aria-label": "Edit note", "data-tip": "Edit", onclick: () => editDraft(item, d) }, icon("pencil")),
            h("button", { type: "button", "aria-label": "Remove note", "data-tip": "Remove", onclick: () => {
              changeDrafts((list) => list.filter((x) => x.id !== d.id));
            } }, icon("x"))));
        return item;
      }));
    for (const b of queueBox.querySelectorAll(".kapi-queued-tools button")) b.disabled = sending;
  }

  const notes = [];
  const noteBySeq = new Map();
  const befores = new Map();
  const diffs = new Map();
  const pinEls = new Map();
  let paintQueued = false;
  let currentNote = 0;
  let pop = null;
  let popTimer = 0;
  let sheetOpen = false;
  const STATE_LABEL = { waiting: "Waiting", answered: "Answered", changed: "Changed", resolved: "Resolved" };
  const NOTE_STATES = ["waiting", "answered", "changed"];
  const DIFF_MAX = 4000000;
  const DIFF_CONTEXT = 4;
  const stateOf = (n) => (n.resolved ? "resolved" : n.last);
  const targetsOf = (n) => n.target.split(",").map((s) => s.trim()).filter(Boolean);
  const reduced = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

  function trackNote(ev) {
    const re = Array.isArray(ev.re) ? ev.re : [];
    if (ev.kind === "note" && !re.length && ev.author === "human") {
      const n = { seq: ev.seq, num: notes.length + 1, target: ev.target || "", part: ev.part || "", quote: ev.quote || "", prefix: ev.prefix || "",
        suffix: ev.suffix || "", rev: ev.rev || 0, text: ev.text, time: ev.time, replies: [], last: "waiting", touched: 0, changedTime: "", resolved: false, files: ev.files || [] };
      notes.push(n);
      noteBySeq.set(ev.seq, n);
    } else if ((ev.kind === "note" || ev.kind === "message") && re.length) {
      for (const seq of re) {
        const n = noteBySeq.get(seq);
        if (!n) continue;
        n.replies.push({ seq: ev.seq, author: ev.author, text: ev.text, time: ev.time, files: ev.files || [] });
        if (ev.kind === "note" && !noteBySeq.has(ev.seq)) noteBySeq.set(ev.seq, n);
        n.last = ev.author === "human" ? "waiting" : "answered";
      }
    } else if (ev.kind === "revision" && ev.author !== "human") {
      const ids = (ev.changed || ev.target || "").split(",").map((s) => s.trim()).filter(Boolean);
      const within = (t) => ids.includes(t) || (!ev.changed && ids.some((id) => !!find(t) && !!find(id)?.contains(find(t))));
      for (const n of notes) {
        if (n.resolved || n.seq > ev.seq || !targetsOf(n).some(within)) continue;
        n.touched = ev.seq;
        n.changedTime = ev.time || "";
        n.changedBy = ev.author;
        n.last = "changed";
      }
    } else if (ev.kind === "resolve") {
      for (const seq of re) {
        const n = noteBySeq.get(seq);
        if (n) n.resolved = true;
      }
    } else return;
    schedulePaint();
  }

  function schedulePaint() {
    if (paintQueued) return;
    paintQueued = true;
    requestAnimationFrame(() => {
      paintQueued = false;
      paintNotes();
    });
  }

  function textMap(blocks) {
    let text = "";
    const at = [];
    const push = (ch, node, offset) => {
      if (ch === " " && (!text || text.endsWith(" "))) return;
      text += ch;
      at.push([node, offset]);
    };
    const walk = (el) => {
      for (const c of el.childNodes) {
        if (c.nodeType === Node.TEXT_NODE) {
          for (let i = 0; i < c.data.length; i++) push(/\s/.test(c.data[i]) ? " " : c.data[i], c, i);
        } else if (c.nodeType === Node.ELEMENT_NODE && !c.matches(CHROME_TEXT)) {
          const gap = !INLINE.test(c.tagName);
          if (gap) push(" ", c, 0);
          walk(c);
          if (gap) push(" ", c, c.childNodes.length);
        }
      }
    };
    blocks.forEach((b, i) => {
      if (i) push(" ", b, 0);
      walk(b);
    });
    const range = (from, to) => {
      while (from < to && text[from] === " ") from++;
      while (to > from && text[to - 1] === " ") to--;
      if (from >= to) return null;
      const r = document.createRange();
      const [sn, so] = at[from];
      const [en, eo] = at[to - 1];
      r.setStart(sn, so);
      if (en.nodeType === Node.TEXT_NODE) r.setEnd(en, eo + 1);
      else r.setEnd(en, eo);
      return r;
    };
    return { text, range };
  }

  const tail = (a, b) => {
    let i = 0;
    while (i < a.length && i < b.length && a[a.length - 1 - i] === b[b.length - 1 - i]) i++;
    return i;
  };
  const head = (a, b) => {
    let i = 0;
    while (i < a.length && i < b.length && a[i] === b[i]) i++;
    return i;
  };

  function findQuote(map, n) {
    const quote = n.quote.endsWith("\u2026") && n.quote.length >= QUOTE_MAX - 1 ? n.quote.slice(0, -1) : n.quote;
    if (!quote) return null;
    let best = -1;
    let score = -1;
    for (let i = map.text.indexOf(quote); i >= 0; i = map.text.indexOf(quote, i + 1)) {
      const s = tail(map.text.slice(Math.max(0, i - CONTEXT - 1), i).trim(), n.prefix.trim()) + head(map.text.slice(i + quote.length).trim(), n.suffix.trim());
      if (s > score) [best, score] = [i, s];
    }
    return best < 0 ? null : map.range(best, best + quote.length);
  }

  function wordsOf(map) {
    const words = [];
    const re = /\S+/g;
    for (let m; (m = re.exec(map.text));) words.push({ w: m[0], from: m.index, to: m.index + m[0].length });
    return words;
  }

  function diffWords(old, now) {
    const a = old.map((x) => x.w);
    const b = now.map((x) => x.w);
    let lo = 0;
    while (lo < a.length && lo < b.length && a[lo] === b[lo]) lo++;
    let hi = 0;
    while (hi < a.length - lo && hi < b.length - lo && a[a.length - 1 - hi] === b[b.length - 1 - hi]) hi++;
    const m = a.length - lo - hi;
    const k = b.length - lo - hi;
    if ((m + 1) * (k + 1) > DIFF_MAX) return null;
    const w = k + 1;
    const lcs = new Uint32Array((m + 1) * w);
    for (let i = m - 1; i >= 0; i--) {
      for (let j = k - 1; j >= 0; j--) {
        lcs[i * w + j] = a[lo + i] === b[lo + j] ? lcs[(i + 1) * w + j + 1] + 1 : Math.max(lcs[(i + 1) * w + j], lcs[i * w + j + 1]);
      }
    }
    const removed = [];
    const added = [];
    let i = 0;
    let j = 0;
    const run = (list, at, word) => {
      const last = list.at(-1);
      if (last && last.end === at) {
        last.words.push(word);
        last.end = at + 1;
      } else list.push({ start: at, end: at + 1, words: [word] });
    };
    while (i < m || j < k) {
      if (i < m && j < k && a[lo + i] === b[lo + j]) {
        i++;
        j++;
      } else if (j < k && (i === m || lcs[i * w + j + 1] >= lcs[(i + 1) * w + j])) {
        run(added, lo + j, b[lo + j]);
        j++;
      } else {
        run(removed, lo + i, a[lo + i]);
        i++;
      }
    }
    return { removed, added, lo, oldEnd: a.length - hi, newEnd: b.length - hi };
  }

  function loadBefore(n) {
    for (const id of targetsOf(n)) {
      const key = `${id}@${n.rev}`;
      if (befores.has(key)) continue;
      befores.set(key, undefined);
      fetch(`${base}/blocks/${encodeURIComponent(id)}?rev=${n.rev}`).then(async (res) => {
        const tpl = document.createElement("template");
        if (res.ok) tpl.innerHTML = (await res.text()).trim();
        befores.set(key, tpl.content.firstElementChild ?? null);
        schedulePaint();
      }, () => befores.delete(key));
    }
  }

  function changeOf(n, blocks) {
    if (!n.touched || n.resolved || !n.rev) return null;
    loadBefore(n);
    const old = targetsOf(n).map((id) => befores.get(`${id}@${n.rev}`));
    if (old.some((el) => el === undefined)) return null;
    const live = blocks.filter(Boolean);
    const nowMap = textMap(live);
    let d = diffs.get(n.seq);
    if (d?.text !== nowMap.text) {
      const before = wordsOf(textMap(old.filter(Boolean)));
      const now = wordsOf(nowMap);
      d = { text: nowMap.text, before, now, result: diffWords(before, now) };
      diffs.set(n.seq, d);
    }
    if (!d.result) return { tooLong: true, marks: [] };
    const marks = d.result.added.map((r) => nowMap.range(d.now[r.start].from, d.now[r.end - 1].to)).filter(Boolean);
    const span = (words, end) => {
      if (end <= d.result.lo) return "";
      const from = Math.max(0, d.result.lo - DIFF_CONTEXT);
      const to = Math.min(words.length, end + DIFF_CONTEXT);
      return `${from ? "\u2026 " : ""}${words.slice(from, to).map((x) => x.w).join(" ")}${to < words.length ? " \u2026" : ""}`;
    };
    return { before: span(d.before, d.result.oldEnd), now: span(d.now, d.result.newEnd), marks };
  }

  function sectionOf(el) {
    let top = el;
    while (top && top.parentElement !== main) top = top.parentElement;
    for (let p = top; p; p = p.previousElementSibling) if (levelOf(p)) return textOf(p);
    return "";
  }

  function locate(n) {
    const blocks = targetsOf(n).map(find);
    const first = blocks.find(Boolean) ?? null;
    if (!first) return { blocks, first, range: null, gone: true };
    const visible = !first.hasAttribute("data-kapi-hidden") && first.getAttribute("data-kapi-state") !== "archived";
    const range = visible ? findQuote(textMap(blocks.filter(Boolean)), n) : null;
    return { blocks, first, range, gone: false, visible };
  }

  function paintNotes() {
    const hl = window.Highlight && CSS.highlights;
    const ranges = { waiting: [], answered: [], changed: [], marks: [] };
    const seen = new Set();
    for (const n of notes) {
      const where = locate(n);
      n.where = where;
      n.change = changeOf(n, where.blocks);
      if (!where.range && where.visible && n.change?.marks.length) where.range = n.change.marks[0];
      n.section = where.first ? sectionOf(where.first) : n.section || "";
      const state = stateOf(n);
      if (state !== "resolved" && where.range) ranges[state].push(where.range);
      if (n.change && where.visible) ranges.marks.push(...n.change.marks);
      if (!where.first || where.visible === false) continue;
      seen.add(n.seq);
      let pin = pinEls.get(n.seq);
      if (!pin) {
        pin = h("button", { type: "button", class: "kapi-pin", "data-note": n.seq });
        pinEls.set(n.seq, pin);
        pins.append(pin);
      }
      pin.textContent = n.num;
      pin.dataset.state = state;
      pin.setAttribute("aria-label", `Note ${n.num}, ${STATE_LABEL[state].toLowerCase()}`);
      placePin(pin, n);
    }
    for (const [seq, pin] of pinEls) {
      if (!seen.has(seq)) {
        pin.remove();
        pinEls.delete(seq);
      }
    }
    if (hl) {
      for (const s of NOTE_STATES) CSS.highlights.set(`kapi-note-${s}`, new Highlight(...ranges[s]));
      CSS.highlights.set("kapi-changed", new Highlight(...ranges.marks));
    }
    renderNotesList();
    paintRail();
    if (pop) fillPop(noteBySeq.get(pop.seq));
  }

  function anchorRect(n) {
    const { range, first } = n.where || locate(n);
    const rects = range ? [...range.getClientRects()].filter((r) => r.width || r.height) : [];
    if (rects.length) return rects.at(-1);
    return first?.getBoundingClientRect() ?? null;
  }

  function placePin(pin, n) {
    const { range, first } = n.where;
    const rects = range ? [...range.getClientRects()].filter((r) => r.width || r.height) : [];
    const r = rects.at(-1);
    const box = first.getBoundingClientRect();
    const x = r ? r.right + 1 : box.right - 22;
    const y = r ? r.top - 7 : box.top - 6;
    pin.style.left = `${Math.round(x + scrollX)}px`;
    pin.style.top = `${Math.round(y + scrollY)}px`;
  }

  function placePins() {
    for (const n of notes) {
      const pin = pinEls.get(n.seq);
      if (pin && n.where?.first?.isConnected) placePin(pin, n);
    }
    paintRail();
  }

  function openNotes() {
    return notes.filter((n) => !n.resolved);
  }

  function pageOrder(list) {
    const y = (n) => {
      const r = n.where?.first?.isConnected ? anchorRect(n) : null;
      return r ? r.top + scrollY : Infinity;
    };
    return list.map((n) => [y(n), n]).sort((a, b) => a[0] - b[0] || a[1].seq - b[1].seq).map(([, n]) => n);
  }

  function stepNote(dir, only) {
    const list = pageOrder(openNotes().filter((n) => !only || stateOf(n) === only));
    if (!list.length) return say(only ? "No changed notes left." : "No open notes on this page.");
    const i = list.findIndex((n) => n.seq === currentNote);
    const next = i < 0 ? list[dir > 0 ? 0 : list.length - 1] : list[(i + dir + list.length) % list.length];
    focusNote(next);
  }

  function focusNote(n) {
    if (!n) return;
    currentNote = n.seq;
    hidePop(true);
    setSheet(false);
    renderNotesList();
    const where = locate(n);
    n.where = where;
    if (!where.first) return showPop(n, null);
    const target = where.range ? where.range.startContainer.parentElement : where.first;
    target.scrollIntoView({ block: "center", behavior: reduced() ? "auto" : "smooth" });
    where.first.removeAttribute("data-kapi-note-flash");
    void where.first.offsetWidth;
    where.first.setAttribute("data-kapi-note-flash", "");
    setTimeout(() => where.first.removeAttribute("data-kapi-note-flash"), 1000);
    setTimeout(() => {
      placePins();
      if (currentNote === n.seq) showPop(n, pinEls.get(n.seq) ?? where.first);
    }, reduced() ? 0 : 450);
  }

  function ago(time) {
    const t = new Date(time);
    if (!time || isNaN(t)) return "";
    const min = Math.round((Date.now() - t) / 60000);
    if (min < 1) return "just now";
    if (min < 60) return `${min} min ago`;
    return t.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  }

  const pinChip = (n, extra = "") => h("span", { class: "kapi-pin kapi-pin-static", "data-state": stateOf(n), "aria-hidden": "true", text: `${n.num}${extra}` });
  const stateChip = (n) => h("span", { class: "kapi-note-chip", "data-state": stateOf(n), text: STATE_LABEL[stateOf(n)] });
  const noteAbout = (n) => (n.quote ? `\u201c${clip(n.quote, 120)}\u201d` : n.part || describe(targetsOf(n)[0] || ""));

  function fillPop(n) {
    if (!pop || !n) return;
    const state = stateOf(n);
    const lastReply = n.replies.at(-1);
    const when = state === "changed" ? `${n.changedBy === "file" ? "Edited in the file" : `${n.changedBy} edited`} \u00b7 ${ago(n.changedTime)}` : lastReply && lastReply.author !== "human" ? `${lastReply.author} \u00b7 ${ago(lastReply.time)}` : `Sent ${ago(n.time)}`;
    const kids = [h("div", { class: "kapi-pop-head" }, pinChip(n), stateChip(n), h("span", { class: "kapi-pop-when", text: when }))];
    if (n.where?.gone) kids.push(h("p", { class: "kapi-pop-gone", text: "This block is no longer on the page." }));
    const c = n.change;
    if (c?.tooLong) kids.push(h("p", { class: "kapi-pop-gone", text: "Changed. Too long to compare word by word." }));
    else if (c && (c.before || c.now)) {
      kids.push(h("div", { class: "kapi-diff-box" },
        ...(c.before ? [h("div", { class: "kapi-diff-row" }, h("span", { class: "kapi-diff-k", text: "Before" }), h("span", { class: "kapi-diff-old", text: clip(c.before, 400) }))] : []),
        ...(c.now ? [h("div", { class: "kapi-diff-row" }, h("span", { class: "kapi-diff-k", text: "Now" }), h("span", { class: "kapi-diff-new", text: clip(c.now, 400) }))] : [])));
    }
    const msg = (who, text, files) => h("div", { class: "kapi-pop-msg" }, h("span", { class: "kapi-pop-who", text: who }), h("span", { class: "kapi-pop-text", text }), ...thumbs(files));
    kids.push(msg("You", n.text, n.files));
    for (const r of n.replies) kids.push(msg(r.author === "human" ? "You" : r.author, r.text, r.files));
    if (!n.replies.some((r) => r.author !== "human") && state !== "changed") kids.push(h("p", { class: "kapi-pop-gone", text: "The agent has not answered yet." }));
    const foot = h("div", { class: "kapi-pop-foot" }, h("span", { class: "kapi-pop-hint", text: matchMedia("(hover: hover)").matches ? "Move away to close" : "Tap outside to close" }));
    if (!n.resolved) {
      foot.append(
        h("button", { type: "button", class: "kapi-link", text: "Reply", onclick: () => replyTo(n) }),
        h("button", { type: "button", class: "kapi-link", text: "Resolve", onclick: () => resolveNote(n) }));
    }
    kids.push(foot);
    pop.box.replaceChildren(...kids);
    placePop();
  }

  function showPop(n, anchor) {
    clearTimeout(popTimer);
    if (asking || editing) return;
    if (!pop || pop.seq !== n.seq) {
      pop?.box.remove();
      const box = h("div", { class: "kapi-note-pop", role: "dialog", "aria-label": `Note ${n.num}` });
      box.addEventListener("pointerenter", () => clearTimeout(popTimer));
      box.addEventListener("pointerleave", (e) => {
        if (e.pointerType === "mouse") hidePop();
      });
      root.append(box);
      pop = { seq: n.seq, box, anchor };
    }
    pop.anchor = anchor;
    fillPop(n);
  }

  function placePop() {
    if (!pop) return;
    const a = pop.anchor?.isConnected ? pop.anchor.getBoundingClientRect() : null;
    const { width, height } = pop.box.getBoundingClientRect();
    const areaRight = phone.matches ? innerWidth : document.documentElement.clientWidth - panel.getBoundingClientRect().width;
    if (!a) {
      pop.box.style.left = `${Math.max(8, (areaRight - width) / 2)}px`;
      pop.box.style.top = `${Math.max(8, (innerHeight - height) / 3)}px`;
      return;
    }
    const left = Math.min(Math.max(8, a.left + a.width / 2 - 28), areaRight - width - 8);
    const below = a.bottom + 10 + height < innerHeight - 8 || a.top - 10 - height < 8;
    pop.box.toggleAttribute("data-above", !below);
    pop.box.style.left = `${Math.max(8, left)}px`;
    pop.box.style.top = `${below ? a.bottom + 10 : a.top - 10 - height}px`;
    pop.box.style.setProperty("--kapi-arrow", `${Math.min(Math.max(14, a.left + a.width / 2 - left - 6), width - 26)}px`);
  }

  function hidePop(now = false) {
    clearTimeout(popTimer);
    const go = () => {
      pop?.box.remove();
      pop = null;
    };
    if (now) go();
    else popTimer = setTimeout(go, 220);
  }

  function replyTo(n) {
    hidePop(true);
    const range = locate(n).range;
    showAsk({ target: n.target, part: n.part, quote: n.quote, prefix: n.prefix, suffix: n.suffix, re: [n.seq] }, range, null);
  }

  async function resolveNote(n) {
    const res = await request("/resolve", { note: n.seq });
    if (res && !res.ok) say(res.data.error || `Not resolved (HTTP ${res.status}).`, "error");
    if (res?.ok) hidePop(true);
  }

  function noteAt(x, y) {
    const el = document.elementFromPoint(x, y);
    const block = el && closest(el, BLOCK);
    if (!block || !main.contains(block)) return null;
    for (const n of notes) {
      if (n.resolved || !n.where?.range || !targetsOf(n).some((id) => find(id)?.contains(block) || block.contains(find(id)))) continue;
      if ([...n.where.range.getClientRects()].some((r) => x >= r.left && x <= r.right && y >= r.top && y <= r.bottom)) return n;
    }
    return null;
  }

  let hoverFrame = 0;
  main.addEventListener("pointermove", (e) => {
    if (e.pointerType !== "mouse" || e.buttons || hoverFrame) return;
    hoverFrame = requestAnimationFrame(() => {
      hoverFrame = 0;
      const n = noteAt(e.clientX, e.clientY);
      if (n) showPop(n, pinEls.get(n.seq) ?? n.where.first);
      else if (pop && !pop.box.matches(":hover")) hidePop();
    });
  });

  function renderNotesList() {
    const open = openNotes();
    const changed = open.filter((n) => stateOf(n) === "changed").length;
    const summary = notes.length ? `${open.length} open \u00b7 ${changed} changed` : "";
    notesTab.textContent = open.length ? `Notes ${open.length}` : "Notes";
    tabs.hidden = !notes.length;
    strip.hidden = !notes.length;
    stripSummary.textContent = notes.length === 1 ? `1 note \u00b7 ${changed} changed` : `${notes.length} notes \u00b7 ${changed} changed`;
    if (!notes.length) return notesView.replaceChildren();
    const nav = h("div", { class: "kapi-notes-nav" },
      h("button", { type: "button", class: "kapi-btn", text: "Previous", onclick: () => stepNote(-1) }),
      h("button", { type: "button", class: "kapi-btn", text: "Next", onclick: () => stepNote(1) }),
      h("button", { type: "button", class: "kapi-btn kapi-primary", text: "Next change", onclick: () => stepNote(1, "changed") }));
    if (!changed) nav.lastChild.disabled = true;
    const item = (n) => h("li", {}, h("button", { type: "button", class: `kapi-note-item${currentNote === n.seq ? " kapi-current" : ""}`, "data-note": n.seq, "data-state": stateOf(n) },
      pinChip(n),
      h("span", { class: "kapi-note-body" },
        ...(n.section || n.where?.gone ? [h("span", { class: "kapi-note-crumb", text: n.where?.gone ? "Block removed" : n.section })] : []),
        h("span", { class: "kapi-note-quote", text: noteAbout(n) }),
        h("span", { class: "kapi-note-text", text: n.text }),
        h("span", { class: "kapi-note-chip", "data-state": stateOf(n), text: stateOf(n) === "changed" && n.changedTime ? `Changed \u00b7 ${ago(n.changedTime)}` : STATE_LABEL[stateOf(n)] }))));
    const resolved = notes.filter((n) => n.resolved);
    notesView.replaceChildren(
      h("div", { class: "kapi-notes-head" }, h("h2", { text: "Notes" }), h("span", { class: "kapi-notes-summary", text: summary }),
        h("button", { type: "button", class: "kapi-notes-close", "aria-label": "Close notes", onclick: () => setSheet(false) }, icon("x"))),
      nav,
      h("p", { class: "kapi-notes-keys" }, h("kbd", { text: "J" }), " next  ", h("kbd", { text: "K" }), " previous"),
      ...(open.length ? [h("ul", { class: "kapi-note-list" }, ...pageOrder(open).map(item))] : [h("p", { class: "kapi-empty", text: "Every note is resolved." })]),
      ...(resolved.length ? [h("h3", { class: "kapi-note-group", text: "Resolved" }), h("ul", { class: "kapi-note-list" }, ...resolved.map(item))] : []));
  }

  function paintRail() {
    const desktop = !phone.matches;
    const list = desktop ? openNotes().filter((n) => n.where?.first?.isConnected && n.where.visible !== false) : [];
    rail.hidden = !list.length;
    const height = document.documentElement.scrollHeight;
    rail.replaceChildren(...list.map((n) => {
      const r = anchorRect(n);
      const top = r ? (r.top + scrollY) / height : 0;
      return h("button", { type: "button", class: "kapi-rail-dot", "data-note": n.seq, "data-state": stateOf(n), "aria-label": `Note ${n.num}, ${STATE_LABEL[stateOf(n)].toLowerCase()}`,
        "data-tip": `Note ${n.num}`, style: `top: calc(${(top * 100).toFixed(2)}% - 5px)` });
    }));
  }

  function showTab(name) {
    const notesOn = name === "notes";
    chatTab.setAttribute("aria-selected", String(!notesOn));
    notesTab.setAttribute("aria-selected", String(notesOn));
    panel.toggleAttribute("data-notes", notesOn);
  }

  function setSheet(open) {
    sheetOpen = open && phone.matches;
    root.toggleAttribute("data-notes-sheet", sheetOpen);
  }

  function refresh(ids) {
    queue = queue.then(() => (ids ? loadBlocks(ids) : loadPage())).catch(() =>
      say("Could not load the latest page from kapi.", "error"));
    return queue;
  }

  const touchesEdit = (el) => !!editing && !!el && (editing.el.contains(el) || el.contains(editing.el));

  function schedule(ids) {
    pending = pending === "page" || !ids ? "page" : new Set([...(pending || []), ...ids]);
    scheduleFlush();
  }

  function scheduleFlush() {
    clearTimeout(flushTimer);
    flushTimer = setTimeout(() => {
      if (!pending) return;
      if (pending === "page") {
        pending = null;
        return refresh(null);
      }
      const held = [...pending].filter((id) => touchesEdit(find(id)));
      const ids = [...pending].filter((id) => !held.includes(id));
      pending = held.length ? new Set(held) : null;
      if (ids.length) refresh(ids);
    }, 30);
  }

  async function loadPage() {
    if (editing) return void (pending = "page");
    const res = await fetch(base + "/", { headers: { Accept: "text/html" } });
    if (!res.ok) throw new Error(res.status);
    const fresh = new DOMParser().parseFromString(await res.text(), "text/html").getElementById("kapi-page");
    if (!fresh) throw new Error("no page");
    if (editing) return void (pending = "page");
    const keep = selected && idOf(selected);
    selected = null;
    main.replaceChildren(...fresh.childNodes);
    main.dataset.rev = fresh.dataset.rev;
    showUndo(fresh.dataset.undo);
    labelArchived(main);
    numberRows(main);
    applyFolds();
    select(keep ? find(keep) : null);
    placeNote();
    schedulePaint();
  }

  async function loadBlocks(ids) {
    for (const id of ids) {
      const el = find(id);
      if (!el) return loadPage();
      if (touchesEdit(el)) {
        schedule([id]);
        continue;
      }
      const res = await fetch(`${base}/blocks/${encodeURIComponent(id)}`);
      if (!res.ok) return loadPage();
      const tpl = document.createElement("template");
      tpl.innerHTML = (await res.text()).trim();
      const next = tpl.content.firstElementChild;
      if (!next) return loadPage();
      if (touchesEdit(el)) {
        schedule([id]);
        continue;
      }
      el.replaceWith(next);
      labelArchived(next);
      numberRows(next);
      const rev = res.headers.get("Kapi-Rev");
      if (rev) main.dataset.rev = rev;
      if (res.headers.has("Kapi-Undo")) showUndo(res.headers.get("Kapi-Undo"));
      if (selected === el) {
        selected = null;
        select(next);
      }
    }
    applyFolds();
    placeNote();
    schedulePaint();
  }

  function addEntry(ev, body) {
    const near = log.parentElement.scrollHeight - log.parentElement.scrollTop - log.parentElement.clientHeight < 40;
    const human = ev.author === "human";
    const meta = h("div", { class: "kapi-msg-meta" }, h("span", { class: "kapi-author", text: human ? "You" : ev.author || "agent" }));
    if (ev.time) {
      const t = new Date(ev.time);
      if (!isNaN(t)) meta.append(h("time", { datetime: ev.time, text: t.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) }));
    }
    const re = Array.isArray(ev.re) ? ev.re.map((seq) => noteBySeq.get(seq)).filter(Boolean) : [];
    const own = ev.kind === "note" && !re.length && noteBySeq.get(ev.seq);
    if (own) meta.append(h("button", { type: "button", class: "kapi-note-ref", "data-note": own.seq, "aria-label": `Note ${own.num}`, text: `Note ${own.num}` }));
    for (const n of re) meta.append(h("button", { type: "button", class: "kapi-note-ref", "data-note": n.seq, text: `on note ${n.num}` }));
    if (ev.target && !re.length) {
      const first = ev.target.split(",")[0].trim();
      const kind = find(first)?.getAttribute("data-kapi-preset")?.replace(/^kapi-/, "");
      const where = ev.part ? (kind ? `${kind}: ${ev.part}` : ev.part) : describe(first);
      meta.append(h("button", { type: "button", class: "kapi-ref", "data-ref": first, title: `#${ev.target}`, text: where }));
    }
    log.append(h("li", { class: `kapi-msg kapi-${ev.kind}`, "data-author": human ? "human" : "agent", "data-seq": ev.seq }, meta, body));
    empty.hidden = true;
    if (near) log.parentElement.scrollTop = log.parentElement.scrollHeight;
  }

  function markRead() {
    for (const li of log.querySelectorAll('.kapi-msg[data-author="human"]:not([data-read])')) {
      const seq = Number(li.dataset.seq);
      const meta = li.querySelector(".kapi-msg-meta");
      if (seq <= readUpTo) {
        li.setAttribute("data-read", "");
        meta.querySelector(".kapi-queued")?.remove();
        meta.append(h("span", { class: "kapi-read", title: `Seen by ${readBy}`, "aria-label": `Seen by ${readBy}`, text: "\u{1F440}" }));
      } else if (seq <= queuedUpTo && !meta.querySelector(".kapi-queued")) {
        meta.append(h("span", { class: "kapi-queued", title: "Handed to the agent, not read yet", text: "Queued" }));
      }
    }
    setStatus(agentState());
  }

  function setEnded(on) {
    ended = on;
    menu.querySelector("[data-menu=end]").hidden = on;
  }

  function agentState() {
    if (ended) return "ended";
    if (lastHuman > lastReply && lastHuman <= readUpTo) return "busy";
    if (lastHuman > lastReply && lastHuman <= queuedUpTo) return "queued";
    return presence;
  }

  let heldRevisions = [];
  let opsInFlight = 0;
  const ownSeqs = new Set();

  function onRevision(ev) {
    if (opsInFlight) return void heldRevisions.push(ev);
    if (ownSeqs.delete(ev.seq)) return;
    schedule(ev.target ? ev.target.split(",").map((s) => s.trim()).filter(Boolean) : null);
  }

  function releaseRevisions() {
    const held = heldRevisions;
    heldRevisions = [];
    held.forEach(onRevision);
  }

  function clock(time) {
    const t = time && new Date(time);
    return t && !isNaN(t) ? ` \u00b7 ${t.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}` : "";
  }

  function onEvent(ev) {
    if (ev.kind === "read" || ev.kind === "queued") {
      if (typeof ev.seq !== "number") return;
      if (ev.kind === "queued") queuedUpTo = Math.max(queuedUpTo, ev.seq);
      else if (ev.seq > readUpTo) {
        readUpTo = ev.seq;
        readBy = ev.reader || "the agent";
      }
      return markRead();
    }
    if (typeof ev.seq === "number") {
      if (ev.seq <= lastSeq) return;
      lastSeq = ev.seq;
    }
    trackNote(ev);
    if (ended && ev.author === "human" && ["message", "answer", "note"].includes(ev.kind)) log.append(h("li", { class: "kapi-again", "data-seq": ev.seq, text: `Session started again${clock(ev.time)}` }));
    if (ev.kind === "message") addEntry(ev, h("div", {}, h("div", { class: "kapi-msg-text", text: ev.text }), ...thumbs(ev.files)));
    else if (ev.kind === "answer") addEntry(ev, h("div", { class: "kapi-msg-text", text: `Sent form answer: ${ev.text}` }));
    else if (ev.kind === "note") {
      addEntry(ev, h("div", {}, ...(ev.quote ? [h("blockquote", { class: "kapi-msg-quote", text: clip(ev.quote, 200) })] : []),
        h("div", { class: "kapi-msg-text", text: ev.text }), ...thumbs(ev.files)));
    }
    if (ev.kind === "end") {
      setEnded(true);
      log.append(h("li", { class: "kapi-end", "data-seq": ev.seq, text: `You ended the session${clock(ev.time)}` }));
      empty.hidden = true;
      setStatus(agentState());
    } else if (ev.kind === "message" || ev.kind === "answer" || ev.kind === "note") {
      if (ev.author === "human") {
        lastHuman = ev.seq;
        setEnded(false);
      } else lastReply = ev.seq;
      markRead();
    } else if (ev.kind === "revision") onRevision(ev);
    else if (ev.kind === "presence") {
      presence = ev.text === "watching" ? "watching" : "none";
      setStatus(agentState());
    }
  }

  async function reconnect() {
    const probe = new AbortController();
    const res = await fetch(base + "/events", { signal: probe.signal }).catch(() => null);
    if (res && (await restartedReply(res))) return;
    probe.abort();
    setTimeout(connect, retryDelay);
    retryDelay = Math.min(retryDelay * 2, 10000);
  }

  function connect() {
    if (restarted) return;
    const es = new EventSource(base + "/events");
    stream = es;
    es.onopen = () => {
      retryDelay = 1000;
      setStatus(agentState());
      if (opened) refresh(null);
      opened = true;
    };
    es.onerror = () => {
      if (restarted) return;
      setStatus("disconnected");
      if (es.readyState === EventSource.CLOSED) reconnect();
    };
    es.onmessage = (e) => {
      let ev;
      try {
        ev = JSON.parse(e.data);
      } catch {
        return;
      }
      onEvent(ev);
    };
  }

  async function send() {
    const text = input.value.trim();
    const files = composerTray.names();
    if (composerTray.blocked()) return say(composerTray.blocked());
    if (!text && !files.length) return;
    input.value = "";
    sendsInFlight++;
    composerTray.take(files);
    const res = await request("/messages", { kind: "message", text, ...(files.length ? { files } : {}) });
    sendsInFlight--;
    composerTray.settle(files, !!res?.ok);
    if (res?.ok) return;
    input.value = input.value ? `${text}\n${input.value}` : text;
    if (res) say(res.data.error || `Message not sent (HTTP ${res.status}).`, "error");
  }

  async function answer(form) {
    const values = {};
    for (const [k, v] of new FormData(form)) {
      if (typeof v === "string") (values[k] ||= []).push(v);
    }
    const name = Element.prototype.getAttribute.call(form, "name") || closest(form, "kapi-form,[data-kapi-form]")?.getAttribute("name") || "";
    const res = await request("/answers", { form: name, values });
    if (!res) return;
    if (res.ok) say("Answer sent to the agent.");
    else say(res.data.error || `Answer not sent (HTTP ${res.status}).`, "error");
  }

  root.addEventListener("click", (e) => {
    const noted = e.target.closest("[data-note]");
    if (noted) {
      const n = noteBySeq.get(Number(noted.dataset.note));
      if (n && noted.classList.contains("kapi-pin")) showPop(n, noted);
      else focusNote(n);
      return;
    }
    const ref = e.target.closest("[data-ref]");
    if (ref) {
      const el = find(ref.dataset.ref);
      if (el) flash(el);
      else say("That block is no longer on the page.");
      return;
    }
    if (e.detail > 1 && bar.contains(e.target) && performance.now() - selectedAt < 600) return startEdit();
    const b = e.target.closest("[data-action]");
    if (b && !b.disabled) action(b.dataset.action);
  });

  function sortTable(th) {
    const table = th.closest("table");
    const body = table.tBodies[0];
    if (!body) return;
    const col = th.cellIndex;
    const dir = th.getAttribute("aria-sort") === "ascending" ? "descending" : "ascending";
    for (const other of th.parentElement.cells) other.removeAttribute("aria-sort");
    th.setAttribute("aria-sort", dir);
    const rows = [...body.rows].filter((r) => r !== th.parentElement);
    const text = (r) => (r.cells[col]?.textContent || "").trim();
    const num = (v) => Number(v.replace(/,/g, ""));
    const numeric = rows.every((r) => text(r) !== "" && Number.isFinite(num(text(r))));
    const cmp = numeric ? (a, b) => num(text(a)) - num(text(b)) : (a, b) => text(a).localeCompare(text(b));
    rows.sort((a, b) => (dir === "ascending" ? cmp(a, b) : cmp(b, a)));
    body.append(...rows);
  }

  function labelTapped(e) {
    const cell = closest(e.target, ".kapi-table td, .kapi-table th");
    if (!cell?.hasAttribute("data-kapi-col") || getComputedStyle(cell).display !== "block") return null;
    if (e.clientX - cell.getBoundingClientRect().left >= parseFloat(getComputedStyle(cell).paddingLeft)) return null;
    const table = cell.closest("table");
    const head = table.tHead?.rows[0] ?? rowsOf(table).find((tr) => tr.cells.length && [...tr.cells].every((c) => c.tagName === "TH"));
    let col = 0;
    for (const th of head?.cells ?? []) {
      col += th.colSpan;
      if (col > Number(cell.dataset.kapiCol)) return th;
    }
    return null;
  }

  function codeText(pre) {
    const lines = [...pre.querySelectorAll(".kapi-line")];
    if (!lines.length) return pre.textContent.replace(/\n$/, "");
    return lines.filter((l) => l.querySelector(".kapi-src") && !l.classList.contains("kapi-diff-del"))
      .map((l) => l.querySelector(".kapi-src").textContent).join("\n");
  }

  function copyCode(pre, show) {
    navigator.clipboard.writeText(codeText(pre)).then(() => show("copied"), () => show("copy failed"));
  }

  function pillNotice(pill) {
    const restore = (pill.dataset.kapiLabel ??= pill.textContent);
    return (text) => {
      pill.textContent = text;
      setTimeout(() => (pill.textContent = restore), 1200);
    };
  }

  let copyTarget = null;
  function placeCopy() {
    copyButton.hidden = !copyTarget?.isConnected || !!editing;
    if (copyButton.hidden) return;
    const r = copyTarget.getBoundingClientRect();
    copyButton.style.top = `${r.top + 6}px`;
    copyButton.style.left = `${r.right - copyButton.offsetWidth - 6}px`;
  }
  function showCopy(pre) {
    copyTarget = pre;
    placeCopy();
  }

  main.addEventListener("mouseover", (e) => {
    const pre = closest(e.target, "pre");
    if (pre && main.contains(pre)) showCopy(pre);
  });
  main.addEventListener("mouseleave", (e) => {
    if (e.relatedTarget !== copyButton && !copyButton.contains(e.relatedTarget)) showCopy(null);
  });
  copyButton.addEventListener("click", () => {
    if (!copyTarget?.isConnected) return;
    copyCode(copyTarget, (text) => {
      copyButton.dataset.tip = text[0].toUpperCase() + text.slice(1);
      copyButton.toggleAttribute("data-kapi-copied", text === "copied");
      setTimeout(() => {
        copyButton.dataset.tip = "Copy";
        copyButton.removeAttribute("data-kapi-copied");
      }, 1200);
    });
  });

  const FOLD_KEY = `kapi-folds:${location.pathname}`;
  const folds = new Set((() => {
    try {
      return JSON.parse(localStorage.getItem(FOLD_KEY) || "[]");
    } catch {
      return [];
    }
  })());
  const FOLD_HIT = 28;
  const levelOf = (el) => Number(/^H([1-6])$/.exec(el.tagName)?.[1] ?? 0);

  function applyFolds() {
    const open = [];
    const close = (entry) => {
      if (entry.folded) entry.el.dataset.kapiFolded = `${entry.count} block${entry.count === 1 ? "" : "s"}`;
      else entry.el.removeAttribute("data-kapi-folded");
    };
    for (const el of main.children) {
      if (el === note?.el) continue;
      const level = levelOf(el);
      while (level && open.length && open.at(-1).level >= level) close(open.pop());
      const hidden = open.some((entry) => entry.folded);
      for (const entry of open) if (entry.folded) entry.count++;
      el.toggleAttribute("data-kapi-hidden", hidden);
      if (level) open.push({ el, level, folded: folds.has(idOf(el)), count: 0 });
    }
    while (open.length) close(open.pop());
    if (selected?.hasAttribute("data-kapi-hidden")) select(null);
  }

  function foldableHeadings(level) {
    return [...main.children].filter((el, i) => {
      const next = main.children[i + 1];
      return levelOf(el) === level && next && !(levelOf(next) && levelOf(next) <= level);
    });
  }

  function toggleFold(heading, all) {
    const id = idOf(heading);
    if (all) {
      const ids = foldableHeadings(levelOf(heading)).map(idOf);
      if (ids.every((each) => folds.has(each))) for (const each of ids) folds.delete(each);
      else for (const each of ids) folds.add(each);
    } else if (!folds.delete(id)) folds.add(id);
    try {
      localStorage.setItem(FOLD_KEY, JSON.stringify([...folds]));
    } catch {}
    applyFolds();
  }

  main.addEventListener("click", (e) => {
    if (editing) {
      if (closest(e.target, "summary")?.isContentEditable) e.preventDefault();
      return;
    }
    const heading = e.target.parentElement === main && levelOf(e.target) ? e.target : null;
    const gap = heading && heading.getBoundingClientRect().left - e.clientX;
    if (gap > 0 && gap <= FOLD_HIT) return toggleFold(heading, e.altKey);
    const pill = closest(e.target, ".kapi-code-lang");
    const pillPre = pill && main.contains(pill) && pill.closest("figure")?.querySelector("pre");
    if (pillPre) return copyCode(pillPre, pillNotice(pill));
    const th = labelTapped(e) ?? closest(e.target, ".kapi-table th");
    if (th && main.contains(th)) sortTable(th);
    const el = closest(e.target, BLOCK);
    if (!el || !main.contains(el)) return;
    if (e.isTrusted) {
      const r = el.getBoundingClientRect();
      lastPoint = { el, dx: e.clientX - r.left, dy: e.clientY - r.top, cell: closest(e.target, "td, th") };
    } else lastPoint = null;
    select(el);
  });

  function trackSelection() {
    const sel = getSelection();
    const r = !editing && sel.rangeCount && !sel.isCollapsed ? sel.getRangeAt(0) : null;
    const next = r && main.contains(r.startContainer) && main.contains(r.endContainer) && r.toString().trim() ? r : null;
    const had = !!textRange;
    textRange = next;
    textBackward = !!next && sel.anchorNode !== null && (sel.anchorNode === sel.focusNode ? sel.focusOffset < sel.anchorOffset
      : !!(sel.anchorNode.compareDocumentPosition(sel.focusNode) & Node.DOCUMENT_POSITION_PRECEDING));
    if (had !== !!next) renderBar();
    else placeRing();
  }

  document.addEventListener("selectionchange", () => {
    if (!pointerDown) trackSelection();
  });
  document.addEventListener("pointerdown", (e) => {
    pointerDown = e.button === 0 && !root.contains(e.target);
  }, true);
  document.addEventListener("pointermove", (e) => {
    if (!pointerDown || e.buttons) return;
    pointerDown = false;
    trackSelection();
  }, true);
  for (const type of ["pointerup", "pointercancel"]) {
    document.addEventListener(type, () => {
      if (!pointerDown) return;
      pointerDown = false;
      trackSelection();
    }, true);
  }
  bar.addEventListener("mousedown", (e) => {
    if (textRange && !editing) e.preventDefault();
  });

  document.addEventListener("click", (e) => {
    if (editing || !selected || !getSelection().isCollapsed) return;
    // A click can re-render the toolbar before it bubbles here, so e.target may already be detached.
    if (e.composedPath().some((n) => n === root || (n instanceof Element && n.matches(BLOCK)))) return;
    select(null);
  });

  main.addEventListener("dblclick", (e) => {
    if (!editing && closest(e.target, BLOCK) === selected) startEdit();
  });

  main.addEventListener("change", async (e) => {
    const box = closest(e.target, '.kapi-task > input[type="checkbox"]');
    if (!box || !main.contains(box)) return;
    if (busy) {
      box.checked = !box.checked;
      return;
    }
    const id = idOf(closest(box, ".kapi-task"));
    box.disabled = true;
    const res = await op("check", { id, done: box.checked });
    box.disabled = false;
    if (res?.ok) return refresh([id]);
    if (res?.status !== 409) box.checked = !box.checked;
  });

  main.addEventListener("submit", (e) => {
    e.preventDefault();
    answer(e.target);
  });

  composer.addEventListener("submit", (e) => {
    e.preventDefault();
    sendNotes();
  });

  input.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" || e.shiftKey || e.isComposing || e.keyCode === 229) return;
    e.preventDefault();
    sendNotes();
  });

  document.addEventListener("keydown", (e) => {
    const focus = document.activeElement;
    const inContent = !!editing && (focus === editing.area || !!editing.hosts?.some(({ host }) => host.contains(focus)));
    const inEditor = inContent || (!!editing && bar.contains(focus));
    const singleLine = inContent && !!editing.hosts?.filter(({ host }) => host.contains(focus)).at(-1)?.host.matches(SINGLE_LINE);
    if (!editing && !asking && !typing(focus) && !e.isComposing && e.keyCode !== 229 && !e.altKey && !e.shiftKey) {
      const key = e.key.toLowerCase();
      if ((e.metaKey || e.ctrlKey) && key === "z") {
        if (undoButton.hidden || busy) return;
        e.preventDefault();
        return action("undo");
      }
      if (!e.metaKey && !e.ctrlKey && (key === "j" || key === "k") && notes.length) {
        e.preventDefault();
        return stepNote(key === "j" ? 1 : -1);
      }
      if (!e.metaKey && !e.ctrlKey && (textRange || selected)) {
        const block = !textRange && !busy && selected;
        const name = key === "a" ? "ask"
          : block && key === "e" ? (selected.getAttribute("data-kapi-state") === "archived" ? "unarchive" : "archive")
            : block && (e.key === "Delete" || e.key === "Backspace") ? "delete" : "";
        if (name) {
          e.preventDefault();
          return action(name);
        }
      }
    }
    if (e.key === "Escape" && (pop || sheetOpen)) {
      hidePop(true);
      setSheet(false);
      return;
    }
    if (e.key === "Escape") {
      dismissNote();
      if (inEditor || (editing && !panel.contains(focus))) cancelEdit();
      else if (panel.contains(focus)) focus.blur();
      else if (!notice.hidden) notice.hidden = true;
      else if (!editing) select(null);
      return;
    }
    if (!inEditor || e.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter" && singleLine && e.shiftKey) {
      e.preventDefault();
    } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey || singleLine)) {
      e.preventDefault();
      defaultAction();
    } else if (e.key === "Tab" && !e.shiftKey && inContent) {
      e.preventDefault();
      bar.querySelector(".kapi-primary")?.focus();
    }
  });

  function defaultAction() {
    if (editing?.op()) saveEdit();
    else cancelEdit();
  }

  addEventListener("scroll", placeRing, { passive: true });
  addEventListener("scroll", placeAsk, { passive: true });
  addEventListener("resize", placeAsk);
  addEventListener("scroll", placeCopy, { passive: true });
  addEventListener("scroll", () => placePop(), { passive: true });
  for (const type of ["dragover", "drop"]) {
    document.addEventListener(type, (e) => {
      if (![...(e.dataTransfer?.types || [])].includes("Files")) return;
      e.preventDefault();
      if (type !== "drop" || editing || !main.contains(e.target)) return;
      const at = closest(e.target, BLOCK);
      addImages([...e.dataTransfer.files], at && main.contains(at) ? idOf(at) : "");
    });
  }
  document.addEventListener("paste", (e) => {
    if (e.defaultPrevented || editing || asking || root.contains(e.target) || typing(document.activeElement) || root.contains(document.activeElement)) return;
    const files = [...(e.clipboardData?.files || [])];
    if (!files.length || e.clipboardData.getData("text/plain")) return;
    e.preventDefault();
    addImages(files, selected ? idOf(selected) : "");
  });
  addEventListener("resize", () => schedulePaint());
  phone.addEventListener("change", () => {
    setSheet(false);
    schedulePaint();
  });
  pins.addEventListener("pointerover", (e) => {
    const pin = e.pointerType === "mouse" && e.target.closest(".kapi-pin");
    if (pin) showPop(noteBySeq.get(Number(pin.dataset.note)), pin);
  });
  pins.addEventListener("pointerout", (e) => {
    if (e.pointerType === "mouse" && !pop?.box.contains(e.relatedTarget)) hidePop();
  });
  document.addEventListener("pointerdown", (e) => {
    if (pop && !pop.box.contains(e.target) && !e.target.closest?.(".kapi-pin")) hidePop(true);
  }, true);
  addEventListener("resize", placeRing);
  const ZWSP = "\u200b";

  function backtickToCode(e) {
    if (e.inputType !== "insertText" || !e.data?.endsWith("`")) return;
    const sel = getSelection();
    const node = sel.anchorNode;
    if (!sel.isCollapsed || node?.nodeType !== Node.TEXT_NODE || node.parentElement.closest("code, pre")) return;
    const before = node.data.slice(0, sel.anchorOffset);
    const open = before.lastIndexOf("`", before.length - 2);
    const inner = before.slice(open + 1, -1);
    if (open < 0 || !inner.trim()) return;
    const code = h("code", { text: inner });
    const rest = document.createTextNode(ZWSP + node.data.slice(sel.anchorOffset));
    node.data = before.slice(0, open);
    node.after(code, rest);
    sel.collapse(rest, 1);
  }

  document.addEventListener("input", (e) => {
    if (editing?.hosts?.some(({ host }) => host.isContentEditable && host.contains(e.target))) backtickToCode(e);
    return editing ? renderBar() : placeRing();
  });
  new ResizeObserver(placeRing).observe(main);
  new ResizeObserver(() => schedulePaint()).observe(main);
  new ResizeObserver(() => {
    if (!panel.classList.contains("kapi-expanded")) {
      document.documentElement.style.setProperty("--kapi-bar-height", `${Math.ceil(panel.getBoundingClientRect().height)}px`);
    }
  }).observe(panel);
  notice.addEventListener("click", () => (notice.hidden = true));
  panel.addEventListener("focusin", (e) => {
    if (!e.target.closest(".kapi-panel-head")) panel.classList.add("kapi-expanded");
  });
  panel.addEventListener("focusout", (e) => {
    if (!panel.contains(e.relatedTarget)) panel.classList.remove("kapi-expanded");
  });

  labelArchived(main);
  numberRows(main);
  applyFolds();
  renderQueue();
  takeOver();
  addEventListener("storage", (e) => {
    if (e.key === handoffKey && e.newValue) takeOver();
  });
  connect();
})();
