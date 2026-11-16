import { h, icon } from "./core.js";
import { dialog, ready, stylesheet } from "./ui.js";

const styled = Promise.all([ready, stylesheet(new URL("./restore-dialog.css", import.meta.url).href)]);
const DONE_MS = 2500;

export const keptWhen = (iso) => new Date(iso).toLocaleString(undefined, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });

export const restoreQuestion = (missing) => (missing.rev
  ? `Restore version ${missing.rev}, from ${keptWhen(missing.at)}?`
  : "kapi kept no copy of this page, so there is nothing to restore.");

let shared;

function build() {
  let options = {};
  let finished = false;
  let timer = 0;
  const question = h("p", { class: "kapi-restore-question" });
  const where = h("p", { class: "kapi-restore-where" });
  const error = h("p", { class: "kapi-restore-error", role: "alert", hidden: "" });
  const doneTitle = h("strong");
  const doneWhere = h("p");
  const done = h("div", { class: "kapi-restore-done", role: "status", hidden: "" }, icon("checkCircle"), h("div", {}, doneTitle, doneWhere));
  const body = h("div", { class: "kapi-restore-body" }, question, where, error, done);
  const leave = h("button", { type: "button", class: "kapi-secondary" });
  const restore = h("button", { type: "button", class: "kapi-primary" });
  const ASKING = "This page's file is missing";
  const ui = dialog({
    title: ASKING,
    body,
    footer: [leave, restore],
    initialFocus: () => (restore.hidden ? leave : restore),
    onClose: () => {
      clearTimeout(timer);
      if (!finished) options.dismissed?.();
    },
  });
  ui.el.classList.add("kapi-restore-picker");
  const heading = ui.el.querySelector("h2");

  const path = (el, lead, text) => el.replaceChildren(lead, h("code", { text }));

  function ask(missing) {
    finished = false;
    clearTimeout(timer);
    heading.textContent = ASKING;
    question.textContent = restoreQuestion(missing);
    path(where, missing.rev ? "kapi writes it back to " : "It was at ", missing.path);
    for (const el of [question, where]) el.hidden = false;
    error.hidden = true;
    done.hidden = true;
    leave.hidden = false;
    leave.textContent = missing.rev ? "No, go home" : "Go home";
    restore.hidden = !missing.rev;
    restore.textContent = `Restore version ${missing.rev}`;
    restore.disabled = false;
  }

  function restored(rev, at) {
    finished = true;
    for (const el of [question, where, error, leave]) el.hidden = true;
    heading.textContent = "Page restored";
    doneTitle.textContent = `Restored version ${rev}`;
    path(doneWhere, "The file is back at ", at);
    done.hidden = false;
    restore.hidden = false;
    restore.disabled = false;
    restore.textContent = "Back to the page";
    if (ui.el.hidden) ui.open();
    restore.focus();
    timer = setTimeout(() => ui.close(), DONE_MS);
  }

  leave.addEventListener("click", () => {
    finished = true;
    options.leave();
  });
  restore.addEventListener("click", async () => {
    if (finished) return ui.close();
    restore.disabled = true;
    body.setAttribute("aria-busy", "true");
    const result = await options.restore();
    body.removeAttribute("aria-busy");
    if (result.rev) return restored(result.rev, options.missing.path);
    restore.disabled = false;
    error.textContent = result.error;
    error.hidden = false;
  });

  return {
    open(next) {
      options = next;
      ask(next.missing);
      if (ui.el.hidden) ui.open();
    },
    restored,
    close() {
      finished = true;
      ui.close();
    },
    isOpen: () => !ui.el.hidden,
  };
}

export async function restorePrompt() {
  await styled;
  shared ??= build();
  return shared;
}
