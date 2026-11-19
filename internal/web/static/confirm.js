import { h, icon, shortcuts } from "./core.js";

const ARM_MS = 500;
const open = new Set();
let made = 0;

document.addEventListener("pointerdown", (e) => {
  for (const step of open) step.away(e.target);
}, true);

const words = (v) => (typeof v === "function" ? v() : v);

export function confirmStep({ label, question, yes = "Delete", no = "Cancel", menu = false, run, onChange }) {
  const id = `kapi-confirm-${++made}`;
  const button = (kind, glyph, text) => h("button", { type: "button", class: `kapi-confirm-${kind}`, ...(menu && { role: "menuitem", tabindex: "-1" }) },
    menu ? icon(glyph) : null, h("span", { class: "kapi-confirm-label", text }));
  const trigger = label ? button("trigger", "trash", words(label)) : null;
  const ask = question ? h("p", { class: "kapi-confirm-question", id, role: "presentation", hidden: "" }) : null;
  const cancel = button("no", "x", no);
  const go = button("yes", "trash", yes);
  for (const b of [cancel, go]) {
    b.hidden = true;
    if (ask) b.setAttribute("aria-describedby", id);
  }
  const el = h("div", { class: "kapi-confirm", "data-layout": menu ? "menu" : "inline", role: menu ? "none" : null }, trigger, ask, cancel, go);
  let timer = 0;
  let busy = false;

  const confirming = () => el.hasAttribute("data-confirming");
  const armed = () => go.hasAttribute("data-armed");

  function refresh() {
    if (trigger) trigger.lastChild.textContent = words(label);
    if (ask && confirming()) ask.textContent = words(question);
  }

  function set(on, focus) {
    const changed = on !== confirming();
    clearTimeout(timer);
    go.removeAttribute("data-armed");
    el.toggleAttribute("data-confirming", on);
    if (trigger) trigger.hidden = on;
    for (const part of [ask, cancel, go]) if (part) part.hidden = !on;
    if (on) {
      refresh();
      timer = setTimeout(() => go.setAttribute("data-armed", ""), ARM_MS);
      open.add(step);
    } else open.delete(step);
    if (focus) (on ? cancel : trigger)?.focus();
    if (changed) onChange?.(on);
  }

  async function proceed() {
    if (!armed() || busy) return;
    busy = true;
    cancel.disabled = go.disabled = true;
    el.setAttribute("aria-busy", "true");
    await run();
    busy = false;
    cancel.disabled = go.disabled = false;
    el.removeAttribute("aria-busy");
  }

  trigger?.addEventListener("click", () => set(true, true));
  cancel.addEventListener("click", () => armed() && set(false, true));
  go.addEventListener("click", proceed);
  el.addEventListener("keydown", (e) => {
    if (!confirming() || !shortcuts.is("dismiss", e)) return;
    e.preventDefault();
    e.stopPropagation();
    set(false, true);
  });
  el.addEventListener("focusout", (e) => {
    if (confirming() && !busy && e.relatedTarget && !el.contains(e.relatedTarget)) set(false, false);
  });

  const step = {
    el,
    trigger,
    arm: (focus = true) => set(true, focus),
    disarm: (focus = false) => set(false, focus),
    confirming,
    busy: () => busy,
    refresh,
    away(target) {
      if (!el.isConnected) open.delete(step);
      else if (!busy && !el.contains(target)) set(false, false);
    },
  };
  return step;
}
