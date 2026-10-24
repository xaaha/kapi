import { after, before, beforeEach, afterEach, test } from "node:test";
import assert from "node:assert/strict";
import { launch } from "./chrome.mjs";
import { startFakeServer } from "./fake-server.mjs";
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
  await page.waitFor(`document.querySelector(".kapi-status").textContent === "Agent is watching"`);
  server.push({ kind: "presence", text: "busy" });
  await page.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
});

test("says Queued until the agent reads a message, then working until it replies", async () => {
  const status = () => page.eval(`document.querySelector(".kapi-status").textContent`);
  const marks = () => page.eval(`[...document.querySelectorAll(".kapi-msg")].map((m) => m.querySelector(".kapi-queued, .kapi-read")?.textContent || "")`);
  server.push({ kind: "presence", text: "watching" });
  server.push({ author: "human", kind: "message", text: "please look" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 1`);
  assert.equal(await status(), "Agent is watching");
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
  await until(async () => (await status()) === "Agent is watching");
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
  assert.deepEqual(tools, [["ask", "Ask AI A", "A"], ["archive", "Archive E", "E"], ["delete", `Delete ${del}`, "Delete"]]);
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
  assert.equal(text, "Agent is watching");
  assert.ok(presence.top >= scroll.bottom - 1 && presence.bottom <= composer.top + 1, JSON.stringify({ presence, scroll, composer }));
  const before = await top();
  await page.eval(`document.querySelector(".kapi-scroll").scrollTop = 0`);
  assert.equal(await top(), before);
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
  const styles = await page.eval(`[...document.querySelectorAll(".kapi-panel-head > button")].map((b) => {
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

  await toggle("h2aaaaaa");
  assert.equal(await shown("p2p2p2p2"), false);
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
  assert.equal(requests("POST", "/ops").length, 0);
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
  const line = await page.eval(`(r => r.top)([...getSelection().getRangeAt(0).getClientRects()].at(-1))`);
  assert.ok((await barBox()).bottom <= line, "the toolbar sits above the selected line");
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
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg")[1].querySelector(".kapi-ref").textContent`), "table: Shard Rows beta 10 Alpha 9 gamma 1,200, row 2, Shard");
  assert.equal(await page.eval(`localStorage.length`), 0);
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

  assert.deepEqual(await page.eval(`Object.keys(localStorage)`), [`kapi-drafts:${new URL(server.url).pathname.split("/").filter(Boolean).pop()}`]);
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
    assert.deepEqual(await phone.eval(`[...document.querySelectorAll(".kapi-bar button")].map((b) => b.dataset.action)`), ["ask", "edit", "archive", "delete"]);
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

test("#39 two tabs on one page keep each other's queued notes", async () => {
  const other = await browser.open(server.url);
  try {
    await other.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "none"`);
    for (const [tab, text] of [[page, "from one"], [other, "from two"]]) {
      await tab.eval(`document.querySelector('[data-kapi-id="cccccccc"]').click()`);
      await tab.key("a");
      await tab.waitFor(`!!document.querySelector(".kapi-ask")`);
      await tab.type(text);
      await tab.key("Enter");
      await tab.waitFor(`!document.querySelector(".kapi-ask")`);
    }
    await page.waitFor(`document.querySelectorAll(".kapi-queued-note").length === 2`);
    await click(".kapi-composer .kapi-primary");
    await until(() => requests("POST", "/notes").length === 1);
    assert.deepEqual(requests("POST", "/notes")[0].body.notes.map((n) => n.text), ["from one", "from two"]);
    await other.waitFor(`document.querySelector(".kapi-queue").hidden`);
  } finally {
    await other.close();
  }
});

test("#39 Escape after asking about a clicked cell leaves no text selected and the toolbar whole", async () => {
  await mouseClick('[data-kapi-id="gggggggg"] tbody tr:nth-child(2) td');
  await page.key("a");
  await page.waitFor(`!!document.querySelector(".kapi-ask")`);
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-ask")`);
  assert.equal(await page.eval(`getSelection().toString()`), "");
  assert.deepEqual(await barActions(), ["ask", "archive", "delete"]);
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
