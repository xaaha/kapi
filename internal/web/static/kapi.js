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
    robot: "M200,48H136V16a8,8,0,0,0-16,0V48H56A32,32,0,0,0,24,80V192a32,32,0,0,0,32,32H200a32,32,0,0,0,32-32V80A32,32,0,0,0,200,48Zm16,144a16,16,0,0,1-16,16H56a16,16,0,0,1-16-16V80A16,16,0,0,1,56,64H200a16,16,0,0,1,16,16Zm-52-56H92a28,28,0,0,0,0,56h72a28,28,0,0,0,0-56Zm-24,16v24H116V152ZM80,164a12,12,0,0,1,12-12h8v24H92A12,12,0,0,1,80,164Zm84,12h-8V152h8a12,12,0,0,1,0,24ZM72,108a12,12,0,1,1,12,12A12,12,0,0,1,72,108Zm88,0a12,12,0,1,1,12,12A12,12,0,0,1,160,108Z",
    x: "M205.66,194.34a8,8,0,0,1-11.32,11.32L128,139.31,61.66,205.66a8,8,0,0,1-11.32-11.32L116.69,128,50.34,61.66A8,8,0,0,1,61.66,50.34L128,116.69l66.34-66.35a8,8,0,0,1,11.32,11.32L139.31,128Z",
    trash: "M216,48H176V40a24,24,0,0,0-24-24H104A24,24,0,0,0,80,40v8H40a8,8,0,0,0,0,16h8V208a16,16,0,0,0,16,16H192a16,16,0,0,0,16-16V64h8a8,8,0,0,0,0-16ZM96,40a8,8,0,0,1,8-8h48a8,8,0,0,1,8,8v8H96Zm96,168H64V64H192ZM112,104v64a8,8,0,0,1-16,0V104a8,8,0,0,1,16,0Zm48,0v64a8,8,0,0,1-16,0V104a8,8,0,0,1,16,0Z",
    pencil: "M227.31,73.37,182.63,28.68a16,16,0,0,0-22.63,0L36.69,152A15.86,15.86,0,0,0,32,163.31V208a16,16,0,0,0,16,16H92.69A15.86,15.86,0,0,0,104,219.31L227.31,96a16,16,0,0,0,0-22.63ZM92.69,208H48V163.31l88-88L180.69,120ZM192,108.68,147.31,64l24-24L216,84.68Z",
    flip: "M224,48V96a8,8,0,0,1-8,8H168a8,8,0,0,1,0-16h28.69L182.06,73.37a79.56,79.56,0,0,0-56.13-23.43h-.45A79.52,79.52,0,0,0,69.59,72.71,8,8,0,0,1,58.41,61.27a96,96,0,0,1,135,.79L208,76.69V48a8,8,0,0,1,16,0ZM186.41,183.29a80,80,0,0,1-112.47-.66L59.31,168H88a8,8,0,0,0,0-16H40a8,8,0,0,0-8,8v48a8,8,0,0,0,16,0V179.31l14.63,14.63A95.43,95.43,0,0,0,130,222.06h.53a95.36,95.36,0,0,0,67.07-27.33,8,8,0,0,0-11.18-11.44Z",
    system: "M208,40H48A24,24,0,0,0,24,64V176a24,24,0,0,0,24,24H208a24,24,0,0,0,24-24V64A24,24,0,0,0,208,40Zm8,136a8,8,0,0,1-8,8H48a8,8,0,0,1-8-8V64a8,8,0,0,1,8-8H208a8,8,0,0,1,8,8Zm-48,48a8,8,0,0,1-8,8H96a8,8,0,0,1,0-16h64A8,8,0,0,1,168,224Z",
    light: "M120,40V16a8,8,0,0,1,16,0V40a8,8,0,0,1-16,0Zm72,88a64,64,0,1,1-64-64A64.07,64.07,0,0,1,192,128Zm-16,0a48,48,0,1,0-48,48A48.05,48.05,0,0,0,176,128ZM58.34,69.66A8,8,0,0,0,69.66,58.34l-16-16A8,8,0,0,0,42.34,53.66Zm0,116.68-16,16a8,8,0,0,0,11.32,11.32l16-16a8,8,0,0,0-11.32-11.32ZM192,72a8,8,0,0,0,5.66-2.34l16-16a8,8,0,0,0-11.32-11.32l-16,16A8,8,0,0,0,192,72Zm5.66,114.34a8,8,0,0,0-11.32,11.32l16,16a8,8,0,0,0,11.32-11.32ZM48,128a8,8,0,0,0-8-8H16a8,8,0,0,0,0,16H40A8,8,0,0,0,48,128Zm80,80a8,8,0,0,0-8,8v24a8,8,0,0,0,16,0V216A8,8,0,0,0,128,208Zm112-88H216a8,8,0,0,0,0,16h24a8,8,0,0,0,0-16Z",
    copy: "M216,32H88a8,8,0,0,0-8,8V80H40a8,8,0,0,0-8,8V216a8,8,0,0,0,8,8H168a8,8,0,0,0,8-8V176h40a8,8,0,0,0,8-8V40A8,8,0,0,0,216,32ZM160,208H48V96H160Zm48-48H176V88a8,8,0,0,0-8-8H96V48H208Z",
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
  const composer = h("form", { class: "kapi-composer" },
    input,
    h("div", { class: "kapi-composer-row" }, h("span", { class: "kapi-dot kapi-composer-dot", "data-state": "connecting" }),
      hint, sendButton));
  const undoButton = button("Undo", "undo", "Undo the last change");
  const showUndo = (can) => {
    undoButton.hidden = String(can) !== "true";
    if (undoButton.hidden) dismissNote();
  };
  const panel = h("aside", { class: "kapi-panel", "aria-label": "kapi chat" },
    h("header", { class: "kapi-panel-head" }, undoButton, ...themeToggle()),
    restartBox, notice, h("div", { class: "kapi-scroll", tabindex: "-1" }, empty, log),
    h("div", { class: "kapi-presence", role: "status" }, dot, statusText), bar, queueBox, composer);
  const ring = h("div", { class: "kapi-ring", hidden: "" });
  const copyButton = h("button", { type: "button", class: "kapi-copy-code", "aria-label": "Copy code", "data-tip": "Copy", hidden: "" }, icon("copy"));
  const root = h("div", { id: "kapi-chrome" }, ring, panel, copyButton);
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
      bar.replaceChildren(tool("robot", "Ask AI", "ask", "A"));
    } else {
      const archived = selected.getAttribute("data-kapi-state") === "archived";
      const flip = selected.matches('[data-kapi-preset="diagram"][data-kapi-dir]');
      const next = selected.getAttribute("data-kapi-dir") === "lr" ? "Flow top to bottom" : "Flow left to right";
      bar.replaceChildren(tool("robot", "Ask AI", "ask", "A"), ...(archived ? [tool("unarchive", "Unarchive", "unarchive", "E")] : [
        ...(phone.matches ? [tool("pencil", "Edit", "edit")] : []),
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

  function handOff() {
    if (!drafts.length) return;
    try {
      const left = JSON.parse(localStorage.getItem(handoffKey) || "[]");
      const known = new Set(left.map((d) => d.id));
      localStorage.setItem(handoffKey, JSON.stringify([...left, ...drafts.filter((d) => !known.has(d.id))]));
    } catch {}
  }

  function takeOver() {
    if (restarted) return;
    let left = [];
    try {
      left = JSON.parse(localStorage.getItem(handoffKey) || "[]");
      localStorage.removeItem(handoffKey);
    } catch {}
    const known = new Set(drafts.map((d) => d.id));
    const fresh = Array.isArray(left) ? left.filter((d) => d && typeof d.target === "string" && typeof d.text === "string" && !known.has(d.id)) : [];
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
      : [...main.querySelectorAll(BLOCK)].filter((b) => !b.parentElement.closest(BLOCK) && range.intersectsNode(b) && (textOf(b) ? rangeText(within(range, b)) : !within(range, b).collapsed));
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

  function showAsk(anchor, range, restore) {
    closeAsk();
    const about = h("span", { class: "kapi-ask-about", text: `About: ${aboutText(anchor)}` });
    const head = h("div", { class: "kapi-ask-head" }, about);
    if (anchor.part && !anchor.target.includes(",")) {
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
    const sync = () => {
      queueButton.disabled = sendNow.disabled = !text.value.trim() || sending;
    };
    text.addEventListener("input", sync);
    text.addEventListener("keydown", (e) => {
      if (e.isComposing || e.keyCode === 229) return;
      if (e.key === "Escape") {
        e.stopPropagation();
        closeAsk(true);
      } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        sendAsk();
      } else if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        queueAsk();
      }
    });
    const box = h("div", { class: "kapi-ask", role: "dialog", "aria-label": "Note for the agent" }, head, text,
      h("div", { class: "kapi-ask-row" }, cancel, queueButton, sendNow));
    root.append(box);
    asking = { box, anchor, range, restore, text, sync, at: (range ?? selected)?.getBoundingClientRect() };
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
    const text = asking?.text.value.trim();
    if (!text || sending) return false;
    const { target, part, quote, prefix, suffix } = asking.anchor;
    const id = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
    closeAsk();
    changeDrafts((list) => [...list, { id, target, part, quote, prefix, suffix, text }]);
    return true;
  }

  function sendAsk() {
    if (queueAsk()) sendNotes();
  }

  async function sendNotes() {
    if (sending) return;
    const text = input.value.trim();
    if (!drafts.length) return send();
    const batch = drafts.slice();
    const ids = new Set(batch.map((d) => d.id));
    sending = true;
    sendButton.disabled = true;
    renderQueue();
    input.value = "";
    const res = await request("/notes", { notes: batch.map(({ target, part, quote, prefix, suffix, text: t }) => ({ target, part, quote, prefix, suffix, text: t })), text });
    sending = false;
    sendButton.disabled = false;
    asking?.sync();
    if (res?.ok) return changeDrafts((list) => list.filter((d) => !ids.has(d.id)));
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
      if (!save || !next || next === d.text) return renderQueue();
      changeDrafts((list) => list.map((x) => (x.id === d.id ? { ...x, text: next } : x)));
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
          h("div", { class: "kapi-queued-tools" },
            h("button", { type: "button", "aria-label": "Edit note", "data-tip": "Edit", onclick: () => editDraft(item, d) }, icon("pencil")),
            h("button", { type: "button", "aria-label": "Remove note", "data-tip": "Remove", onclick: () => {
              changeDrafts((list) => list.filter((x) => x.id !== d.id));
            } }, icon("x"))));
        return item;
      }));
    for (const b of queueBox.querySelectorAll(".kapi-queued-tools button")) b.disabled = sending;
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
  }

  function addEntry(ev, body) {
    const near = log.parentElement.scrollHeight - log.parentElement.scrollTop - log.parentElement.clientHeight < 40;
    const human = ev.author === "human";
    const meta = h("div", { class: "kapi-msg-meta" }, h("span", { class: "kapi-author", text: human ? "You" : ev.author || "agent" }));
    if (ev.time) {
      const t = new Date(ev.time);
      if (!isNaN(t)) meta.append(h("time", { datetime: ev.time, text: t.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) }));
    }
    if (ev.target) {
      const first = ev.target.split(",")[0].trim();
      const where = ev.part ? `${describe(first)}, ${ev.part}` : describe(first);
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

  function agentState() {
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
    if (ev.kind === "message") addEntry(ev, h("div", { class: "kapi-msg-text", text: ev.text }));
    else if (ev.kind === "answer") addEntry(ev, h("div", { class: "kapi-msg-text", text: `Sent form answer: ${ev.text}` }));
    else if (ev.kind === "note") {
      addEntry(ev, h("div", {}, ...(ev.quote ? [h("blockquote", { class: "kapi-msg-quote", text: clip(ev.quote, 200) })] : []),
        h("div", { class: "kapi-msg-text", text: ev.text })));
    }
    if (ev.kind === "message" || ev.kind === "answer" || ev.kind === "note") {
      if (ev.author === "human") lastHuman = ev.seq;
      else lastReply = ev.seq;
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
    if (!text) return;
    input.value = "";
    sendButton.disabled = true;
    const res = await request("/messages", { kind: "message", text });
    sendButton.disabled = false;
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
