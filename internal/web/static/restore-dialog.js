import { h } from "./core.js";
import { popover, popoverButton, popoverContent } from "./popover.js";

const DONE_MS = 2500;
const ASKING = "This page's file is missing";

export const keptWhen = (iso) => new Date(iso).toLocaleString(undefined, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });

export const restoreQuestion = (missing) => (missing.rev
  ? `Restore version ${missing.rev}, from ${keptWhen(missing.at)}?`
  : "kapi kept no copy of this page, so there is nothing to restore.");

const path = (lead, text) => [lead, h("code", { text })];

let shared;

async function build() {
  let options = {};
  let finished = false;
  let timer = 0;
  const ui = await popover({
    title: ASKING,
    className: "kapi-restore-picker",
    onClose: () => {
      clearTimeout(timer);
      if (!finished) options.dismissed?.();
    },
  });

  function ask(missing, problem, busy = false) {
    const leave = popoverButton({ text: missing.rev ? "No, go home" : "Go home", run: () => {
      finished = true;
      options.leave();
    } });
    const restore = missing.rev ? popoverButton({ text: `Restore version ${missing.rev}`, kind: "primary", disabled: busy, run: () => restoreNow(missing) }) : null;
    ui.render({
      title: ASKING,
      content: popoverContent({
        question: restoreQuestion(missing),
        detail: path(missing.rev ? "kapi writes it back to " : "It was at ", missing.path),
        problem,
        busy,
      }),
      actions: [leave, restore],
    });
  }

  async function restoreNow(missing) {
    ask(missing, null, true);
    const result = await options.restore();
    if (result.rev) return restored(result.rev, missing.path);
    ask(missing, result.error);
    ui.el.querySelector(".kapi-pop-button[data-kind=primary]")?.focus();
  }

  function restored(rev, at) {
    finished = true;
    const back = popoverButton({ text: "Back to the page", kind: "primary", run: () => ui.close() });
    ui.render({
      title: "Page restored",
      content: popoverContent({ done: { title: `Restored version ${rev}`, detail: path("The file is back at ", at) } }),
      actions: [back],
    });
    if (ui.el.hidden) ui.open();
    back.focus();
    clearTimeout(timer);
    timer = setTimeout(() => ui.close(), DONE_MS);
  }

  return {
    open(next) {
      options = next;
      finished = false;
      clearTimeout(timer);
      ask(next.missing);
      if (ui.el.hidden) ui.open();
    },
    restored,
    close() {
      finished = true;
      ui.close();
    },
    isOpen: ui.isOpen,
  };
}

export async function restorePrompt() {
  shared ??= build();
  return shared;
}
