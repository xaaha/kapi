import { h, icon, root, shortcuts } from "../../static/core.js";

const UNDO_MS = 10000;
const ARM_MS = 500;

async function post(path) {
  let res;
  try {
    res = await fetch(`${root}api/notebook/${path}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
  } catch {
    return { ok: false, error: "Could not reach kapi. Check that the daemon is running." };
  }
  const data = await res.json().catch(() => ({}));
  return { ok: res.ok, data, error: data.error || `HTTP ${res.status}` };
}

export function undoNote() {
  const text = h("span", { class: "nb-undo-text" });
  const undo = h("button", { type: "button", class: "nb-undo-button", text: "Undo" });
  const dot = h("span", { class: "nb-undo-dot", "aria-hidden": "true", text: "\u00b7" });
  const el = h("div", { class: "nb-undo", role: "status", hidden: "" }, text, dot, undo);
  let shown = null;
  let timer = 0;

  const hide = () => {
    clearTimeout(timer);
    shown = null;
    el.hidden = true;
  };
  const show = (message, page) => {
    clearTimeout(timer);
    shown = page;
    text.textContent = message;
    undo.hidden = !page;
    dot.hidden = !page;
    el.hidden = false;
    el.toggleAttribute("data-error", !page);
    timer = setTimeout(hide, UNDO_MS);
  };
  undo.addEventListener("click", async () => {
    if (!shown) return;
    const page = shown;
    undo.disabled = true;
    const res = await post(`pages/${page.id}/restore`);
    undo.disabled = false;
    if (res.ok) hide();
    else show(`Could not bring back ${page.title}: ${res.error}`, null);
  });

  return {
    el,
    deleted: (page) => show(page.kept ? `Deleted ${page.title}. Its images stay, since another note uses them` : `Deleted ${page.title}`, page),
    failed: (message) => show(message, null),
  };
}

export function cardMenu(note) {
  const question = h("p", { class: "nb-card-menu-question", id: "nb-card-menu-question", role: "presentation", hidden: "" });
  const item = (kind, glyph, label) => h("button", { type: "button", role: "menuitem", tabindex: "-1", "data-card-action": kind }, icon(glyph), h("span", { text: label }));
  const remove = item("delete", "trash", "Delete");
  const yes = item("delete-yes", "trash", "Delete");
  const no = item("delete-no", "x", "Cancel");
  for (const el of [yes, no]) {
    el.hidden = true;
    el.setAttribute("aria-describedby", question.id);
  }
  const menu = h("div", { class: "nb-card-menu", role: "menu", "aria-label": "Note actions", hidden: "" }, remove, question, no, yes);
  let open = null;
  let armTimer = 0;

  const items = () => [...menu.querySelectorAll('[role="menuitem"]')].filter((el) => !el.hidden);
  const anchor = () => open && document.querySelector(`.nb-card-menu-button[data-page="${CSS.escape(open.page.id)}"]`);

  function confirm(on) {
    remove.hidden = on;
    for (const el of [question, yes, no]) el.hidden = !on;
    yes.disabled = false;
    yes.removeAttribute("data-armed");
    clearTimeout(armTimer);
    if (on) armTimer = setTimeout(() => yes.setAttribute("data-armed", ""), ARM_MS);
    if (open) question.textContent = `Delete ${open.title}?`;
    (on ? no : remove).focus();
  }

  function place() {
    const button = anchor();
    if (!button) return close(false);
    const r = button.getBoundingClientRect();
    const width = menu.offsetWidth;
    menu.style.top = `${r.bottom + 4}px`;
    menu.style.left = `${Math.max(8, Math.min(r.right - width, innerWidth - width - 8))}px`;
  }

  function close(refocus) {
    if (!open) return;
    const button = anchor();
    open = null;
    menu.hidden = true;
    button?.setAttribute("aria-expanded", "false");
    if (refocus) button?.focus();
  }

  function show(button, page, title) {
    if (open?.page.id === page.id) return close(true);
    close(false);
    open = { page, title };
    menu.hidden = false;
    button.setAttribute("aria-expanded", "true");
    confirm(false);
    place();
  }

  async function deletePage() {
    if (!open || !yes.hasAttribute("data-armed") || yes.disabled) return;
    const { page, title } = open;
    yes.disabled = true;
    const res = await post(`pages/${page.id}/delete`);
    close(false);
    if (res.ok) note.deleted({ id: page.id, title, kept: res.data.keptImages === true });
    else note.failed(`Could not delete ${title}: ${res.error}`);
  }

  remove.addEventListener("click", () => confirm(true));
  no.addEventListener("click", () => yes.hasAttribute("data-armed") && confirm(false));
  yes.addEventListener("click", deletePage);
  menu.addEventListener("keydown", (e) => {
    const list = items();
    const at = list.indexOf(document.activeElement);
    const to = shortcuts.is("menuNext", e) ? at + 1 : shortcuts.is("menuPrevious", e) ? at - 1 : shortcuts.is("menuFirst", e) ? 0 : shortcuts.is("menuLast", e) ? list.length - 1 : undefined;
    if (to !== undefined) {
      e.preventDefault();
      list[(to + list.length) % list.length].focus();
    } else if (shortcuts.is("dismiss", e)) {
      e.preventDefault();
      close(true);
    } else if (shortcuts.is("menuLeave", e)) close(false);
  });
  document.addEventListener("pointerdown", (e) => {
    if (open && !menu.contains(e.target) && !anchor()?.contains(e.target)) close(false);
  }, true);
  addEventListener("resize", () => open && place());
  addEventListener("scroll", () => open && place(), true);

  return {
    el: menu,
    button(page, title) {
      const button = h("button", { type: "button", class: "nb-card-menu-button", "data-page": page.id, "data-key": `menu\n${page.id}`, "aria-label": `Actions for ${title}`, title: "Actions", "aria-haspopup": "menu", "aria-expanded": String(open?.page.id === page.id) }, icon("dots"));
      button.addEventListener("click", () => show(button, page, title));
      return button;
    },
    redrawn: () => open && place(),
  };
}
