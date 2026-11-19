import { confirmStep } from "../../static/confirm.js";
import { root } from "../../static/core.js";
import { inlinePopover, popoverButton, popoverContent } from "../../static/popover.js";
import { restoreQuestion } from "../../static/restore-dialog.js";
import { deletePage } from "./trash.js";

export const keptNote = (p) => (p.rev ? `Version ${p.rev} kept` : "No copy kept");

export function missingCards(note, redraw) {
  const asking = new Map();
  const problems = new Map();
  const removing = new Map();
  let busy = false;

  const focus = (id) => requestAnimationFrame(() => {
    const target = asking.has(id)
      ? document.querySelector(`.nb-missing-ask[data-page="${CSS.escape(id)}"] .kapi-pop-actions :is(.kapi-pop-button:last-child, .kapi-confirm-no)`)
      : document.querySelector(`.nb-card[data-key="${CSS.escape(`card\n${id}`)}"]`);
    target?.focus({ preventScroll: true });
  });

  function step(id, next) {
    if (next) asking.set(id, next);
    else asking.delete(id);
    removing.delete(id);
    problems.delete(id);
    redraw();
    focus(id);
  }

  async function act(id, run) {
    if (busy) return;
    busy = true;
    redraw();
    const problem = await run();
    busy = false;
    if (problem) problems.set(id, problem);
    redraw();
  }

  async function restore(p) {
    let res;
    try {
      res = await fetch(`${root}p/${p.id}/recover`, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
    } catch {
      return "Could not reach kapi. Check that the daemon is running.";
    }
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return data.error || `Not restored (HTTP ${res.status}).`;
    location.assign(`${root}p/${p.id}/?restored=${data.rev}`);
    return null;
  }

  async function remove(p, title) {
    if (!(await deletePage(p.id, title, note))) return;
    asking.delete(p.id);
    removing.delete(p.id);
  }

  function confirmDelete(p, title) {
    if (!removing.has(p.id)) {
      const ask = confirmStep({ no: "Keep", run: () => act(p.id, () => remove(p, title)), onChange: (on) => on || step(p.id, null) });
      ask.arm(false);
      removing.set(p.id, ask);
    }
    return removing.get(p.id).el;
  }

  function panel(p, title) {
    const now = p.missing && asking.get(p.id);
    if (!now) return null;
    const button = (text, kind, run) => popoverButton({ text, kind, run, disabled: busy });
    const restoring = now === "restore";
    const question = restoring ? restoreQuestion({ rev: p.rev, at: p.revAt }) : p.rev ? "Delete it instead?" : "kapi kept no copy of it. Delete it?";
    const detail = restoring ? "kapi writes it back where it was." : "Its notes go to the Trash. You can undo for 10 seconds.";
    const actions = restoring
      ? [button("No", "secondary", () => step(p.id, "delete")), button(`Restore version ${p.rev}`, "primary", () => act(p.id, () => restore(p)))]
      : [confirmDelete(p, title)];
    return inlinePopover({
      label: `${title} is missing`,
      className: "nb-missing-ask",
      attrs: { "data-page": p.id, "aria-busy": String(busy) },
      content: popoverContent({ kicker: { text: "Page missing" }, subject: title, question, detail, problem: problems.get(p.id), busy }),
      actions,
      onDismiss: () => step(p.id, null),
    });
  }

  return {
    open: (p) => step(p.id, p.rev ? "restore" : "delete"),
    panel,
  };
}
