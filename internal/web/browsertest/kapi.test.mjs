import { after, before, beforeEach, afterEach, test } from "node:test";
import assert from "node:assert/strict";
import { launch } from "./chrome.mjs";
import { startFakeServer } from "./fake-server.mjs";

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
  server.push({ kind: "presence", text: "busy" });
  await page.waitFor(`document.querySelector(".kapi-panel .kapi-dot").dataset.state === "busy"`);
  server.push({ kind: "presence", text: "watching" });
  await page.waitFor(`document.querySelector(".kapi-status").textContent === "Agent is watching"`);
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
  await click('.kapi-bar [data-action="done"]');
  await until(async () => /page changed/.test(await notice()));
  assert.equal(await text("cccccccc"), "Newer list");
  assert.equal(await page.eval(`document.getElementById("kapi-page").dataset.rev`), "2");
  assert.equal(requests("POST", "/ops")[0].body.rev, 1);
});

test("shows the 422 inbound-link error and keeps the block", async () => {
  await select("bbbbbbbb");
  await click('.kapi-bar [data-action="delete"]');
  await until(async () => (await notice()) === "#bbbbbbbb is linked from #dddddddd");
  assert.equal(await page.eval(`document.querySelector(".kapi-notice").dataset.kind`), "error");
  assert.notEqual(await text("bbbbbbbb"), null);
  await click('.kapi-bar [data-action="edit"]');
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
  assert.equal(await notice(), "");
});

test("deletes an unlinked block", async () => {
  await select("cccccccc");
  await click('.kapi-bar [data-action="delete"]');
  await until(async () => (await text("cccccccc")) === null);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "delete", id: "cccccccc" } });
});

test("sends a chat message with Ctrl+Enter, targeted at the selected block", async () => {
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("Looks good");
  await page.key("Enter", { ctrl: true });
  await page.waitFor(`document.querySelector(".kapi-msg .kapi-msg-text")?.textContent === "Looks good"`);
  assert.deepEqual(requests("POST", "/messages")[0].body, { kind: "message", target: "", text: "Looks good" });
  await page.waitFor(`document.querySelector(".kapi-composer textarea").value === ""`);

  await select("bbbbbbbb");
  assert.match(await page.eval(`document.querySelector(".kapi-target").textContent`), /#bbbbbbbb/);
  await page.eval(`document.querySelector(".kapi-composer textarea").focus()`);
  await page.type("Why two steps?");
  await click(".kapi-composer .kapi-primary");
  await until(() => requests("POST", "/messages").length === 2);
  assert.deepEqual(requests("POST", "/messages")[1].body, { kind: "message", target: "bbbbbbbb", text: "Why two steps?" });
  await page.waitFor(`document.querySelectorAll(".kapi-msg").length === 2`);
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-msg")[1].querySelector(".kapi-ref").textContent`), "#bbbbbbbb");
});

test("edits a block in place and posts its new outer HTML", async () => {
  await select("bbbbbbbb");
  await click('.kapi-bar [data-action="edit"]');
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
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
  await click('.kapi-bar [data-action="edit"]');
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
  await click('.kapi-bar [data-action="edit"]');
  await page.waitFor(`document.activeElement.dataset.kapiId === "bbbbbbbb"`);
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
  assert.equal(await page.eval(`document.querySelector(".kapi-message .kapi-ref").textContent`), `#${evil}`);
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
  await click('.kapi-bar [data-action="edit"]');
  await page.waitFor(`document.activeElement.dataset.kapiId === "aaaaaaaa"`);
  server.bumpBehindBrowser("cccccccc", '<ul data-kapi-id="cccccccc"><li>Agent rewrote this</li></ul>');
  server.push({ kind: "revision", target: "aaaaaaaa,cccccccc", text: "edited" });
  await until(async () => (await text("cccccccc")) === "Agent rewrote this");
  assert.equal(requests("GET", "/blocks/aaaaaaaa").length, 0);
  assert.equal(await page.eval(`document.activeElement.dataset.kapiId`), "aaaaaaaa");
  await page.key("Escape");
  await until(() => requests("GET", "/blocks/aaaaaaaa").length === 1);
});

test("edits a preset block as source in a textarea", async () => {
  await select("ffffffff");
  await click('.kapi-bar [data-action="edit"]');
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  assert.equal(requests("GET", "/source/ffffffff").length, 1);
  assert.equal(await page.eval(`document.activeElement.value`), server.state.blocks.find((b) => b.id === "ffffffff").source);
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 0);
  assert.equal(await page.eval(`getComputedStyle(document.activeElement).fontFamily.includes("mono")`), true);

  const next = '<kapi-callout data-kapi-id="ffffffff" kind="warning">Copy at night.</kapi-callout>';
  await page.eval(`document.activeElement.value = ${JSON.stringify(next)}`);
  await page.key("Enter", { ctrl: true });
  await until(() => requests("POST", "/ops").length === 1);
  assert.deepEqual(requests("POST", "/ops")[0].body, { rev: 1, op: { kind: "edit", id: "ffffffff", html: next } });
  await until(async () => (await text("ffffffff")) === "Copy at night.");
  assert.equal(await page.eval(`document.querySelectorAll(".kapi-source").length`), 0);
});

test("Escape cancels a source edit and shows the block again", async () => {
  await select("ffffffff");
  await click('.kapi-bar [data-action="edit"]');
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  await page.type("junk");
  await page.key("Escape");
  await page.waitFor(`!document.querySelector(".kapi-source")`);
  assert.equal(await page.eval(`document.querySelector('[data-kapi-id="ffffffff"]').checkVisibility()`), true);
  assert.equal(requests("POST", "/ops").length, 0);
});

test("the toolbar does not show the raw block id", async () => {
  await select("cccccccc");
  await page.waitFor(`!document.querySelector(".kapi-bar").hidden`);
  assert.doesNotMatch(await page.eval(`document.querySelector(".kapi-bar").textContent`), /#/);
});

test("the selection ring sits on the content box", async () => {
  await select("cccccccc");
  await page.waitFor(`!!document.querySelector(".kapi-ring:not([hidden])")`);
  const [ring, content] = await page.eval(`(() => {
    const el = document.querySelector('[data-kapi-id="cccccccc"]');
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    const ring = document.querySelector(".kapi-ring").getBoundingClientRect();
    return [[ring.left, ring.right], [r.left + parseFloat(s.paddingLeft) + parseFloat(s.borderLeftWidth), r.right - parseFloat(s.paddingRight) - parseFloat(s.borderRightWidth)]];
  })()`);
  assert.ok(Math.abs(ring[0] - content[0]) <= 1, `ring left ${ring[0]} vs content ${content[0]}`);
  assert.ok(Math.abs(ring[1] - content[1]) <= 1, `ring right ${ring[1]} vs content ${content[1]}`);
});

test("on narrow screens chat starts as a bar and opens to at most 40% of the height", async () => {
  const narrow = await browser.open(server.url, { width: 390, height: 800 });
  try {
    await narrow.waitFor(`!document.documentElement.classList.contains("kapi-panel-open")`);
    assert.equal(await narrow.eval(`document.querySelector(".kapi-opener").getBoundingClientRect().width`), 390);
    await narrow.eval(`document.querySelector(".kapi-opener").click()`);
    await narrow.waitFor(`document.documentElement.classList.contains("kapi-panel-open")`);
    assert.ok((await narrow.eval(`document.querySelector(".kapi-panel").getBoundingClientRect().height`)) <= 320);
    assert.deepEqual(narrow.errors, []);
  } finally {
    await narrow.close();
  }
});

test("Undo and Hide render as quiet bordered buttons", async () => {
  const styles = await page.eval(`[...document.querySelectorAll(".kapi-panel-head button")].map((b) => {
    const s = getComputedStyle(b);
    return [b.textContent, s.borderTopWidth, s.borderTopStyle];
  })`);
  assert.deepEqual(styles, [["Undo", "1px", "solid"], ["Hide", "1px", "solid"]]);
});

test("every request carries the token prefix and every POST is JSON", async () => {
  await page.eval(`document.querySelector(".kapi-composer textarea").value = "hi"`);
  await click(".kapi-composer .kapi-primary");
  await select("cccccccc");
  await click('.kapi-bar [data-action="done"]');
  await click('kapi-form button[type="submit"]');
  await until(() => requests("POST", "").length === 3);
  const prefix = new URL(server.url).pathname.split("/p/")[0] + "/";
  assert.deepEqual(server.state.requests.filter((r) => !r.path.startsWith(prefix) && r.path !== "/favicon.ico"), []);
  for (const r of requests("POST", "")) assert.equal(r.headers["content-type"], "application/json");
});

test("sortable tables sort by a header click, numeric or by locale, without an op", async () => {
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
  await click('.kapi-bar [data-action="edit"]');
  await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
  assert.equal(requests("GET", "/source/hhhhhhhh").length, 1);
  assert.equal(await page.eval(`document.querySelectorAll("[contenteditable]").length`), 0);
});
