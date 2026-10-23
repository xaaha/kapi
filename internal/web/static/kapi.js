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
  let retryDelay = 1000;

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
    up: "M205.66,117.66a8,8,0,0,1-11.32,0L136,59.31V216a8,8,0,0,1-16,0V59.31L61.66,117.66a8,8,0,0,1-11.32-11.32l72-72a8,8,0,0,1,11.32,0l72,72A8,8,0,0,1,205.66,117.66Z",
    down: "M205.66,149.66l-72,72a8,8,0,0,1-11.32,0l-72-72a8,8,0,0,1,11.32-11.32L120,196.69V40a8,8,0,0,1,16,0V196.69l58.34-58.35a8,8,0,0,1,11.32,11.32Z",
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

  const tool = (glyph, label, action) =>
    h("button", { type: "button", "data-action": action, "aria-label": label, "data-tip": label }, icon(glyph), h("span", { class: "kapi-tool-label", text: label }));

  const iconTool = (glyph, label, action) =>
    h("button", { type: "button", "data-action": action, "aria-label": label, "data-tip": label }, icon(glyph));

  const dot = h("span", { class: "kapi-dot", "data-state": "connecting" });
  const bar = h("div", { class: "kapi-bar", role: "toolbar", "aria-label": "Block actions", hidden: "" });
  const statusText = h("span", { class: "kapi-status", text: STATUS.connecting });
  const notice = h("div", { class: "kapi-notice", role: "status", hidden: "" });
  const restartBox = h("div", { class: "kapi-restart", role: "alert", hidden: "" });
  const log = h("ol", { class: "kapi-log", "aria-live": "polite" });
  const empty = h("p", { class: "kapi-empty", text: "Messages with the agent show up here." });
  const targetChip = h("button", { type: "button", class: "kapi-target", hidden: "", title: "Stop targeting this block" });
  const input = h("textarea", { rows: "3", placeholder: "Message the agent", "aria-label": "Message" });
  const sendButton = h("button", { type: "submit", class: "kapi-primary", text: "Send" });
  const composer = h("form", { class: "kapi-composer" },
    targetChip, input,
    h("div", { class: "kapi-composer-row" }, h("span", { class: "kapi-dot kapi-composer-dot", "data-state": "connecting" }),
      h("span", { class: "kapi-hint", text: "Enter to send, Shift+Enter for a new line" }), sendButton));
  const panel = h("aside", { class: "kapi-panel", "aria-label": "kapi chat" },
    h("header", { class: "kapi-panel-head" }, ...themeToggle(),
      button("Undo", "undo", "Undo the last change")),
    restartBox, notice, h("div", { class: "kapi-scroll", tabindex: "-1" }, empty, log),
    h("div", { class: "kapi-presence", role: "status" }, dot, statusText), bar, composer);
  const ring = h("div", { class: "kapi-ring", hidden: "" });
  const roomStyle = h("style");
  const copyButton = h("button", { type: "button", class: "kapi-copy-code", "aria-label": "Copy code", "data-tip": "Copy", hidden: "" }, icon("copy"));
  const root = h("div", { id: "kapi-chrome" }, ring, panel, roomStyle, copyButton);
  const phone = matchMedia("(max-width: 760px)");
  phone.addEventListener("change", () => renderBar());
  const BAR_GAP = 8;
  const SHORTCUT = /Mac|iPhone|iPad/.test(navigator.platform) ? "\u2318\u21b5" : "Ctrl+\u21b5";
  let room = null;
  document.body.append(root);
  main.before(h("header", { class: "kapi-site-head" },
    h("span", { class: "kapi-logo", "aria-hidden": "true" }), h("span", { class: "kapi-site-name", text: "Kapi" })));

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

  function select(el) {
    if (editing) return;
    selected?.removeAttribute("data-kapi-selected");
    selected = el;
    el?.setAttribute("data-kapi-selected", "");
    targetChip.hidden = !el;
    targetChip.textContent = el ? `About: ${describe(idOf(el))}  ×` : "";
    targetChip.title = el ? `#${idOf(el)}` : "";
    renderBar();
  }

  function renderBar() {
    if (!selected || !selected.isConnected) {
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
    } else {
      const list = peers(selected);
      const i = list.indexOf(selected);
      const archived = selected.getAttribute("data-kapi-state") === "archived";
      const flip = selected.matches('[data-kapi-preset="diagram"][data-kapi-dir]');
      const next = selected.getAttribute("data-kapi-dir") === "lr" ? "Flow top to bottom" : "Flow left to right";
      bar.replaceChildren(...(archived ? [tool("unarchive", "Unarchive", "unarchive")] : [
        ...(phone.matches ? [tool("pencil", "Edit", "edit")] : []),
        ...(flip ? [iconTool("flip", next, "flip")] : []),
        tool("archive", "Archive", "archive")]),
        iconTool("up", "Move up", "up"), iconTool("down", "Move down", "down"),
        tool("trash", "Delete", "delete"));
      bar.querySelector('[data-action="up"]').disabled = i <= 0;
      bar.querySelector('[data-action="down"]').disabled = i >= list.length - 1;
    }
    for (const b of bar.querySelectorAll("button")) b.disabled ||= busy;
    bar.hidden = false;
    placeRing();
  }

  function setRoom(el, px) {
    if (room?.el === el && room.px === px) return;
    if (room && room.el !== el) room.el.removeAttribute("data-kapi-bar-room");
    if (!el) {
      room = null;
      roomStyle.textContent = "";
      return;
    }
    const base = room?.el === el ? room.base : parseFloat(getComputedStyle(el).paddingTop) || 0;
    room = { el, px, base };
    el.setAttribute("data-kapi-bar-room", "");
    roomStyle.textContent = `#kapi-page [data-kapi-bar-room] { padding-top: ${base + px}px !important; }`;
  }

  function dockBar(floating) {
    if (floating && bar.parentElement !== root) root.append(bar);
    if (!floating && bar.parentElement !== panel) panel.insertBefore(bar, composer);
    if (!floating) bar.removeAttribute("data-place");
  }

  function placeBar() {
    const target = editing?.box || selected;
    if (phone.matches || bar.hidden || !target?.isConnected) {
      dockBar(false);
      return setRoom(null);
    }
    dockBar(true);
    bar.dataset.place = "outside";
    const areaRight = document.documentElement.clientWidth - panel.getBoundingClientRect().width;
    let r = target.getBoundingClientRect();
    let { width, height } = bar.getBoundingClientRect();
    if (r.right + BAR_GAP + width <= areaRight - 4) {
      setRoom(null);
      r = target.getBoundingClientRect();
      bar.style.left = `${r.right + BAR_GAP}px`;
      bar.style.top = `${Math.min(Math.max(r.top, Math.min(8, r.bottom - height)), innerHeight - height - 8)}px`;
      return;
    }
    bar.dataset.place = "inside";
    ({ width, height } = bar.getBoundingClientRect());
    setRoom(target, Math.ceil(height) + 8);
    r = target.getBoundingClientRect();
    if (r.top + 4 < 0) return dockBar(false);
    bar.style.left = `${r.right - width - 4}px`;
    bar.style.top = `${r.top + 4}px`;
  }

  function placeRing() {
    placeBar();
    ring.hidden = bar.hidden || !selected || !!editing?.area;
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
    busy = true;
    renderBar();
    try {
      const res = await request("/ops", { rev, op: { kind, ...fields } });
      if (!res || res.ok) {
        if (res) main.dataset.rev = res.data.rev;
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
      renderBar();
    }
  }

  const BLOCK = '[data-kapi-id]:not([data-kapi-id] [data-kapi-preset="badge"])';
  const PROSE_PRESETS = new Set(["callout", "option", "step", "risk", "question"]);
  const PROSE_BODY = ":scope > .kapi-callout-body, :scope > .kapi-option-body, :scope > .kapi-plan-body";

  function editInPlace(el, hosts, rev, op, singleLine = false) {
    editing = { el, rev, hosts: hosts.map((host) => ({ host, original: host.innerHTML })), op, singleLine };
    for (const { host } of editing.hosts) host.contentEditable = host.matches("th, td") ? "plaintext-only" : "true";
    const first = hosts[0];
    first.focus();
    getSelection().selectAllChildren(first);
    getSelection().collapseToEnd();
    renderBar();
  }

  function editProse(el) {
    const initial = outerHTMLOf(el);
    editInPlace(el, [el], Number(main.dataset.rev), () => {
      const html = outerHTMLOf(el);
      return html === initial ? null : { kind: "edit", html };
    });
  }

  function rowsOf(table) {
    return [...table.querySelectorAll("tr")].filter((tr) => tr.closest("table") === table);
  }

  function numberRows(scope) {
    for (const table of scope.querySelectorAll('[data-kapi-preset$="table"] table')) {
      const rows = rowsOf(table);
      rows.forEach((tr, i) => tr.setAttribute("data-kapi-row", i));
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

  function editCells(el, table) {
    const cells = rowsOf(table).flatMap((tr) =>
      [...tr.cells].map((cell, col) => ({ cell, row: Number(tr.dataset.kapiRow), col, text: cell.textContent })));
    editInPlace(el, cells.map((c) => c.cell), Number(main.dataset.rev), () => {
      const changed = cells.filter((c) => c.cell.textContent !== c.text).map(({ row, col, cell }) => ({ row, col, text: cell.textContent }));
      return changed.length ? { kind: "cells", cells: changed } : null;
    }, true);
  }

  function roundTrips(src, body) {
    const tpl = document.createElement("template");
    tpl.innerHTML = src;
    return tpl.innerHTML.trim() === body.innerHTML.trim();
  }

  async function startEdit() {
    if (!selected || editing || busy) return;
    const el = selected;
    notice.hidden = true;
    if (!el.matches("[data-kapi-preset], :has([data-kapi-preset])")) return editProse(el);
    const preset = el.getAttribute("data-kapi-preset")?.replace(/^kapi-/, "");
    const table = preset === "table" && el.querySelector("table");
    if (table && !table.querySelector("[data-kapi-preset]")) return editCells(el, table);
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
    if (src === null) say("Could not load the source of this block.", "error");
    if (src === null || selected !== el || editing) return renderBar();
    const body = PROSE_PRESETS.has(preset) && el.querySelector(PROSE_BODY);
    if (body && roundTrips(src, body)) {
      const initial = body.innerHTML;
      return editInPlace(el, [body], rev, () => (body.innerHTML === initial ? null : { kind: "edit", inner: body.innerHTML }));
    }
    const area = h("textarea", { class: "kapi-source", spellcheck: "false", "aria-label": `Source of ${describe(idOf(el))}` });
    area.value = src;
    area.rows = Math.min(24, src.split("\n").length + 2);
    const box = h("div", { class: "kapi-source-box" }, h("div", { class: "kapi-source-label", text: `Editing source of: ${preset || "this section"}` }), area);
    const style = getComputedStyle(el);
    box.style.marginTop = style.marginTop;
    box.style.marginBottom = style.marginBottom;
    el.before(box);
    el.setAttribute("data-kapi-editing-source", "");
    editing = { el, area, box, rev, op: () => (area.value === src ? null : { kind: "edit", inner: area.value }) };
    area.focus();
    renderBar();
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
    }
    editing = null;
    renderBar();
    if (pending) scheduleFlush();
  }

  function outerHTMLOf(el) {
    const copy = el.cloneNode(true);
    copy.removeAttribute("contenteditable");
    copy.removeAttribute("data-kapi-selected");
    copy.removeAttribute("data-kapi-excerpt");
    copy.removeAttribute("data-kapi-bar-room");
    return copy.outerHTML;
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
    if (name === "undo") return (await op("undo", { id: "" }))?.ok && refresh(null);
    if (name === "save") return saveEdit();
    if (name === "cancel") return cancelEdit();
    if (!selected) return;
    const id = idOf(selected);
    if (name === "edit") return startEdit();
    if (name === "archive" || name === "unarchive") return (await op(name, { id }))?.ok && refresh([id]);
    if (name === "flip") {
      const dir = selected.getAttribute("data-kapi-dir") === "lr" ? "tb" : "lr";
      return (await op("orient", { id, dir }))?.ok && refresh([id]);
    }
    if (name === "delete") return (await op("delete", { id }))?.ok && refresh(null);
    const list = peers(selected);
    const i = list.indexOf(selected);
    const before = name === "up" ? list[i - 1] : list[i + 2];
    if (name === "up" && !before) return;
    if (name === "down" && i >= list.length - 1) return;
    if ((await op("move", { id, before: before ? idOf(before) : "" }))?.ok) await refresh(null);
  }

  function refresh(ids) {
    queue = queue.then(() => (ids ? loadBlocks(ids) : loadPage())).catch(() =>
      say("Could not load the latest page from kapi.", "error"));
    return queue;
  }

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
      const held = editing && idOf(editing.el);
      const ids = [...pending].filter((id) => id !== held);
      pending = pending.has(held) ? new Set([held]) : null;
      if (ids.length) refresh(ids);
    }, 30);
  }

  async function loadPage() {
    if (editing) return void (pending = "page");
    const res = await fetch(base + "/", { headers: { Accept: "text/html" } });
    if (!res.ok) throw new Error(res.status);
    const fresh = new DOMParser().parseFromString(await res.text(), "text/html").getElementById("kapi-page");
    if (!fresh) throw new Error("no page");
    const keep = selected && idOf(selected);
    selected = null;
    main.replaceChildren(...fresh.childNodes);
    main.dataset.rev = fresh.dataset.rev;
    labelArchived(main);
    numberRows(main);
    select(keep ? find(keep) : null);
  }

  async function loadBlocks(ids) {
    for (const id of ids) {
      const el = find(id);
      if (!el) return loadPage();
      if (editing?.el === el) {
        schedule([id]);
        continue;
      }
      const res = await fetch(`${base}/blocks/${encodeURIComponent(id)}`);
      if (!res.ok) return loadPage();
      const tpl = document.createElement("template");
      tpl.innerHTML = (await res.text()).trim();
      const next = tpl.content.firstElementChild;
      if (!next) return loadPage();
      el.replaceWith(next);
      labelArchived(next);
      numberRows(next);
      const rev = res.headers.get("Kapi-Rev");
      if (rev) main.dataset.rev = rev;
      if (selected === el) {
        selected = null;
        select(next);
      }
    }
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
      meta.append(h("button", { type: "button", class: "kapi-ref", "data-ref": ev.target, title: `#${ev.target}`, text: describe(ev.target) }));
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
    if (ev.kind === "message" || ev.kind === "answer") {
      if (ev.author === "human") lastHuman = ev.seq;
      else lastReply = ev.seq;
      markRead();
    } else if (ev.kind === "revision") schedule(ev.target ? ev.target.split(",").map((s) => s.trim()).filter(Boolean) : null);
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
    const res = await request("/messages", { kind: "message", target: selected ? idOf(selected) : "", text });
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
    if (e.target === targetChip) return select(null);
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

  main.addEventListener("click", (e) => {
    if (editing) return;
    const pill = closest(e.target, ".kapi-code-lang");
    const pillPre = pill && main.contains(pill) && pill.closest("figure")?.querySelector("pre");
    if (pillPre) return copyCode(pillPre, pillNotice(pill));
    const th = labelTapped(e) ?? closest(e.target, ".kapi-table th");
    if (th && main.contains(th)) sortTable(th);
    const el = closest(e.target, BLOCK);
    if (el && main.contains(el)) select(el);
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
    send();
  });

  input.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" || e.shiftKey || e.isComposing || e.keyCode === 229) return;
    e.preventDefault();
    send();
  });

  document.addEventListener("keydown", (e) => {
    const focus = document.activeElement;
    const inContent = !!editing && (focus === editing.area || !!editing.hosts?.some(({ host }) => host.contains(focus)));
    const inEditor = inContent || (!!editing && bar.contains(focus));
    if (e.key === "Escape") {
      if (inEditor) cancelEdit();
      else if (panel.contains(focus)) focus.blur();
      else if (!notice.hidden) notice.hidden = true;
      else if (!editing) select(null);
      return;
    }
    if (!inEditor || e.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter" && editing.singleLine && inContent && e.shiftKey) {
      e.preventDefault();
    } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey || (editing.singleLine && inContent))) {
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
  addEventListener("scroll", placeCopy, { passive: true });
  addEventListener("resize", placeRing);
  document.addEventListener("input", () => (editing ? renderBar() : placeRing()));
  new ResizeObserver(placeRing).observe(main);
  new ResizeObserver(() => {
    if (!panel.classList.contains("kapi-expanded")) {
      document.documentElement.style.setProperty("--kapi-bar-height", `${Math.ceil(panel.getBoundingClientRect().height)}px`);
    }
  }).observe(panel);
  notice.addEventListener("click", () => (notice.hidden = true));
  panel.addEventListener("focusin", () => panel.classList.add("kapi-expanded"));
  panel.addEventListener("focusout", (e) => {
    if (!panel.contains(e.relatedTarget)) panel.classList.remove("kapi-expanded");
  });

  labelArchived(main);
  numberRows(main);
  connect();
})();
