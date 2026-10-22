(() => {
  "use strict";

  const main = document.getElementById("kapi-page");
  const base = location.pathname.match(/^(?:.*?\/)?p\/[^/]+(?=\/)/)?.[0];
  if (!main || !base) return;

  const STATUS = {
    connecting: "Connecting to kapi\u2026",
    watching: "Agent is watching",
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

  function themeToggle() {
    const theme = window.kapiTheme;
    if (!theme) return [];
    const icons = {
      system: ["M4 4h16a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z", "M8 20h8M12 16v4"],
      light: ["M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8z", "M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"],
      dark: ["M20.5 14.5A8.5 8.5 0 1 1 9.5 3.5a7 7 0 0 0 11 11z"],
    };
    const icon = (paths) => {
      const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("viewBox", "0 0 24 24");
      svg.setAttribute("aria-hidden", "true");
      for (const d of paths) {
        const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
        p.setAttribute("d", d);
        svg.append(p);
      }
      return svg;
    };
    const names = { system: "System", light: "Light", dark: "Dark" };
    const buttons = theme.choices.map((c) =>
      h("button", { type: "button", "data-theme-choice": c, title: `${names[c]} theme`, "aria-label": `${names[c]} theme`, onclick: () => theme.set(c) }, icon(icons[c])));
    const sync = () => {
      for (const b of buttons) b.setAttribute("aria-pressed", String(b.dataset.themeChoice === theme.get()));
    };
    document.addEventListener("kapi-theme", sync);
    sync();
    return [h("div", { class: "kapi-theme", role: "group", "aria-label": "Theme" }, ...buttons)];
  }

  const button = (label, action, title = label) =>
    h("button", { type: "button", "data-action": action, title, text: label });

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
    h("header", { class: "kapi-panel-head" }, h("span", { class: "kapi-logo", role: "img", "aria-label": "Kapi", title: "Kapi" }), ...themeToggle(),
      button("Undo", "undo", "Undo the last change")),
    restartBox, notice, h("div", { class: "kapi-scroll", tabindex: "-1" }, empty, log,
      h("div", { class: "kapi-presence", role: "status" }, dot, statusText)), bar, composer);
  const ring = h("div", { class: "kapi-ring", hidden: "" });
  const roomStyle = h("style");
  const root = h("div", { id: "kapi-chrome" }, ring, panel, roomStyle);
  const phone = matchMedia("(max-width: 760px)");
  const BAR_GAP = 8;
  const SHORTCUT = /Mac|iPhone|iPad/.test(navigator.platform) ? "\u2318\u21b5" : "Ctrl+\u21b5";
  let room = null;
  document.body.append(root);

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
      bar.replaceChildren(h("span", { class: "kapi-bar-label", text: "Editing" }), primary,
        ...(changed ? [button("Cancel", "cancel", "Cancel (Escape)")] : []));
    } else {
      const list = peers(selected);
      const i = list.indexOf(selected);
      const archived = selected.getAttribute("data-kapi-state") === "archived";
      bar.replaceChildren(...(archived ? [button("Unarchive", "unarchive")] : [button("Edit", "edit"), button("Archive", "archive")]),
        button("↑", "up", "Move up"), button("↓", "down", "Move down"),
        button("Delete", "delete"));
      if (!archived && selected.matches('[data-kapi-preset="diagram"][data-kapi-dir]')) {
        bar.querySelector('[data-action="edit"]').after(button("Flip", "flip", "Flip between top to bottom and left to right"));
      }
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
      bar.style.top = `${Math.max(r.top, Math.min(8, r.bottom - height))}px`;
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
      rowsOf(table).forEach((tr, i) => tr.setAttribute("data-kapi-row", i));
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
      if (Number(li.dataset.seq) > readUpTo) continue;
      li.setAttribute("data-read", "");
      li.querySelector(".kapi-msg-meta").append(h("span", { class: "kapi-read", title: `Seen by ${readBy}`, "aria-label": `Seen by ${readBy}`, text: "\u{1F440}" }));
    }
  }

  function onEvent(ev) {
    if (ev.kind === "read") {
      if (typeof ev.seq === "number" && ev.seq > readUpTo) {
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
    if (ev.kind === "message" || ev.kind === "answer") markRead();
    else if (ev.kind === "revision") schedule(ev.target ? ev.target.split(",").map((s) => s.trim()).filter(Boolean) : null);
    else if (ev.kind === "presence") {
      presence = STATUS[ev.text] ? ev.text : "none";
      setStatus(presence);
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
      setStatus(presence);
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
    sendButton.disabled = true;
    const res = await request("/messages", { kind: "message", target: selected ? idOf(selected) : "", text });
    sendButton.disabled = false;
    if (!res) return;
    if (!res.ok) return say(res.data.error || `Message not sent (HTTP ${res.status}).`, "error");
    input.value = "";
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

  main.addEventListener("click", (e) => {
    if (editing) return;
    const th = closest(e.target, '[data-kapi-sort="on"] th');
    if (th && main.contains(th)) sortTable(th);
    const el = closest(e.target, BLOCK);
    if (el && main.contains(el)) select(el);
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
