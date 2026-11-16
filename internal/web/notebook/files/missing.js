import { h, icon, root, shortcuts } from "../../static/core.js";
import { restoreQuestion } from "../../static/restore-dialog.js";
import { deletePage } from "./trash.js";

export const keptNote = (p) => (p.rev ? `Version ${p.rev} kept` : "No copy kept");

export function missingCards(note, redraw) {
  const asking = new Map();
  const problems = new Map();
  let busy = false;

  const focus = (id) => requestAnimationFrame(() => {
    const target = asking.has(id)
      ? document.querySelector(`.nb-missing-ask[data-page="${CSS.escape(id)}"] .nb-missing-primary`)
      : document.querySelector(`.nb-card[data-key="${CSS.escape(`card\n${id}`)}"]`);
    target?.focus({ preventScroll: true });
  });

  function step(id, next) {
    if (next) asking.set(id, next);
    else asking.delete(id);
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
    if (await deletePage(p.id, title, note)) asking.delete(p.id);
  }

  function panel(p, title) {
    const now = p.missing && asking.get(p.id);
    if (!now) return null;
    const button = (text, kind, run) => {
      const el = h("button", { type: "button", class: `nb-missing-button nb-missing-${kind}`, text });
      el.disabled = busy;
      el.addEventListener("click", run);
      return el;
    };
    const restoring = now === "restore";
    const question = restoring ? restoreQuestion({ rev: p.rev, at: p.revAt }) : p.rev ? "Delete it instead?" : "kapi kept no copy of it. Delete it?";
    const detail = restoring ? "kapi writes it back where it was." : "Its notes go to the Trash. You can undo for 10 seconds.";
    const actions = restoring
      ? [button("No", "secondary", () => step(p.id, "delete")), button(`Restore version ${p.rev}`, "primary", () => act(p.id, () => restore(p)))]
      : [button("Keep", "secondary", () => step(p.id, null)), button("Delete", "primary", () => act(p.id, () => remove(p, title)))];
    if (!restoring) actions[1].classList.add("nb-missing-danger");
    const problem = problems.get(p.id);
    const el = h("div", { class: "nb-missing-ask", role: "group", "data-page": p.id, "aria-label": `${title} is missing`, "aria-busy": String(busy) },
      h("p", { class: "nb-missing-head" }, icon("warning"), h("span", { text: "Page missing" })),
      h("p", { class: "nb-missing-name", text: title }),
      h("p", { class: "nb-missing-question", text: question }),
      h("p", { class: "nb-missing-detail", text: detail }),
      problem ? h("p", { class: "nb-missing-problem", role: "alert", text: problem }) : null,
      h("div", { class: "nb-missing-actions" }, ...actions));
    el.addEventListener("keydown", (e) => {
      if (!shortcuts.is("dismiss", e)) return;
      e.preventDefault();
      step(p.id, null);
    });
    return el;
  }

  return {
    open: (p) => step(p.id, p.rev ? "restore" : "delete"),
    panel,
  };
}
