import { h, icon, shortcuts } from "./core.js";

const sheet = new URL("./popover.css", import.meta.url).href;

export function popoverButton({ text, kind = "secondary", run, disabled = false }) {
  const el = h("button", { type: "button", class: "kapi-pop-button", "data-kind": kind, text });
  el.disabled = disabled;
  if (run) el.addEventListener("click", run);
  return el;
}

export function popoverContent({ kicker, subject, question, detail, children = [], problem, done, busy = false }) {
  const detailNodes = typeof detail === "string" ? [detail] : detail ?? [];
  return h("div", { class: "kapi-pop", "aria-busy": String(busy) },
    kicker ? h("p", { class: "kapi-pop-kicker" }, icon(kicker.icon ?? "warning"), h("span", { text: kicker.text })) : null,
    subject ? h("p", { class: "kapi-pop-subject", text: subject }) : null,
    question ? h("p", { class: "kapi-pop-question", text: question }) : null,
    detailNodes.length ? h("p", { class: "kapi-pop-detail" }, ...detailNodes) : null,
    ...children,
    problem ? h("p", { class: "kapi-pop-error", role: "alert", text: problem }) : null,
    done ? h("div", { class: "kapi-pop-done", role: "status" }, icon("checkCircle"), h("div", {}, h("strong", { text: done.title }), ...(done.detail ? [h("p", {}, ...[done.detail].flat())] : []))) : null);
}

export const popoverActions = (buttons) => h("div", { class: "kapi-pop-actions" }, ...buttons.filter(Boolean));

export function inlinePopover({ label, className, attrs = {}, content, actions, onDismiss }) {
  const el = h("div", { ...attrs, class: className ? `kapi-pop-inline ${className}` : "kapi-pop-inline", role: "group", "aria-label": label }, content, popoverActions(actions));
  el.addEventListener("keydown", (e) => {
    if (!shortcuts.is("dismiss", e)) return;
    e.preventDefault();
    onDismiss?.();
  });
  return el;
}

export async function popover({ title, description, className, initialFocus, onClose }) {
  const { dialog, ready, stylesheet } = await import("./ui.js");
  await Promise.all([ready, stylesheet(sheet)]);
  const holder = h("div", { class: "kapi-pop-holder" });
  const foot = h("div", { class: "kapi-pop-actions" });
  const ui = dialog({ title, description, body: holder, footer: [foot], initialFocus: () => initialFocus?.() ?? foot.querySelector("[data-kind=primary]:not([hidden])") ?? foot.querySelector("button"), onClose });
  ui.el.classList.add("kapi-popover", ...(className ? [className] : []));
  const heading = ui.el.querySelector("h2");
  const footer = foot.closest("footer");
  return {
    ...ui,
    isOpen: () => !ui.el.hidden,
    render({ title: next, content, actions = [] }) {
      if (next) heading.textContent = next;
      holder.replaceChildren(content);
      foot.replaceChildren(...actions.filter(Boolean));
      footer.hidden = !foot.children.length;
    },
  };
}
