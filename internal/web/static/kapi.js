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
      h("span", { class: "kapi-hint", text: "Ctrl+Enter to send" }), sendButton));
  const panel = h("aside", { class: "kapi-panel", "aria-label": "kapi chat" },
    h("header", { class: "kapi-panel-head" }, dot, statusText,
      button("Undo", "undo", "Undo the last change")),
    restartBox, notice, h("div", { class: "kapi-scroll", tabindex: "-1" }, empty, log), bar, composer);
  const ring = h("div", { class: "kapi-ring", hidden: "" });
  const root = h("div", { id: "kapi-chrome" }, ring, panel);
  document.body.append(root);

  if (/Mac|iPhone|iPad/.test(navigator.platform)) composer.querySelector(".kapi-hint").textContent = "⌘ Enter to send";

  const closest = (node, selector) => Element.prototype.closest.call(node, selector);
  const idOf = (el) => el.getAttribute("data-kapi-id");
  const find = (id) => main.querySelector(`[data-kapi-id="${CSS.escape(id)}"]`);
  const peers = (el) => [...el.parentElement.children].filter((c) => c.hasAttribute("data-kapi-id"));

  function describe(id) {
    const el = find(id);
    if (!el) return "a block no longer on the page";
    const text = (el.innerText || el.textContent).replace(/\s+/g, " ").trim();
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
      bar.replaceChildren(h("span", { class: "kapi-bar-label", text: "Editing" }),
        button("Save", "save", "Save (Ctrl+Enter)"), button("Cancel", "cancel", "Cancel (Escape)"));
    } else {
      const list = peers(selected);
      const i = list.indexOf(selected);
      const state = selected.getAttribute("data-kapi-state");
      bar.replaceChildren(button("Edit", "edit"), button("Done", "done", "Mark done"), button("Archive", "archive"),
        button("↑", "up", "Move up"), button("↓", "down", "Move down"),
        button("Delete", "delete"));
      bar.querySelector('[data-action="done"]').disabled = state === "done";
      bar.querySelector('[data-action="archive"]').disabled = state === "archived";
      bar.querySelector('[data-action="up"]').disabled = i <= 0;
      bar.querySelector('[data-action="down"]').disabled = i >= list.length - 1;
    }
    for (const b of bar.querySelectorAll("button")) b.disabled ||= busy;
    bar.hidden = false;
    placeRing();
  }

  function placeRing() {
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

  function showRestart(reopen) {
    restarted = true;
    stream?.close();
    setStatus("disconnected");
    statusText.textContent = "Kapi restarted";
    const copy = h("button", { type: "button", class: "kapi-copy", text: "Copy" });
    copy.addEventListener("click", () =>
      navigator.clipboard.writeText(reopen).then(() => (copy.textContent = "Copied"), () => (copy.textContent = "Copy failed")));
    restartBox.replaceChildren(h("p", {}, "Kapi restarted. Run ", h("code", { text: reopen }), " to continue."), copy);
    restartBox.hidden = false;
  }

  async function restartedReply(res) {
    if (res.status !== 403) return false;
    const data = await res.clone().json().catch(() => ({}));
    if (typeof data.reopen !== "string") return false;
    showRestart(data.reopen);
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

  async function startEdit() {
    if (!selected || editing || busy) return;
    const el = selected;
    notice.hidden = true;
    if (!el.matches("[data-kapi-preset], :has([data-kapi-preset])")) {
      editing = { el, rev: Number(main.dataset.rev), original: el.innerHTML, initial: outerHTMLOf(el), read: () => outerHTMLOf(el) };
      el.contentEditable = "true";
      el.focus();
      getSelection().selectAllChildren(el);
      getSelection().collapseToEnd();
      return renderBar();
    }
    busy = true;
    renderBar();
    let rev = Number(main.dataset.rev);
    const src = await fetch(`${base}/source/${encodeURIComponent(idOf(el))}`)
      .then((res) => {
        if (!res.ok) return null;
        rev = Number(res.headers.get("Kapi-Rev") || rev);
        return res.text();
      }, () => null);
    busy = false;
    if (src === null) say("Could not load the source of this block.", "error");
    if (src === null || selected !== el || editing) return renderBar();
    const preset = el.getAttribute("data-kapi-preset")?.replace(/^kapi-/, "") || "this section";
    const area = h("textarea", { class: "kapi-source", spellcheck: "false", "aria-label": `Source of ${describe(idOf(el))}` });
    area.value = src;
    area.rows = Math.min(24, src.split("\n").length + 2);
    const box = h("div", { class: "kapi-source-box" }, h("div", { class: "kapi-source-label", text: `Editing source of: ${preset}` }), area);
    const style = getComputedStyle(el);
    box.style.marginTop = style.marginTop;
    box.style.marginBottom = style.marginBottom;
    el.before(box);
    el.setAttribute("data-kapi-editing-source", "");
    editing = { el, area, box, rev, initial: src, read: () => area.value };
    area.focus();
    renderBar();
  }

  function endEdit(restore) {
    const { el, area, box, original } = editing;
    if (area) {
      box.remove();
      el.removeAttribute("data-kapi-editing-source");
    } else {
      if (restore) el.innerHTML = original;
      el.removeAttribute("contenteditable");
    }
    editing = null;
    renderBar();
    if (pending) scheduleFlush();
  }

  function outerHTMLOf(el) {
    const copy = el.cloneNode(true);
    copy.removeAttribute("contenteditable");
    copy.removeAttribute("data-kapi-selected");
    return copy.outerHTML;
  }

  function cancelEdit() {
    if (editing && !busy) endEdit(true);
  }

  async function saveEdit() {
    if (!editing || busy) return;
    const session = editing;
    const html = session.read();
    if (html === session.initial) return endEdit(false);
    const id = idOf(session.el);
    const res = await op("edit", { id, html }, { rev: session.rev, stale: STALE_EDIT });
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
    if (name === "done" || name === "archive") return (await op(name, { id }))?.ok && refresh([id]);
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

  function onEvent(ev) {
    if (typeof ev.seq === "number") {
      if (ev.seq <= lastSeq) return;
      lastSeq = ev.seq;
    }
    if (ev.kind === "message") addEntry(ev, h("div", { class: "kapi-msg-text", text: ev.text }));
    else if (ev.kind === "answer") addEntry(ev, h("div", { class: "kapi-msg-text", text: `Sent form answer: ${ev.text}` }));
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
    const el = closest(e.target, "[data-kapi-id]");
    if (el && main.contains(el)) select(el);
  });

  main.addEventListener("dblclick", (e) => {
    if (!editing && closest(e.target, "[data-kapi-id]") === selected) startEdit();
  });

  main.addEventListener("submit", (e) => {
    e.preventDefault();
    answer(e.target);
  });

  composer.addEventListener("submit", (e) => {
    e.preventDefault();
    send();
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (editing) cancelEdit();
      else if (panel.contains(document.activeElement)) document.activeElement.blur();
      else if (!notice.hidden) notice.hidden = true;
      else select(null);
      return;
    }
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      if (editing) {
        e.preventDefault();
        saveEdit();
      } else if (document.activeElement === input) {
        e.preventDefault();
        send();
      }
    }
  });

  addEventListener("scroll", placeRing, { passive: true });
  addEventListener("resize", placeRing);
  document.addEventListener("input", placeRing);
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

  connect();
})();
