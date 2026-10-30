import { after, before, beforeEach, afterEach, test } from "node:test";
import assert from "node:assert/strict";
import { launch } from "./chrome.mjs";
import { startFakeServer, SAMPLE } from "./fake-server.mjs";
import { readFile } from "node:fs/promises";

let browser;
let server;
let page;

before(async () => {
  browser = await launch();
});

after(async () => {
  await browser?.close();
});

beforeEach(async () => {
  server = await startFakeServer();
  page = await browser.open(server.url);
  await page.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
});

afterEach(async () => {
  const errors = page.errors;
  await page.close();
  await server.close();
  assert.deepEqual(errors, []);
});

const requests = (method, suffix) =>
  server.state.requests.filter((r) => r.method === method && r.path.endsWith(suffix));
const pageLoads = () => requests("GET", new URL(server.url).pathname).length;
const click = (selector) => page.eval(`document.querySelector(${JSON.stringify(selector)}).click()`);
const select = (id) => click(`[data-kapi-id="${id}"]`);
const editSelected = () => page.eval(`document.querySelector("[data-kapi-selected]").dispatchEvent(new MouseEvent("dblclick", { bubbles: true }))`);
const notice = () => page.eval(`(n => n.hidden ? "" : n.textContent)(document.querySelector(".kapi-notice"))`);
const text = (id) => page.eval(`document.querySelector('[data-kapi-id="${id}"]')?.textContent ?? null`);

async function until(fn, timeout = 3000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await fn()) return;
    await new Promise((r) => setTimeout(r, 25));
  }
  assert.fail(`timed out waiting for ${fn}`);
}

test("refetches the page after the stream reconnects, resuming from the last event id", async () => {
  server.push({ kind: "message", text: "hello" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 1`);
  const loads = pageLoads();
  server.bumpBehindBrowser("bbbbbbbb", '<p data-kapi-id="bbbbbbbb">Changed while offline.</p>');
  server.dropStreams();

  await until(() => requests("GET", "/events").some((r) => r.headers["last-event-id"] === "1"));
  await until(async () => (await text("bbbbbbbb")) === "Changed while offline.");
  assert.equal(pageLoads(), loads + 1);
  assert.equal(await page.eval(`document.getElementById("kapi-page").dataset.rev`), "2");
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg").length`), 1);
});

test("shows disconnected when the stream fails and recovers without duplicating chat", async () => {
  server.push({ kind: "message", text: "first" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 1`);
  server.state.refuseEvents = true;
  server.dropStreams();
  await page.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "disconnected"`);
  assert.match(await page.eval(`document.querySelector(".kapi-status").textContent`), /Disconnected/);
  await until(() => server.state.refused > 0);
  await new Promise((r) => setTimeout(r, 200));

  const loads = pageLoads();
  server.state.refuseEvents = false;
  await page.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`, 5000);
  await until(() => pageLoads() > loads);
  assert.equal(requests("GET", "/events").at(-1).headers["last-event-id"], undefined);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg").length`), 1);
});

test("sets the status dot from presence events", async () => {
  server.push({ kind: "presence", text: "watching" });
  await page.waitFor(`document.querySelector(".kapi-status").textContent === "Agent ready"`);
  server.push({ kind: "presence", text: "busy" });
  await page.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
});

test("says Queued until the agent reads a message, then working until it replies", async () => {
  const status = () => page.eval(`document.querySelector(".kapi-status").textContent`);
  const marks = () => page.eval(`[...document.querySelectorAll(".kapi-msg")].map((m) => m.querySelector(".kapi-queued, .kapi-read")?.textContent || "")`);
  server.push({ kind: "presence", text: "watching" });
  server.push({ author: "human", kind: "message", text: "please look" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 1`);
  assert.equal(await status(), "Agent ready");
  assert.deepEqual(await marks(), [""]);

  server.queue("agent", 2);
  await until(async () => (await status()) === "Queued for the agent");
  assert.deepEqual(await marks(), ["Queued"]);
  assert.equal(await page.eval(`document.querySelector(".kapi-panel .kapi-dot").dataset.state`), "queued");

  await page.eval(`location.reload()`);
  await page.waitFor(`document.readyState === "complete" && !!document.querySelector(".kapi-msg .kapi-queued")`);
  assert.equal(await status(), "Queued for the agent");

  server.ack("agent", 2);
  await until(async () => (await status()) === "Agent is working");
  assert.deepEqual(await marks(), ["\u{1F440}"]);

  server.push({ author: "agent", kind: "message", text: "on it" });
  await until(async () => (await status()) === "Agent ready");
});

test("refetches only the changed block, or the whole page when the target is empty", async () => {
  const loads = pageLoads();
  server.bumpBehindBrowser("cccccccc", '<ul data-kapi-id="cccccccc"><li>Agent rewrote this</li></ul>');
  server.push({ kind: "revision", target: "cccccccc", text: "edited #cccccccc" });
  await until(async () => (await text("cccccccc")) === "Agent rewrote this");
  assert.equal(requests("GET", "/blocks/cccccccc").length, 1);
  assert.equal(pageLoads(), loads);

  server.bumpBehindBrowser("aaaaaaaa", '<h1 data-kapi-id="aaaaaaaa">Renamed plan</h1>');
  server.push({ kind: "revision", target: "", text: "file changed" });
  await until(async () => (await text("aaaaaaaa")) === "Renamed plan");
  assert.equal(pageLoads(), loads + 1);
});

test("on 409 it refetches the page and tells the user", async () => {
  server.bumpBehindBrowser("cccccccc", '<ul data-kapi-id="cccccccc"><li>Newer list</li></ul>');
  await select("cccccccc");
  await click('.kapi-bar [data-action="archive"]');
  await until(async () => /page changed/.test(await notice()));
  assert.equal(await text("cccccccc"), "Newer list");
  assert.equal(await page.eval(`document.getElementById("kapi-page").dataset.rev`), "2");
  assert.equal(requests("POST", "/ops")[0].body.rev, 1);
});

test("double click edits the selected block, Enter does not, and the desktop toolbar has no Edit button", async () => {
  await select("bbbbbbbb");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  assert.equal(await page.eval(`document.querySelectorAll('.kapi-bar [data-action="edit"]').length`), 0);
  await page.key("Enter");
  assert.equal(await page.eval(`!!document.querySelector('.kapi-bar [data-action="cancel"]')`), false);
  await editSelected();
  await page.waitFor(`!!document.querySelector('.kapi-bar [data-action="cancel"]')`);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector('.kapi-bar [data-action="cancel"]')`);
});

test("shows the 422 inbound-link error and keeps the block", async () => {
  await select("bbbbbbbb");
  await click('.kapi-bar [data-action="delete"]');
  await until(async () => (await notice()) === "“Move the session store to the new clust…” is linked from “See the summary before starting.”");
  assert.equal(await page.eval(`document.querySelector(".kapi-notice").dataset.kind`), "error");
  assert.notEqual(await text("bbbbbbbb"), null);
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  assert.equal(await notice(), "");
});

test("archives a block to one dimmed labelled line and unarchives it", async () => {
  const height = () => page.eval(`(el => (s => el.getBoundingClientRect().height - parseFloat(s.paddingTop) - parseFloat(s.paddingBottom))(getComputedStyle(el)))(document.querySelector('[data-kapi-id="cccccccc"]'))`);
  const full = await height();
  await select("cccccccc");
  assert.equal(await page.eval(`document.querySelectorAll('.kapi-bar [data-action="done"]').length`), 0);
  await click('.kapi-bar [data-action="archive"]');
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "archive", id: "cccccccc" } });
  await page.waitFor(`document.querySelector('[data-kapi-id="cccccccc"]').dataset.kapiState === "archived"`);
  const [opacity, label, excerpt] = await page.eval(`(() => {
    const el = document.querySelector('[data-kapi-id="cccccccc"]');
    return [Number(getComputedStyle(el).opacity), getComputedStyle(el, "::before").content, getComputedStyle(el, "::after").content];
  })()`);
  assert.ok(opacity < 1, `opacity ${opacity}`);
  assert.equal(label, '"archived"');
  assert.equal(excerpt, '"Copy data Flip reads"');
  assert.ok((await height()) < 24, `archived height ${await height()}, full ${full}`);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="cccccccc"]').checkVisibility()`), true);

  await page.waitFor(`!!document.querySelector('.kapi-bar [data-action="unarchive"]')`);
  assert.equal(await page.eval(`document.querySelectorAll('.kapi-bar [data-action="archive"], .kapi-bar [data-action="edit"]').length`), 0);
  await click('.kapi-bar [data-action="unarchive"]');
  await until(() => requests("POST", "/ops").length === 2);
  assert.deepEqual(requests("POST", "/ops")[1].body, { rev: 2, op: { kind: "unarchive", id: "cccccccc" } });
  await page.waitFor(`!document.querySelector('[data-kapi-id="cccccccc"]').hasAttribute("data-kapi-state")`);
  assert.equal(await height(), full);
});

test("deletes an unlinked block", async () => {
  await select("cccccccc");
  await click('.kapi-bar [data-action="delete"]');
  await until(async () => (await text("cccccccc")) === null);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "delete", id: "cccccccc" } });
});

const undoNote = () => page.eval(`(n => n ? { text: n.textContent, prev: n.previousElementSibling?.dataset.kapiId ?? null, next: n.nextElementSibling?.dataset.kapiId ?? null } : null)(document.querySelector("#kapi-page .kapi-undo-note"))`);
const deleteBlock = async (id) => {
  const loads = pageLoads();
  await select(id);
  await click('.kapi-bar [data-action="delete"]');
  await page.waitFor(`!!document.querySelector("#kapi-page .kapi-undo-note")`);
  await until(() => pageLoads() > loads);
  await page.waitFor(`!!document.querySelector("#kapi-page .kapi-undo-note") && !document.querySelector("[data-kapi-selected]")`);
  await settled();
};

async function settled() {
  let last = await page.eval(`window.kapiSettle = 0; new MutationObserver(() => window.kapiSettle++).observe(document.getElementById("kapi-page"), { childList: true }); 0`);
  for (let quiet = 0; quiet < 3; ) {
    await new Promise((r) => setTimeout(r, 100));
    const now = await page.eval(`window.kapiSettle`);
    quiet = now === last ? quiet + 1 : 0;
    last = now;
  }
}

test("a delete leaves an Undo notice where the block was", async () => {
  const before = await page.eval(`document.documentElement.scrollHeight`);
  await deleteBlock("cccccccc");
  assert.deepEqual(await undoNote(), { text: "DeletedUndo", prev: "bbbbbbbb", next: "dddddddd" });
  assert.equal(await text("cccccccc"), null);
  assert.ok((await page.eval(`document.documentElement.scrollHeight`)) <= before);
});

test("Undo in the notice restores the block with an undo op and removes the notice", async () => {
  await deleteBlock("cccccccc");
  await click("#kapi-page .kapi-undo-note button");
  await until(async () => (await text("cccccccc")) !== null);
  assert.deepEqual(requests("POST", "/ops")[1].body.op, { kind: "undo", id: "" });
  assert.equal(await undoNote(), null);
});

test("the delete notice survives the page reload that follows the delete", async () => {
  await deleteBlock("cccccccc");
  await until(() => pageLoads() >= 2);
  assert.notEqual(await undoNote(), null);
});

test("the delete notice goes away after a few seconds", async () => {
  await deleteBlock("cccccccc");
  await page.waitFor(`!document.querySelector("#kapi-page .kapi-undo-note")`, 8000);
});

test("the delete notice goes away on the next op", async () => {
  await deleteBlock("cccccccc");
  await select("dddddddd");
  await click('.kapi-bar [data-action="archive"]');
  await page.waitFor(`!document.querySelector("#kapi-page .kapi-undo-note")`);
});

test("the delete notice goes away on Escape", async () => {
  await deleteBlock("cccccccc");
  await page.key("Escape");
  await page.waitFor(`!document.querySelector("#kapi-page .kapi-undo-note")`);
});

test("the delete notice does not appear for a delete made by the agent", async () => {
  server.state.blocks.splice(2, 1);
  server.state.rev++;
  server.push({ kind: "revision", target: "", text: "deleted #cccccccc" });
  await until(async () => (await text("cccccccc")) === null);
  assert.equal(await undoNote(), null);
});

test("the delete notice goes away when undo stops being available", async () => {
  await deleteBlock("cccccccc");
  server.state.history = [];
  server.push({ kind: "revision", target: "", text: "file changed" });
  await page.waitFor(`document.querySelector('.kapi-panel [data-action="undo"]').hidden`);
  await page.waitFor(`!document.querySelector("#kapi-page .kapi-undo-note")`);
});

test("a refused Undo keeps the notice so the reader can retry", async () => {
  await deleteBlock("cccccccc");
  server.state.rev++;
  await click("#kapi-page .kapi-undo-note button");
  await until(async () => (await notice()) !== "");
  assert.notEqual(await undoNote(), null);
  assert.equal(await page.eval(`document.querySelector("#kapi-page .kapi-undo-note button").disabled`), false);
  await click("#kapi-page .kapi-undo-note button");
  await until(async () => (await text("cccccccc")) !== null);
  assert.equal(await undoNote(), null);
});

test("the delete notice neither counts as a folded block nor gets folded away", async () => {
  await reopenWith([
    { id: "h2aaaaaa", html: '<h2 data-kapi-id="h2aaaaaa">Callouts</h2>' },
    { id: "p2p2p2p2", html: '<p data-kapi-id="p2p2p2p2">A note.</p>' },
    { id: "h2bbbbbb", html: '<h2 data-kapi-id="h2bbbbbb">Tables</h2>' },
    { id: "p3p3p3p3", html: '<p data-kapi-id="p3p3p3p3">A table.</p>' },
  ]);
  await deleteBlock("h2bbbbbb");
  const { x, y } = await page.eval(`(({ left, top, height }) => ({ x: left - 12, y: top + height / 2 }))(document.querySelector('[data-kapi-id="h2aaaaaa"]').getBoundingClientRect())`);
  await mouseClickAt(x, y);
  await page.waitFor(`!!document.querySelector('[data-kapi-id="h2aaaaaa"]').dataset.kapiFolded`);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="h2aaaaaa"]').dataset.kapiFolded`), "2 blocks");
  assert.equal(await page.eval(`document.querySelector("#kapi-page .kapi-undo-note").checkVisibility()`), true);
});

test("a second click on Undo lands on the notice and does nothing", async () => {
  await deleteBlock("cccccccc");
  server.state.opDelay = 400;
  const { x, y } = await page.eval(`(({ left, top, width, height }) => ({ x: left + width - 20, y: top + height / 2 }))(document.querySelector("#kapi-page .kapi-undo-note button").getBoundingClientRect())`);
  await mouseClickAt(x, y);
  await mouseClickAt(x, y);
  assert.equal(await page.eval(`!!document.querySelector("[data-kapi-selected]")`), false);
  assert.notEqual(await undoNote(), null);
  await until(async () => (await text("cccccccc")) !== null);
  assert.equal(requests("POST", "/ops").length, 2);
  assert.equal(await undoNote(), null);
});

test("the delete notice fits the 390px phone layout", async () => {
  const phone = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await phone.eval(`document.querySelector('[data-kapi-id="cccccccc"]').click()`);
    await phone.waitFor(`!document.querySelector(".kapi-bar").hidden`);
    await phone.eval(`document.querySelector('.kapi-bar [data-action="delete"]').click()`);
    await phone.waitFor(`!!document.querySelector("#kapi-page .kapi-undo-note")`);
    assert.ok(await phone.eval(`(r => r.left >= 0 && r.right <= innerWidth && r.height >= 28)(document.querySelector("#kapi-page .kapi-undo-note").getBoundingClientRect())`));
  } finally {
    await phone.close();
  }
});

test("sends a chat message with Enter, never targeted at the selected block", async () => {
  assert.equal(await page.eval(`document.querySelector(".kapi-hint").textContent`), "Enter to send, Shift+Enter for a new line");
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("Looks good");
  await page.key("Enter");
  await page.waitFor(`document.querySelector(".kapi-msg .kapi-msg-text")?.textContent === "Looks good"`);
  assert.deepEqual(requests("POST", "/messages")[0].body, { kind: "message", text: "Looks good" });
  await page.waitFor(`document.querySelector(".kapi-composer textarea").value === "" && !document.querySelector(".kapi-composer .kapi-primary").disabled`);

  await select("bbbbbbbb");
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-composer .kapi-target").length`), 0);
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("Why two steps?");
  await click(".kapi-composer .kapi-primary");
  await until(() => requests("POST", "/messages").length === 2);
  assert.deepEqual(requests("POST", "/messages")[1].body, { kind: "message", text: "Why two steps?" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 2`);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg")[1].querySelector(".kapi-ref")`), null);
});

test("an agent message targeted at a block links to it", async () => {
  server.push({ author: "agent", kind: "message", target: "bbbbbbbb", text: "Done." });
  await page.waitFor(`!!document.querySelector(".kapi-msg .kapi-ref")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-msg .kapi-ref").textContent`), "Move the session store to the new clust…");
  assert.equal(await page.eval(`document.querySelector(".kapi-msg .kapi-ref").title`), "#bbbbbbbb");
});

test("Shift+Enter in the composer adds a new line and Enter sends both lines", async () => {
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("line one");
  await page.key("Enter", { shift: true });
  await page.type("line two");
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(requests("POST", "/messages").length, 0);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "line one\nline two");
  await page.key("Enter");
  await until(() => requests("POST", "/messages").length === 1);
  assert.equal(requests("POST", "/messages")[0].body.text, "line one\nline two");
});

test("Enter does not send while an input method is composing", async () => {
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("nihon");
  const sent = await page.eval(`(() => {
    const input = document.querySelector(".kapi-composer textarea");
    const events = [{ isComposing: true }, { keyCode: 229 }].map((init) =>
      new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true, ...init }));
    return events.map((e) => [e.isComposing || e.keyCode === 229, input.dispatchEvent(e)]);
  })()`);
  assert.deepEqual(sent, [[true, true], [true, true]]);
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(requests("POST", "/messages").length, 0);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "nihon");
});

test("typing `text` in a block turns it into inline code and the caret keeps typing outside it", async () => {
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  for (const ch of " the `new` cluster") await page.type(ch);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"] code')?.textContent`), "new");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.match(requests("POST", "/ops")[0].body.op.parts[0].inner, /the <code>new<\/code>\u200b? cluster$/);
  await until(() => /the <code>new<\/code> cluster<\/p>$/.test(server.state.blocks.find((b) => b.id === "bbbbbbbb").html));
});

test("plain Enter in the block and source editors is a new line, not a save", async () => {
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  await page.key("Enter");
  await page.type("next line");
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(requests("POST", "/ops").length, 0);
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 1);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector("[contenteditable]")`);

  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  await page.eval(`(a => a.setSelectionRange(a.value.length, a.value.length))(document.activeElement)`);
  await page.key("Enter");
  await page.type("more");
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(requests("POST", "/ops").length, 0);
  assert.equal(await page.eval(`document.activeElement.value`), "if a &lt; b {\n\tswap()\n}\nmore");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.equal(requests("POST", "/ops")[0].body.op.parts[0].inner, "if a &lt; b {\n\tswap()\n}\nmore");
});

test("edits a prose preset's rendered body in place and saves it as inner", async () => {
  await select("ffffffff");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-callout-body")`);
  assert.equal(await page.eval(`document.activeElement.isContentEditable`), true);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-source").length`), 0);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="ffffffff"] .kapi-callout-label').isContentEditable`), false);
  await page.type(" Then verify.");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "text", id: "ffffffff", parts: [{ id: "ffffffff", inner: "Copy during the <em>quiet</em> window. Then verify." }] } });
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  assert.equal(server.state.blocks.find((b) => b.id === "ffffffff").source, '<kapi-callout data-kapi-id="ffffffff" kind="note">Copy during the <em>quiet</em> window. Then verify.</kapi-callout>');
});

async function addBlock(block) {
  server.state.blocks.push(block);
  await page.eval(`location.reload()`);
  await page.waitFor(`document.readyState === "complete" && !!document.querySelector('[data-kapi-id="${block.id}"]') && !!document.getElementById("kapi-chrome")`);
}

test("Escape ends an edit even when focus has left the text", async () => {
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  await page.eval(`document.activeElement.blur()`);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  await select("dddddddd");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "dddddddd"`);
});

test("clicking outside an edit cancels it and the same click selects its target", async () => {
  await select("ffffffff");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-callout-body")`);
  await page.type(" discarded");
  await page.eval(`(() => {
    const target = document.querySelector('[data-kapi-id="bbbbbbbb"]');
    target.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, pointerType: "mouse" }));
    target.click();
  })()`);
  await page.waitFor(`!document.querySelector("[contenteditable], .kapi-source")`);
  assert.equal(await page.eval(`document.querySelector("[data-kapi-selected]")?.dataset.kapiId`), "bbbbbbbb");
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="ffffffff"] .kapi-callout-body').textContent`), "Copy during the quiet window.");
  assert.equal(requests("POST", "/ops").length, 0);
});

test("a code block inside a callout stays one piece while the callout is edited", async () => {
  await addBlock({
    id: "kkkkkkkk",
    html: '<aside class="kapi-callout" data-kapi-preset="callout" data-kapi-id="kkkkkkkk"><div class="kapi-callout-body" data-kapi-text="kkkkkkkk"><p>Run</p>' +
      '<figure class="kapi-code" data-kapi-preset="code" data-kapi-id="llllllll" data-kapi-text="llllllll" data-kapi-raw=""><pre><code>kapi open</code></pre></figure></div></aside>',
  });
  await select("kkkkkkkk");
  await editSelected();
  await page.waitFor(`document.querySelector('[data-kapi-id="kkkkkkkk"] .kapi-callout-body').isContentEditable`);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="llllllll"]').isContentEditable`), false);
  assert.equal(await page.eval(`getSelection().anchorNode.parentElement.tagName`), "P");
  await page.type(" it");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  const { parts } = requests("POST", "/ops")[0].body.op;
  assert.deepEqual(parts.map((p) => p.id), ["kkkkkkkk"]);
  assert.match(parts[0].inner, /^<p>Run it<\/p><figure/);
});

test("a closed details block opens when it is edited", async () => {
  await addBlock({
    id: "pppppppp",
    html: '<details class="kapi-details" data-kapi-preset="details" data-kapi-id="pppppppp"><summary>Why</summary><div class="kapi-details-body" data-kapi-text="pppppppp"><p>Because.</p></div></details>',
  });
  await select("pppppppp");
  await editSelected();
  await page.waitFor(`document.querySelector('[data-kapi-id="pppppppp"]').open && document.activeElement?.classList.contains("kapi-details-body")`);
  assert.equal(await page.eval(`document.activeElement.checkVisibility()`), true);
});

test("ticking a task while its checklist is being edited keeps the edit", async () => {
  const task = (id, text) => `<li class="kapi-task" data-kapi-preset="task" data-kapi-id="${id}"><input type="checkbox"><span class="kapi-task-body" data-kapi-text="${id}">${text}</span></li>`;
  await addBlock({ id: "mmmmmmmm", html: `<ul class="kapi-checklist" data-kapi-preset="checklist" data-kapi-id="mmmmmmmm">${task("nnnnnnnn", "one")}${task("oooooooo", "two")}</ul>` });
  await select("mmmmmmmm");
  await editSelected();
  await page.waitFor(`document.querySelectorAll('[data-kapi-id="mmmmmmmm"] [contenteditable="true"]').length === 2`);
  await click('[data-kapi-id="oooooooo"] input');
  await until(() => requests("POST", "/ops").some((r) => r.body.op.kind === "check"));
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(await page.eval(`document.querySelectorAll('[data-kapi-id="mmmmmmmm"] [contenteditable="true"]').length`), 2);
  await page.eval(`(h => { h.focus(); getSelection().selectAllChildren(h); getSelection().collapseToEnd(); })(document.querySelector('[data-kapi-text="oooooooo"]'))`);
  await page.type(" more");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").some((r) => r.body.op.kind === "text"));
  const op = requests("POST", "/ops").find((r) => r.body.op.kind === "text").body.op;
  assert.deepEqual(op.parts, [{ id: "oooooooo", inner: "two more" }]);
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  await until(() => page.eval(`document.querySelector('[data-kapi-id="oooooooo"] input').checked`));
});

test("a block with no text of its own says so and opens no editor", async () => {
  await page.eval(`document.querySelector('[data-kapi-id="eeeeeeee"]').click()`);
  await page.waitFor(`document.querySelector('[data-kapi-id="eeeeeeee"]').hasAttribute("data-kapi-selected")`);
  await editSelected();
  await until(async () => /no text to edit/.test(await notice()));
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable], .kapi-source").length`), 0);
  assert.equal(requests("GET", "/source/eeeeeeee").length, 0);
});

test("edits table cells in place and sends only the changed cells", async () => {
  await click('[data-kapi-id="gggggggg"] th');
  await page.waitFor(`document.querySelector('[data-kapi-id="gggggggg"]').hasAttribute("data-kapi-selected")`);
  await click('[data-kapi-id="gggggggg"] th');
  const order = () => page.eval(`[...document.querySelectorAll('[data-kapi-id="gggggggg"] tbody td:first-child')].map((td) => td.textContent).join(",")`);
  assert.notEqual(await order(), "beta,Alpha,gamma");
  await editSelected();
  await page.waitFor(`document.querySelectorAll('[data-kapi-id="gggggggg"] [contenteditable="true"]').length === 8`);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-source").length`), 0);
  await page.eval(`(() => {
    const cell = [...document.querySelectorAll('[data-kapi-id="gggggggg"] td')].find((td) => td.textContent === "beta");
    cell.focus();
    getSelection().selectAllChildren(cell);
  })()`);
  await page.type("b<e>ta & co");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "text", id: "gggggggg", parts: [{ id: "gggggggg", row: 1, col: 0, inner: "b&lt;e&gt;ta &amp; co" }] } });
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  await until(async () => (await page.eval(`document.querySelector('[data-kapi-id="gggggggg"] td').textContent`)) === "b<e>ta & co");
});

const barButtons = () => page.eval(`[...document.querySelectorAll(".kapi-bar button")].map((b) => (b.classList.contains("kapi-primary") ? "*" : "") + b.textContent)`);
const shortcut = () => page.eval(`/Mac|iPhone|iPad/.test(navigator.platform) ? "\u2318\u21b5" : "Ctrl+\u21b5"`);

test("the edit bar offers Save only after a change and highlights the default action", async () => {
  const mod = await shortcut();
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  assert.deepEqual(await barButtons(), [`*Cancel ${mod}`]);
  await page.type("!");
  await page.waitFor(`!!document.querySelector('.kapi-bar .kapi-primary[data-action="save"]')`);
  assert.deepEqual(await barButtons(), [`*Save ${mod}`, "Cancel"]);
  await page.eval(`(sel => { sel.collapseToEnd(); sel.modify("extend", "backward", "character"); })(getSelection())`);
  await page.call("Input.dispatchKeyEvent", { type: "rawKeyDown", key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 });
  await page.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 });
  await page.waitFor(`!document.querySelector('.kapi-bar [data-action="save"]')`);
  assert.deepEqual(await barButtons(), [`*Cancel ${mod}`]);
  await page.key("Enter", { ctrl: true });
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  assert.equal(requests("POST", "/ops").length, 0);
});

test("Cmd or Ctrl+Enter runs the default action in every editor, and Escape cancels", async () => {
  const kinds = [
    { id: "bbbbbbbb", ready: `document.activeElement.dataset.kapiId === "bbbbbbbb"` },
    { id: "ffffffff", ready: `document.activeElement?.classList.contains("kapi-callout-body")` },
    { id: "gggggggg", ready: `document.activeElement?.matches('[data-kapi-id="gggggggg"] th')` },
    { id: "jjjjjjjj", ready: `document.activeElement?.classList.contains("kapi-source")` },
  ];
  for (const { id, ready } of kinds) {
    for (const end of ["ctrl unchanged", "escape changed"]) {
      await select(id);
      await editSelected();
      await page.waitFor(ready);
      if (end === "escape changed") {
        await page.type("x");
        await page.waitFor(`!!document.querySelector('.kapi-bar [data-action="save"]')`);
        await page.key("Escape");
      } else await page.key("Enter", { ctrl: true });
      await page.waitFor(`!document.querySelector('[contenteditable], .kapi-source')`);
      assert.equal(requests("POST", "/ops").length, 0, `${id} ${end}`);
    }
  }
  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(kinds[3].ready);
  await page.type("x");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
});

test("in a table cell plain Enter runs the default action, and Tab moves to the bar", async () => {
  await click('[data-kapi-id="gggggggg"] th');
  await editSelected();
  await page.waitFor(`document.activeElement?.matches('[data-kapi-id="gggggggg"] th')`);
  await page.key("Tab");
  assert.equal(await page.eval(`document.activeElement.dataset.action`), "cancel");
  await page.eval(`document.querySelector('[data-kapi-id="gggggggg"] th').focus()`);
  await page.key("Enter");
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  assert.equal(requests("POST", "/ops").length, 0);

  await editSelected();
  await page.waitFor(`document.activeElement?.matches('[data-kapi-id="gggggggg"] th')`);
  await page.type("!");
  await page.key("Tab");
  assert.equal(await page.eval(`document.activeElement.dataset.action`), "save");
  await page.eval(`document.querySelector('[data-kapi-id="gggggggg"] th').focus()`);
  await page.key("Enter");
  await until(() => requests("POST", "/ops").length === 1);
  assert.equal(requests("POST", "/ops")[0].body.op.kind, "text");
});

test("while a block is being edited the composer keys belong to the composer", async () => {
  await click('[data-kapi-id="gggggggg"] th');
  await editSelected();
  await page.waitFor(`document.activeElement?.matches('[data-kapi-id="gggggggg"] th')`);
  await page.type("!");
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("about this table");
  await page.key("Enter");
  await until(() => requests("POST", "/messages").length === 1);
  await page.type("and again");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/messages").length === 2);
  await page.key("Escape");
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(requests("POST", "/ops").length, 0);
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 8);
  assert.equal(await page.eval(`!!document.querySelector('.kapi-bar [data-action="save"]')`), true);
});

test("text typed while a message is still sending stays in the composer", async () => {
  server.state.messageDelay = 300;
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("first");
  await page.key("Enter");
  await page.type("second");
  await until(() => server.state.events.some((e) => e.text === "first"));
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "second");
});

test("Shift+Enter in a table cell inserts nothing", async () => {
  await click('[data-kapi-id="gggggggg"] th');
  await editSelected();
  await page.waitFor(`document.activeElement?.matches('[data-kapi-id="gggggggg"] th')`);
  await page.key("Enter", { shift: true });
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="gggggggg"] th').textContent`), "Shard");
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 8);
  assert.equal(requests("POST", "/ops").length, 0);
});

test("edits a block in place and posts its new inner HTML", async () => {
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  assert.equal(await page.eval(`document.activeElement.innerText`), "Move the session store to the new cluster in two steps.");
  await page.type(" Then verify.");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);

  assert.deepEqual(requests("POST", "/ops")[0].body, {
    rev: 1,
    op: {
      kind: "text",
      id: "bbbbbbbb",
      parts: [{ id: "bbbbbbbb", inner: "Move the session store to the new cluster in two steps. Then verify." }],
    },
  });
  await until(() => requests("GET", "/blocks/bbbbbbbb").length >= 1);
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  assert.equal(await page.eval(`document.getElementById("kapi-page").dataset.rev`), "2");
});

test("Escape cancels an edit without posting", async () => {
  await select("aaaaaaaa");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "aaaaaaaa"`);
  await page.type("junk");
  await page.key("Escape");
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  assert.equal(await text("aaaaaaaa"), "Migration plan");
  assert.equal(requests("POST", "/ops").length, 0);
});

test("the toolbar is Ask AI, Archive and Delete, with their keys, and no move buttons", async () => {
  await select("cccccccc");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  const tools = await page.eval(`[...document.querySelectorAll(".kapi-bar button")].map((b) => [b.dataset.action, b.dataset.tip, b.getAttribute("aria-keyshortcuts")])`);
  const del = (await page.eval(`/Mac/.test(navigator.platform)`)) ? "\u232b" : "Del";
  assert.deepEqual(tools, [["ask", "Ask AI A", "A"], ["copy", "Copy as Markdown C", "C"], ["archive", "Archive E", "E"], ["delete", `Delete ${del}`, "Delete"]]);
});

test("the floating block toolbar is larger than the surrounding chrome", async () => {
  await select("bbbbbbbb");
  await page.waitFor(`document.querySelector(".kapi-bar").parentElement.id === "kapi-chrome"`);
  const sizes = await page.eval(`(() => {
    const button = document.querySelector('.kapi-bar [data-action="copy"]');
    const icon = button.querySelector("svg");
    return [button.getBoundingClientRect().height, icon.getBoundingClientRect().width];
  })()`);
  assert.deepEqual(sizes, [34, 20]);
});

test("Copy copies the selected block through the Markdown export", async () => {
  server.state.blocks.find((b) => b.id === "bbbbbbbb").markdown = "Move the **session store**.\n";
  server.state.blocks.find((b) => b.id === "gggggggg").markdown = "| Shard | Rows |\n| --- | --- |\n| beta | 10 |\n";
  await page.eval(`window.copied = []; navigator.clipboard.writeText = async (text) => { window.copied.push(text); }`);
  await select("bbbbbbbb");
  await click('.kapi-bar [data-action="copy"]');
  await until(async () => (await page.eval(`window.copied.length`)) === 1);
  assert.deepEqual(await page.eval(`(() => { const b = document.querySelector('.kapi-bar [data-action="copy"]'); return [b.dataset.copyState, b.getAttribute("aria-label"), b.textContent]; })()`), ["copied", "Copied", "Copied"]);
  assert.equal(await page.eval(`getComputedStyle(document.querySelector('.kapi-bar [data-action="copy"]')).color`), await page.eval(`(() => { const s = document.createElement("span"); s.style.color = "var(--kapi-green-text)"; document.body.append(s); const color = getComputedStyle(s).color; s.remove(); return color; })()`));
  assert.equal(await notice(), "");
  await select("gggggggg");
  await page.key("c");
  await until(async () => (await page.eval(`window.copied.length`)) === 2);
  assert.deepEqual(await page.eval(`window.copied`), ["Move the **session store**.\n", "| Shard | Rows |\n| --- | --- |\n| beta | 10 |\n"]);
  assert.equal(requests("GET", "/export/markdown").at(-1).search, "?block=gggggggg");
  assert.deepEqual(await page.eval(`(() => { const b = document.querySelector('.kapi-bar [data-action="copy"]'); return [b.dataset.copyState, b.getAttribute("aria-label"), b.textContent]; })()`), ["copied", "Copied", "Copied"]);
});

test("a selected diagram has Phosphor zoom controls that resize only its SVG", async () => {
  await page.eval(`(() => {
    const block = document.querySelector('[data-kapi-id="bbbbbbbb"]');
    block.dataset.kapiPreset = "diagram";
    block.className = "kapi-diagram";
    block.innerHTML = '<svg viewBox="0 0 800 200" width="800" height="200"><rect width="800" height="200"></rect></svg>';
  })()`);
  await select("bbbbbbbb");
  assert.deepEqual(await barActions(), ["ask", "copy", "zoom-out", "zoom-in", "archive", "delete"]);
  const before = await page.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"] svg').getBoundingClientRect().width`);
  await click('.kapi-bar [data-action="zoom-in"]');
  const larger = await page.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"] svg').getBoundingClientRect().width`);
  await click('.kapi-bar [data-action="zoom-out"]');
  const restored = await page.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"] svg').getBoundingClientRect().width`);
  assert.ok(larger > before, JSON.stringify({ before, larger }));
  assert.ok(Math.abs(restored - before) < 1, JSON.stringify({ before, restored }));
  assert.equal(await page.eval(`document.querySelector("#kapi-page").style.transform`), "");
  await page.eval(`(() => {
    const block = document.querySelector('[data-kapi-id="bbbbbbbb"]');
    delete block.dataset.kapiPreset;
    block.className = "";
    block.textContent = "Move the session store to the new cluster in two steps.";
  })()`);
});

test("Undo shows only when there is something to undo, on the left so the theme toggle never moves", async () => {
  const undoShown = () => page.eval(`!document.querySelector('.kapi-panel [data-action="undo"]').hidden`);
  const themeLeft = () => page.eval(`document.querySelector(".kapi-panel .kapi-theme").getBoundingClientRect().left`);
  assert.equal(await undoShown(), false);
  const before = await themeLeft();
  await select("cccccccc");
  await click('.kapi-bar [data-action="archive"]');
  await page.waitFor(`!document.querySelector('.kapi-panel [data-action="undo"]').hidden`);
  assert.equal(await themeLeft(), before);
  assert.ok(await page.eval(`document.querySelector('.kapi-panel [data-action="undo"]').getBoundingClientRect().right < document.querySelector(".kapi-panel .kapi-theme").getBoundingClientRect().left`));
  await click('.kapi-panel [data-action="undo"]');
  await page.waitFor(`document.querySelector('.kapi-panel [data-action="undo"]').hidden`);
});

test("the Kapi logo stays at the top left on wide desktops and scrolls away when there is no room", async () => {
  const wide = await browser.open(server.url, { width: 1440, height: 700 });
  try {
    await wide.eval(`document.body.style.paddingBottom = "3000px"; scrollTo(0, 1200)`);
    await wide.waitFor(`scrollY >= 1200`);
    const [head, page] = await wide.eval(`[".kapi-site-head", "#kapi-page"].map((q) => (({ top, left, right, bottom }) => ({ top, left, right, bottom }))(document.querySelector(q).getBoundingClientRect()))`);
    assert.ok(head.top >= 0 && head.bottom <= 80, JSON.stringify(head));
    const textLeft = await wide.eval(`document.querySelector("#kapi-page").getBoundingClientRect().left + parseFloat(getComputedStyle(document.querySelector("#kapi-page")).paddingLeft)`);
    assert.ok(head.right <= textLeft, JSON.stringify({ head, page, textLeft }));
  } finally {
    await wide.close();
  }
  const narrow = await browser.open(server.url, { width: 1100, height: 700 });
  try {
    await narrow.eval(`document.body.style.paddingBottom = "3000px"; scrollTo(0, 1200)`);
    await narrow.waitFor(`scrollY >= 1200`);
    assert.ok(await narrow.eval(`document.querySelector(".kapi-site-head").getBoundingClientRect().bottom < 0`));
  } finally {
    await narrow.close();
  }
});

test("submits kapi-form answers", async () => {
  await click('input[name="when"][value="later"]');
  await click('input[name="envs"][value="staging"]');
  await click('input[name="envs"][value="prod"]');
  await page.eval(`document.querySelector('input[name="notes"]').value = "after 6pm"`);
  await click('kapi-form button[type="submit"]');
  await until(() => requests("POST", "/answers").length === 1);
  assert.deepEqual(requests("POST", "/answers")[0].body, {
    form: "rollout",
    values: { when: ["later"], envs: ["staging", "prod"], notes: ["after 6pm"] },
  });
  await page.waitFor(`!!document.querySelector(".kapi-msg.kapi-answer")`);
  assert.equal(pageLoads(), 1);
});

test("ignores Escape and a second save while a save is in flight", async () => {
  server.state.opDelay = 300;
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  assert.equal(await page.eval(`document.activeElement.innerText`), "Move the session store to the new cluster in two steps.");
  await page.type(" Then verify.");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  await page.key("Escape");
  await page.key("Enter", { ctrl: true });
  await click('.kapi-bar [data-action="cancel"]');
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  await until(async () => /Then verify\.$/.test(await text("bbbbbbbb")));
  await new Promise((r) => setTimeout(r, 400));
  assert.equal(requests("POST", "/ops").length, 1);
  assert.equal(await notice(), "");
});

test("marks human messages the agent has read with eyes, and keeps them after a reconnect", async () => {
  server.push({ author: "human", kind: "message", text: "first" });
  server.push({ author: "agent", kind: "message", text: "reply" });
  server.push({ author: "human", kind: "message", text: "second" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 3`);
  const eyes = () => page.eval(`[...document.querySelectorAll(".kapi-msg")].map((m) => m.querySelector(".kapi-read")?.textContent || "")`);
  assert.deepEqual(await eyes(), ["", "", ""]);
  server.ack("agent", 2);
  await until(async () => (await eyes())[0] === "\u{1F440}");
  assert.deepEqual(await eyes(), ["\u{1F440}", "", ""]);
  assert.equal(await page.eval(`document.querySelector(".kapi-read").title`), "Seen by agent");
  assert.equal(await page.eval(`document.getElementById("kapi-page").dataset.rev`), "1");

  const streams = requests("GET", "/events").length;
  server.dropStreams();
  await until(() => requests("GET", "/events").length > streams);
  server.ack("agent", 3);
  await until(async () => (await eyes())[2] === "\u{1F440}");
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg").length`), 3);
  assert.equal(requests("GET", "/events").at(-1).headers["last-event-id"], "3");
});

test("a reconnecting page gets the read position at once", async () => {
  server.push({ author: "human", kind: "message", text: "seen before load" });
  server.ack("agent", 1);
  await page.eval(`location.reload()`);
  await page.waitFor(`document.readyState === "complete" && !!document.querySelector(".kapi-msg .kapi-read")`);
});

test("presence stays pinned above the message box while the list scrolls", async () => {
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-panel-head .kapi-dot, .kapi-panel-head .kapi-status").length`), 0);
  server.push({ kind: "presence", text: "watching" });
  for (let i = 0; i < 30; i++) server.push({ author: i % 2 ? "agent" : "human", kind: "message", text: `message ${i}` });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 30`);
  const top = () => page.eval(`document.querySelector(".kapi-presence").getBoundingClientRect().top`);
  const [presence, scroll, composer, text] = await page.eval(`(() => {
    const box = (e) => (({ top, bottom }) => ({ top, bottom }))(e.getBoundingClientRect());
    const p = document.querySelector(".kapi-presence");
    return [box(p), box(document.querySelector(".kapi-scroll")), box(document.querySelector(".kapi-composer")), p.textContent];
  })()`);
  assert.equal(text, "Agent ready");
  assert.ok(presence.top >= scroll.bottom - 1 && presence.bottom <= composer.top + 1, JSON.stringify({ presence, scroll, composer }));
  const before = await top();
  await page.eval(`document.querySelector(".kapi-scroll").scrollTop = 0`);
  assert.equal(await top(), before);
});

test("the down arrow reveals more chat and a sent annotation scrolls to its delivered messages", async () => {
  for (let i = 0; i < 30; i++) server.push({ author: i % 2 ? "agent" : "human", kind: "message", text: `history ${i}` });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 30`);
  await page.eval(`document.querySelector(".kapi-scroll").scrollTop = 0`);
  await page.waitFor(`!document.querySelector(".kapi-scroll-bottom").hidden`);
  assert.equal(await page.eval(`document.querySelector(".kapi-scroll-bottom").getAttribute("aria-label")`), "Go to latest message");

  await click(".kapi-scroll-bottom");
  await page.waitFor(`(s => s.scrollHeight - s.scrollTop - s.clientHeight < 2)(document.querySelector(".kapi-scroll"))`);
  assert.equal(await page.eval(`document.querySelector(".kapi-scroll-bottom").hidden`), true);

  await page.eval(`document.querySelector(".kapi-scroll").scrollTop = 0`);
  await page.waitFor(`!document.querySelector(".kapi-scroll-bottom").hidden`);
  await select("cccccccc");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("please check this");
  await page.key("Enter");
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("sent while reading old messages");
  await page.key("Enter");
  await until(() => requests("POST", "/notes").length === 1);
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 32`);
  await page.waitFor(`(s => s.scrollHeight - s.scrollTop - s.clientHeight < 2)(document.querySelector(".kapi-scroll"))`);
  assert.equal(await page.eval(`document.querySelector(".kapi-scroll-bottom").hidden`), true);
  assert.equal(await page.eval(`document.querySelector(".kapi-msg:last-child .kapi-msg-text").textContent`), "sent while reading old messages");
});

test("renders markup from events as literal text", async () => {
  const evil = `<img src=x onerror="window.pwned=1">`;
  server.push({ author: evil, kind: "message", target: evil, text: evil });
  server.push({ kind: "answer", text: evil });
  server.push({ kind: "revision", target: "", text: evil });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 2`);
  await until(() => pageLoads() === 2);
  assert.equal(await page.eval(`document.querySelectorAll("img").length`), 0);
  assert.equal(await page.eval(`document.querySelector(".kapi-message .kapi-msg-text").textContent`), evil);
  assert.equal(await page.eval(`document.querySelector(".kapi-message .kapi-author").textContent`), evil);
  assert.equal(await page.eval(`document.querySelector(".kapi-message .kapi-ref").title`), `#${evil}`);
  assert.equal(await page.eval(`document.querySelector(".kapi-message .kapi-ref").textContent`), "a block no longer on the page");
  assert.match(await page.eval(`document.querySelector(".kapi-answer .kapi-msg-text").textContent`), /onerror=/);
  assert.equal(await page.eval(`window.pwned`), undefined);
});

test("chrome styles do not reach page content that reuses chrome class names", async () => {
  const leaks = await page.eval(`(() => {
    const names = [...new Set([...document.querySelectorAll("#kapi-chrome [class]")].flatMap((e) => [...e.classList]))];
    const probe = document.createElement("div");
    document.getElementById("kapi-page").append(probe);
    const styles = () => {
      const s = getComputedStyle(probe);
      return JSON.stringify([...s].map((k) => s[k]));
    };
    const plain = styles();
    return names.filter((n) => {
      probe.className = n;
      return styles() !== plain;
    });
  })()`);
  assert.deepEqual(leaks, []);
});

test("refetches other changed blocks while one is being edited", async () => {
  await select("aaaaaaaa");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "aaaaaaaa"`);
  server.bumpBehindBrowser("cccccccc", '<ul data-kapi-id="cccccccc"><li>Agent rewrote this</li></ul>');
  server.push({ kind: "revision", target: "aaaaaaaa,cccccccc", text: "edited" });
  await until(async () => (await text("cccccccc")) === "Agent rewrote this");
  assert.equal(requests("GET", "/blocks/aaaaaaaa").length, 0);
  assert.equal(await page.eval(`document.activeElement.dataset.kapiId`), "aaaaaaaa");
  assert.equal(await page.eval(`document.getElementById("kapi-page").dataset.rev`), "2");
  await page.key("Escape");
  await until(() => requests("GET", "/blocks/aaaaaaaa").length === 1);
});

test("an edit started while its block is being refetched keeps the block it is editing", async () => {
  server.state.blocksDelay = 300;
  server.bumpBehindBrowser("bbbbbbbb", '<p data-kapi-id="bbbbbbbb">Agent rewrote this.</p>');
  server.push({ kind: "revision", target: "bbbbbbbb", text: "edited" });
  await until(() => requests("GET", "/blocks/bbbbbbbb").length === 1);
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  await page.type(" Mine.");
  await new Promise((r) => setTimeout(r, 400));
  assert.equal(await page.eval(`document.activeElement.isConnected && document.activeElement.isContentEditable`), true);
  assert.match(await text("bbbbbbbb"), /Mine\.$/);
  server.state.blocksDelay = 0;
  await page.key("Escape");
  await until(async () => (await text("bbbbbbbb")) === "Agent rewrote this.");
});

test("a save sends the rev from when the edit started, and a stale save keeps the text", async () => {
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  await page.type(" Mine.");
  server.bumpBehindBrowser("bbbbbbbb", '<p data-kapi-id="bbbbbbbb">Other tab wrote this.</p>');
  server.bumpBehindBrowser("cccccccc", '<ul data-kapi-id="cccccccc"><li>Agent rewrote this</li></ul>');
  server.push({ kind: "revision", target: "cccccccc", text: "edited" });
  await until(async () => (await text("cccccccc")) === "Agent rewrote this");

  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.equal(requests("POST", "/ops")[0].body.rev, 1);
  await until(async () => /changed/.test(await notice()));
  assert.equal(server.state.blocks.find((b) => b.id === "bbbbbbbb").html, '<p data-kapi-id="bbbbbbbb">Other tab wrote this.</p>');
  assert.equal(await page.eval(`document.activeElement.dataset.kapiId`), "bbbbbbbb");
  assert.match(await text("bbbbbbbb"), /Mine\.$/);

  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 2);
  assert.equal(requests("POST", "/ops")[1].body.rev, 3);
  await until(async () => /Mine\.<\/p>$/.test(server.state.blocks.find((b) => b.id === "bbbbbbbb").html));
});

test("a stale source save keeps the source text", async () => {
  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  const mine = "Mine.";
  await page.eval(`document.activeElement.value = ${JSON.stringify(mine)}`);
  server.bumpBehindBrowser("jjjjjjjj", '<figure data-kapi-id="jjjjjjjj" data-kapi-preset="code" class="kapi-code"><pre><code>agent()</code></pre></figure>');
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.equal(requests("POST", "/ops")[0].body.rev, 1);
  await until(async () => /changed/.test(await notice()));
  assert.equal(await page.eval(`document.querySelector(".kapi-source")?.value`), mine);
});

test("a whole-page refresh waits until the edit ends", async () => {
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  await page.type(" Mine.");
  const loads = pageLoads();
  const streams = requests("GET", "/events").length;
  server.push({ kind: "revision", target: "", text: "file changed" });
  server.dropStreams();
  await until(() => requests("GET", "/events").length > streams);
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(pageLoads(), loads);
  assert.match(await text("bbbbbbbb"), /Mine\.$/);
  await page.key("Escape");
  await until(() => pageLoads() === loads + 1);
});

test("server error text renders as text in the notice", async () => {
  const evil = `<img src=x onerror="window.pwned=1">`;
  server.state.nextOpError = evil;
  await select("cccccccc");
  await click('.kapi-bar [data-action="archive"]');
  await until(async () => (await notice()) === evil);
  assert.equal(await page.eval(`document.querySelectorAll("#kapi-chrome img").length`), 0);
  assert.equal(await page.eval(`window.pwned`), undefined);
});

test("edits a code block as its inner source in a textarea", async () => {
  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  assert.deepEqual(requests("GET", "/source/jjjjjjjj").map((r) => r.search), ["?part=inner"]);
  assert.equal(await page.eval(`document.activeElement.value`), "if a &lt; b {\n\tswap()\n}");
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 0);
  assert.equal(await page.eval(`getComputedStyle(document.activeElement).fontFamily.includes("mono")`), true);

  await page.eval(`document.activeElement.value = "swap()"`);
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "text", id: "jjjjjjjj", parts: [{ id: "jjjjjjjj", inner: "swap()" }] } });
  await until(async () => (await text("jjjjjjjj")) === "swap()");
  assert.equal(server.state.blocks.find((b) => b.id === "jjjjjjjj").source, '<kapi-code data-kapi-id="jjjjjjjj" lang="go">swap()</kapi-code>');
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-source").length`), 0);
});

test("saving a source edit with no change just closes the editor", async () => {
  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  await page.key("Enter", { ctrl: true });
  await page.waitFor(`!document.querySelector(".kapi-source")`);
  assert.equal(requests("POST", "/ops").length, 0);
});

test("Escape cancels a source edit and shows the block again", async () => {
  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  await page.type("junk");
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-source")`);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="jjjjjjjj"]').checkVisibility()`), true);
  assert.equal(requests("POST", "/ops").length, 0);
});

test("the toolbar does not show the raw block id", async () => {
  await select("cccccccc");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  assert.doesNotMatch(await page.eval(`document.querySelector(".kapi-bar").textContent`), /#/);
});

test("the selection ring wraps the whole element, list markers included", async () => {
  await select("cccccccc");
  await page.waitFor(`!!document.querySelector(".kapi-ring:not([hidden])")`);
  const [ring, box, marker] = await page.eval(`(() => {
    const el = document.querySelector('[data-kapi-id="cccccccc"]');
    const pick = ({ left, right, top, bottom }) => ({ left, right, top, bottom });
    const li = el.querySelector("li").getBoundingClientRect();
    return [pick(document.querySelector(".kapi-ring").getBoundingClientRect()), pick(el.getBoundingClientRect()), li.left - 24];
  })()`);
  assert.ok(ring.left <= box.left && ring.left <= marker, `ring left ${ring.left}, element ${box.left}, marker ${marker}`);
  assert.ok(ring.right >= box.right && ring.top <= box.top && ring.bottom >= box.bottom, JSON.stringify({ ring, box }));
  assert.ok(box.left - ring.left <= 8 && ring.right - box.right <= 8, JSON.stringify({ ring, box }));
});

test("chat is always docked on the right on wide screens, with no hide control", async () => {
  const [r, w, hgt] = await page.eval(`[(({ left, right, top, bottom }) => ({ left, right, top, bottom }))(document.querySelector(".kapi-panel").getBoundingClientRect()), document.documentElement.clientWidth, document.documentElement.clientHeight]`);
  assert.deepEqual(r, { left: w - 340, right: w, top: 0, bottom: hgt });
  assert.equal(await page.eval(`getComputedStyle(document.documentElement).paddingRight`), "340px");
  assert.equal(await page.eval(`document.querySelectorAll('#kapi-chrome [data-action="collapse"], #kapi-chrome [data-action="expand"]').length`), 0);
});

test("on phones chat is a composer bar that expands to 40% and collapses back", async () => {
  const phone = await browser.open(server.url, { width: 390, height: 800 });
  try {
    const box = () => phone.eval(`(({ top, height }) => ({ top, height }))(document.querySelector(".kapi-panel").getBoundingClientRect())`);
    const visible = (sel) => phone.eval(`document.querySelector(${JSON.stringify(sel)}).checkVisibility()`);
    const bar = await box();
    assert.ok(bar.height <= 120, `bar height ${bar.height}`);
    assert.equal(bar.top + bar.height, await phone.eval(`document.documentElement.clientHeight`));
    assert.equal(await visible(".kapi-composer textarea"), true);
    assert.equal(await visible(".kapi-scroll"), false);

    await phone.eval(`document.querySelector(".kapi-composer textarea").focus()`);
    await phone.waitFor(`document.querySelector(".kapi-scroll").checkVisibility()`);
    const open = await box();
    assert.ok(open.height > bar.height && open.height <= 320, `open height ${open.height}`);

    await phone.key("Escape");
    await phone.waitFor(`!document.querySelector(".kapi-scroll").checkVisibility()`);
    assert.deepEqual(await box(), bar);
    assert.deepEqual(phone.errors, []);
  } finally {
    await phone.close();
  }
});

test("Undo renders as a quiet bordered button", async () => {
  const styles = await page.eval(`[...document.querySelectorAll(".kapi-panel-head > button:not(.kapi-menu-button)")].map((b) => {
    const s = getComputedStyle(b);
    return [b.textContent, s.borderTopWidth, s.borderTopStyle];
  })`);
  assert.deepEqual(styles, [["Undo", "1px", "solid"]]);
});

const theme = () => page.eval(`[document.documentElement.dataset.theme, getComputedStyle(document.body).backgroundColor, [...document.querySelectorAll(".kapi-theme button")].filter((b) => b.getAttribute("aria-pressed") === "true").map((b) => b.dataset.themeChoice).join()]`);
const prefer = async (scheme) => {
  await page.call("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: scheme }] });
  await page.waitFor(`matchMedia("(prefers-color-scheme: ${scheme})").matches`);
};
const expectTheme = async (want) => {
  await until(async () => JSON.stringify(await theme()) === JSON.stringify(want)).catch(() => {});
  assert.deepEqual(await theme(), want);
};
const reload = async () => {
  await page.call("Page.reload");
  await page.waitFor(`document.readyState === "complete" && !!document.querySelector(".kapi-theme")`, 5000);
};

test("the theme toggle switches light and dark and remembers the choice across a reload", async () => {
  await prefer("light");
  await expectTheme(["light", "rgb(255, 255, 255)", "system"]);
  await click('.kapi-theme [data-theme-choice="dark"]');
  await expectTheme(["dark", "rgb(0, 0, 0)", "dark"]);
  assert.equal(await page.eval(`localStorage.getItem("kapi-theme")`), "dark");
  await reload();
  await expectTheme(["dark", "rgb(0, 0, 0)", "dark"]);
  await click('.kapi-theme [data-theme-choice="system"]');
  await expectTheme(["light", "rgb(255, 255, 255)", "system"]);
  assert.equal(await page.eval(`localStorage.getItem("kapi-theme")`), null);
});

test("Theme in the menu lists Default and Paper, recolours the page in both modes, and remembers the choice across a reload", async () => {
  const state = () => page.eval(`[document.documentElement.dataset.palette ?? "default", [...document.querySelectorAll(".kapi-menu [role=menuitemradio]")].map((b) => [b.textContent, b.getAttribute("aria-checked")]), getComputedStyle(document.body).backgroundColor]`);
  const expectState = async (want) => {
    await until(async () => JSON.stringify(await state()) === JSON.stringify(want)).catch(() => {});
    assert.deepEqual(await state(), want);
  };
  const pick = async (id) => {
    if (!(await menuOpen())) {
      await click(".kapi-menu-button");
      await until(menuOpen);
    }
    await click(".kapi-menu [data-menu=theme]");
    await page.waitFor(`!document.querySelector(".kapi-menu [data-menu=theme] + .kapi-submenu").hidden`);
    await click(`.kapi-menu [data-palette=${id}]`);
  };
  await prefer("light");
  await expectTheme(["light", "rgb(255, 255, 255)", "system"]);
  await expectState(["default", [["Default", "true"], ["Paper", "false"]], "rgb(255, 255, 255)"]);
  await pick("paper");
  await expectState(["paper", [["Default", "false"], ["Paper", "true"]], "rgb(250, 248, 243)"]);
  assert.equal(await page.eval(`localStorage.getItem("kapi-palette")`), "paper");
  await page.key("Escape");
  await click('.kapi-theme [data-theme-choice="dark"]');
  await expectState(["paper", [["Default", "false"], ["Paper", "true"]], "rgb(22, 25, 31)"]);
  await reload();
  await expectState(["paper", [["Default", "false"], ["Paper", "true"]], "rgb(22, 25, 31)"]);
  await pick("default");
  await expectState(["default", [["Default", "true"], ["Paper", "false"]], "rgb(0, 0, 0)"]);
  assert.equal(await page.eval(`localStorage.getItem("kapi-palette")`), null);
});

test("the Theme submenu opens by keys like Export, and opening one submenu closes the other", async () => {
  await page.eval(`document.querySelector(".kapi-menu-button").focus()`);
  await page.key("Enter");
  await until(menuOpen);
  await pressKey("ArrowDown");
  assert.equal(await focusedItem(), "Theme");
  await pressKey("ArrowRight");
  assert.equal(await focusedItem(), "Default");
  await pressKey("ArrowDown");
  assert.equal(await focusedItem(), "Paper");
  await pressKey("ArrowLeft");
  assert.equal(await focusedItem(), "Theme");
  await click(".kapi-menu [data-menu=theme]");
  await click(".kapi-menu [data-menu=export]");
  assert.deepEqual(await page.eval(`[...document.querySelectorAll(".kapi-submenu")].map((m) => m.hidden)`), [false, true]);
});

test("the theme is set before the first paint, not after the chat panel loads", async () => {
  await page.eval(`localStorage.setItem("kapi-theme", "dark")`);
  await page.call("Page.addScriptToEvaluateOnNewDocument", {
    source: `document.addEventListener("readystatechange", () => { if (!window.firstTheme) window.firstTheme = document.documentElement.dataset.theme || "unset"; }, { once: true });`,
  });
  await reload();
  assert.equal(await page.eval(`window.firstTheme`), "dark");
});

test("System follows the OS colour scheme live, and an explicit choice ignores it", async () => {
  await prefer("light");
  await expectTheme(["light", "rgb(255, 255, 255)", "system"]);
  await prefer("dark");
  await expectTheme(["dark", "rgb(0, 0, 0)", "system"]);
  await click('.kapi-theme [data-theme-choice="light"]');
  await prefer("light");
  await prefer("dark");
  await expectTheme(["light", "rgb(255, 255, 255)", "light"]);
});

test("the theme toggle still works when storage is blocked", async () => {
  await page.call("Page.addScriptToEvaluateOnNewDocument", {
    source: `Object.defineProperty(window, "localStorage", { get() { throw new DOMException("blocked", "SecurityError"); } });`,
  });
  await prefer("light");
  await reload();
  await expectTheme(["light", "rgb(255, 255, 255)", "system"]);
  await click('.kapi-theme [data-theme-choice="dark"]');
  await expectTheme(["dark", "rgb(0, 0, 0)", "dark"]);
});

test("printing stays light when the dark theme is on", async () => {
  await click('.kapi-theme [data-theme-choice="dark"]');
  await expectTheme(["dark", "rgb(0, 0, 0)", "dark"]);
  await page.call("Emulation.setEmulatedMedia", { media: "print" });
  const bg = await page.eval(`getComputedStyle(document.body).backgroundColor`);
  await page.call("Emulation.setEmulatedMedia", { media: "" });
  assert.equal(bg, "rgb(255, 255, 255)");
});

test("printing hides all chrome and the space it reserves", async () => {
  await select("bbbbbbbb");
  await page.call("Emulation.setEmulatedMedia", { media: "print" });
  const shown = await page.eval(`[...document.querySelectorAll("#kapi-chrome, #kapi-chrome *")].filter((e) => e.checkVisibility()).length`);
  const pad = await page.eval(`getComputedStyle(document.documentElement).paddingRight + " " + getComputedStyle(document.documentElement).paddingBottom`);
  await page.call("Emulation.setEmulatedMedia", { media: "" });
  assert.equal(shown, 0);
  assert.equal(pad, "0px 0px");
});

test("every request carries the token prefix and every POST is JSON", async () => {
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "hi"`);
  await click(".kapi-composer .kapi-primary");
  await select("cccccccc");
  await click('.kapi-bar [data-action="archive"]');
  await click('kapi-form button[type="submit"]');
  await until(() => requests("POST", "").length === 3);
  const prefix = new URL(server.url).pathname.split("/p/")[0] + "/";
  assert.deepEqual(server.state.requests.filter((r) => !r.path.startsWith(prefix) && r.path !== "/favicon.ico"), []);
  for (const r of requests("POST", "")) assert.equal(r.headers["content-type"], "application/json");
});

test("every table sorts by a header click, numeric or by locale, without an op", async () => {
  const column = (i) => page.eval(`[...document.querySelectorAll('[data-kapi-id="gggggggg"] tbody tr')].map((r) => r.cells[${i}].textContent)`);
  const sortState = () => page.eval(`[...document.querySelectorAll('[data-kapi-id="gggggggg"] th')].map((th) => th.getAttribute("aria-sort"))`);
  const header = (i) => click(`[data-kapi-id="gggggggg"] th:nth-child(${i + 1})`);

  await header(1);
  assert.deepEqual(await column(1), ["9", "10", "1,200"]);
  assert.deepEqual(await sortState(), [null, "ascending"]);
  await header(1);
  assert.deepEqual(await column(1), ["1,200", "10", "9"]);
  assert.deepEqual(await sortState(), [null, "descending"]);
  await header(0);
  assert.deepEqual(await column(0), ["Alpha", "beta", "gamma"]);
  assert.deepEqual(await sortState(), ["ascending", null]);
  assert.equal(requests("POST", "").length, 0);
});

test("prose that holds a preset edits in place and the preset stays one piece", async () => {
  await select("hhhhhhhh");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "hhhhhhhh"`);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-source").length`), 0);
  assert.equal(requests("GET", "/source/hhhhhhhh").length, 0);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="hhhhhhhh"] .kapi-callout').isContentEditable`), false);
  await page.eval(`(p => { p.focus(); getSelection().selectAllChildren(p); getSelection().collapseToEnd(); })(document.querySelector('[data-kapi-id="hhhhhhhh"] p'))`);
  await page.type(" now");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  const [part] = requests("POST", "/ops")[0].body.op.parts;
  assert.equal(part.id, "hhhhhhhh");
  assert.match(part.inner, /^<p>Before the cutover: now<\/p><aside [^>]*contenteditable="false"[^>]*>/);
});

test("forms whose names shadow DOM properties still select and submit", async () => {
  await page.eval(`document.querySelector('form[name="submit"]').dispatchEvent(new MouseEvent("click", { bubbles: true }))`);
  await page.waitFor(`document.querySelector('[data-kapi-id="iiiiiiii"]').hasAttribute("data-kapi-selected")`);
  await click('input[name="action"][value="ship"]');
  await page.eval(`document.querySelector('input[name="getAttribute"]').value = "ready"`);
  await click('form[name="submit"] button[type="submit"]');
  await until(() => requests("POST", "/answers").length === 1);
  assert.deepEqual(requests("POST", "/answers")[0].body, {
    form: "submit",
    values: { action: ["ship"], getAttribute: ["ready"], closest: ["x"] },
  });
});

const OVERLAPS = `(() => {
  const b = document.querySelector(".kapi-bar").getBoundingClientRect();
  const hit = (r) => r.width && r.height && r.left < b.right && b.left < r.right && r.top < b.bottom && b.top < r.bottom;
  const hits = [];
  const walk = document.createTreeWalker(document.getElementById("kapi-page"), NodeFilter.SHOW_TEXT);
  for (let n; (n = walk.nextNode()); ) {
    if (!n.data.trim()) continue;
    const range = document.createRange();
    range.selectNodeContents(n);
    if ([...range.getClientRects()].some(hit)) hits.push(n.data.trim().slice(0, 24));
  }
  for (const e of document.querySelectorAll("#kapi-page :is(input, img, svg, textarea, button)")) if (hit(e.getBoundingClientRect())) hits.push(e.tagName);
  return hits;
})()`;

const barBox = () => page.eval(`(({ left, right, top, bottom }) => ({ left, right, top, bottom }))(document.querySelector(".kapi-bar").getBoundingClientRect())`);
const boxOf = (id) => page.eval(`(({ left, right, top, bottom }) => ({ left, right, top, bottom }))(document.querySelector('[data-kapi-id="${id}"]').getBoundingClientRect())`);

test("on wide screens the toolbar sits in the margin at the block's top right, and Save and Cancel take its place", async () => {
  await page.call("Emulation.setDeviceMetricsOverride", { width: 1440, height: 800, deviceScaleFactor: 2, mobile: false });
  await page.waitFor(`innerWidth === 1440`);
  await select("bbbbbbbb");
  await page.waitFor(`document.querySelector(".kapi-bar").dataset.place === "outside"`);
  assert.equal(await page.eval(`document.querySelector(".kapi-bar").parentElement.id`), "kapi-chrome");
  const [bar, el] = [await barBox(), await boxOf("bbbbbbbb")];
  assert.ok(bar.left >= el.right && bar.left - el.right <= 12 && Math.abs(bar.top - el.top) <= 1, JSON.stringify({ bar, el }));
  assert.deepEqual(await page.eval(OVERLAPS), []);
  await editSelected();
  await page.waitFor(`!!document.querySelector('.kapi-bar .kapi-primary[data-action="cancel"]')`);
  const editing = await barBox();
  assert.ok(Math.abs(editing.left - bar.left) <= 1 && Math.abs(editing.top - bar.top) <= 1, JSON.stringify({ bar, editing }));
  assert.deepEqual(await page.eval(OVERLAPS), []);
});

test("on a tall block the toolbar stays in view while the block scrolls", async () => {
  await page.call("Emulation.setDeviceMetricsOverride", { width: 1440, height: 800, deviceScaleFactor: 2, mobile: false });
  await page.waitFor(`innerWidth === 1440`);
  await page.eval(`document.querySelector('[data-kapi-id="cccccccc"]').style.paddingBottom = "2400px"`);
  await select("cccccccc");
  await page.waitFor(`document.querySelector(".kapi-bar").dataset.place === "outside"`);
  await page.eval(`scrollTo(0, document.querySelector('[data-kapi-id="cccccccc"]').offsetTop + 900)`);
  await page.waitFor(`(b => b.top >= 0 && b.bottom <= innerHeight)(document.querySelector(".kapi-bar").getBoundingClientRect())`);
  assert.deepEqual(await page.eval(OVERLAPS), []);
  await page.eval(`scrollTo(0, document.body.scrollHeight)`);
  await page.waitFor(`(b => b.parentElement.classList.contains("kapi-panel") || b.getBoundingClientRect().bottom <= document.querySelector('[data-kapi-id="cccccccc"]').getBoundingClientRect().bottom + 1)(document.querySelector(".kapi-bar"))`);
});

test("the toolbar is one row and stays fully in view for a block at the bottom of the window", async () => {
  await page.call("Emulation.setDeviceMetricsOverride", { width: 1440, height: 800, deviceScaleFactor: 2, mobile: false });
  await page.waitFor(`innerWidth === 1440`);
  await page.eval(`document.body.style.paddingBottom = "2000px"`);
  await page.eval(`scrollTo(0, document.querySelector('[data-kapi-id="cccccccc"]').getBoundingClientRect().top + scrollY - innerHeight + 40)`);
  await select("cccccccc");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  const tops = await page.eval(`[...new Set([...document.querySelectorAll(".kapi-bar button")].map((b) => Math.round(b.getBoundingClientRect().top)))]`);
  assert.equal(tops.length, 1, JSON.stringify(tops));
  const bar = await barBox();
  assert.ok(bar.top >= 0 && bar.bottom <= 800, JSON.stringify(bar));
  assert.deepEqual(await page.eval(OVERLAPS), []);
});

test("in a narrow window the toolbar floats at the block's top right edge, and docks when the block's top scrolls away", async () => {
  const narrow = await browser.open(server.url, { width: 900, height: 700 });
  try {
    await narrow.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').click()`);
    await narrow.waitFor(`["above", "below"].includes(document.querySelector(".kapi-bar").dataset.place)`);
    const [bar, el] = await narrow.eval(`[".kapi-bar", '[data-kapi-id="bbbbbbbb"]'].map((q) => (({ left, right, top, bottom }) => ({ left, right, top, bottom }))(document.querySelector(q).getBoundingClientRect()))`);
    assert.ok((bar.bottom <= el.top - 4 || bar.top >= el.bottom + 4) && Math.abs(bar.right - el.right) <= 1, JSON.stringify({ bar, el }));
    assert.deepEqual(await narrow.eval(OVERLAPS), []);
    await narrow.eval(`document.querySelector("[data-kapi-selected]").dispatchEvent(new MouseEvent("dblclick", { bubbles: true }))`);
    await narrow.waitFor(`!!document.querySelector('.kapi-bar .kapi-primary[data-action="cancel"]') && document.querySelector(".kapi-bar").parentElement.id === "kapi-chrome"`);
    assert.deepEqual(await narrow.eval(OVERLAPS), []);
    await narrow.eval(`document.querySelector('.kapi-bar [data-action="cancel"]').click()`);
    await narrow.eval(`document.querySelector('[data-kapi-id="cccccccc"]').style.paddingBottom = "2400px"`);
    await narrow.eval(`document.querySelector('[data-kapi-id="cccccccc"]').click()`);
    await narrow.eval(`scrollTo(0, document.querySelector('[data-kapi-id="cccccccc"]').offsetTop + 900)`);
    await narrow.waitFor(`document.querySelector(".kapi-bar").parentElement.classList.contains("kapi-panel")`);
    assert.deepEqual(await narrow.eval(OVERLAPS), []);
    assert.equal(await narrow.eval(`!document.querySelector(".kapi-bar").hidden && document.querySelector(".kapi-bar").checkVisibility()`), true);
  } finally {
    await narrow.close();
  }
});

test("the block toolbar never covers page content", async () => {
  await select("bbbbbbbb");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  assert.deepEqual(await page.eval(OVERLAPS), []);
  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  assert.deepEqual(await page.eval(OVERLAPS), []);

  const phone = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await phone.eval(`document.querySelector('[data-kapi-id="cccccccc"]').click()`);
    await phone.waitFor(`!document.querySelector(".kapi-bar").hidden`);
    assert.equal(await phone.eval(`document.querySelector(".kapi-bar").parentElement.classList.contains("kapi-panel")`), true);
    await phone.eval(`document.querySelector('.kapi-bar [data-action="edit"]').click()`);
    await phone.waitFor(`!!document.querySelector('.kapi-panel .kapi-bar .kapi-primary[data-action="cancel"]')`);
    await phone.waitFor(`parseFloat(getComputedStyle(document.documentElement).paddingBottom) >= document.querySelector(".kapi-panel").getBoundingClientRect().height`);
  } finally {
    await phone.close();
  }
});

const COMPARE = [
  { id: "aaaaaaaa", html: '<h2 data-kapi-id="aaaaaaaa">Compare</h2>' },
  {
    id: "kkkkkkkk",
    html:
      '<div data-kapi-id="kkkkkkkk" style="display: grid; grid-template-columns: 1fr 1fr; gap: 14px">' +
      '<section data-kapi-id="oooooooo" style="padding: 16px"><h3 style="margin: 0">Server-sent events</h3><p>One long GET per tab.</p></section>' +
      '<section data-kapi-id="qqqqqqqq" style="padding: 16px"><h3 style="margin: 0">WebSocket</h3><p>Two-way, with its own replay.</p></section></div>',
  },
  { id: "bbbbbbbb", html: '<p data-kapi-id="bbbbbbbb">Move the session store to the new cluster in two steps, copying first.</p>' },
  { id: "cccccccc", html: '<ul data-kapi-id="cccccccc"><li>Copy data</li><li>Flip reads</li></ul>' },
];

test("selecting a block never moves the page, and its toolbar covers no block's content, at every desktop width", async () => {
  await reopenWith(COMPARE);
  for (const width of [900, 1100, 1280, 1440, 1920]) {
    await page.call("Emulation.setDeviceMetricsOverride", { width, height: 800, deviceScaleFactor: 1, mobile: false });
    await page.waitFor(`innerWidth === ${width}`);
    for (const id of ["aaaaaaaa", "kkkkkkkk", "oooooooo", "qqqqqqqq", "bbbbbbbb", "cccccccc"]) {
      await page.key("Escape");
      await page.waitFor(`!document.querySelector("[data-kapi-selected]")`);
      const boxes = `JSON.stringify([...document.querySelectorAll("#kapi-page [data-kapi-id]")].map((e) => (({ left, top, width, height }) => [left, top, width, height])(e.getBoundingClientRect())))`;
      const before = await page.eval(boxes);
      await select(id);
      await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
      await page.eval(`new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))`);
      assert.equal(await page.eval(boxes), before, `${id} at ${width}`);
      assert.deepEqual(await page.eval(OVERLAPS), [], `${id} at ${width}`);
    }
  }
  await page.call("Emulation.clearDeviceMetricsOverride");
});

test("block tags name presets and jump to and flash the block", async () => {
  server.push({ kind: "message", target: "ffffffff", text: "Is this window right?" });
  await page.waitFor(`!!document.querySelector(".kapi-ref")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-ref").textContent`), "callout: Copy during the quiet window.");
  await click(".kapi-ref");
  await page.waitFor(`document.querySelector('[data-kapi-id="ffffffff"]').hasAttribute("data-kapi-selected")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-ring").hasAttribute("data-flash")`), true);
  await page.waitFor(`(r => r.top >= 0 && r.bottom <= innerHeight)(document.querySelector('[data-kapi-id="ffffffff"]').getBoundingClientRect())`);
});

test("block excerpts keep words from separate list items apart", async () => {
  server.push({ kind: "message", target: "cccccccc", text: "Order?" });
  await page.waitFor(`!!document.querySelector(".kapi-ref")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-ref").textContent`), "Copy data Flip reads");
});

test("on phones Send sits to the right of the composer on the same row", async () => {
  const phone = await browser.open(server.url, { width: 390, height: 800 });
  const row = `(() => {
    const t = document.querySelector(".kapi-composer textarea").getBoundingClientRect();
    const s = document.querySelector(".kapi-composer .kapi-primary").getBoundingClientRect();
    return { right: s.left >= t.right, sameRow: s.top < t.bottom && t.top < s.bottom, edge: document.documentElement.clientWidth - s.right };
  })()`;
  try {
    assert.deepEqual(await phone.eval(row), { right: true, sameRow: true, edge: 12 });
    await phone.eval(`document.querySelector(".kapi-composer textarea").focus()`);
    await phone.waitFor(`document.querySelector(".kapi-scroll").checkVisibility()`);
    assert.deepEqual(await phone.eval(row), { right: true, sameRow: true, edge: 12 });
  } finally {
    await phone.close();
  }
});

for (const width of [390, 360]) {
  test(`on ${width} px phones the logo and theme toggle stay on screen while chat is collapsed`, async () => {
    const phone = await browser.open(server.url, { width, height: 800 });
    const hit = (sel) => `(() => {
      const r = document.querySelector(${JSON.stringify(sel)}).getBoundingClientRect();
      const at = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
      return r.width > 0 && r.top >= 0 && r.bottom <= innerHeight && r.right <= innerWidth && !!at?.closest(${JSON.stringify(sel)});
    })()`;
    try {
      await phone.eval(`document.getElementById("kapi-page").style.paddingBottom = "3000px"; scrollTo(0, 1500)`);
      await phone.waitFor(`scrollY > 1000`);
      assert.equal(await phone.eval(`document.querySelector(".kapi-panel").classList.contains("kapi-expanded")`), false);
      assert.ok(await phone.eval(`document.querySelector(".kapi-panel").getBoundingClientRect().height`) <= 80);
      assert.equal(await phone.eval(hit(".kapi-site-head .kapi-logo")), true);
      assert.equal(await phone.eval(hit('.kapi-theme [data-theme-choice="dark"]')), true);
      await phone.eval(`document.querySelector('.kapi-theme [data-theme-choice="dark"]').click()`);
      await phone.waitFor(`document.documentElement.dataset.theme === "dark"`);
      assert.deepEqual(phone.errors, []);
    } finally {
      await phone.close();
    }
  });
}

async function mobilePage(width) {
  const phone = await browser.open(server.url, { width, height: 800 });
  await phone.call("Emulation.setDeviceMetricsOverride", { width, height: 800, deviceScaleFactor: 2, mobile: true });
  await phone.call("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 });
  await phone.call("Page.navigate", { url: server.url });
  await phone.waitFor(`document.readyState === "complete" && !!document.querySelector(".kapi-theme") && innerWidth === ${width}`);
  return phone;
}

const tap = async (phone, selector) => {
  const { x, y } = await phone.eval(`(({ left, top, width, height }) => ({ x: left + width / 2, y: top + height / 2 }))(document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect())`);
  await phone.call("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
  await phone.call("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
};

test("tapping the theme toggle on a phone does not expand the chat", async () => {
  const phone = await mobilePage(390);
  try {
    await tap(phone, '.kapi-theme [data-theme-choice="dark"]');
    await phone.waitFor(`document.documentElement.dataset.theme === "dark"`);
    assert.equal(await phone.eval(`document.querySelector(".kapi-panel").classList.contains("kapi-expanded")`), false);
    assert.ok(await phone.eval(`document.querySelector(".kapi-panel").getBoundingClientRect().height`) <= 80);
    await phone.eval(`document.querySelector(".kapi-composer textarea").focus()`);
    await phone.waitFor(`document.querySelector(".kapi-panel").classList.contains("kapi-expanded")`);
  } finally {
    await phone.close();
  }
});

test("on phones anchors and the selection ring stay clear of the top strip", async () => {
  const phone = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await phone.eval(`(() => {
      const s = document.createElement("style");
      s.textContent = ":where(#kapi-page) :where([id]) { scroll-margin-top: 1.5rem; }";
      document.head.prepend(s);
      document.getElementById("kapi-page").style.paddingBottom = "3000px";
      document.querySelector('[data-kapi-id="bbbbbbbb"]').id = "target";
      location.hash = "target";
    })()`);
    await phone.waitFor(`scrollY > 0`);
    const strip = await phone.eval(`document.querySelector(".kapi-site-head").getBoundingClientRect().bottom`);
    assert.ok(await phone.eval(`document.getElementById("target").getBoundingClientRect().top`) >= strip);
    const z = (sel) => phone.eval(`Number(getComputedStyle(document.querySelector(${JSON.stringify(sel)})).zIndex)`);
    assert.ok((await z(".kapi-ring")) < (await z(".kapi-site-head")));
  } finally {
    await phone.close();
  }
});

test("the source editor is labelled and takes the block's place", async () => {
  await page.eval(`document.querySelector('[data-kapi-id="jjjjjjjj"]').style.margin = "20px 0"`);
  const before = await page.eval(`document.querySelector('[data-kapi-id="jjjjjjjj"]').getBoundingClientRect().top + scrollY`);
  await select("jjjjjjjj");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  const [label, top] = await page.eval(`(() => {
    const box = document.querySelector(".kapi-source").closest(".kapi-source-box");
    return [box.querySelector(".kapi-source-label").textContent, box.getBoundingClientRect().top + scrollY];
  })()`);
  assert.equal(label, "Editing code");
  assert.ok(Math.abs(top - before) <= 1, `editor at ${top}, block was at ${before}`);
});

test("block tags stay inside their message bubble and the panel", async () => {
  server.push({ author: "human", kind: "message", target: "bbbbbbbb", text: "Why?" });
  await page.waitFor(`!!document.querySelector(".kapi-ref")`);
  const fit = await page.eval(`(() => {
    const ref = document.querySelector(".kapi-ref").getBoundingClientRect();
    const msg = document.querySelector(".kapi-msg").getBoundingClientRect();
    const panel = document.querySelector(".kapi-panel").getBoundingClientRect();
    return { inBubble: ref.right <= msg.right, bubbleInPanel: msg.right <= panel.right, mono: getComputedStyle(document.querySelector(".kapi-ref")).fontFamily.includes("mono") };
  })()`);
  assert.deepEqual(fit, { inBubble: true, bubbleInPanel: true, mono: false });
});

const restartText = () => page.eval(`(b => b && !b.hidden ? b.textContent : "")(document.querySelector(".kapi-restart"))`);

test("after a daemon restart the stream stops and the reopen command is shown, keeping unsaved text", async () => {
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  await page.type(" Mine.");
  server.state.restarted = "kapi open plan.html";
  server.dropStreams();
  await until(async () => (await restartText()).startsWith("Kapi restarted."));
  assert.equal(await page.eval(`document.querySelector(".kapi-restart code").textContent`), "kapi open plan.html");
  assert.match(await restartText(), /^Kapi restarted\. Run kapi open plan\.html to continue/);
  const streams = requests("GET", "/events").length;
  await new Promise((r) => setTimeout(r, 1500));
  assert.equal(requests("GET", "/events").length, streams);
  assert.match(await text("bbbbbbbb"), /Mine\.$/);
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 1);

  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  await new Promise((r) => setTimeout(r, 200));
  assert.match(await text("bbbbbbbb"), /Mine\.$/);
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 1);

  await page.eval(`window.copied = []; navigator.clipboard.writeText = async (t) => { window.copied.push(t); }`);
  await click(".kapi-restart button");
  assert.deepEqual(await page.eval(`window.copied`), ["kapi open plan.html"]);
  await page.key("Escape");
  await page.key("Escape");
  assert.notEqual(await restartText(), "");
});

test("clicking a code block's language pill copies its code", async () => {
  await page.eval(`(() => {
    const fig = document.querySelector('[data-kapi-id="jjjjjjjj"]');
    const head = document.createElement("figcaption");
    head.className = "kapi-code-head";
    head.innerHTML = '<span class="kapi-code-lang">go</span>';
    fig.prepend(head);
  })()`);
  await page.eval(`window.copied = []; navigator.clipboard.writeText = async (t) => { window.copied.push(t); }`);
  await click('[data-kapi-id="jjjjjjjj"] .kapi-code-lang');
  await until(async () => (await page.eval(`window.copied.length`)) === 1);
  assert.deepEqual(await page.eval(`window.copied`), ["if a < b {\n\tswap()\n}"]);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="jjjjjjjj"] .kapi-code-lang').textContent`), "copied");
  await page.waitFor(`document.querySelector('[data-kapi-id="jjjjjjjj"] .kapi-code-lang').textContent === "go"`, 3000);
});

test("hovering any code block shows a copy button that copies its code, skipping removed diff lines", async () => {
  await page.eval(`window.copied = []; navigator.clipboard.writeText = async (t) => { window.copied.push(t); }`);
  await page.eval(`document.querySelector('[data-kapi-id="jjjjjjjj"] pre').dispatchEvent(new MouseEvent("mouseover", { bubbles: true }))`);
  await page.waitFor(`!document.querySelector(".kapi-copy-code").hidden`);
  const [btn, pre] = await page.eval(`[".kapi-copy-code", '[data-kapi-id="jjjjjjjj"] pre'].map((q) => (({ top, right }) => ({ top, right }))(document.querySelector(q).getBoundingClientRect()))`);
  assert.ok(btn.top >= pre.top && btn.right <= pre.right, JSON.stringify({ btn, pre }));
  await click(".kapi-copy-code");
  await until(async () => (await page.eval(`window.copied.length`)) === 1);
  assert.deepEqual(await page.eval(`window.copied`), ["if a < b {\n\tswap()\n}"]);

  await page.eval(`(() => {
    const pre = document.querySelector('[data-kapi-id="jjjjjjjj"] pre');
    pre.innerHTML = '<code><span class="kapi-line kapi-diff-hunk">@@ -1 +1 @@</span><span class="kapi-line kapi-diff-del"><span class="kapi-diff-sign">-</span><span class="kapi-src">old()</span></span><span class="kapi-line kapi-diff-add"><span class="kapi-diff-sign">+</span><span class="kapi-src">new()</span></span><span class="kapi-line kapi-diff-ctx"><span class="kapi-diff-sign"> </span><span class="kapi-src">done()</span></span></code>';
  })()`);
  await click(".kapi-copy-code");
  await until(async () => (await page.eval(`window.copied.length`)) === 2);
  assert.equal(await page.eval(`window.copied[1]`), "new()\ndone()");
});

test("a POST refused after a restart shows the reopen command as text", async () => {
  const evil = `<img src=x onerror="window.pwned=1">`;
  server.state.restarted = evil;
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "hi"`);
  await click(".kapi-composer .kapi-primary");
  await until(async () => (await restartText()).startsWith("Kapi restarted."));
  assert.equal(await page.eval(`document.querySelector(".kapi-restart code").textContent`), evil);
  assert.equal(await page.eval(`document.querySelectorAll("#kapi-chrome img").length`), 0);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "hi");
  assert.equal(await notice(), "");
});

const REOPENED = "Kapi restarted and reopened this page in a new tab. You can close this one.";
const composerText = () => page.eval(`document.querySelector(".kapi-composer textarea").value`);
const copyable = (selector) =>
  page.eval(`(el => !el.disabled && !el.readOnly && getComputedStyle(el).userSelect !== "none")(document.querySelector(${JSON.stringify(selector)}))`);

test("after a restart that reopened the page, the old tab says so and keeps unsent and unsaved text", async () => {
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "unsent note"`);
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  await page.type(" Mine.");
  server.state.restarted = "kapi open plan.html";
  server.state.reopened = true;
  server.dropStreams();
  await until(async () => (await restartText()) === REOPENED);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-restart code, .kapi-restart button").length`), 0);
  assert.match(await text("bbbbbbbb"), /Mine\.$/);
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 1);
  assert.equal(await composerText(), "unsent note");
  assert.equal(await copyable(".kapi-composer textarea"), true);
  assert.equal(await copyable('[data-kapi-id="bbbbbbbb"]'), true);

  await click(".kapi-composer .kapi-primary");
  await until(() => requests("POST", "/messages").length === 1);
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(await restartText(), REOPENED);
  assert.equal(await notice(), "");
  assert.equal(await composerText(), "unsent note");
  assert.match(await text("bbbbbbbb"), /Mine\.$/);
});

test("a message refused after a restart that reopened the page names the new tab, never HTTP 403", async () => {
  server.state.restarted = "kapi open plan.html";
  server.state.reopened = true;
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "hi"`);
  await click(".kapi-composer .kapi-primary");
  await until(async () => (await restartText()) === REOPENED);
  assert.equal(await notice(), "");
  assert.equal(await composerText(), "hi");
  assert.equal(await copyable(".kapi-composer textarea"), true);
});

test("a message refused after a restart without a reopen shows the command, never HTTP 403", async () => {
  server.state.restarted = "kapi open plan.html";
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "hi"`);
  await click(".kapi-composer .kapi-primary");
  await until(async () => (await restartText()).startsWith("Kapi restarted."));
  assert.match(await restartText(), /^Kapi restarted\. Run kapi open plan\.html to continue\./);
  assert.doesNotMatch(await restartText(), /new tab/);
  assert.equal(await notice(), "");
  assert.equal(await composerText(), "hi");
  assert.equal(await copyable(".kapi-composer textarea"), true);
});


const task = (id, done, label) =>
  `<li class="kapi-task" data-kapi-preset="task" data-kapi-id="${id}"><input type="checkbox"${done ? ' checked=""' : ""} aria-label="${label}">` +
  `<span class="kapi-task-body">${label}</span></li>`;

async function reopenWith(blocks) {
  await page.close();
  await server.close();
  server = await startFakeServer({ blocks });
  page = await browser.open(server.url);
  await page.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
}

const CHECKLIST = [
  {
    id: "llllllll",
    html: `<ul class="kapi-checklist" data-kapi-preset="checklist" data-kapi-id="llllllll">${task("tttttttt", false, "Copy")}${task("uuuuuuuu", true, "Flip")}</ul>`,
  },
];
const checked = (id) => page.eval(`document.querySelector('[data-kapi-id="${id}"] input').checked`);

test("ticking a task sends a check op and refetches only that task", async () => {
  await reopenWith(CHECKLIST);
  const loads = pageLoads();
  await click('[data-kapi-id="tttttttt"] input');
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "check", id: "tttttttt", done: true } });
  await until(() => requests("GET", "/blocks/tttttttt").length === 1);
  assert.equal(await checked("tttttttt"), true);
  await page.waitFor(`!document.querySelector('[data-kapi-id="tttttttt"] input').disabled`);

  await click('[data-kapi-id="uuuuuuuu"] input');
  await until(() => requests("POST", "/ops").length === 2);
  assert.deepEqual(requests("POST", "/ops")[1].body, { rev: 2, op: { kind: "check", id: "uuuuuuuu", done: false } });
  await until(() => requests("GET", "/blocks/uuuuuuuu").length === 1);
  assert.equal(await checked("uuuuuuuu"), false);
  assert.equal(pageLoads(), loads);
});

async function mouseClick(selector, clickCount = 1) {
  const { x, y } = await page.eval(`(r => ({ x: r.x + r.width / 2, y: r.y + r.height / 2 }))(document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect())`);
  for (let n = 1; n <= clickCount; n++) {
    await page.call("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: n });
    await page.call("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: n });
  }
}

test("clicking or double-clicking a task's text selects it and never ticks the box", async () => {
  await reopenWith(CHECKLIST);
  await mouseClick('[data-kapi-id="tttttttt"] .kapi-task-body');
  await page.waitFor(`document.querySelector('[data-kapi-id="tttttttt"]').hasAttribute("data-kapi-selected")`);
  await mouseClick('[data-kapi-id="uuuuuuuu"] .kapi-task-body', 2);
  await page.waitFor(`document.querySelector('[data-kapi-id="uuuuuuuu"]').hasAttribute("data-kapi-selected")`);
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(requests("POST", "/ops").length, 0);
  assert.equal(await checked("tttttttt"), false);
  assert.equal(await checked("uuuuuuuu"), true);
});

test("clicking a badge selects the block around it, never the badge", async () => {
  await reopenWith([
    { id: "pppppppp", html: '<p data-kapi-id="pppppppp">PR #19 is <span class="kapi-badge kapi-tone-green" data-kapi-preset="badge" data-kapi-id="bbbbbbbb">merged</span>.</p>' },
  ]);
  await mouseClick('[data-kapi-id="bbbbbbbb"]');
  await page.waitFor(`document.querySelector('[data-kapi-id="pppppppp"]').hasAttribute("data-kapi-selected")`);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').hasAttribute("data-kapi-selected")`), false);
});

test("a top-level badge is a block of its own and selects on click", async () => {
  await reopenWith([
    { id: "pppppppp", html: '<p data-kapi-id="pppppppp">Prose.</p>' },
    { id: "bbbbbbbb", html: '<span class="kapi-badge kapi-tone-green" data-kapi-preset="badge" data-kapi-id="bbbbbbbb">merged</span>' },
  ]);
  await mouseClick('[data-kapi-id="bbbbbbbb"]');
  await page.waitFor(`document.querySelector('[data-kapi-id="bbbbbbbb"]').hasAttribute("data-kapi-selected")`);
});

test("a box ticked while another op is in flight sends nothing and stays as it was", async () => {
  await reopenWith([...CHECKLIST, { id: "pppppppp", html: '<p data-kapi-id="pppppppp">Prose.</p>' }]);
  server.state.opDelay = 400;
  await select("pppppppp");
  await click('.kapi-bar [data-action="archive"]');
  await until(() => requests("POST", "/ops").length === 1);
  await mouseClick('[data-kapi-id="tttttttt"] input');
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(await page.eval(`document.querySelector('.kapi-bar [data-action="delete"]').disabled`), true);
  await new Promise((r) => setTimeout(r, 600));
  assert.equal(requests("POST", "/ops").length, 1);
  assert.equal(await checked("tttttttt"), false);
});

test("a refused check puts the box back and says why", async () => {
  await reopenWith(CHECKLIST);
  server.state.nextOpError = "check needs a kapi-task";
  await click('[data-kapi-id="tttttttt"] input');
  await until(async () => (await notice()) === "check needs a kapi-task");
  assert.equal(await checked("tttttttt"), false);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="tttttttt"] input').disabled`), false);
});

const DIAGRAMS = [
  { id: "ffffffff", html: '<figure class="kapi-diagram kapi-diagram-flow" data-kapi-preset="diagram" data-kapi-dir="tb" data-kapi-id="ffffffff"><svg width="40" height="40"></svg></figure>' },
  { id: "ssssssss", html: '<figure class="kapi-diagram kapi-diagram-sequence" data-kapi-preset="diagram" data-kapi-id="ssssssss"><svg width="40" height="40"></svg></figure>' },
  { id: "pppppppp", html: '<p data-kapi-id="pppppppp">Prose.</p>' },
];
const hasFlip = () => page.eval(`!!document.querySelector('.kapi-bar [data-action="flip"]')`);

const PAGE_STYLES = await readFile(new URL("../../render/style.css", import.meta.url), "utf8");

async function withPageStyles(width, fn) {
  const p = await browser.open(server.url, { width, height: 800 });
  try {
    await p.eval(`document.head.append(Object.assign(document.createElement("style"), { textContent: ${JSON.stringify(PAGE_STYLES)} }))`);
    await fn(p);
  } finally {
    await p.close();
  }
}

const cellDisplay = (p) => p.eval(`getComputedStyle(document.querySelector('[data-kapi-id="gggggggg"] td')).display`);

test("on desktop a long table's header row sticks to the top of the window", async () => {
  await withPageStyles(1280, async (p) => {
    await p.eval(`(t => { for (let i = 0; i < 60; i++) t.tBodies[0].insertRow().append(...["row" + i, String(i)].map((v) => Object.assign(document.createElement("td"), { textContent: v }))); })(document.querySelector('[data-kapi-id="gggggggg"] table'))`);
    await p.eval(`(t => scrollTo(0, t.getBoundingClientRect().top + scrollY + t.offsetHeight / 2))(document.querySelector('[data-kapi-id="gggggggg"]'))`);
    const [top, band, bg] = await p.eval(`(th => [Math.round(th.getBoundingClientRect().top), getComputedStyle(th).backgroundColor, getComputedStyle(document.body).backgroundColor])(document.querySelector('[data-kapi-id="gggggggg"] thead th'))`);
    assert.equal(top, 0);
    assert.notEqual(band, bg);
    assert.notEqual(band, "rgba(0, 0, 0, 0)");
    assert.equal(await p.eval(`getComputedStyle(document.querySelector(".kapi-site-head")).position`), "static");
  });
});

test("on desktop a table sits on the page surface and tints the whole row under the pointer", async () => {
  await page.eval(`document.head.append(Object.assign(document.createElement("style"), { textContent: ${JSON.stringify(PAGE_STYLES)} }))`);
  const look = () => page.eval(`(t => [getComputedStyle(t).backgroundColor, getComputedStyle(t).boxShadow, [...t.querySelectorAll("tbody tr")[1].cells].map((c) => getComputedStyle(c).backgroundColor)])(document.querySelector('[data-kapi-id="gggggggg"]'))`);
  const [bg, shadow, before] = await look();
  assert.deepEqual([bg, shadow], ["rgba(0, 0, 0, 0)", "none"]);
  const [x, y] = await page.eval(`(r => [r.left + 20, r.top + r.height / 2])((el => (el.scrollIntoView({ block: "center" }), el.getBoundingClientRect()))(document.querySelectorAll('[data-kapi-id="gggggggg"] tbody tr')[1]))`);
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  const [, , after] = await look();
  assert.equal(new Set(after).size, 1, JSON.stringify(after));
  assert.notEqual(after[0], before[0]);
  assert.notEqual(after[0], "rgba(0, 0, 0, 0)");
});

test("tables turn into cards only when the window itself is phone width", async () => {
  await withPageStyles(900, async (p) => assert.equal(await cellDisplay(p), "table-cell"));
  await withPageStyles(390, async (p) => assert.equal(await cellDisplay(p), "block"));
});

test("on a phone, tapping a cell's column label sorts the table by that column", async () => {
  await withPageStyles(390, async (p) => {
    const order = () => p.eval(`[...document.querySelectorAll('[data-kapi-id="gggggggg"] tbody tr')].map((r) => r.cells[0].textContent).join(",")`);
    const tap = async (row, col, where) => {
      await p.eval(`(c => {
        c.scrollIntoView({ block: "center" });
        const r = c.getBoundingClientRect();
        const x = ${where === "label" ? "r.left + 12" : "r.right - 12"};
        const y = r.top + r.height / 2;
        document.elementFromPoint(x, y).dispatchEvent(new MouseEvent("click", { bubbles: true, clientX: x, clientY: y }));
      })(document.querySelectorAll('[data-kapi-id="gggggggg"] tbody tr')[${row}].cells[${col}])`);
    };
    assert.equal(await order(), "beta,Alpha,gamma");
    await tap(0, 1, "value");
    assert.equal(await order(), "beta,Alpha,gamma");
    await tap(0, 1, "label");
    assert.equal(await order(), "Alpha,beta,gamma");
    await tap(0, 1, "label");
    assert.equal(await order(), "gamma,beta,Alpha");
    await tap(1, 0, "label");
    assert.equal(await order(), "Alpha,beta,gamma");
  });
});

const SPANNED = {
  id: "rrrrrrrr",
  html:
    '<div class="kapi-table" data-kapi-preset="table" data-kapi-id="rrrrrrrr"><table>' +
    "<thead><tr><th>Name</th><th>Key</th><th>Value</th></tr></thead><tbody>" +
    '<tr><td rowspan="2">beta</td><td>x</td><td>1</td></tr><tr><td>y</td><td>2</td></tr>' +
    '<tr><th scope="row">alpha</th><td>z</td><td>3</td></tr>' +
    "</tbody></table></div>",
};

test("on a phone a row header reads as bold cell text, not as a column header", async () => {
  await reopenWith([SPANNED]);
  await withPageStyles(390, async (p) => {
    const [bg, transform, arrow, weight] = await p.eval(`(th => (s => [s.backgroundColor, s.textTransform, getComputedStyle(th, "::after").content, Number(s.fontWeight)])(getComputedStyle(th)))(document.querySelector('[data-kapi-id="rrrrrrrr"] tbody th'))`);
    assert.deepEqual([bg, transform, arrow], ["rgba(0, 0, 0, 0)", "none", "none"]);
    assert.ok(weight >= 600, String(weight));
  });
});

test("table body cells carry their column header as a label for the narrow card layout", async () => {
  const labels = () => page.eval(`[...document.querySelectorAll('[data-kapi-id="gggggggg"] tbody tr')].map((r) => [...r.cells].map((c) => c.dataset.kapiLabel ?? null))`);
  assert.deepEqual(await labels(), [["Shard", "Rows"], ["Shard", "Rows"], ["Shard", "Rows"]]);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="gggggggg"] th').hasAttribute("data-kapi-label")`), false);
  await click('[data-kapi-id="gggggggg"] th');
  assert.deepEqual(await labels(), [["Shard", "Rows"], ["Shard", "Rows"], ["Shard", "Rows"]]);

  await reopenWith([SPANNED]);
  assert.deepEqual(await page.eval(`[...document.querySelectorAll('[data-kapi-id="rrrrrrrr"] tbody tr')].map((r) => [...r.cells].map((c) => c.dataset.kapiLabel))`),
    [["Name", "Key", "Value"], ["Key", "Value"], ["Name", "Key", "Value"]]);
});

test("the logo sits at the top left of the page as a site header, not in the chat panel", async () => {
  const [head, page_, inPanel, label] = await page.eval(`(() => {
    const box = (el) => (({ left, top, bottom }) => ({ left, top, bottom }))(el.getBoundingClientRect());
    const head = document.querySelector("body > .kapi-site-head");
    return [head && box(head), box(document.getElementById("kapi-page")), !!document.querySelector(".kapi-panel .kapi-logo"), head && [head.querySelector(".kapi-logo").getAttribute("aria-hidden"), head.querySelector(".kapi-logo").hasAttribute("role"), head.textContent]];
  })()`);
  assert.ok(head, "no site header");
  assert.ok(head.left <= 24 && head.top <= 24 && head.bottom <= page_.top, JSON.stringify({ head, page_ }));
  assert.deepEqual(label, ["true", false, "Kapi"]);
  assert.equal(inPanel, false);
});

test("the docked phone toolbar keeps each icon's label beside it", async () => {
  const phone = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await phone.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').click()`);
    await phone.waitFor(`!document.querySelector(".kapi-bar").hidden`);
    assert.deepEqual(await phone.eval(`[...document.querySelectorAll('.kapi-bar [data-action="archive"], .kapi-bar [data-action="delete"], .kapi-bar [data-action="edit"]')].map((b) => b.innerText.trim())`), ["Edit", "Archive", "Delete"]);
  } finally {
    await phone.close();
  }
});

test("the chat panel sits on its own tinted surface, with agent bubbles on the page white", async () => {
  const [panel, tint, bubble, surface] = await page.eval(`(() => {
    const probe = (v) => { const e = document.createElement("span"); e.style.backgroundColor = v; document.body.append(e); const c = getComputedStyle(e).backgroundColor; e.remove(); return c; };
    const msg = document.createElement("li");
    msg.className = "kapi-msg";
    msg.dataset.author = "agent";
    document.querySelector(".kapi-log").append(msg);
    const bubble = getComputedStyle(msg).backgroundColor;
    msg.remove();
    return [getComputedStyle(document.querySelector(".kapi-panel")).backgroundColor, probe("var(--kapi-panel)"), bubble, probe("var(--kapi-surface)")];
  })()`);
  assert.equal(panel, tint);
  assert.notEqual(tint, await page.eval(`getComputedStyle(document.body).backgroundColor`));
  assert.equal(bubble, surface);
});

test("the selection ring follows the rounded corners of the selected block", async () => {
  await page.eval(`document.querySelector('[data-kapi-id="gggggggg"]').style.borderRadius = "18px"`);
  await select("gggggggg");
  await page.waitFor(`!document.querySelector(".kapi-ring").hidden`);
  assert.equal(await page.eval(`getComputedStyle(document.querySelector(".kapi-ring")).borderRadius`), "18px");
  await page.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').style.borderRadius = "10% 4px"`);
  await select("bbbbbbbb");
  await page.waitFor(`getComputedStyle(document.querySelector(".kapi-ring")).borderRadius === "10% 4px"`);
});

test("the selection ring, the human message and the Send button use the blue accent", async () => {
  await select("bbbbbbbb");
  await page.waitFor(`!document.querySelector(".kapi-ring").hidden`);
  const [blue, solid, ring, send, mine] = await page.eval(`(() => {
    const probe = (v) => {
      const s = document.createElement("span");
      s.style.color = v;
      document.getElementById("kapi-chrome").append(s);
      const c = getComputedStyle(s).color;
      s.remove();
      return c;
    };
    const msg = document.createElement("li");
    msg.className = "kapi-msg";
    msg.dataset.author = "human";
    document.querySelector(".kapi-log").append(msg);
    const mine = getComputedStyle(msg).borderTopColor;
    msg.remove();
    return [probe("var(--kapi-blue-text)"), probe("var(--kapi-blue-solid)"),
      getComputedStyle(document.querySelector(".kapi-ring")).boxShadow, getComputedStyle(document.querySelector(".kapi-composer .kapi-primary")).backgroundColor, mine];
  })()`);
  assert.ok(ring.startsWith(blue), ring);
  assert.equal(send, solid);
  assert.equal(mine, await page.eval(`(() => { const s = document.createElement("span"); s.style.color = "var(--kapi-blue-border)"; document.body.append(s); const c = getComputedStyle(s).color; s.remove(); return c; })()`));
});

test("the floating toolbar shows icons only, each named by an instant tooltip, and Delete is drawn in the theme red", async () => {
  await select("bbbbbbbb");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden && document.querySelector(".kapi-bar").parentElement.id === "kapi-chrome"`);
  const tools = await page.eval(`[...document.querySelectorAll(".kapi-bar button")].map((b) => [b.dataset.action, b.getAttribute("aria-label"), b.hasAttribute("title"), !!b.querySelector("svg"),
    [...b.childNodes].filter((n) => n.nodeType === 3 ? n.data.trim() : n.checkVisibility?.() && n.tagName !== "svg").length])`);
  assert.deepEqual(tools, [
    ["ask", "Ask AI", false, true, 0],
    ["copy", "Copy as Markdown", false, true, 0],
    ["archive", "Archive", false, true, 0],
    ["delete", "Delete", false, true, 0],
  ]);
  const tip = () => page.eval(`(b => getComputedStyle(b, "::after").content)(document.querySelector('.kapi-bar [data-action="delete"]'))`);
  assert.equal(await tip(), "none");
  const [x, y] = await page.eval(`(r => [r.left + r.width / 2, r.top + r.height / 2])(document.querySelector('.kapi-bar [data-action="delete"]').getBoundingClientRect())`);
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  assert.equal(await tip(), `"Delete ${(await page.eval(`/Mac/.test(navigator.platform)`)) ? "\u232b" : "Del"}"`);
  assert.equal(await page.eval(`getComputedStyle(document.querySelector('.kapi-bar [data-action="delete"]'), "::after").transitionDuration`), "0s");
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x: 5, y: 5 });
  await page.eval(`document.querySelector('.kapi-bar [data-action="archive"]').focus()`);
  assert.equal(await page.eval(`(b => b.matches(":focus-visible") ? getComputedStyle(b, "::after").content : "not focus-visible")(document.querySelector('.kapi-bar [data-action="archive"]'))`), '"Archive E"');
  const [del, red, icon] = await page.eval(`(() => {
    const probe = document.createElement("span");
    probe.style.color = "var(--kapi-red-text)";
    document.getElementById("kapi-chrome").append(probe);
    const red = getComputedStyle(probe).color;
    probe.remove();
    const b = document.querySelector('.kapi-bar [data-action="delete"]');
    return [getComputedStyle(b).color, red, getComputedStyle(b.querySelector("svg")).fill];
  })()`);
  assert.equal(del, red);
  assert.equal(icon, red);
});

test("an op fetches its block once, whether its own revision event comes before or after the reply", async () => {
  for (const delay of [0, 300]) {
    const before = requests("GET", "/blocks/cccccccc").length;
    server.state.opDelay = delay;
    await select("cccccccc");
    await click('.kapi-bar [data-action="archive"]');
    await page.waitFor(`document.querySelector('[data-kapi-id="cccccccc"]')?.dataset.kapiState === "archived"`);
    await click('.kapi-bar [data-action="unarchive"]');
    await page.waitFor(`!document.querySelector('[data-kapi-id="cccccccc"]')?.dataset.kapiState`);
    await new Promise((r) => setTimeout(r, 400));
    assert.equal(requests("GET", "/blocks/cccccccc").length - before, 2, `delay ${delay}`);
  }
});

test("an agent edit that lands while an op is in flight is still shown", async () => {
  server.state.opDelay = 300;
  await select("cccccccc");
  await click('.kapi-bar [data-action="archive"]');
  await until(() => requests("POST", "/ops").length === 1);
  server.bumpBehindBrowser("dddddddd", '<p data-kapi-id="dddddddd">The agent changed this.</p>');
  server.push({ kind: "revision", target: "dddddddd", text: "edited" });
  await until(async () => (await text("dddddddd")) === "The agent changed this.");
});

test("Flip on a diagram sends an orient op and refetches the diagram", async () => {
  await reopenWith(DIAGRAMS);
  await select("ffffffff");
  await page.waitFor(`!!document.querySelector('.kapi-bar [data-action="flip"]')`);
  const flip = () => page.eval(`(b => [b.textContent, b.getAttribute("aria-label"), b.dataset.tip, !!b.querySelector("svg")])(document.querySelector('.kapi-bar [data-action="flip"]'))`);
  assert.deepEqual(await flip(), ["", "Flow left to right", "Flow left to right", true]);
  await click('.kapi-bar [data-action="flip"]');
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "orient", id: "ffffffff", dir: "lr" } });
  await until(async () => (await page.eval(`document.querySelector('[data-kapi-id="ffffffff"]').dataset.kapiDir`)) === "lr");
  assert.equal(requests("GET", "/blocks/ffffffff").length, 1);
  await page.waitFor(`document.querySelector('.kapi-bar [data-action="flip"]')?.getAttribute("aria-label") === "Flow top to bottom"`);
  await click('.kapi-bar [data-action="flip"]');
  await until(() => requests("POST", "/ops").length === 2);
  assert.equal(requests("POST", "/ops")[1].body.op.dir, "tb");
});

test("sequence diagrams and prose show no Flip", async () => {
  await reopenWith(DIAGRAMS);
  for (const id of ["ssssssss", "pppppppp"]) {
    await select(id);
    await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
    assert.equal(await hasFlip(), false, id);
  }
});

async function mouseClickAt(x, y) {
  await page.call("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
  await page.call("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
}

test("a real double click edits a block even when its toolbar appears under the pointer", async () => {
  await reopenWith([{ id: "pppppppp", html: '<p data-kapi-id="pppppppp">Prose that the toolbar lands on.</p>' }]);
  for (const width of [1100, 1280, 1920]) {
    await page.call("Emulation.setDeviceMetricsOverride", { width, height: 800, deviceScaleFactor: 1, mobile: false });
    const { x, y } = await page.eval(`(r => ({ x: r.right - 20, y: r.top + 8 }))(document.querySelector('[data-kapi-id="pppppppp"]').getBoundingClientRect())`);
    for (const clickCount of [1, 2]) {
      await page.call("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount });
      await page.call("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount });
    }
    await page.waitFor(`document.activeElement?.dataset.kapiId === "pppppppp" && document.activeElement.isContentEditable`);
    assert.equal(requests("POST", "/ops").length, 0, `width ${width}`);
    await page.key("Escape");
    await page.waitFor(`!document.querySelector("[contenteditable]")`);
    await page.key("Escape");
  }
  await page.call("Emulation.clearDeviceMetricsOverride");
});

test("clicking empty page space clears the selection, and clicking the chat keeps it", async () => {
  await reopenWith([{ id: "pppppppp", html: '<p data-kapi-id="pppppppp">Prose.</p>' }]);
  const selected = () => page.eval(`document.querySelector("[data-kapi-selected]")?.dataset.kapiId ?? null`);
  await mouseClick('[data-kapi-id="pppppppp"]');
  await page.waitFor(`document.querySelector('[data-kapi-id="pppppppp"]').hasAttribute("data-kapi-selected")`);

  await mouseClick(".kapi-panel textarea");
  assert.equal(await selected(), "pppppppp");

  const { x, y } = await page.eval(`(() => {
    const r = document.querySelector('[data-kapi-id="pppppppp"]').getBoundingClientRect();
    return { x: r.x + 10, y: r.bottom + 40 };
  })()`);
  assert.equal(await page.eval(`!!document.elementFromPoint(${x}, ${y}).closest("[data-kapi-id], #kapi-chrome")`), false);
  await mouseClickAt(x, y);
  await page.waitFor(`!document.querySelector("[data-kapi-selected]")`);
});

test("a heading folds everything under it until the next heading of the same or higher level, per viewer", async () => {
  await reopenWith([
    { id: "h1h1h1h1", html: '<h1 data-kapi-id="h1h1h1h1">Gallery</h1>' },
    { id: "p1p1p1p1", html: '<p data-kapi-id="p1p1p1p1">Intro.</p>' },
    { id: "h2aaaaaa", html: '<h2 data-kapi-id="h2aaaaaa">Callouts</h2>' },
    { id: "p2p2p2p2", html: '<p data-kapi-id="p2p2p2p2">A note.</p>' },
    { id: "h2bbbbbb", html: '<h2 data-kapi-id="h2bbbbbb">Tables</h2>' },
    { id: "p3p3p3p3", html: '<p data-kapi-id="p3p3p3p3">A table.</p>' },
  ]);
  const shown = (id) => page.eval(`document.querySelector('[data-kapi-id="${id}"]').checkVisibility()`);
  const toggle = async (id) => {
    const { x, y } = await page.eval(`(({ left, top, height }) => ({ x: left - 12, y: top + height / 2 }))(document.querySelector('[data-kapi-id="${id}"]').getBoundingClientRect())`);
    await mouseClickAt(x, y);
  };
  assert.equal(await page.eval(`getComputedStyle(document.querySelector('[data-kapi-id="h2aaaaaa"]'), "::before").opacity`), "0");
  server.push({ author: "human", kind: "note", target: "p2p2p2p2", quote: "A note.", rev: 1, text: "belongs to Callouts" });
  await page.waitFor(`!!document.querySelector('.kapi-pins .kapi-pin[data-note="1"]')`);

  await toggle("h2aaaaaa");
  assert.equal(await shown("p2p2p2p2"), false);
  await page.waitFor(`!document.querySelector('.kapi-pins .kapi-pin[data-note="1"]')`);
  assert.equal(await shown("h2bbbbbb"), true);
  assert.equal(await shown("p3p3p3p3"), true);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="h2aaaaaa"]').dataset.kapiFolded`), "1 block");
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="h2aaaaaa"]').hasAttribute("data-kapi-selected")`), false);
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x: 5, y: 5 });
  await page.waitFor(`getComputedStyle(document.querySelector('[data-kapi-id="h2aaaaaa"]'), "::before").opacity === "0"`);

  await toggle("h1h1h1h1");
  for (const id of ["p1p1p1p1", "h2aaaaaa", "p2p2p2p2", "h2bbbbbb", "p3p3p3p3"]) assert.equal(await shown(id), false, id);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="h1h1h1h1"]').dataset.kapiFolded`), "5 blocks");

  await page.eval(`location.reload()`);
  await page.waitFor(`!!document.querySelector('[data-kapi-id="h1h1h1h1"][data-kapi-folded]')`);
  assert.equal(await shown("p3p3p3p3"), false);

  await toggle("h1h1h1h1");
  assert.equal(await shown("h2bbbbbb"), true);
  assert.equal(await shown("p2p2p2p2"), false);
  await toggle("h2aaaaaa");
  assert.equal(await shown("p2p2p2p2"), true);
  await page.waitFor(`!!document.querySelector('.kapi-pins .kapi-pin[data-note="1"]')`);
  assert.equal(requests("POST", "/ops").length, 0);
});

test("deleting a folded heading deletes its whole hidden section and Undo restores it", async () => {
  await reopenWith([
    { id: "h2aaaaaa", html: '<h2 data-kapi-id="h2aaaaaa">1. First</h2>' },
    { id: "p2p2p2p2", html: '<p data-kapi-id="p2p2p2p2">One.</p>' },
    { id: "h3cccccc", html: '<h3 data-kapi-id="h3cccccc">Detail</h3>' },
    { id: "p3p3p3p3", html: '<p data-kapi-id="p3p3p3p3">More.</p>' },
    { id: "h2bbbbbb", html: '<h2 data-kapi-id="h2bbbbbb">2. Second</h2>' },
    { id: "p4p4p4p4", html: '<p data-kapi-id="p4p4p4p4">Two.</p>' },
  ]);
  const { x, y } = await page.eval(`(({ left, top, height }) => ({ x: left - 12, y: top + height / 2 }))(document.querySelector('[data-kapi-id="h2aaaaaa"]').getBoundingClientRect())`);
  await mouseClickAt(x, y);
  await page.waitFor(`document.querySelector('[data-kapi-id="h2aaaaaa"]').dataset.kapiFolded === "3 blocks"`);
  await click('[data-kapi-id="h2aaaaaa"]');
  await click('.kapi-bar [data-action="delete"]');
  await page.waitFor(`!document.querySelector('[data-kapi-id="h2aaaaaa"]')`);
  assert.deepEqual(requests("POST", "/ops")[0].body.op, { kind: "delete", id: "h2aaaaaa", section: true });
  for (const id of ["p2p2p2p2", "h3cccccc", "p3p3p3p3"]) assert.equal(await page.eval(`!!document.querySelector('[data-kapi-id="${id}"]')`), false, id);
  assert.equal(await page.eval(`!!document.querySelector('[data-kapi-id="h2bbbbbb"]')`), true);

  await click("#kapi-page .kapi-undo-note button");
  await page.waitFor(`!!document.querySelector('[data-kapi-id="p3p3p3p3"]')`);
  for (const id of ["h2aaaaaa", "p2p2p2p2", "h3cccccc", "p3p3p3p3", "h2bbbbbb", "p4p4p4p4"]) {
    assert.equal(await page.eval(`!!document.querySelector('[data-kapi-id="${id}"]')`), true, id);
  }
});

test("archiving a folded heading archives its section and unarchiving restores it", async () => {
  await reopenWith([
    { id: "h2aaaaaa", html: '<h2 data-kapi-id="h2aaaaaa">1. First</h2>' },
    { id: "p2p2p2p2", html: '<p data-kapi-id="p2p2p2p2">One.</p>' },
    { id: "h3cccccc", html: '<h3 data-kapi-id="h3cccccc">Detail</h3>' },
    { id: "p3p3p3p3", html: '<p data-kapi-id="p3p3p3p3">More.</p>' },
    { id: "oldarchiv", html: '<p data-kapi-id="oldarchiv" data-kapi-state="archived">Already archived.</p>' },
    { id: "h2bbbbbb", html: '<h2 data-kapi-id="h2bbbbbb">2. Second</h2>' },
    { id: "p4p4p4p4", html: '<p data-kapi-id="p4p4p4p4">Two.</p>' },
  ]);
  const { x, y } = await page.eval(`(({ left, top, height }) => ({ x: left - 12, y: top + height / 2 }))(document.querySelector('[data-kapi-id="h2aaaaaa"]').getBoundingClientRect())`);
  await mouseClickAt(x, y);
  await click('[data-kapi-id="h2aaaaaa"]');
  await click('.kapi-bar [data-action="archive"]');
  await page.waitFor(`document.querySelector('[data-kapi-id="p3p3p3p3"]')?.dataset.kapiState === "archived"`);
  assert.deepEqual(requests("POST", "/ops")[0].body.op, { kind: "archive", id: "h2aaaaaa", section: true });
  for (const id of ["h2aaaaaa", "p2p2p2p2", "h3cccccc", "p3p3p3p3"]) {
    assert.equal(await page.eval(`document.querySelector('[data-kapi-id="${id}"]').dataset.kapiState`), "archived", id);
  }
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="oldarchiv"]').dataset.kapiState`), "archived");
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="h2bbbbbb"]').hasAttribute("data-kapi-state")`), false);

  await click('[data-kapi-id="h2aaaaaa"]');
  await click('.kapi-bar [data-action="unarchive"]');
  await page.waitFor(`!document.querySelector('[data-kapi-id="p3p3p3p3"]').hasAttribute("data-kapi-state")`);
  assert.deepEqual(requests("POST", "/ops")[1].body.op, { kind: "unarchive", id: "h2aaaaaa", section: true });
  for (const id of ["h2aaaaaa", "p2p2p2p2", "h3cccccc", "p3p3p3p3"]) {
    assert.equal(await page.eval(`document.querySelector('[data-kapi-id="${id}"]').hasAttribute("data-kapi-state")`), false, id);
  }
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="oldarchiv"]').dataset.kapiState`), "archived");
});

const FOLD_ALL_PAGE = [
  { id: "h1h1h1h1", html: '<h1 data-kapi-id="h1h1h1h1">Gallery</h1>' },
  { id: "p1p1p1p1", html: '<p data-kapi-id="p1p1p1p1">Intro.</p>' },
  { id: "h2aaaaaa", html: '<h2 data-kapi-id="h2aaaaaa">Callouts</h2>' },
  { id: "p2p2p2p2", html: '<p data-kapi-id="p2p2p2p2">A note.</p>' },
  { id: "h2bbbbbb", html: '<h2 data-kapi-id="h2bbbbbb">Tables</h2>' },
  { id: "p3p3p3p3", html: '<p data-kapi-id="p3p3p3p3">A table.</p>' },
  { id: "h3cccccc", html: '<h3 data-kapi-id="h3cccccc">Sub</h3>' },
  { id: "p4p4p4p4", html: '<p data-kapi-id="p4p4p4p4">Sub text.</p>' },
  { id: "h3eeeeee", html: '<h3 data-kapi-id="h3eeeeee">Empty</h3>' },
  { id: "h2dddddd", html: '<h2 data-kapi-id="h2dddddd">Last</h2>' },
];

async function arrowClick(id, modifiers = 0) {
  const { x, y } = await page.eval(`(({ left, top, height }) => ({ x: left - 12, y: top + height / 2 }))(document.querySelector('[data-kapi-id="${id}"]').getBoundingClientRect())`);
  await page.call("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1, modifiers });
  await page.call("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1, modifiers });
}

const foldedIds = () => page.eval(`[...document.querySelectorAll("[data-kapi-folded]")].map((el) => el.dataset.kapiId)`);
const ALT = 1;

test("Option-click on an h2 arrow folds every h2 with content, again unfolds them, and other levels keep their state", async () => {
  await reopenWith(FOLD_ALL_PAGE);
  await arrowClick("h3cccccc");
  await arrowClick("h2bbbbbb", ALT);
  assert.deepEqual(await foldedIds(), ["h2aaaaaa", "h2bbbbbb", "h3cccccc"]);
  await arrowClick("h2aaaaaa", ALT);
  assert.deepEqual(await foldedIds(), ["h3cccccc"]);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="p2p2p2p2"]').checkVisibility()`), true);
  assert.equal(requests("POST", "/ops").length, 0);
});

test("Option-click folds the rest of a level from a mixed state, h3 arrows touch only h3, and the state survives a reload", async () => {
  await reopenWith(FOLD_ALL_PAGE);
  await arrowClick("h2aaaaaa");
  await arrowClick("h2bbbbbb", ALT);
  assert.deepEqual(await foldedIds(), ["h2aaaaaa", "h2bbbbbb"]);
  await page.eval(`location.reload()`);
  await page.waitFor(`!!document.querySelector('[data-kapi-id="h2aaaaaa"][data-kapi-folded]')`);
  assert.deepEqual(await foldedIds(), ["h2aaaaaa", "h2bbbbbb"]);
  await arrowClick("h2bbbbbb", ALT);
  assert.deepEqual(await foldedIds(), []);
  await arrowClick("h3cccccc", ALT);
  assert.deepEqual(await foldedIds(), ["h3cccccc"]);
});

test("Option-click on the only h1 folds it, a plain click folds one heading, and Option-click selects nothing", async () => {
  await reopenWith(FOLD_ALL_PAGE);
  await arrowClick("h1h1h1h1", ALT);
  assert.deepEqual(await foldedIds(), ["h1h1h1h1"]);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="p3p3p3p3"]').checkVisibility()`), false);
  await arrowClick("h1h1h1h1", ALT);
  assert.deepEqual(await foldedIds(), []);
  await arrowClick("h2bbbbbb");
  assert.deepEqual(await foldedIds(), ["h2bbbbbb"]);
  await arrowClick("h2bbbbbb");
  await arrowClick("h2aaaaaa", ALT);
  assert.equal(await page.eval(`!!document.querySelector("[data-kapi-selected]")`), false);
  assert.equal(await page.eval(`document.querySelector(".kapi-bar").hidden`), true);
});

const selectText = (from, fromAt, to, toAt) => page.eval(`(() => {
  const at = (q, n) => {
    const w = document.createTreeWalker(document.querySelector(q), NodeFilter.SHOW_TEXT);
    let node;
    while ((node = w.nextNode()) && n > node.data.length) n -= node.data.length;
    return [node, n];
  };
  const r = document.createRange();
  r.setStart(...at(${JSON.stringify(from)}, ${fromAt}));
  r.setEnd(...at(${JSON.stringify(to)}, ${toAt}));
  const changed = new Promise((done) => document.addEventListener("selectionchange", () => setTimeout(done), { once: true }));
  getSelection().removeAllRanges();
  getSelection().addRange(r);
  return changed;
})()`);
const asking = () => page.eval(`(b => b ? b.querySelector(".kapi-ask-about").textContent : null)(document.querySelector(".kapi-ask"))`);
const queued = () => page.eval(`[...document.querySelectorAll(".kapi-queued-note")].map((n) => [n.querySelector(".kapi-queued-about").textContent, n.querySelector(".kapi-queued-text, textarea").textContent || n.querySelector("textarea")?.value])`);
const barActions = () => page.eval(`document.querySelector(".kapi-bar").hidden ? null : [...document.querySelectorAll(".kapi-bar button")].map((b) => b.dataset.action)`);
const modifier = async () => ((await page.eval(`/Mac/.test(navigator.platform)`)) ? { meta: true } : { ctrl: true });

test("#39 selected text copies with the keyboard as usual, and the toolbar only offers Ask AI", async () => {
  const [x1, x2, y] = await page.eval(`(() => {
    const p = document.querySelector('[data-kapi-id="bbbbbbbb"]').firstChild;
    const r = document.createRange();
    r.setStart(p, 9); r.setEnd(p, 10);
    const a = r.getBoundingClientRect();
    r.setStart(p, 21); r.setEnd(p, 22);
    const b = r.getBoundingClientRect();
    return [a.left + 1, b.right - 1, a.top + a.height / 2];
  })()`);
  await page.call("Input.dispatchMouseEvent", { type: "mousePressed", x: x1, y, button: "left", clickCount: 1 });
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x: x2, y, button: "left", buttons: 1 });
  await page.call("Input.dispatchMouseEvent", { type: "mouseReleased", x: x2, y, button: "left", clickCount: 1 });
  assert.equal(await page.eval(`getSelection().toString()`), "session store");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  assert.deepEqual(await barActions(), ["ask"]);
  const line = await page.eval(`(r => ({ top: r.top, bottom: r.bottom }))([...getSelection().getRangeAt(0).getClientRects()].at(-1))`);
  const box = await barBox();
  assert.ok(box.bottom > line.top - 60 && box.top < line.bottom + 60, "the toolbar stays next to the selected line");
  assert.equal(await page.eval(`(() => {
    const b = document.querySelector(".kapi-bar").getBoundingClientRect();
    const walk = document.createTreeWalker(document.getElementById("kapi-page"), NodeFilter.SHOW_TEXT);
    const r = document.createRange();
    while (walk.nextNode()) {
      if (!walk.currentNode.data.trim()) continue;
      r.selectNodeContents(walk.currentNode);
      for (const t of r.getClientRects()) if (t.left < b.right && b.left < t.right && t.top < b.bottom && b.top < t.bottom) return walk.currentNode.data;
    }
    return "";
  })()`), "", "the toolbar covers no page text");
  await page.eval(`window.copyPrevented = null; addEventListener("copy", (e) => (window.copyPrevented = e.defaultPrevented))`);
  await page.key("c", { ...(await modifier()), commands: ["copy"] });
  assert.equal(await page.eval(`window.copyPrevented`), false);
  assert.equal(await page.eval(`getSelection().toString()`), "session store");
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("v", { ...(await modifier()), commands: ["paste"] });
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "session store");
});

test("#39 notes on a text selection across blocks and on a table cell queue, edit, cancel and go out as one batch", async () => {
  await selectText('[data-kapi-id="bbbbbbbb"]', 45, '[data-kapi-id="dddddddd"]', 3);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  await page.key("a");
  await page.waitFor(`document.activeElement === document.querySelector(".kapi-ask textarea")`);
  assert.equal(await barActions(), null);
  assert.equal(await asking(), "About: “two steps. Copy data Flip reads See”");
  await page.type("first note");
  await page.key("Enter");
  await page.waitFor(`!document.querySelector(".kapi-ask")`);
  assert.deepEqual(await queued(), [["“two steps. Copy data Flip reads See”", "first note"]]);

  await mouseClick('[data-kapi-id="gggggggg"] tbody tr:nth-child(2) td');
  await page.waitFor(`document.querySelector('[data-kapi-id="gggggggg"]').hasAttribute("data-kapi-selected")`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await asking(), "About: row 2, Shard: “Alpha”");
  await page.type("second");
  await page.key("Enter");

  await select("cccccccc");
  await click('.kapi-bar [data-action="ask"]');
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await asking(), "About: Copy data Flip reads");
  await page.type("third");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 3`);
  assert.equal(await page.eval(`document.querySelector(".kapi-queue-head").textContent`), "3 notes queued");

  await click('.kapi-queued-note[data-draft]:nth-of-type(1) [aria-label="Edit note"]');
  await page.waitFor(`document.activeElement === document.querySelector(".kapi-queued-note textarea")`);
  assert.equal(await page.eval(`document.activeElement.value`), "first note");
  assert.ok(await page.eval(`(t => t.getBoundingClientRect().width >= t.parentElement.clientWidth - 20)(document.activeElement)`), "the edit box takes the note's full width");
  await page.eval(`document.activeElement.value = "first note, edited"`);
  await page.key("Enter");
  await page.waitFor(`!document.querySelector(".kapi-queued-note textarea")`);
  await click('.kapi-queued-note:nth-of-type(3) [aria-label="Remove note"]');
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 2`);
  assert.equal(requests("POST", "/notes").length, 0);

  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("and tidy up");
  await page.key("Enter");
  await until(() => requests("POST", "/notes").length === 1);
  assert.deepEqual(requests("POST", "/notes")[0].body, {
    rev: 1,
    notes: [
      { target: "bbbbbbbb,cccccccc,dddddddd", part: "", quote: "two steps. Copy data Flip reads See", prefix: "sion store to the new cluster in", suffix: "the summary before starting.", text: "first note, edited" },
      { target: "gggggggg", part: "row 2, Shard", quote: "Alpha", prefix: "Shard Rows beta 10", suffix: "9 gamma 1,200", text: "second" },
    ],
    text: "and tidy up",
  });
  assert.equal(requests("POST", "/messages").length, 0);
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 3 && document.querySelector(".kapi-queue").hidden`);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "");
  assert.equal(await page.eval(`document.querySelector(".kapi-msg .kapi-msg-quote").textContent`), "two steps. Copy data Flip reads See");
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg")[1].querySelector(".kapi-ref").textContent`), "table: row 2, Shard");
  assert.equal(await page.eval(`sessionStorage.length`), 0);
});

test("#39 Cmd/Ctrl+Enter in a note sends it with everything queued, and Escape cancels and gives the selection back", async () => {
  await select("cccccccc");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("queued first");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);

  await selectText('[data-kapi-id="bbbbbbbb"]', 0, '[data-kapi-id="bbbbbbbb"]', 4);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-ask .kapi-ask-send").disabled`), true);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-ask")`);
  assert.equal(await page.eval(`getSelection().toString()`), "Move");
  assert.equal(await page.eval(`!!document.querySelector('[data-kapi-id="cccccccc"]')`), true);

  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("send now");
  await page.key("Enter", await modifier());
  await until(() => requests("POST", "/notes").length === 1);
  assert.deepEqual(requests("POST", "/notes")[0].body.notes.map((n) => [n.target, n.quote, n.text]), [["cccccccc", "", "queued first"], ["bbbbbbbb", "Move", "send now"]]);
  assert.equal(requests("POST", "/notes")[0].body.text, "");
  await page.waitFor(`document.querySelector(".kapi-queue").hidden && !document.querySelector(".kapi-ask")`);
});

test("#39 queued notes survive a reload and stay queued when sending fails", async () => {
  await select("cccccccc");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("keep me");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
  await page.eval(`location.reload()`);
  await page.waitFor(`document.readyState === "complete" && document.querySelectorAll(".kapi-queued-note").length === 1`, 5000);
  assert.deepEqual(await queued(), [["Copy data Flip reads", "keep me"]]);
  assert.equal(await page.eval(`document.querySelector(".kapi-hint").textContent`), "Enter sends the note");

  assert.deepEqual(await page.eval(`Object.keys(sessionStorage)`), [`kapi-drafts:${new URL(server.url).pathname.split("/").filter(Boolean).pop()}`]);
  server.state.nextNotesError = "disk full";
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "and this"`);
  await click(".kapi-composer .kapi-primary");
  await until(() => requests("POST", "/notes").length === 1);
  await page.waitFor(`/disk full/.test(document.querySelector(".kapi-notice").textContent)`);
  assert.deepEqual(await queued(), [["Copy data Flip reads", "keep me"]]);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "and this");
  server.state.notesDelay = 300;
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("Enter");
  await page.key("Enter");
  await page.waitFor(`document.querySelector(".kapi-queue").hidden`);
  assert.equal(requests("POST", "/notes").length, 2);
});

test("#39 A asks, E archives and Delete deletes only while a block is selected and nothing else has the keys", async () => {
  await page.key("z", await modifier());
  await select("cccccccc");
  await page.key("a", await modifier());
  await page.key("e", { shift: true });
  await page.key("e", { alt: true });
  await page.key("e", { ime: true });
  await page.key("Backspace", { ime: true });
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(await page.eval(`!!document.querySelector(".kapi-ask")`), false);
  assert.equal(requests("POST", "/ops").length, 0);

  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("e");
  await page.key("Backspace");
  await page.eval(`document.querySelector(".kapi-composer textarea").blur()`);
  assert.equal(requests("POST", "/ops").length, 0);

  await selectText('[data-kapi-id="cccccccc"] li', 0, '[data-kapi-id="cccccccc"] li', 4);
  await page.waitFor(`document.querySelector(".kapi-bar").childElementCount === 1`);
  await page.key("e");
  await page.key("Delete");
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(requests("POST", "/ops").length, 0);
  await page.eval(`getSelection().removeAllRanges()`);

  await select("cccccccc");
  await page.key("e");
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body.op, { kind: "archive", id: "cccccccc" });
  await page.waitFor(`document.querySelector('[data-kapi-id="cccccccc"]')?.getAttribute("data-kapi-state") === "archived"`);
  await select("cccccccc");
  await page.key("e");
  await until(() => requests("POST", "/ops").length === 2);
  assert.deepEqual(requests("POST", "/ops")[1].body.op, { kind: "unarchive", id: "cccccccc" });
  await page.waitFor(`!document.querySelector('[data-kapi-id="cccccccc"]')?.hasAttribute("data-kapi-state")`);
  await select("dddddddd");
  await page.key("Backspace");
  await until(() => requests("POST", "/ops").length === 3);
  assert.deepEqual(requests("POST", "/ops")[2].body.op, { kind: "delete", id: "dddddddd" });
  await page.waitFor(`!!document.querySelector("#kapi-page .kapi-undo-note")`);
  await page.key("z", await modifier());
  await until(() => requests("POST", "/ops").length === 4);
  assert.deepEqual(requests("POST", "/ops")[3].body.op, { kind: "undo", id: "" });
  await page.waitFor(`!!document.querySelector('[data-kapi-id="dddddddd"]')`);
  await select("bbbbbbbb");
  await page.key("Delete");
  await until(() => requests("POST", "/ops").length === 5);
  assert.deepEqual(requests("POST", "/ops")[4].body.op, { kind: "delete", id: "bbbbbbbb" });
});

test("#39 a note on code names its lines and a note on a badge names the badge", async () => {
  await reopenWith([
    { id: "pppppppp", html: '<p data-kapi-id="pppppppp">PR #19 is <span class="kapi-badge kapi-tone-green" data-kapi-preset="badge" data-kapi-id="bbbbbbbb">merged</span>.</p>' },
    { id: "jjjjjjjj", html: '<figure class="kapi-code" data-kapi-preset="code" data-kapi-id="jjjjjjjj"><pre><code>if a {\n\tswap()\n}\n</code></pre></figure>' },
  ]);
  await selectText('[data-kapi-id="bbbbbbbb"]', 0, '[data-kapi-id="bbbbbbbb"]', 6);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await asking(), "About: badge: “merged”");
  await page.key("Escape");
  await selectText('[data-kapi-id="jjjjjjjj"] code', 8, '[data-kapi-id="jjjjjjjj"] code', 12);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await asking(), "About: line 2: “swap”");
  await page.key("Escape");
  await selectText('[data-kapi-id="jjjjjjjj"] code', 0, '[data-kapi-id="jjjjjjjj"] code', 12);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.match(await asking(), /^About: lines 1 to 2: /);
});

test("#39 the x on a cell note widens it to the whole table", async () => {
  await mouseClick('[data-kapi-id="gggggggg"] tbody tr:nth-child(1) td');
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask-widen")`);
  await click(".kapi-ask-widen");
  await page.waitFor(`!document.querySelector(".kapi-ask-widen")`);
  assert.equal(await asking(), "About: table: Shard Rows beta 10 Alpha 9 gamma 1,200");
  assert.equal(await page.eval(`document.activeElement === document.querySelector(".kapi-ask textarea")`), true);
  await page.type("whole table");
  await page.key("Enter");
  await click(".kapi-composer .kapi-primary");
  await until(() => requests("POST", "/notes").length === 1);
  assert.deepEqual(requests("POST", "/notes")[0].body.notes[0], { target: "gggggggg", part: "", quote: "", prefix: "", suffix: "", text: "whole table" });
});

test("#39 a real click opens the toolbar beside the pointer, on the clicked line", async () => {
  const { x, y } = await page.eval(`(r => ({ x: r.left + 40, y: r.top + r.height / 2 }))(document.querySelector('[data-kapi-id="bbbbbbbb"]').getBoundingClientRect())`);
  await page.call("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
  await page.call("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
  await page.waitFor(`document.querySelector(".kapi-bar").dataset.place === "point"`);
  const b = await barBox();
  assert.ok(b.left >= x + 8 && b.left <= x + 24, `bar left ${b.left} for a click at ${x}`);
  assert.ok(Math.abs((b.top + b.bottom) / 2 - y) <= 2, `bar centre ${(b.top + b.bottom) / 2} for a click at ${y}`);
});

test("#39 on a phone the docked toolbar offers Ask AI and the note opens at the bottom", async () => {
  const phone = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await phone.call("Emulation.setDeviceMetricsOverride", { width: 390, height: 800, deviceScaleFactor: 2, mobile: true });
    await phone.waitFor(`matchMedia("(max-width: 760px)").matches`);
    await phone.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').click()`);
    await phone.waitFor(`!document.querySelector(".kapi-bar").hidden && document.querySelector(".kapi-bar").parentElement.classList.contains("kapi-panel")`);
    assert.deepEqual(await phone.eval(`[...document.querySelectorAll(".kapi-bar button")].map((b) => b.dataset.action)`), ["ask", "copy", "edit", "archive", "delete"]);
    await phone.eval(`document.querySelector('.kapi-bar [data-action="ask"]').click()`);
    await phone.waitFor(`!!document.querySelector(".kapi-ask")`);
    assert.equal(await phone.eval(`Math.round(document.querySelector(".kapi-ask").getBoundingClientRect().bottom) === innerHeight`), true);
  } finally {
    await phone.close();
  }
});

test("#39 Escape cancels an edit to a queued note and clicking away keeps it", async () => {
  await select("cccccccc");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("original");
  await page.key("Enter");
  await click('.kapi-queued-note [aria-label="Edit note"]');
  await page.waitFor(`document.activeElement === document.querySelector(".kapi-queued-note textarea")`);
  await page.eval(`document.activeElement.value = "changed"`);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-queued-note textarea")`);
  assert.deepEqual(await queued(), [["Copy data Flip reads", "original"]]);
  await click('.kapi-queued-note [aria-label="Edit note"]');
  await page.waitFor(`document.activeElement === document.querySelector(".kapi-queued-note textarea")`);
  await page.eval(`document.activeElement.value = "kept"; document.activeElement.blur()`);
  await page.waitFor(`!document.querySelector(".kapi-queued-note textarea")`);
  assert.deepEqual(await queued(), [["Copy data Flip reads", "kept"]]);
});

test("#39 Escape after asking about a clicked cell leaves no text selected and the toolbar whole", async () => {
  await mouseClick('[data-kapi-id="gggggggg"] tbody tr:nth-child(2) td');
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-ask")`);
  assert.equal(await page.eval(`getSelection().toString()`), "");
  assert.deepEqual(await barActions(), ["ask", "copy", "archive", "delete"]);
});

test("#39 a selection that ends at the start of the next block targets only the blocks with selected text", async () => {
  await page.eval(`(() => {
    const r = document.createRange();
    r.setStart(document.querySelector('[data-kapi-id="bbbbbbbb"]').firstChild, 0);
    r.setEnd(document.querySelector('[data-kapi-id="cccccccc"] li').firstChild, 0);
    getSelection().removeAllRanges();
    getSelection().addRange(r);
  })()`);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden && document.querySelector(".kapi-bar").childElementCount === 1`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("just this");
  await page.key("Enter", await modifier());
  await until(() => requests("POST", "/notes").length === 1);
  assert.equal(requests("POST", "/notes")[0].body.notes[0].target, "bbbbbbbb");
});

test("#39 a cell in a table with no header row is named by its row", async () => {
  await reopenWith([
    { id: "gggggggg", html: '<div class="kapi-table" data-kapi-preset="table" data-kapi-id="gggggggg"><table><tbody><tr><td>Ann</td><td>1</td></tr><tr><td>Bob</td><td>2</td></tr></tbody></table></div>' },
  ]);
  await mouseClick('[data-kapi-id="gggggggg"] tr:nth-child(2) td');
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await asking(), "About: row 2: “Bob”");
});

test("#39 a selection across a block with no text keeps that block in the note", async () => {
  await reopenWith([
    { id: "aaaaaaaa", html: '<p data-kapi-id="aaaaaaaa">Before the picture.</p>' },
    { id: "mmmmmmmm", html: '<figure data-kapi-id="mmmmmmmm"><img alt="" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" width="40" height="40"></figure>' },
    { id: "bbbbbbbb", html: '<p data-kapi-id="bbbbbbbb">After the picture.</p>' },
  ]);
  await selectText('[data-kapi-id="aaaaaaaa"]', 7, '[data-kapi-id="bbbbbbbb"]', 5);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("about the picture");
  await page.key("Enter", await modifier());
  await until(() => requests("POST", "/notes").length === 1);
  assert.equal(requests("POST", "/notes")[0].body.notes[0].target, "aaaaaaaa,mmmmmmmm,bbbbbbbb");
  await page.eval(`(() => {
    const fig = document.querySelector('[data-kapi-id="mmmmmmmm"]');
    const r = document.createRange();
    r.setStart(document.querySelector('[data-kapi-id="aaaaaaaa"]').firstChild, 7);
    r.setEnd(fig, fig.childNodes.length);
    const changed = new Promise((done) => document.addEventListener("selectionchange", () => setTimeout(done), { once: true }));
    getSelection().removeAllRanges();
    getSelection().addRange(r);
    return changed;
  })()`);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden && document.querySelector(".kapi-bar").childElementCount === 1`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("ends in the picture");
  await page.key("Enter", await modifier());
  await until(() => requests("POST", "/notes").length === 2);
  assert.equal(requests("POST", "/notes")[1].body.notes[0].target, "aaaaaaaa,mmmmmmmm");
  await page.waitFor(`!document.querySelector(".kapi-composer .kapi-primary").disabled`);
  await page.eval(`(() => {
    const r = document.createRange();
    r.setStart(document.querySelector('[data-kapi-id="aaaaaaaa"]').firstChild, 0);
    r.setEnd(document.querySelector('[data-kapi-id="mmmmmmmm"]'), 0);
    const changed = new Promise((done) => document.addEventListener("selectionchange", () => setTimeout(done), { once: true }));
    getSelection().removeAllRanges();
    getSelection().addRange(r);
    return changed;
  })()`);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden && document.querySelector(".kapi-bar").childElementCount === 1`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("only the text");
  await page.key("Enter", await modifier());
  await until(() => requests("POST", "/notes").length === 3);
  assert.equal(requests("POST", "/notes")[2].body.notes[0].target, "aaaaaaaa");
});

test("#39 a cancelled pointer never stops the toolbar following the selection", async () => {
  await page.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, pointerType: "touch" }))`);
  await page.eval(`document.dispatchEvent(new PointerEvent("pointercancel", { bubbles: true, pointerType: "touch" }))`);
  await selectText('[data-kapi-id="bbbbbbbb"]', 0, '[data-kapi-id="bbbbbbbb"]', 4);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  assert.deepEqual(await barActions(), ["ask"]);
});

test("#39 a section row inside the body is a row, not a column", async () => {
  await reopenWith([
    { id: "gggggggg", html: '<div class="kapi-table" data-kapi-preset="table" data-kapi-id="gggggggg"><table><thead><tr><th>Name</th><th>Count</th></tr></thead><tbody><tr><th colspan="2">Group A</th></tr><tr><td>Ann</td><td>1</td></tr></tbody></table></div>' },
  ]);
  await mouseClick('[data-kapi-id="gggggggg"] tbody tr:nth-child(1) th');
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await asking(), "About: row 1, Name: “Group A”");
  await page.key("Escape");
  await mouseClick('[data-kapi-id="gggggggg"] tbody tr:nth-child(2) td');
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await asking(), "About: row 2, Name: “Ann”");
});


test("#39 queued notes belong to their tab", async () => {
  await select("cccccccc");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("this tab only");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
  const other = await browser.open(server.url);
  try {
    await other.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
    assert.equal(await other.eval(`document.querySelector(".kapi-queue").hidden`), true);
    await other.eval(`document.querySelector(".kapi-composer textarea").value = "hello"`);
    await other.eval(`document.querySelector(".kapi-composer .kapi-primary").click()`);
    await until(() => requests("POST", "/messages").length === 1);
    assert.equal(requests("POST", "/notes").length, 0);
    assert.deepEqual(await queued(), [["Copy data Flip reads", "this tab only"]]);
  } finally {
    await other.close();
  }
});

test("#39 a note box opened while a send is in flight can queue once the send ends", async () => {
  await select("cccccccc");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("first");
  await page.key("Enter");
  server.state.notesDelay = 300;
  await click(".kapi-composer .kapi-primary");
  await select("bbbbbbbb");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("second");
  assert.equal(await page.eval(`document.querySelector(".kapi-ask .kapi-ask-queue").disabled`), true);
  await page.waitFor(`!document.querySelector(".kapi-ask .kapi-ask-queue").disabled`);
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
  assert.deepEqual(await queued(), [["Move the session store to the new clust…", "second"]]);
});

async function queueOn(tab, id, text) {
  await tab.eval(`document.querySelector('[data-kapi-id="${id}"]').click()`);
  await tab.key("a");
  await tab.waitFor(`!!document.querySelector(".kapi-ask")`);
  await tab.type(text);
  await tab.key("Enter");
  await tab.waitFor(`!document.querySelector(".kapi-ask")`);
}

test("#39 notes queued in a tab kapi restarted move to the page's next tab, once", async () => {
  await queueOn(page, "cccccccc", "carry me");
  server.state.restarted = "kapi open plan.html";
  server.state.reopened = true;
  server.dropStreams();
  await until(async () => (await restartText()) === REOPENED);
  server.state.restarted = "";
  server.state.reopened = false;
  const next = await browser.open(server.url);
  const late = await browser.open(server.url);
  try {
    await next.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
    assert.equal(await next.eval(`document.querySelector(".kapi-queued-note .kapi-queued-text").textContent`), "carry me");
    assert.match(await next.eval(`document.querySelector(".kapi-notice").textContent`), /Brought over a queued note/);
    assert.equal(await late.eval(`document.querySelector(".kapi-queue").hidden`), true);
    assert.equal(await next.eval(`localStorage.getItem(Object.keys(localStorage).find((k) => k.startsWith("kapi-handoff:")) ?? "") === null`), true);
    assert.equal(await page.eval(`document.querySelector(".kapi-queue").hidden`), true);
    await next.eval(`document.querySelector('.kapi-queued-note [aria-label="Remove note"]').click()`);
    await next.waitFor(`document.querySelector(".kapi-queue").hidden`);
    await page.eval(`document.querySelector(".kapi-composer textarea").value = "after the restart"`);
    await click(".kapi-composer .kapi-primary");
    await until(() => requests("POST", "/messages").length === 1);
    await new Promise((r) => setTimeout(r, 200));
    assert.equal(await next.eval(`document.querySelector(".kapi-queue").hidden`), true);
  } finally {
    await next.close();
    await late.close();
  }
});

test("#39 a tab that is already open takes the notes when the restarted tab notices late, after an open edit", async () => {
  const next = await browser.open(server.url);
  try {
    await next.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
    await queueOn(next, "bbbbbbbb", "mine");
    await next.eval(`document.querySelector('.kapi-queued-note [aria-label="Edit note"]').click()`);
    await next.waitFor(`document.activeElement === document.querySelector(".kapi-queued-note textarea")`);
    await next.eval(`document.activeElement.value = "mine, edited"`);
    const key = `kapi-handoff:${new URL(server.url).pathname.split("/").filter(Boolean).pop()}`;
    await page.eval(`localStorage.setItem(${JSON.stringify(key)}, JSON.stringify([{ id: "x1", target: "cccccccc", part: "", quote: "", prefix: "", suffix: "", text: "late carry" }]))`);
    await new Promise((r) => setTimeout(r, 200));
    assert.equal(await next.eval(`document.activeElement === document.querySelector(".kapi-queued-note textarea")`), true);
    await next.key("Enter");
    await next.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 2`);
    assert.deepEqual(await next.eval(`[...document.querySelectorAll(".kapi-queued-text")].map((n) => n.textContent)`), ["mine, edited", "late carry"]);
    assert.equal(await page.eval(`localStorage.getItem(${JSON.stringify(key)})`), null);
    assert.equal(await page.eval(`document.querySelector(".kapi-queue").hidden`), true);
  } finally {
    await next.close();
  }
});

test("#39 a bare picture or divider that a selection crosses stays in the note", async () => {
  await reopenWith([
    { id: "aaaaaaaa", html: '<p data-kapi-id="aaaaaaaa">Before.</p>' },
    { id: "hhhhhhhh", html: '<hr data-kapi-id="hhhhhhhh">' },
    { id: "iiiiiiii", html: '<img data-kapi-id="iiiiiiii" alt="" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" width="40" height="40">' },
    { id: "bbbbbbbb", html: '<p data-kapi-id="bbbbbbbb">After.</p>' },
  ]);
  await selectText('[data-kapi-id="aaaaaaaa"]', 2, '[data-kapi-id="bbbbbbbb"]', 3);
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("all four");
  await page.key("Enter", await modifier());
  await until(() => requests("POST", "/notes").length === 1);
  assert.equal(requests("POST", "/notes")[0].body.notes[0].target, "aaaaaaaa,hhhhhhhh,iiiiiiii,bbbbbbbb");
});

test("#39 a broken handoff is ignored", async () => {
  const key = `kapi-handoff:${new URL(server.url).pathname.split("/").filter(Boolean).pop()}`;
  for (const value of ["{}", "[null, 3]", "not json"]) {
    await page.eval(`localStorage.setItem(${JSON.stringify(key)}, ${JSON.stringify(value)})`);
    const next = await browser.open(server.url);
    try {
      await next.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
      assert.equal(await next.eval(`document.querySelector(".kapi-queue").hidden`), true);
      assert.deepEqual(next.errors, []);
    } finally {
      await next.close();
    }
  }
});

const pinStates = () => page.eval(`[...document.querySelectorAll(".kapi-pins .kapi-pin")].map((p) => p.textContent + ":" + p.dataset.state).join(" ")`);
const highlighted = (name) => page.eval(`[...(CSS.highlights.get(${JSON.stringify(name)}) ?? [])].map((r) => r.toString())`);
const hoverPin = async (n) => {
  const p = await page.eval(`(r => ({ x: r.left + r.width / 2, y: r.top + r.height / 2 }))(document.querySelector('.kapi-pins .kapi-pin[data-note="${n}"]').getBoundingClientRect())`);
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x: p.x, y: p.y });
  await page.waitFor(`!!document.querySelector(".kapi-note-pop")`);
};
const sessionNote = { author: "human", kind: "note", target: "bbbbbbbb", quote: "session store", prefix: "Move the ", suffix: " to the new cluster", rev: 1, text: "why this store?" };

test("#39 every note gets a numbered pin coloured by its state, and only --reply answers it", async () => {
  server.push(sessionNote);
  server.push({ author: "human", kind: "note", target: "gggggggg", part: "row 2, Shard", quote: "Alpha", prefix: "Rows beta 10", suffix: "9 gamma", rev: 1, text: "why so few?" });
  await until(async () => (await pinStates()) === "1:waiting 2:waiting");
  assert.deepEqual(await highlighted("kapi-note-waiting"), ["session store", "Alpha"]);
  const [pin, quote] = await page.eval(`(() => {
    const r = [...CSS.highlights.get("kapi-note-waiting")][0].getClientRects();
    const q = r[r.length - 1];
    const p = document.querySelector('.kapi-pins .kapi-pin[data-note="1"]').getBoundingClientRect();
    return [{ left: p.left, top: p.top }, { right: q.right, top: q.top }];
  })()`);
  assert.ok(Math.abs(pin.left - quote.right) <= 3 && pin.top < quote.top, "the pin sits raised right after the quote");
  assert.equal(await page.eval(`(() => { const r = document.createRange(); r.selectNodeContents(document.querySelector('[data-kapi-id="bbbbbbbb"]')); return r.toString(); })()`),
    "Move the session store to the new cluster in two steps.");

  server.push({ author: "agent", kind: "message", text: "looking" });
  server.push({ author: "human", kind: "revision", target: "bbbbbbbb", rev: 2, text: "edited" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 3`);
  assert.equal(await pinStates(), "1:waiting 2:waiting");
  server.push({ author: "agent", kind: "message", re: [1], text: "it is the only one" });
  await until(async () => (await pinStates()) === "1:answered 2:waiting");
  assert.deepEqual(await highlighted("kapi-note-answered"), ["session store"]);
  server.push({ author: "human", kind: "note", target: "bbbbbbbb", quote: "session store", re: [1], text: "and later?" });
  await until(async () => (await pinStates()) === "1:waiting 2:waiting");
});

test("#39 note pins still open while a block is being edited, and clicking one cancels the edit", async () => {
  server.push(sessionNote);
  await until(async () => (await pinStates()) === "1:waiting");
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.activeElement?.dataset.kapiId === "bbbbbbbb"`);
  await page.type(" discarded");
  await hoverPin(1);
  assert.equal(await page.eval(`!!document.querySelector("[contenteditable]")`), true, "hovering shows the thread without discarding the edit");
  await page.eval(`(() => {
    const pin = document.querySelector('.kapi-pins .kapi-pin[data-note="1"]');
    pin.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, pointerType: "mouse" }));
    pin.click();
  })()`);
  await page.waitFor(`!document.querySelector("[contenteditable], .kapi-source")`);
  assert.equal(await page.eval(`!!document.querySelector(".kapi-note-pop")`), true);
  assert.equal(await text("bbbbbbbb"), "Move the session store to the new cluster in two steps.");
});

test("#39 note threads use the chat colours to distinguish the human and agent", async () => {
  server.push(sessionNote);
  server.push({ author: "agent", kind: "message", re: [1], text: "because it is shared" });
  await until(async () => (await pinStates()) === "1:answered");
  await hoverPin(1);
  const colours = await page.eval(`(() => {
    const colour = (el) => { const s = getComputedStyle(el); return [s.backgroundColor, s.borderColor]; };
    return {
      chatHuman: colour(document.querySelector('.kapi-msg[data-author="human"]')),
      chatAgent: colour(document.querySelector('.kapi-msg[data-author="agent"]')),
      popHuman: colour(document.querySelector('.kapi-pop-msg[data-author="human"]')),
      popAgent: colour(document.querySelector('.kapi-pop-msg[data-author="agent"]')),
    };
  })()`);
  assert.deepEqual(colours.popHuman, colours.chatHuman);
  assert.deepEqual(colours.popAgent, colours.chatAgent);
  assert.notDeepEqual(colours.popHuman, colours.popAgent);
});

test("the Ask AI action uses the Android logo", async () => {
  await click('[data-kapi-id="bbbbbbbb"]');
  const path = await page.eval(`document.querySelector('.kapi-bar [data-action="ask"] svg path').getAttribute("d")`);
  assert.ok(path.startsWith("M176,148a12,12"));
});

test("#39 a tall note popover stays inside a short page and scrolls to Reply and Resolve", async () => {
  await reopenWith([{ id: "bbbbbbbb", html: '<p data-kapi-id="bbbbbbbb">Short session store.</p>' }]);
  server.push({ author: "human", kind: "note", target: "bbbbbbbb", quote: "session store", prefix: "Short ", suffix: ".", rev: 1, text: "explain this" });
  for (let i = 0; i < 8; i++) {
    server.push({ author: "agent", kind: "message", re: [1], text: `Reply ${i + 1}: ${"A detailed explanation that makes the thread taller. ".repeat(4)}` });
  }
  await until(async () => (await pinStates()) === "1:answered");
  await hoverPin(1);
  const bounds = await page.eval(`(() => {
    const p = document.querySelector(".kapi-note-pop");
    const r = p.getBoundingClientRect();
    return { top: r.top, bottom: r.bottom, viewport: innerHeight, client: p.clientHeight, scroll: p.scrollHeight };
  })()`);
  assert.ok(bounds.top >= 8, `popover top ${bounds.top} must stay in the viewport`);
  assert.ok(bounds.bottom <= bounds.viewport - 8, `popover bottom ${bounds.bottom} must stay in the viewport`);
  assert.ok(bounds.scroll > bounds.client, "the long thread scrolls inside the popover");
  await page.eval(`document.querySelector(".kapi-note-pop").scrollTop = document.querySelector(".kapi-note-pop").scrollHeight`);
  assert.equal(await page.eval(`(() => {
    const pop = document.querySelector(".kapi-note-pop").getBoundingClientRect();
    const resolve = [...document.querySelectorAll(".kapi-note-pop button")].find((b) => b.textContent === "Resolve").getBoundingClientRect();
    return resolve.top >= pop.top && resolve.bottom <= pop.bottom;
  })()`), true, "Resolve is reachable at the bottom of the internal scroll");
});

test("#39 an agent edit marks its note changed with Before and Now, and Resolve clears the marks", async () => {
  server.push({ ...sessionNote, quote: "two steps", prefix: "new cluster in ", suffix: "." });
  await until(async () => (await pinStates()) === "1:waiting");
  server.agentEdit("bbbbbbbb", '<p data-kapi-id="bbbbbbbb">Move the session store to the new cluster in three careful steps.</p>');
  await until(async () => (await pinStates()) === "1:changed");
  await until(async () => (await highlighted("kapi-changed")).join() === "three careful");
  assert.ok(requests("GET", "/blocks/bbbbbbbb").some((r) => r.search === "?rev=1"), "Before comes from the note's revision");
  await hoverPin(1);
  await until(async () => (await page.eval(`document.querySelector(".kapi-note-pop").innerText`)).includes("three careful steps."));
  const pop = await page.eval(`document.querySelector(".kapi-note-pop").innerText`);
  assert.match(pop, /Before\s+… the new cluster in two steps\./i);
  assert.match(pop, /Now\s+… the new cluster in three careful steps\./i);
  assert.match(pop, /agent edited/);
  await page.eval(`[...document.querySelectorAll(".kapi-note-pop button")].find((b) => b.textContent === "Resolve").click()`);
  await until(() => requests("POST", "/resolve").length === 1);
  assert.deepEqual(requests("POST", "/resolve")[0].body, { note: 1 });
  await until(async () => (await pinStates()) === "1:resolved");
  assert.deepEqual(await highlighted("kapi-changed"), []);
  assert.deepEqual(await highlighted("kapi-note-changed"), []);
  assert.equal(await page.eval(`!!document.querySelector(".kapi-note-pop")`), false);
});

test("a note can be deleted from its popover", async () => {
  server.push(sessionNote);
  await until(async () => (await pinStates()) === "1:waiting");
  await hoverPin(1);
  assert.deepEqual(await page.eval(`[...document.querySelectorAll(".kapi-note-pop button")].map((b) => b.textContent)`), ["Reply", "Resolve", "Delete"]);
  assert.equal(await page.eval(`[...document.querySelectorAll(".kapi-note-pop button")].find((b) => b.textContent === "Delete").classList.contains("kapi-danger")`), true);
  await page.eval(`window.confirm = () => true; [...document.querySelectorAll(".kapi-note-pop button")].find((b) => b.textContent === "Delete").click()`);
  await until(() => requests("POST", "/delete-notes").length === 1);
  assert.deepEqual(requests("POST", "/delete-notes")[0].body, { notes: [1] });
  await until(async () => (await pinStates()) === "");
  assert.equal(await page.eval(`document.querySelector(".kapi-tabs").hidden`), true);
  assert.equal(await page.eval(`!!document.querySelector(".kapi-note-pop")`), false);
});

test("Delete all removes open and resolved notes from the Notes section", async () => {
  server.push(sessionNote);
  server.push({ author: "human", kind: "note", target: "cccccccc", quote: "Flip reads", rev: 1, text: "second" });
  server.push({ author: "human", kind: "resolve", re: [2] });
  await until(async () => (await pinStates()) === "1:waiting 2:resolved");
  await page.eval(`document.querySelectorAll(".kapi-tabs button")[1].click()`);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-note-item").length`), 2);
  assert.equal(await page.eval(`document.querySelector(".kapi-notes-delete-all").classList.contains("kapi-danger")`), true);
  await page.eval(`window.confirm = () => true; document.querySelector(".kapi-notes-delete-all").click()`);
  await until(() => requests("POST", "/delete-notes").length === 1);
  assert.deepEqual(requests("POST", "/delete-notes")[0].body, { notes: [1, 2] });
  await until(async () => await page.eval(`document.querySelector(".kapi-tabs").hidden`));
  assert.equal(await page.eval(`document.querySelector(".kapi-panel").hasAttribute("data-notes")`), false);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-note-item").length`), 0);
});

test("#39 Reply on a note queues a reply that names its note, and the chat links replies to their pin", async () => {
  server.push(sessionNote);
  server.push({ author: "agent", kind: "message", re: [1], text: "because it is shared" });
  await page.waitFor(`!!document.querySelector('.kapi-msg .kapi-note-ref[data-note="1"]')`);
  assert.deepEqual(await page.eval(`[...document.querySelectorAll(".kapi-msg .kapi-note-ref")].map((b) => b.textContent)`), ["Note 1", "on note 1"]);
  await page.eval(`document.querySelectorAll(".kapi-msg .kapi-note-ref")[1].click()`);
  await page.waitFor(`!!document.querySelector(".kapi-note-pop") && document.querySelector('[data-kapi-id="bbbbbbbb"]').hasAttribute("data-kapi-note-flash")`, 3000);
  await page.eval(`[...document.querySelectorAll(".kapi-note-pop button")].find((b) => b.textContent === "Reply").click()`);
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-ask-about").textContent`), "About: Reply to note 1");
  assert.equal(await page.eval(`!!document.querySelector(".kapi-ask-widen")`), false);
  await page.type("is it shared by both tracks?");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
  assert.equal(await page.eval(`document.querySelector(".kapi-queued-about").textContent`), "Reply to note 1");
  await click('button.kapi-primary[type="submit"]');
  await until(() => requests("POST", "/notes").length === 1);
  const sent = requests("POST", "/notes")[0].body.notes[0];
  assert.deepEqual(sent.re, [1]);
  assert.equal(sent.text, "is it shared by both tracks?");
  await until(async () => (await pinStates()) === "1:waiting");
});

test("#39 the Notes list keeps open notes in page order and resolved ones apart, and J and K step through them", async () => {
  server.push({ author: "human", kind: "note", target: "dddddddd", quote: "before starting", rev: 1, text: "third on the page" });
  server.push(sessionNote);
  server.push({ author: "human", kind: "note", target: "cccccccc", quote: "Flip reads", rev: 1, text: "resolved one" });
  server.push({ author: "human", kind: "resolve", re: [3] });
  await until(async () => (await pinStates()) === "1:waiting 2:waiting 3:resolved");
  assert.equal(await page.eval(`document.querySelector(".kapi-tabs").hidden`), false);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-tabs button")[1].textContent`), "Notes 2");
  await page.eval(`document.querySelectorAll(".kapi-tabs button")[1].click()`);
  assert.equal(await page.eval(`getComputedStyle(document.querySelector(".kapi-notes")).display !== "none" && getComputedStyle(document.querySelector(".kapi-chat")).display === "none"`), true);
  assert.deepEqual(await page.eval(`[...document.querySelectorAll(".kapi-note-list")].map((l) => [...l.querySelectorAll(".kapi-note-item")].map((i) => i.dataset.note))`), [["2", "1"], ["3"]]);
  assert.equal(await page.eval(`document.querySelector('.kapi-note-item[data-note="2"] .kapi-note-crumb').textContent`), "Migration plan");
  assert.equal(await page.eval(`document.querySelector(".kapi-notes-summary").textContent`), "2 open · 0 changed");

  await page.eval(`document.activeElement.blur()`);
  await page.key("j");
  await page.waitFor(`document.querySelector(".kapi-note-pop")?.getAttribute("aria-label") === "Note 2"`, 3000);
  await page.key("j");
  await page.waitFor(`document.querySelector(".kapi-note-pop")?.getAttribute("aria-label") === "Note 1"`, 3000);
  await page.key("k");
  await page.waitFor(`document.querySelector(".kapi-note-pop")?.getAttribute("aria-label") === "Note 2"`, 3000);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-note-pop")`);
  await page.eval(`(t => { t.focus(); t.addEventListener("keydown", (e) => setTimeout(() => (window.jPrevented = e.defaultPrevented))); })(document.querySelector(".kapi-composer textarea"))`);
  await page.key("j");
  await page.waitFor(`window.jPrevented !== undefined`);
  assert.equal(await page.eval(`window.jPrevented`), false);
  assert.equal(await page.eval(`!!document.querySelector(".kapi-note-pop")`), false);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-rail .kapi-rail-dot").length`), 2);
});

test("#39 on a phone, a strip counts the notes and opens them in a sheet, and Next change jumps to a changed note", async () => {
  server.push(sessionNote);
  server.push({ author: "human", kind: "note", target: "dddddddd", quote: "before starting", rev: 1, text: "second" });
  server.agentEdit("dddddddd", '<p data-kapi-id="dddddddd">See <a href="#bbbbbbbb">the summary</a> before you start.</p>');
  const phone = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await phone.waitFor(`!document.querySelector(".kapi-notes-strip").hidden`);
    assert.equal(await phone.eval(`document.querySelector(".kapi-strip-summary").textContent`), "2 notes · 1 changed");
    assert.equal(await phone.eval(`getComputedStyle(document.querySelector(".kapi-tabs")).display`), "none");
    assert.equal(await phone.eval(`getComputedStyle(document.querySelector(".kapi-notes")).display`), "none");
    await phone.eval(`[...document.querySelectorAll(".kapi-notes-strip button")].find((b) => b.textContent === "Notes").click()`);
    await phone.waitFor(`getComputedStyle(document.querySelector(".kapi-notes")).position === "fixed"`);
    assert.ok(await phone.eval(`(r => r.left >= 0 && r.right <= innerWidth && r.bottom <= innerHeight + 1)(document.querySelector(".kapi-notes").getBoundingClientRect())`));
    await phone.eval(`[...document.querySelectorAll(".kapi-notes .kapi-btn")].find((b) => b.textContent === "Next change").click()`);
    await phone.waitFor(`document.querySelector(".kapi-note-pop")?.getAttribute("aria-label") === "Note 2"`, 3000);
    assert.equal(await phone.eval(`getComputedStyle(document.querySelector(".kapi-notes")).display`), "none");
    assert.ok(await phone.eval(`(r => r.left >= 0 && r.right <= innerWidth)(document.querySelector(".kapi-note-pop").getBoundingClientRect())`));
    assert.deepEqual(phone.errors, []);
  } finally {
    await phone.close();
  }
});

test("#39 an answer to a reply answers its note", async () => {
  server.push(sessionNote);
  server.push({ author: "human", kind: "note", target: "bbbbbbbb", quote: "session store", re: [1], text: "and later?" });
  server.push({ author: "agent", kind: "message", re: [2], text: "later too" });
  await until(async () => (await pinStates()) === "1:answered");
  assert.deepEqual(await page.eval(`[...document.querySelectorAll(".kapi-msg")].at(-1).querySelector(".kapi-note-ref").textContent`), "on note 1");
});

test("#39 editing a changed note's block by hand keeps the pins working", async () => {
  server.push({ ...sessionNote, quote: "two steps", prefix: "new cluster in ", suffix: "." });
  server.agentEdit("bbbbbbbb", '<p data-kapi-id="bbbbbbbb">Move the session store to the new cluster in three careful steps.</p>');
  await until(async () => (await highlighted("kapi-changed")).join() === "three careful");
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.querySelector('[data-kapi-id="bbbbbbbb"]').isContentEditable`);
  await page.eval(`(p => { p.textContent = "Short."; p.dispatchEvent(new InputEvent("input", { bubbles: true })); })(document.querySelector('[data-kapi-id="bbbbbbbb"]'))`);
  server.push({ author: "agent", kind: "message", re: [1], text: "done" });
  await until(async () => (await pinStates()) === "1:answered");
  assert.deepEqual(await highlighted("kapi-changed"), ["Short."]);
});

test("#39 a quote cut at the length limit still finds its text", async () => {
  const words = Array.from({ length: 400 }, (_, i) => `w${i % 10}`).join(" ");
  const long = await startFakeServer({ blocks: [...SAMPLE, { id: "llllllll", html: `<p data-kapi-id="llllllll">${words}</p>` }] });
  const tab = await browser.open(long.url);
  try {
    const quote = `${words.slice(0, 999).trimEnd()}\u2026`;
    assert.equal(quote.length, 999, "the cut ends on a space, so clip drops it");
    long.push({ author: "human", kind: "note", target: "llllllll", quote, rev: 1, text: "too long?" });
    await tab.waitFor(`[...(CSS.highlights.get("kapi-note-waiting") ?? [])].length === 1`);
    assert.equal(await tab.eval(`[...CSS.highlights.get("kapi-note-waiting")][0].toString().length`), 998);
    assert.deepEqual(tab.errors, []);
  } finally {
    await tab.close();
    await long.close();
  }
});

async function withPage(blocks, fn) {
  const own = await startFakeServer({ blocks: [...SAMPLE, ...blocks] });
  const tab = await browser.open(own.url);
  try {
    await fn(own, tab);
    assert.deepEqual(tab.errors, []);
  } finally {
    await tab.close();
    await own.close();
  }
}

test("#39 a file edit that adds or removes blocks still marks the notes on the blocks it changed", async () => {
  server.push(sessionNote);
  server.push({ author: "human", kind: "note", target: "dddddddd", quote: "before starting", rev: 1, text: "keep?" });
  await until(async () => (await pinStates()) === "1:waiting 2:waiting");
  server.agentEdit("bbbbbbbb", '<p data-kapi-id="bbbbbbbb">Move the session store to the new cluster in one step.</p>', { author: "file", target: "", changed: "bbbbbbbb" });
  await until(async () => (await pinStates()) === "1:changed 2:waiting");
  await until(async () => (await highlighted("kapi-changed")).join() === "one step.");
  await hoverPin(1);
  assert.match(await page.eval(`document.querySelector(".kapi-note-pop").innerText`), /Edited in the file/);
  server.agentEdit("dddddddd", '<p data-kapi-id="dddddddd">See <a href="#bbbbbbbb">the summary</a> before starting, twice.</p>', { author: "file", target: "", changed: "dddddddd" });
  await until(async () => (await pinStates()) === "1:changed 2:changed");
});

test("#39 a revision on the outer block marks a note on a block inside it", async () => {
  await withPage([{ id: "pppppppp", html: '<div data-kapi-id="pppppppp"><p data-kapi-id="qqqqqqqq">Inner words here.</p></div>' }], async (own, tab) => {
    own.push({ author: "human", kind: "note", target: "qqqqqqqq", quote: "words", rev: 1, text: "inner?" });
    await tab.waitFor(`document.querySelector(".kapi-pins .kapi-pin")?.dataset.state === "waiting"`);
    own.agentEdit("pppppppp", '<div data-kapi-id="pppppppp"><p data-kapi-id="qqqqqqqq">Inner words moved here.</p></div>');
    await tab.waitFor(`document.querySelector(".kapi-pins .kapi-pin")?.dataset.state === "changed"`);
  });
});

test("#39 a quote that appears twice anchors where its surrounding text matches", async () => {
  await withPage([{ id: "rrrrrrrr", html: '<p data-kapi-id="rrrrrrrr">one go, two go, three go</p>' }], async (own, tab) => {
    own.push({ author: "human", kind: "note", target: "rrrrrrrr", quote: "go", prefix: "one go, two ", suffix: ", three go", rev: 1, text: "this one" });
    await tab.waitFor(`[...(CSS.highlights.get("kapi-note-waiting") ?? [])].length === 1`);
    assert.equal(await tab.eval(`[...CSS.highlights.get("kapi-note-waiting")][0].startOffset`), 12);
  });
});

test("#39 the note popover closes when the mouse leaves the pin", async () => {
  server.push(sessionNote);
  await until(async () => (await pinStates()) === "1:waiting");
  await hoverPin(1);
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x: 5, y: 700 });
  await page.waitFor(`!document.querySelector(".kapi-note-pop")`);

  const at = (expr) => page.eval(`(r => ({ x: r.left + r.width / 2, y: r.top + r.height / 2 }))(${expr}.getBoundingClientRect())`);
  const move = ({ x, y }) => page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  await move(await at(`[...CSS.highlights.get("kapi-note-waiting")][0]`));
  await page.waitFor(`!!document.querySelector(".kapi-note-pop")`);
  const word = await page.eval(`(() => { const t = document.querySelector('[data-kapi-id="bbbbbbbb"]').firstChild; const r = document.createRange(); r.setStart(t, t.data.indexOf("cluster")); r.setEnd(t, t.data.indexOf("cluster") + 7); const b = r.getBoundingClientRect(); return { x: b.left + b.width / 2, y: b.top + b.height / 2 }; })()`);
  await move(word);
  await page.waitFor(`!document.querySelector(".kapi-note-pop")`);

  await hoverPin(1);
  await move(await at(`document.querySelector(".kapi-note-pop")`));
  await new Promise((r) => setTimeout(r, 300));
  assert.equal(await page.eval(`!!document.querySelector(".kapi-note-pop")`), true, "moving into the popover keeps it open");
  await move(await at(`document.querySelector(".kapi-panel-head")`));
  await page.waitFor(`!document.querySelector(".kapi-note-pop")`);
});

test("#39 a note sent before notes kept their revision turns changed without guessing what changed", async () => {
  const { rev, ...old } = sessionNote;
  server.push(old);
  server.agentEdit("bbbbbbbb", '<p data-kapi-id="bbbbbbbb">Move the session store to the new cluster today.</p>');
  await until(async () => (await pinStates()) === "1:changed");
  assert.deepEqual(await highlighted("kapi-changed"), []);
  assert.ok(!requests("GET", "/blocks/bbbbbbbb").some((r) => r.search.includes("rev=")), "nothing to compare against");
});

test("#39 a note whose block was deleted stays in the list and says the block is gone", async () => {
  server.push({ author: "human", kind: "note", target: "dddddddd", quote: "before starting", rev: 1, text: "keep?" });
  await until(async () => (await pinStates()) === "1:waiting");
  server.agentDelete("dddddddd");
  await until(async () => (await pinStates()) === "");
  await page.eval(`document.querySelectorAll(".kapi-tabs button")[1].click()`);
  assert.equal(await page.eval(`document.querySelector('.kapi-note-item[data-note="1"] .kapi-note-crumb').textContent`), "Block removed");
  assert.equal(await page.eval(`document.querySelector('.kapi-note-item[data-note="1"]').dataset.state`), "changed");
  await click('.kapi-note-item[data-note="1"]');
  await page.waitFor(`!!document.querySelector(".kapi-note-pop")`);
  assert.match(await page.eval(`document.querySelector(".kapi-note-pop").innerText`), /no longer on the page/);
});

test("#39 a file edit while you edit another block still marks the note it changed", async () => {
  server.push({ author: "human", kind: "note", target: "dddddddd", quote: "before starting", rev: 1, text: "keep?" });
  await until(async () => (await pinStates()) === "1:waiting");
  await select("bbbbbbbb");
  await editSelected();
  await page.waitFor(`document.querySelector('[data-kapi-id="bbbbbbbb"]').isContentEditable`);
  server.agentEdit("dddddddd", '<p data-kapi-id="dddddddd">See <a href="#bbbbbbbb">the summary</a> first.</p>', { author: "file", target: "", changed: "dddddddd" });
  await until(async () => (await pinStates()) === "1:changed");
  await page.key("Escape");
  await until(async () => (await highlighted("kapi-changed")).join() === "first.");
  assert.equal(await pinStates(), "1:changed");
});

test("#39 an edit to one list item leaves a note on its sibling waiting", async () => {
  await withPage([{ id: "uuuuuuuu", html: '<ul data-kapi-id="uuuuuuuu"><li data-kapi-id="vvvvvvvv">first item</li><li data-kapi-id="wwwwwwww">second item</li></ul>' }], async (own, tab) => {
    own.push({ author: "human", kind: "note", target: "wwwwwwww", quote: "second", rev: 1, text: "this one" });
    await tab.waitFor(`document.querySelector(".kapi-pins .kapi-pin")?.dataset.state === "waiting"`);
    own.agentEdit("uuuuuuuu", '<ul data-kapi-id="uuuuuuuu"><li data-kapi-id="vvvvvvvv">first item, edited</li><li data-kapi-id="wwwwwwww">second item</li></ul>', { author: "file", changed: "uuuuuuuu,vvvvvvvv" });
    own.push({ author: "agent", kind: "message", text: "done" });
    await tab.waitFor(`document.querySelectorAll(".kapi-msg").length === 2`);
    assert.equal(await tab.eval(`document.querySelector(".kapi-pins .kapi-pin").dataset.state`), "waiting");
  });
});

const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==";
const giveImage = (how, selector, { type = "image/png", tag = "a", count = 1 } = {}) => page.eval(`(() => {
  const bytes = Uint8Array.from(atob(${JSON.stringify(PNG)}), (c) => c.charCodeAt(0));
  const dt = new DataTransfer();
  for (let i = 0; i < ${count}; i++) dt.items.add(new File([bytes, ${JSON.stringify(tag)} + i], ${JSON.stringify(tag)} + i + ".png", { type: ${JSON.stringify(type)} }));
  const el = document.querySelector(${JSON.stringify(selector)});
  if (${JSON.stringify(how)} === "paste") el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  else if (${JSON.stringify(how)} === "drop") {
    el.dispatchEvent(new DragEvent("dragover", { dataTransfer: dt, bubbles: true, cancelable: true }));
    el.dispatchEvent(new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true }));
  } else {
    el.files = dt.files;
    el.dispatchEvent(new Event("change", { bubbles: true }));
  }
})()`);
const chips = (scope) => page.eval(`[...document.querySelectorAll(${JSON.stringify(scope + " .kapi-chip")})].map((c) => c.dataset.state)`);

test("#30 an image pasted into a note uploads, queues with the note and goes out with it", async () => {
  await select("bbbbbbbb");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await giveImage("paste", ".kapi-ask textarea");
  await until(async () => (await chips(".kapi-ask")).join() === "ready");
  assert.equal(requests("POST", "/files").length, 1);
  assert.equal(requests("POST", "/files")[0].headers["content-type"], "image/png");
  assert.equal(await page.eval(`document.querySelector(".kapi-ask .kapi-ask-queue").disabled`), false, "an image alone can be queued");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
  const name = Object.keys(server.state.files)[0];
  assert.equal(await page.eval(`document.querySelector(".kapi-queued-note .kapi-thumb img").getAttribute("src").endsWith("/files/${name}")`), true);
  await click('button.kapi-primary[type="submit"]');
  await until(() => requests("POST", "/notes").length === 1);
  assert.deepEqual(requests("POST", "/notes")[0].body.notes[0].files, [name]);
  await page.waitFor(`!!document.querySelector(".kapi-msg .kapi-thumb img")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-msg .kapi-thumb").getAttribute("href").endsWith("/files/${name}")`), true);
  await until(async () => (await pinStates()) === "1:waiting");
  await hoverPin(1);
  assert.equal(await page.eval(`!!document.querySelector(".kapi-note-pop .kapi-thumb img")`), true, "the popover shows the note's image");
});

test("#30 images dropped or picked in the message box go out with the message, even with no text", async () => {
  await giveImage("drop", ".kapi-composer");
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("Enter");
  await until(() => requests("POST", "/messages").length === 1);
  const name = Object.keys(server.state.files)[0];
  assert.deepEqual(requests("POST", "/messages")[0].body, { kind: "message", text: "", files: [name] });
  await until(async () => (await chips(".kapi-composer")).length === 0);

  await giveImage("pick", ".kapi-composer input[type=file]", { tag: "b" });
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "see this"`);
  await page.key("Enter");
  await until(() => requests("POST", "/messages").length === 2);
  assert.equal(requests("POST", "/messages")[1].body.text, "see this");
  assert.equal(requests("POST", "/messages")[1].body.files.length, 1);
  await page.waitFor(`document.querySelectorAll(".kapi-msg .kapi-thumb img").length === 2`);
});

test("#30 a failed upload can be retried or removed, and nothing sends while an image uploads", async () => {
  server.state.nextUploadError = "only PNG, JPEG, GIF and WebP images can be attached";
  await giveImage("paste", ".kapi-composer textarea");
  await until(async () => (await chips(".kapi-composer")).join() === "error");
  assert.match(await page.eval(`document.querySelector(".kapi-composer .kapi-chip-error").textContent`), /only PNG/);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer .kapi-primary").disabled`), true);
  server.state.uploadDelay = 400;
  await click(".kapi-composer .kapi-chip-retry");
  await until(async () => (await chips(".kapi-composer")).join() === "uploading");
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "wait"`);
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("Enter");
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(requests("POST", "/messages").length, 0, "nothing is sent while an image is still uploading");
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  await click(".kapi-composer .kapi-chip-remove");
  assert.deepEqual(await chips(".kapi-composer"), []);

  await giveImage("paste", ".kapi-composer textarea", { count: 5 });
  await until(async () => (await notice()).includes("At most 4 images"));
  await until(async () => (await chips(".kapi-composer")).length === 4);
  await giveImage("paste", ".kapi-composer textarea", { type: "image/svg+xml", tag: "s" });
  await until(async () => (await notice()).includes("Only PNG, JPEG, GIF and WebP"));
});

test("#30 an image that expired shows as a placeholder", async () => {
  server.push({ author: "human", kind: "message", text: "old one", files: ["/home/.kapi/pages/x/files/ffffffffffffffffffffffffffffffff.png"] });
  await page.waitFor(`!!document.querySelector(".kapi-msg .kapi-expired")`);
  assert.equal(await page.eval(`document.querySelector(".kapi-msg .kapi-expired").textContent`), "Image expired");
});

test("#30 a second Enter while a message sends does not send its images again, and an image added meanwhile stays", async () => {
  await giveImage("paste", ".kapi-composer textarea");
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  server.state.messageDelay = 400;
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "hi"`);
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("Enter");
  await page.key("Enter");
  await giveImage("paste", ".kapi-composer textarea", { tag: "later" });
  await until(() => requests("POST", "/messages").length === 1);
  await new Promise((r) => setTimeout(r, 600));
  assert.equal(requests("POST", "/messages").length, 1, "the second Enter sent nothing");
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  const later = Object.keys(server.state.files).at(-1);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer .kapi-chip img").getAttribute("src").endsWith("/files/${later}")`), true, "the image pasted during the send is still there");
});

test("#30 a failed image blocks sending with a reason, and text pasted with an image stays text", async () => {
  server.state.nextUploadError = "nope";
  await giveImage("paste", ".kapi-composer textarea");
  await until(async () => (await chips(".kapi-composer")).join() === "error");
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "x"`);
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("Enter");
  await until(async () => (await notice()).includes("Retry or remove"));
  assert.equal(requests("POST", "/messages").length, 0);
  await click(".kapi-composer .kapi-chip-remove");
  const before = requests("POST", "/files").length;
  await page.eval(`(() => {
    const dt = new DataTransfer();
    dt.setData("text/plain", "A1 B1");
    dt.items.add(new File([new Uint8Array([137, 80, 78, 71])], "cells.png", { type: "image/png" }));
    document.querySelector(".kapi-composer textarea").dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  })()`);
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(requests("POST", "/files").length, before, "a paste that carries text does not upload its picture");
});

test("#30 keyboard focus stays on a chip's button when another upload finishes, and Escape there closes the note box", async () => {
  await select("bbbbbbbb");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await giveImage("paste", ".kapi-ask textarea");
  await until(async () => (await chips(".kapi-ask")).join() === "ready");
  server.state.uploadDelay = 300;
  await giveImage("paste", ".kapi-ask textarea", { tag: "second" });
  assert.equal(await page.eval(`document.querySelector(".kapi-ask .kapi-ask-queue").disabled`), true, "Queue waits for the upload");
  await page.eval(`document.querySelector(".kapi-ask .kapi-chip:nth-child(2) .kapi-chip-remove").focus()`);
  await until(async () => (await chips(".kapi-ask")).join() === "ready,ready");
  assert.equal(await page.eval(`document.activeElement === document.querySelector(".kapi-ask .kapi-chip:nth-child(2) .kapi-chip-remove")`), true);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-ask")`);
});

test("#30 composer images go out on the message when notes are queued, in the order attached", async () => {
  server.push({ author: "agent", kind: "message", text: "hello" });
  await select("bbbbbbbb");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("note");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
  await giveImage("paste", ".kapi-composer textarea", { tag: "first" });
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  await giveImage("paste", ".kapi-composer textarea", { tag: "second" });
  await until(async () => (await chips(".kapi-composer")).join() === "ready,ready");
  const names = Object.keys(server.state.files);
  await click('button.kapi-primary[type="submit"]');
  await until(() => requests("POST", "/notes").length === 1);
  assert.deepEqual(requests("POST", "/notes")[0].body.files, names);
  await until(async () => (await chips(".kapi-composer")).length === 0);
});

test("#30 a file dropped outside the page and the boxes does not replace the page", async () => {
  const prevented = await page.eval(`(() => {
    const dt = new DataTransfer();
    dt.items.add(new File(["x"], "a.png", { type: "image/png" }));
    const over = new DragEvent("dragover", { dataTransfer: dt, bubbles: true, cancelable: true });
    const drop = new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true });
    document.querySelector(".kapi-panel-head").dispatchEvent(over);
    document.querySelector(".kapi-panel-head").dispatchEvent(drop);
    return over.defaultPrevented && drop.defaultPrevented;
  })()`);
  assert.equal(prevented, true);
  assert.equal(requests("POST", "/images").length, 0);
});

test("#30 a message that fails to send keeps its images", async () => {
  await giveImage("paste", ".kapi-composer textarea");
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  await page.eval(`window.fetch = ((orig) => (url, opts) => (String(url).endsWith("/messages") ? Promise.reject(new TypeError("offline")) : orig(url, opts)))(window.fetch)`);
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "x"`);
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.key("Enter");
  await until(async () => (await notice()).includes("Could not reach kapi"));
  assert.deepEqual(await chips(".kapi-composer"), ["ready"]);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer textarea").value`), "x");
});

test("#30 Send stays off while notes send, images out in a send count toward the limit, and Retry keeps focus on its chip", async () => {
  await select("bbbbbbbb");
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.type("note");
  await page.key("Enter");
  await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 1`);
  await giveImage("paste", ".kapi-composer textarea", { count: 2 });
  await until(async () => (await chips(".kapi-composer")).join() === "ready,ready");
  server.state.notesDelay = 400;
  server.state.nextNotesError = "busy";
  await click('button.kapi-primary[type="submit"]');
  await until(() => requests("POST", "/notes").length === 1);
  assert.equal(await page.eval(`document.querySelector(".kapi-composer .kapi-primary").disabled`), true, "Send is off while notes send");
  await giveImage("paste", ".kapi-composer textarea", { count: 3, tag: "during" });
  await until(async () => (await notice()).includes("At most 4 images"));
  await until(async () => (await chips(".kapi-composer")).join() === "ready,ready,ready,ready");
  assert.equal(await page.eval(`document.querySelector(".kapi-composer .kapi-primary").disabled`), false);

  for (const _ of [0, 1, 2]) await click(".kapi-composer .kapi-chip-remove");
  server.state.nextUploadError = "nope";
  await giveImage("paste", ".kapi-composer textarea", { tag: "bad" });
  await until(async () => (await chips(".kapi-composer")).join() === "ready,error");
  server.state.uploadDelay = 200;
  await page.eval(`document.querySelector(".kapi-composer .kapi-chip:nth-child(2) .kapi-chip-retry").focus()`);
  await page.key("Enter");
  await until(async () => (await chips(".kapi-composer")).join() === "ready,uploading");
  assert.equal(await page.eval(`document.activeElement === document.querySelector(".kapi-composer .kapi-chip:nth-child(2) .kapi-chip-remove")`), true);
});

const figures = () => page.eval(`[...document.querySelectorAll("#kapi-page > figure:has(> img)")].map((f) => (f.previousElementSibling?.dataset.kapiId ?? "") + ">" + f.querySelector("img").getAttribute("src").split("/").pop())`);

test("#30 an image dropped on a block goes in after it, selected, with a Download button", async () => {
  await giveImage("drop", '[data-kapi-id="bbbbbbbb"]');
  await until(() => requests("POST", "/ops").length === 1);
  const name = Object.keys(server.state.pageFiles)[0];
  assert.deepEqual(requests("POST", "/ops")[0].body.op, { kind: "image", id: "bbbbbbbb", src: name });
  await until(async () => (await figures()).join() === `bbbbbbbb>${name}`);
  await page.waitFor(`document.querySelector("[data-kapi-selected]")?.matches("figure")`);
  assert.deepEqual(await barActions(), ["ask", "copy", "download", "archive", "delete"]);
  assert.equal(await page.eval(`document.querySelector("#kapi-page > figure > img").naturalWidth`), 1, "the image loads from the page's files folder");
  await page.eval(`window.downloaded = null; HTMLAnchorElement.prototype.click = function () { window.downloaded = [this.download, this.getAttribute("href")]; }`);
  await click('.kapi-bar [data-action="download"]');
  const [file, href] = await page.eval(`window.downloaded`);
  assert.equal(file, name);
  assert.ok(href.endsWith(`/plan.files/${name}`));
});

test("#30 an image pasted on the page goes after the selected block, or at the end, and Undo takes it out", async () => {
  await select("cccccccc");
  await page.eval(`document.activeElement.blur()`);
  await giveImage("paste", "body");
  await until(async () => (await figures()).length === 1);
  assert.equal((await figures())[0].split(">")[0], "cccccccc");
  await page.eval(`document.querySelector("[data-kapi-selected]")?.removeAttribute("data-kapi-selected")`);
  await page.key("Escape");
  await page.key("Escape");
  await giveImage("paste", "body", { tag: "end" });
  await until(async () => (await figures()).length === 2);
  assert.equal(requests("POST", "/ops").at(-1).body.op.id, "");
  await page.key("z", await modifier());
  await until(async () => (await figures()).length === 1);
  await giveImage("paste", ".kapi-composer textarea", { tag: "chat" });
  await until(async () => (await chips(".kapi-composer")).join() === "ready");
  assert.equal(requests("POST", "/images").length, 2, "a paste in the message box attaches to the message, not the page");
});

test("#30 a page image that is not a supported type, or fails to upload, says so and adds nothing", async () => {
  await giveImage("drop", '[data-kapi-id="bbbbbbbb"]', { type: "image/svg+xml" });
  await until(async () => (await notice()).includes("Only PNG, JPEG, GIF and WebP"));
  server.state.nextUploadError = "the disk is full";
  await giveImage("drop", '[data-kapi-id="bbbbbbbb"]');
  await until(async () => (await notice()).includes("the disk is full"));
  assert.equal(requests("POST", "/ops").length, 0);
  assert.deepEqual(await figures(), []);
});

test("#30 the same image dropped twice selects the new copy, and a page that changed meanwhile still gets the image", async () => {
  await giveImage("drop", '[data-kapi-id="bbbbbbbb"]');
  await until(async () => (await figures()).length === 1);
  const first = await page.eval(`document.querySelector("#kapi-page > figure:has(> img)").dataset.kapiId`);
  await giveImage("drop", '[data-kapi-id="bbbbbbbb"]');
  await until(async () => (await figures()).length === 2);
  await page.waitFor(`document.querySelector("[data-kapi-selected]")?.matches("figure") && document.querySelector("[data-kapi-selected]").dataset.kapiId !== ${JSON.stringify(first)}`);
  assert.equal(await page.eval(`document.querySelector("[data-kapi-selected]").previousElementSibling.dataset.kapiId`), "bbbbbbbb");

  server.bumpBehindBrowser("dddddddd", '<p data-kapi-id="dddddddd">Changed by the agent.</p>');
  await giveImage("drop", '[data-kapi-id="cccccccc"]', { tag: "late" });
  await until(async () => (await figures()).length === 3);
  const tries = requests("POST", "/ops").slice(-2).map((r) => r.body.op.id);
  assert.deepEqual(tries, ["cccccccc", "cccccccc"], "the image op is tried again after a stale refusal");
});

test("#30 a drop of an image and a file that is not one adds the image and says what was skipped", async () => {
  await page.eval(`(() => {
    const bytes = Uint8Array.from(atob(${JSON.stringify(PNG)}), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "a.png", { type: "image/png" }));
    dt.items.add(new File(["%PDF"], "b.pdf", { type: "application/pdf" }));
    document.querySelector('[data-kapi-id="bbbbbbbb"]').dispatchEvent(new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true }));
  })()`);
  await until(async () => (await figures()).length === 1);
  await until(async () => (await notice()).includes("Only PNG, JPEG, GIF and WebP"));
});

test("#30 an image from another site has no Download tool", async () => {
  await withPage([{ id: "eeeeeeef", html: '<figure data-kapi-id="eeeeeeef"><img src="data:image/png;base64,' + PNG + '" alt=""></figure>' }], async (own, tab) => {
    await tab.eval(`document.querySelector('[data-kapi-id="eeeeeeef"]').click()`);
    await tab.waitFor(`!document.querySelector(".kapi-bar").hidden`);
    assert.deepEqual(await tab.eval(`[...document.querySelectorAll(".kapi-bar button")].map((b) => b.dataset.action)`), ["ask", "copy", "archive", "delete"]);
  });
});

const menuOpen = () => page.eval(`!document.querySelector(".kapi-menu").hidden`);
const expanded = () => page.eval(`document.querySelector(".kapi-menu-button").getAttribute("aria-expanded")`);
const focusedItem = () => page.eval(`document.activeElement.closest(".kapi-menu") ? document.activeElement.querySelector(".kapi-menu-label").textContent : document.activeElement.className`);
const topLabels = () => page.eval(`[...document.querySelectorAll(".kapi-menu [role=menuitem]:not([hidden])")].filter((b) => !b.closest(".kapi-submenu")).map((b) => b.querySelector(".kapi-menu-label").textContent)`);
const submenuOpen = () => page.eval(`!document.querySelector(".kapi-submenu").hidden`);
const exportExpanded = () => page.eval(`document.querySelector(".kapi-menu [data-menu=export]").getAttribute("aria-expanded")`);
const menuButtonFocused = () => page.eval(`document.activeElement === document.querySelector(".kapi-menu-button")`);
const menuItem = (label) => page.eval(`[...document.querySelectorAll(".kapi-menu [role=menuitem]")].find((b) => b.textContent.startsWith(${JSON.stringify(label)})).click()`);
const exportAs = async (label) => {
  await click(".kapi-menu [data-menu=export]");
  await until(submenuOpen);
  await page.eval(`[...document.querySelectorAll(".kapi-submenu [role=menuitem]")].find((b) => b.textContent === ${JSON.stringify(label)}).click()`);
};
const mouseMove = async (selector) => {
  const { x, y } = await page.eval(`(({ left, top, width, height }) => ({ x: left + width / 2, y: top + height / 2 }))(document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect())`);
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
};
const pressKey = async (key) => {
  await page.call("Input.dispatchKeyEvent", { type: "rawKeyDown", key, code: key });
  await page.call("Input.dispatchKeyEvent", { type: "keyUp", key, code: key });
};
const mouseOn = async (selector) => {
  const { x, y } = await page.eval(`(({ left, top, width, height }) => ({ x: left + width / 2, y: top + height / 2 }))(document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect())`);
  await mouseClickAt(x, y);
};

test("#23 the three-dot menu opens on its button, steps with the arrow keys, and Escape, Tab or a click outside closes it", async () => {
  assert.equal(await expanded(), "false");
  await page.eval(`document.querySelector(".kapi-menu-button").focus()`);
  await page.key("Enter");
  await until(menuOpen);
  assert.equal(await expanded(), "true");
  assert.deepEqual(await topLabels(), ["Export", "Theme", "Copy path", "End session"]);
  assert.equal(await submenuOpen(), false);
  assert.equal(await focusedItem(), "Export");
  for (const [key, want] of [["ArrowDown", "Theme"], ["ArrowDown", "Copy path"], ["ArrowDown", "End session"], ["ArrowDown", "Export"], ["ArrowUp", "End session"], ["Home", "Export"], ["End", "End session"]]) {
    await pressKey(key);
    assert.equal(await focusedItem(), want, `after ${key}`);
  }
  await page.key("Escape");
  assert.equal(await menuOpen(), false);
  assert.equal(await menuButtonFocused(), true);
  assert.equal(await expanded(), "false");

  await page.key("Enter");
  await until(menuOpen);
  await page.key("Tab");
  assert.equal(await menuOpen(), false);
  assert.equal(await expanded(), "false");

  await mouseOn(".kapi-menu-button");
  await until(menuOpen);
  await mouseOn('[data-kapi-id="bbbbbbbb"]');
  assert.equal(await menuOpen(), false);
  assert.equal(await expanded(), "false");
  await mouseOn(".kapi-menu-button");
  await until(menuOpen);
  await mouseOn(".kapi-menu-button");
  assert.equal(await menuOpen(), false);
  assert.equal(await expanded(), "false");
});

test("#23 keys pressed in the open menu do not reach the page's block shortcuts", async () => {
  await select("bbbbbbbb");
  await page.waitFor(`!!document.querySelector("[data-kapi-selected]")`);
  await click(".kapi-menu-button");
  await until(menuOpen);
  for (const inSubmenu of [false, true]) {
    if (inSubmenu) await pressKey("ArrowRight");
    for (const key of ["Delete", "Backspace", "e", "a", "j", "k"]) await page.key(key);
    await page.key("z", { meta: true });
    await page.key("z", { ctrl: true });
  }
  assert.equal(await submenuOpen(), true);
  await new Promise((r) => setTimeout(r, 200));
  assert.deepEqual(requests("POST", "/ops").map((r) => r.body.op.kind), []);
  assert.equal(await page.eval(`!!document.querySelector(".kapi-ask")?.checkVisibility()`), false);
  assert.equal(await menuOpen(), true);
  assert.equal(await page.eval(`document.querySelector("[data-kapi-selected]")?.dataset.kapiId`), "bbbbbbbb");
});

test("#23 Export opens a submenu of HTML, Markdown and PDF by keys, and ArrowLeft or Escape goes back to Export", async () => {
  const exportItem = ".kapi-menu [data-menu=export]";
  assert.deepEqual(await page.eval(`[document.querySelector(${JSON.stringify(exportItem)}).getAttribute("aria-haspopup"), document.querySelector(".kapi-submenu").getAttribute("role")]`), ["menu", "menu"]);
  assert.equal(await exportExpanded(), "false");
  await page.eval(`document.querySelector(".kapi-menu-button").focus()`);
  await page.key("Enter");
  await until(menuOpen);
  assert.equal(await focusedItem(), "Export");
  await pressKey("ArrowRight");
  assert.equal(await submenuOpen(), true);
  assert.equal(await exportExpanded(), "true");
  assert.deepEqual(await page.eval(`[...document.querySelectorAll(".kapi-submenu [role=menuitem]")].map((b) => b.querySelector(".kapi-menu-label").textContent)`), ["HTML", "Markdown", "PDF"]);
  assert.equal(await focusedItem(), "HTML");
  for (const [key, want] of [["ArrowDown", "Markdown"], ["ArrowDown", "PDF"], ["ArrowDown", "HTML"], ["ArrowUp", "PDF"], ["Home", "HTML"], ["End", "PDF"]]) {
    await pressKey(key);
    assert.equal(await focusedItem(), want, `after ${key}`);
  }
  await pressKey("ArrowLeft");
  assert.equal(await submenuOpen(), false);
  assert.equal(await exportExpanded(), "false");
  assert.equal(await focusedItem(), "Export");
  assert.equal(await menuOpen(), true);

  await page.key("Enter");
  assert.equal(await submenuOpen(), true);
  assert.equal(await focusedItem(), "HTML");
  await page.key("Escape");
  assert.equal(await submenuOpen(), false);
  assert.equal(await focusedItem(), "Export");
  assert.equal(await menuOpen(), true);

  await pressKey(" ");
  assert.equal(await submenuOpen(), true);
  assert.equal(await focusedItem(), "HTML");
  await page.key("Escape");
  await page.key("Escape");
  assert.equal(await menuOpen(), false);
  assert.equal(await menuButtonFocused(), true);

  await page.key("Enter");
  await until(menuOpen);
  assert.equal(await submenuOpen(), false);
  await pressKey("ArrowRight");
  await page.key("Tab");
  assert.equal(await menuOpen(), false);
  assert.equal(await submenuOpen(), false);
});

test("#23 on a desktop the Export submenu opens on hover, stays open on the way into it, and stays inside the screen", async () => {
  await mouseOn(".kapi-menu-button");
  await until(menuOpen);
  await mouseMove(".kapi-menu [data-menu=export]");
  await until(submenuOpen);
  assert.equal(await exportExpanded(), "true");
  const box = await page.eval(`(r => ({ left: r.left, right: r.right, top: r.top, bottom: r.bottom }))(document.querySelector(".kapi-submenu").getBoundingClientRect())`);
  const menuBox = await page.eval(`(r => ({ left: r.left, right: r.right }))(document.querySelector(".kapi-menu").getBoundingClientRect())`);
  assert.ok(box.left >= 0 && box.right <= 1280 && box.top >= 0 && box.bottom <= 800, JSON.stringify(box));
  assert.ok(box.right <= menuBox.left + 8, JSON.stringify({ box, menuBox }));
  const from = await page.eval(`(r => ({ x: r.left + r.width / 2, y: r.top + r.height / 2 }))(document.querySelector(".kapi-menu [data-menu=export]").getBoundingClientRect())`);
  await mouseClickAt(from.x, from.y);
  assert.equal(await submenuOpen(), true);
  const to = await page.eval(`(r => ({ x: r.left + r.width / 2, y: r.top + r.height / 2 }))([...document.querySelectorAll(".kapi-submenu [role=menuitem]")][1].getBoundingClientRect())`);
  for (let i = 1; i <= 20; i++) {
    await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x: from.x + ((to.x - from.x) * i) / 20, y: from.y + ((to.y - from.y) * i) / 20 });
    assert.equal(await submenuOpen(), true, `step ${i}`);
  }
  await mouseMove(".kapi-menu [data-menu=copy]");
  await until(async () => !(await submenuOpen()));
  assert.equal(await exportExpanded(), "false");
});

test("hovering from Export onto Theme swaps the open submenu after a short pause", async () => {
  const open = () => page.eval(`[...document.querySelectorAll(".kapi-submenu")].map((m) => !m.hidden)`);
  await mouseOn(".kapi-menu-button");
  await until(menuOpen);
  await mouseMove(".kapi-menu [data-menu=export]");
  await until(submenuOpen);
  await mouseMove(".kapi-menu [data-menu=theme]");
  assert.deepEqual(await open(), [true, false]);
  await until(async () => JSON.stringify(await open()) === JSON.stringify([false, true]));
  assert.equal(await page.eval(`document.querySelector(".kapi-menu [data-menu=theme]").getAttribute("aria-expanded")`), "true");
});

test("#23 Export HTML downloads the server's export under the page's file name, and says why when it fails", async () => {
  await page.eval(`window.downloads = []; HTMLAnchorElement.prototype.click = function () { const name = this.download; fetch(this.href).then((r) => r.text()).then((text) => downloads.push({ name, text })); }`);
  await click(".kapi-menu-button");
  await exportAs("HTML");
  await until(() => page.eval(`downloads.length === 1`));
  const [got] = await page.eval(`downloads`);
  assert.equal(got.name, "plan.html");
  assert.match(got.text, /Move the session store/);
  assert.equal(requests("GET", "/export/html").length, 1);
  assert.equal(await menuOpen(), false);

  server.state.nextExportError = "the page shows images that are missing from plan.files: 0123.png";
  await click(".kapi-menu-button");
  await exportAs("HTML");
  await until(async () => (await notice()) === "Could not export the page: the page shows images that are missing from plan.files: 0123.png");

  server.state.dropExport = true;
  await click(".kapi-menu-button");
  await exportAs("HTML");
  await until(async () => /Could not reach kapi/.test(await notice()));
  server.state.dropExport = false;

  server.state.restarted = "kapi open plan.html";
  await click(".kapi-menu-button");
  await exportAs("HTML");
  await until(() => page.eval(`!document.querySelector(".kapi-restart").hidden`));
  assert.equal(await page.eval(`downloads.length`), 1);
});

test("#23 Export Markdown downloads plan.md, or plan.zip when the page has images, gives focus back, and says why when it fails", async () => {
  await page.eval(`window.downloads = []; HTMLAnchorElement.prototype.click = function () { const name = this.download; fetch(this.href).then((r) => r.text()).then((text) => downloads.push({ name, text })); }`);
  await click(".kapi-menu-button");
  await exportAs("Markdown");
  await until(() => page.eval(`downloads.length === 1`));
  assert.deepEqual(await page.eval(`downloads[0]`), { name: "plan.md", text: "# Plan\n" });
  assert.equal(requests("GET", "/export/markdown").length, 1);
  assert.equal(await menuOpen(), false);
  assert.equal(await menuButtonFocused(), true);

  server.state.markdownZip = true;
  await click(".kapi-menu-button");
  await exportAs("Markdown");
  await until(() => page.eval(`downloads.length === 2`));
  assert.deepEqual(await page.eval(`downloads[1]`), { name: "plan.zip", text: "PK zip of plan.md and images/" });
  server.state.markdownZip = false;

  server.state.nextExportError = "render page: boom";
  await click(".kapi-menu-button");
  await exportAs("Markdown");
  await until(async () => (await notice()) === "Could not export the page: render page: boom");

  server.state.dropExport = true;
  await click(".kapi-menu-button");
  await exportAs("Markdown");
  await until(async () => /Could not reach kapi/.test(await notice()));
  server.state.dropExport = false;

  server.state.restarted = "kapi open plan.html";
  await click(".kapi-menu-button");
  await exportAs("Markdown");
  await until(() => page.eval(`!document.querySelector(".kapi-restart").hidden`));
  assert.equal(await page.eval(`downloads.length`), 2);
});

test("#23 Export PDF calls the browser's print and gives focus back to the menu button", async () => {
  await page.eval(`window.printCalls = 0; window.print = () => { printCalls++; }`);
  await click(".kapi-menu-button");
  await until(menuOpen);
  await exportAs("PDF");
  assert.equal(await page.eval(`printCalls`), 1);
  assert.equal(await menuOpen(), false);
  assert.equal(await menuButtonFocused(), true);
});

test("#23 while printing, by the menu or Ctrl+P, the document title is the page's name", async () => {
  await page.eval(`dispatchEvent(new Event("beforeprint"))`);
  assert.equal(await page.eval(`document.title`), "plan");
  await page.eval(`dispatchEvent(new Event("afterprint"))`);
  assert.equal(await page.eval(`document.title`), "Fake kapi");
});

test("#23 printing leaves out the chat panel, the menu, fold arrows, note marks and the undo note, and prints folded sections in full", async () => {
  await reopenWith(FOLD_ALL_PAGE);
  await arrowClick("h2aaaaaa");
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="p2p2p2p2"]').checkVisibility()`), false);
  await deleteBlock("p4p4p4p4");
  await click(".kapi-menu-button");
  const pseudo = (which) => page.eval(`getComputedStyle(document.querySelector('[data-kapi-id="h2aaaaaa"]'), "${which}").content`);
  const mark = (name) => page.eval(`(s => [s.backgroundColor, s.textDecorationLine])(getComputedStyle(document.querySelector('[data-kapi-id="p1p1p1p1"]'), "::highlight(${name})"))`);
  const undoShown = () => page.eval(`document.querySelector("#kapi-page .kapi-undo-note").checkVisibility()`);
  assert.notEqual(await pseudo("::after"), "none");
  assert.notDeepEqual(await mark("kapi-changed"), ["rgba(0, 0, 0, 0)", "none"]);
  assert.notDeepEqual(await mark("kapi-note-answered"), ["rgba(0, 0, 0, 0)", "none"]);
  assert.equal(await undoShown(), true);
  await page.call("Emulation.setEmulatedMedia", { media: "print" });
  try {
    assert.deepEqual(await page.eval(`[".kapi-panel", ".kapi-menu", ".kapi-site-head"].map((s) => document.querySelector(s).checkVisibility())`), [false, false, false]);
    assert.equal(await page.eval(`getComputedStyle(document.documentElement).paddingRight`), "0px");
    assert.equal(await page.eval(`document.querySelector('[data-kapi-id="p2p2p2p2"]').checkVisibility()`), true);
    assert.deepEqual([await pseudo("::before"), await pseudo("::after")], ["none", "none"]);
    assert.equal(await undoShown(), false);
    for (const name of ["kapi-ask", "kapi-note-waiting", "kapi-note-answered", "kapi-note-changed", "kapi-changed"]) {
      assert.deepEqual(await mark(name), ["rgba(0, 0, 0, 0)", "none"], name);
    }
  } finally {
    await page.call("Emulation.setEmulatedMedia", { media: "" });
  }
});

test("#23 the menu shows the page's path and Copy path puts it on the clipboard and gives focus back", async () => {
  await page.eval(`window.copied = []; navigator.clipboard.writeText = async (t) => { window.copied.push(t); }`);
  await click(".kapi-menu-button");
  assert.equal(await page.eval(`document.querySelector(".kapi-menu-detail").title`), "/home/me/notes/plan.html");
  await menuItem("Copy path");
  await until(async () => (await notice()) === "Copied the page path");
  assert.deepEqual(await page.eval(`copied`), ["/home/me/notes/plan.html"]);
  assert.equal(await menuButtonFocused(), true);
  await page.eval(`navigator.clipboard.writeText = async () => { throw new Error("denied"); }`);
  await click(".kapi-menu-button");
  await menuItem("Copy path");
  await until(async () => (await notice()) === "Could not copy the page path");
});

test("#23 a page path with quotes and angle brackets reaches the menu as written", async () => {
  const path = `/home/me/a"<b>&'.html`;
  const own = await startFakeServer({ path });
  const tab = await browser.open(own.url);
  try {
    await tab.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
    assert.equal(await tab.eval(`document.getElementById("kapi-page").dataset.path`), path);
    assert.equal(await tab.eval(`document.querySelector(".kapi-menu-detail").title`), path);
    assert.equal(await tab.eval(`document.querySelectorAll("#kapi-page b").length`), 0);
    assert.deepEqual(tab.errors, []);
  } finally {
    await tab.close();
    await own.close();
  }
});

test("#23 on a phone the menu opens under the top bar inside the screen and leaves the chat collapsed", async () => {
  const p = await browser.open(server.url, { width: 390, height: 844 });
  try {
    await p.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
    await p.eval(`document.querySelector(".kapi-menu-button").click()`);
    await p.waitFor(`!document.querySelector(".kapi-menu").hidden`);
    const box = await p.eval(`(() => { const r = document.querySelector(".kapi-menu").getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; })()`);
    const head = await p.eval(`Math.max(...[".kapi-panel-head", ".kapi-site-head"].map((s) => document.querySelector(s).getBoundingClientRect().bottom))`);
    assert.ok(box.left >= 0 && box.right <= 390 && box.top >= head && box.bottom < 844 / 2, JSON.stringify({ ...box, head }));
    assert.equal(await p.eval(`document.querySelector(".kapi-panel").classList.contains("kapi-expanded")`), false);
    assert.equal(await p.eval(`document.elementFromPoint(${(box.left + box.right) / 2}, ${box.top + 20}).closest("[role=menuitem]")?.querySelector(".kapi-menu-label").textContent`), "Export");
    assert.deepEqual(p.errors, []);
  } finally {
    await p.close();
  }
});

test("#23 on a phone a tap on Export opens HTML, Markdown and PDF under it inside the screen, and a second tap closes them", async () => {
  const p = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await p.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
    await p.call("Emulation.setTouchEmulationEnabled", { enabled: true });
    await p.eval(`document.querySelector(".kapi-menu-button").click()`);
    await p.waitFor(`!document.querySelector(".kapi-menu").hidden`);
    const rect = (sel) => p.eval(`(r => ({ left: r.left, right: r.right, top: r.top, bottom: r.bottom }))(document.querySelector(${JSON.stringify(sel)}).getBoundingClientRect())`);
    const tap = async (sel) => {
      const r = await rect(sel);
      const point = { x: (r.left + r.right) / 2, y: (r.top + r.bottom) / 2 };
      await p.call("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [point] });
      await p.call("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    };
    await tap(".kapi-menu [data-menu=export]");
    await p.waitFor(`!document.querySelector(".kapi-submenu").hidden`);
    assert.equal(await p.eval(`document.querySelector(".kapi-menu [data-menu=export]").getAttribute("aria-expanded")`), "true");
    const [sub, item, menu, next] = [await rect(".kapi-submenu"), await rect(".kapi-menu [data-menu=export]"), await rect(".kapi-menu"), await rect(".kapi-menu [data-menu=copy]")];
    assert.ok(sub.left >= menu.left && sub.right <= menu.right && sub.right <= 390 && sub.top >= item.bottom - 1 && next.top >= sub.bottom - 1 && menu.bottom <= 800, JSON.stringify({ sub, item, menu, next }));
    await tap(".kapi-menu [data-menu=export]");
    await p.waitFor(`document.querySelector(".kapi-submenu").hidden`);
    assert.equal(await p.eval(`!document.querySelector(".kapi-menu").hidden`), true);
    assert.deepEqual(p.errors, []);
  } finally {
    await p.close();
  }
});

test("#26 End session in the menu is red like the block Delete action, in light and dark", async () => {
  await select("bbbbbbbb");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  await click(".kapi-menu-button");
  await until(menuOpen);
  for (const theme of ["light", "dark"]) {
    await page.eval(`document.documentElement.dataset.theme = "${theme}"`);
    const [del, end, copy] = await page.eval(`[document.querySelector('.kapi-bar [data-action="delete"]'), document.querySelector(".kapi-menu [data-menu=end]"), document.querySelector(".kapi-menu [data-menu=copy]")].map((b) => getComputedStyle(b).color)`);
    assert.equal(end, del, theme);
    assert.notEqual(end, copy, theme);
  }
});

test("#26 a human message, note or answer after an end shows Session started again before it, also after a reload", async () => {
  const lines = () => page.eval(`[...document.querySelectorAll(".kapi-log > li")].map((li) => li.classList.contains("kapi-msg") ? li.querySelector(".kapi-msg-text").textContent : li.className + ": " + li.textContent.replace(/ \u00b7 .*/, ""))`);
  const want = ["first", "kapi-end: You ended the session", "bye", "kapi-again: Session started again", "and another", "kapi-end: You ended the session", "kapi-again: Session started again", "Sent form answer: yes", "kapi-end: You ended the session", "kapi-again: Session started again", "on it"];
  server.push({ author: "human", kind: "message", text: "first" });
  server.push({ author: "human", kind: "end" });
  server.push({ author: "agent", kind: "message", text: "bye" });
  server.push({ author: "human", kind: "message", text: "and another" });
  server.push({ author: "human", kind: "end" });
  server.push({ author: "human", kind: "answer", text: "yes" });
  server.push({ author: "human", kind: "end" });
  server.push({ ...sessionNote, text: "on it" });
  await until(async () => (await lines()).length === want.length);
  assert.deepEqual(await lines(), want);
  assert.match(await page.eval(`document.querySelector(".kapi-again").textContent`), /^Session started again \u00b7 \d/);
  await page.eval(`location.reload()`);
  await page.waitFor(`document.readyState === "complete" && document.querySelectorAll(".kapi-log > li").length === ${want.length}`);
  assert.deepEqual(await lines(), want);
});

test("#26 End session in the menu posts an end, and the item is gone while the session is ended", async () => {
  const labels = topLabels;
  await mouseOn(".kapi-menu-button");
  await until(menuOpen);
  await mouseOn(".kapi-menu [data-menu=end]");
  await until(() => requests("POST", "/end").length === 1);
  assert.deepEqual(requests("POST", "/end")[0].body, {});
  await page.waitFor(`document.querySelector(".kapi-status").textContent === "Session ended. Send a message to start again."`);
  assert.equal(await menuOpen(), false);

  await page.eval(`document.querySelector(".kapi-menu-button").focus()`);
  await page.key("Enter");
  await until(menuOpen);
  assert.deepEqual(await labels(), ["Export", "Theme", "Copy path"]);
  await pressKey("End");
  assert.equal(await focusedItem(), "Copy path");
  await page.key("Escape");

  server.push({ author: "human", kind: "message", text: "back" });
  await page.waitFor(`document.querySelector(".kapi-status").textContent !== "Session ended. Send a message to start again."`);
  await mouseOn(".kapi-menu-button");
  await until(menuOpen);
  assert.deepEqual(await labels(), ["Export", "Theme", "Copy path", "End session"]);
  await page.key("Escape");
});

test("#26 an ended session shows in the chat until the human sends a new message", async () => {
  const status = () => page.eval(`document.querySelector(".kapi-status").textContent`);
  const dot = () => page.eval(`document.querySelector(".kapi-panel .kapi-dot").dataset.state`);
  server.push({ kind: "presence", text: "watching" });
  server.push({ author: "human", kind: "message", text: "thanks" });
  server.push({ author: "human", kind: "end" });
  await page.waitFor(`!!document.querySelector(".kapi-log .kapi-end")`);
  assert.match(await page.eval(`document.querySelector(".kapi-end").textContent`), /^You ended the session · \d/);
  assert.equal(await status(), "Session ended. Send a message to start again.");
  assert.equal(await dot(), "ended");
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg").length`), 1);

  server.push({ author: "agent", kind: "message", text: "bye" });
  server.push({ kind: "presence", text: "none" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 2`);
  assert.equal(await status(), "Session ended. Send a message to start again.");

  await page.eval(`location.reload()`);
  await page.waitFor(`document.readyState === "complete" && !!document.querySelector(".kapi-end")`);
  assert.equal(await status(), "Session ended. Send a message to start again.");

  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("one more thing");
  await page.key("Enter");
  await until(async () => (await status()) === "Agent idle · messages are saved");
  assert.equal(await dot(), "none");
  assert.deepEqual(requests("POST", "/messages")[0].body, { kind: "message", text: "one more thing" });
});
