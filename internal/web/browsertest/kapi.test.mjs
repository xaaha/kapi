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

test("sends a chat message with Enter, targeted at the selected block", async () => {
  assert.equal(await page.eval(`document.querySelector(".kapi-hint").textContent`), "Enter to send, Shift+Enter for a new line");
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("Looks good");
  await page.key("Enter");
  await page.waitFor(`document.querySelector(".kapi-msg .kapi-msg-text")?.textContent === "Looks good"`);
  assert.deepEqual(requests("POST", "/messages")[0].body, { kind: "message", target: "", text: "Looks good" });
  await page.waitFor(`document.querySelector(".kapi-composer textarea").value === "" && !document.querySelector(".kapi-composer .kapi-primary").disabled`);

  await select("bbbbbbbb");
  assert.equal(await page.eval(`document.querySelector(".kapi-target").textContent`), "About: Move the session store to the new clust…  ×");
  assert.equal(await page.eval(`document.querySelector(".kapi-target").title`), "#bbbbbbbb");
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("Why two steps?");
  await click(".kapi-composer .kapi-primary");
  await until(() => requests("POST", "/messages").length === 2);
  assert.deepEqual(requests("POST", "/messages")[1].body, { kind: "message", target: "bbbbbbbb", text: "Why two steps?" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 2`);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg")[1].querySelector(".kapi-ref").textContent`), "Move the session store to the new clust…");
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg")[1].querySelector(".kapi-ref").title`), "#bbbbbbbb");
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
  assert.equal(requests("POST", "/ops")[0].body.op.inner, "if a &lt; b {\n\tswap()\n}\nmore");
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
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "edit", id: "ffffffff", inner: "Copy during the <em>quiet</em> window. Then verify." } });
  await page.waitFor(`!document.querySelector("[contenteditable]")`);
  assert.equal(server.state.blocks.find((b) => b.id === "ffffffff").source, '<kapi-callout data-kapi-id="ffffffff" kind="note">Copy during the <em>quiet</em> window. Then verify.</kapi-callout>');
});

test("a prose preset whose body does not round-trip to its source is edited as source", async () => {
  const b = server.state.blocks.find((x) => x.id === "ffffffff");
  b.html = b.html.replace("<em>quiet</em>", "<b>quiet</b>");
  await page.eval(`location.reload()`);
  await page.waitFor(`document.readyState === "complete" && !!document.querySelector('[data-kapi-id="ffffffff"] b')`);
  await select("ffffffff");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  assert.equal(await page.eval(`document.activeElement.value`), "Copy during the <em>quiet</em> window.");
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 0);
});

test("edits table cells in place as text and sends only the changed cells", async () => {
  await click('[data-kapi-id="gggggggg"] th');
  await page.waitFor(`document.querySelector('[data-kapi-id="gggggggg"]').hasAttribute("data-kapi-selected")`);
  await click('[data-kapi-id="gggggggg"] th');
  const order = () => page.eval(`[...document.querySelectorAll('[data-kapi-id="gggggggg"] tbody td:first-child')].map((td) => td.textContent).join(",")`);
  assert.notEqual(await order(), "beta,Alpha,gamma");
  await editSelected();
  await page.waitFor(`document.querySelectorAll('[data-kapi-id="gggggggg"] [contenteditable="plaintext-only"]').length === 8`);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-source").length`), 0);
  await page.eval(`(() => {
    const cell = [...document.querySelectorAll('[data-kapi-id="gggggggg"] td')].find((td) => td.textContent === "beta");
    cell.focus();
    getSelection().selectAllChildren(cell);
  })()`);
  await page.type("b<e>ta & co");
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "cells", id: "gggggggg", cells: [{ row: 1, col: 0, text: "b<e>ta & co" }] } });
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
  assert.equal(requests("POST", "/ops")[0].body.op.kind, "cells");
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

test("edits a block in place and posts its new outer HTML", async () => {
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
      kind: "edit",
      id: "bbbbbbbb",
      html: '<p data-kapi-id="bbbbbbbb">Move the session store to the new cluster in two steps. Then verify.</p>',
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

test("moves blocks up and down and undoes", async () => {
  await select("cccccccc");
  await click('.kapi-bar [data-action="up"]');
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body.op, { kind: "move", id: "cccccccc", before: "bbbbbbbb" });
  const order = () => page.eval(`[...document.querySelectorAll("#kapi-page > [data-kapi-id]")].map(e => e.dataset.kapiId).join(",")`);
  await until(async () => (await order()).startsWith("aaaaaaaa,cccccccc,bbbbbbbb"));

  await page.waitFor(`!document.querySelector('.kapi-bar [data-action="down"]').disabled`);
  await click('.kapi-bar [data-action="down"]');
  await until(() => requests("POST", "/ops").length === 2);
  assert.deepEqual(requests("POST", "/ops")[1].body, { rev: 2, op: { kind: "move", id: "cccccccc", before: "dddddddd" } });

  await page.waitFor(`document.getElementById("kapi-page").dataset.rev === "3"`);
  await click('.kapi-panel [data-action="undo"]');
  await until(() => requests("POST", "/ops").length === 3);
  assert.deepEqual(requests("POST", "/ops")[2].body, { rev: 3, op: { kind: "undo", id: "" } });
  await until(async () => (await order()).startsWith("aaaaaaaa,cccccccc,bbbbbbbb"));
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
  server.bumpBehindBrowser("cccccccc", '<ul data-kapi-id="cccccccc"><li>Agent rewrote this</li></ul>');
  server.push({ kind: "revision", target: "cccccccc", text: "edited" });
  await until(async () => (await text("cccccccc")) === "Agent rewrote this");
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
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "edit", id: "jjjjjjjj", inner: "swap()" } });
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

test("edits prose that contains a preset as source", async () => {
  await select("hhhhhhhh");
  await editSelected();
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  assert.deepEqual(requests("GET", "/source/hhhhhhhh").map((r) => r.search), ["?part=inner"]);
  assert.equal(await page.eval(`document.activeElement.value`), '<p>Before the cutover:</p><kapi-callout kind="note">Freeze writes.</kapi-callout>');
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 0);
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
  await page.waitFor(`(b => b.bottom <= document.querySelector('[data-kapi-id="cccccccc"]').getBoundingClientRect().bottom + 1)(document.querySelector(".kapi-bar").getBoundingClientRect())`);
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

test("in a narrow window the toolbar goes inside the ring above the block's content", async () => {
  const narrow = await browser.open(server.url, { width: 900, height: 700 });
  try {
    await narrow.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').click()`);
    await narrow.waitFor(`document.querySelector(".kapi-bar").dataset.place === "inside"`);
    assert.equal(await narrow.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').hasAttribute("data-kapi-bar-room")`), true);
    const [bar, el] = await narrow.eval(`[".kapi-bar", '[data-kapi-id="bbbbbbbb"]'].map((q) => (({ left, right, top, bottom }) => ({ left, right, top, bottom }))(document.querySelector(q).getBoundingClientRect()))`);
    assert.ok(bar.top >= el.top && bar.right <= el.right && bar.left >= el.left, JSON.stringify({ bar, el }));
    assert.deepEqual(await narrow.eval(OVERLAPS), []);
    await narrow.eval(`document.querySelector("[data-kapi-selected]").dispatchEvent(new MouseEvent("dblclick", { bubbles: true }))`);
    await narrow.waitFor(`!!document.querySelector('.kapi-bar .kapi-primary[data-action="cancel"]') && document.querySelector(".kapi-bar").dataset.place === "inside"`);
    assert.deepEqual(await narrow.eval(OVERLAPS), []);
    await narrow.eval(`document.querySelector('.kapi-bar [data-action="cancel"]').click()`);
    await narrow.eval(`document.querySelector('[data-kapi-id="cccccccc"]').style.paddingBottom = "2400px"`);
    await narrow.eval(`document.querySelector('[data-kapi-id="cccccccc"]').click()`);
    await narrow.waitFor(`document.querySelector(".kapi-bar").dataset.place === "inside"`);
    await narrow.eval(`scrollTo(0, document.querySelector('[data-kapi-id="cccccccc"]').offsetTop + 900)`);
    await narrow.waitFor(`document.querySelector(".kapi-bar").parentElement.classList.contains("kapi-panel")`);
    assert.deepEqual(await narrow.eval(OVERLAPS), []);
    assert.equal(await narrow.eval(`!document.querySelector(".kapi-bar").hidden && document.querySelector(".kapi-bar").checkVisibility()`), true);
    await narrow.eval(`document.querySelector('[data-kapi-id="bbbbbbbb"]').click()`);
    await narrow.waitFor(`!document.querySelector('[data-kapi-id="cccccccc"]').hasAttribute("data-kapi-bar-room")`);
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
  assert.equal(label, "Editing source of: code");
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
  assert.equal(await page.eval(`document.querySelector('.kapi-bar [data-action="up"]').disabled`), true);
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
    ["archive", "Archive", false, true, 0],
    ["up", "Move up", false, true, 0],
    ["down", "Move down", false, true, 0],
    ["delete", "Delete", false, true, 0],
  ]);
  const tip = () => page.eval(`(b => getComputedStyle(b, "::after").content)(document.querySelector('.kapi-bar [data-action="delete"]'))`);
  assert.equal(await tip(), "none");
  const [x, y] = await page.eval(`(r => [r.left + r.width / 2, r.top + r.height / 2])(document.querySelector('.kapi-bar [data-action="delete"]').getBoundingClientRect())`);
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  assert.equal(await tip(), '"Delete"');
  assert.equal(await page.eval(`getComputedStyle(document.querySelector('.kapi-bar [data-action="delete"]'), "::after").transitionDuration`), "0s");
  await page.call("Input.dispatchMouseEvent", { type: "mouseMoved", x: 5, y: 5 });
  await page.eval(`document.querySelector('.kapi-bar [data-action="archive"]').focus()`);
  assert.equal(await page.eval(`(b => b.matches(":focus-visible") ? getComputedStyle(b, "::after").content : "not focus-visible")(document.querySelector('.kapi-bar [data-action="archive"]'))`), '"Archive"');
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
