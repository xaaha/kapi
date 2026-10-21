import { test } from "node:test";
import assert from "node:assert/strict";
import worker from "../src/index.js";
import { FakeR2 } from "./fake-r2.js";

const TOKEN = "s3cret-token-value";
const ID = "0123456789abcdef";
const BASE = "https://hub.example.test";

function setup(options) {
  return { PAGES: new FakeR2(options), KAPI_HUB_TOKEN: TOKEN };
}

function call(env, method, path, { headers = {}, body } = {}) {
  return worker.fetch(new Request(BASE + path, { method, headers, body }), env);
}

function put(env, id, rev, html, { token = TOKEN, title = "plan.html", headers = {} } = {}) {
  return call(env, "PUT", `/api/pages/${id}/revs/${rev}`, {
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "text/html; charset=utf-8",
      "Kapi-Title": encodeURIComponent(title),
      ...headers,
    },
    body: html,
  });
}

test("a published revision is served as the latest page with a script-free CSP", async () => {
  const env = setup();
  const res = await put(env, ID, "1", "<p>hello</p>");
  assert.equal(res.status, 201);
  assert.deepEqual(await res.json(), { id: ID, rev: "1" });

  const page = await call(env, "GET", `/p/${ID}/`);
  assert.equal(page.status, 200);
  assert.equal(await page.text(), "<p>hello</p>");
  const csp = page.headers.get("Content-Security-Policy");
  assert.match(csp, /default-src 'none'/);
  assert.doesNotMatch(csp, /script-src/);
  assert.equal(page.headers.get("X-Content-Type-Options"), "nosniff");
});

test("PUT without a bearer token is rejected and stores nothing", async () => {
  const env = setup();
  const res = await call(env, "PUT", `/api/pages/${ID}/revs/1`, {
    headers: { "Content-Type": "text/html" },
    body: "<p>x</p>",
  });
  assert.equal(res.status, 401);
  assert.equal(res.headers.get("WWW-Authenticate"), "Bearer");
  assert.equal(env.PAGES.objects.size, 0);
});

test("PUT with a wrong token is rejected", async () => {
  for (const token of ["wrong", TOKEN + "x", TOKEN.slice(0, -1), ""]) {
    const env = setup();
    const res = await put(env, ID, "1", "<p>x</p>", { token });
    assert.equal(res.status, 401, `token ${JSON.stringify(token)}`);
    assert.equal(env.PAGES.objects.size, 0);
  }
});

test("PUT fails closed when the hub secret is not configured", async () => {
  const env = setup();
  delete env.KAPI_HUB_TOKEN;
  const res = await put(env, ID, "1", "<p>x</p>", { token: "undefined" });
  assert.equal(res.status, 503);
  assert.equal(env.PAGES.objects.size, 0);
});

test("PUT rejects bad ids, revisions, content types, titles and oversized bodies", async () => {
  const cases = [
    ["bad id", () => put(setup(), "NOT-HEX", "1", "<p>x</p>"), 400],
    ["short id", () => put(setup(), "abc", "1", "<p>x</p>"), 400],
    ["bad rev", () => put(setup(), ID, "1a", "<p>x</p>"), 400],
    ["wrong type", () => put(setup(), ID, "1", "{}", { headers: { "Content-Type": "application/json" } }), 415],
    ["bad title", () => put(setup(), ID, "1", "<p>x</p>", { headers: { "Kapi-Title": "%E0%A4%A" } }), 400],
    ["too large", () => put(setup(), ID, "1", "x".repeat(10 * 1024 * 1024 + 1)), 413],
  ];
  for (const [name, run, want] of cases) {
    assert.equal((await run()).status, want, name);
  }
});

test("the latest page is the most recently published revision", async () => {
  const env = setup();
  await put(env, ID, "1", "<p>one</p>");
  await put(env, ID, "2", "<p>two</p>");
  assert.equal(await (await call(env, "GET", `/p/${ID}/`)).text(), "<p>two</p>");
  assert.equal(await (await call(env, "GET", `/p/${ID}/revs/1/`)).text(), "<p>one</p>");
});

test("republishing a revision replaces it without duplicating it", async () => {
  const env = setup();
  await put(env, ID, "1", "<p>first try</p>");
  await put(env, ID, "1", "<p>retry</p>");
  assert.equal(await (await call(env, "GET", `/p/${ID}/`)).text(), "<p>retry</p>");
  const list = await (await call(env, "GET", `/p/${ID}/revs/`)).text();
  assert.equal(list.match(/revision 1</g).length, 1);
});

test("missing pages and revisions are 404", async () => {
  const env = setup();
  await put(env, ID, "1", "<p>x</p>");
  for (const path of [
    "/p/fedcba9876543210/",
    "/p/fedcba9876543210/revs/",
    `/p/${ID}/revs/2/`,
    "/p/not-an-id/",
    "/nope",
  ]) {
    assert.equal((await call(env, "GET", path)).status, 404, path);
  }
});

test("the revision list links every revision newest first, across list pages", async () => {
  const env = setup({ pageSize: 2 });
  for (const rev of ["1", "2", "3"]) await put(env, ID, rev, `<p>${rev}</p>`);
  const res = await call(env, "GET", `/p/${ID}/revs/`);
  assert.equal(res.status, 200);
  assert.match(res.headers.get("Content-Security-Policy"), /default-src 'none'/);
  const hrefs = [...(await res.text()).matchAll(/href="\.\/(\d+)\/"/g)].map((m) => m[1]);
  assert.deepEqual(hrefs, ["3", "2", "1"]);
});

test("the index lists every page with its title escaped", async () => {
  const env = setup({ pageSize: 1 });
  const other = "fedcba9876543210";
  await put(env, ID, "1", "<p>a</p>", { title: "plan <b>v2</b>.html" });
  await put(env, other, "5", "<p>b</p>", { title: "notes.html" });
  const res = await call(env, "GET", "/");
  assert.equal(res.status, 200);
  const body = await res.text();
  assert.match(body, /plan &lt;b&gt;v2&lt;\/b&gt;\.html/);
  assert.doesNotMatch(body, /<b>v2/);
  assert.ok(body.indexOf(`/p/${other}/`) < body.indexOf(`/p/${ID}/`), "newest page first");
});

test("the index says so when nothing is published", async () => {
  const body = await (await call(setup(), "GET", "/")).text();
  assert.match(body, /No pages published yet/);
});

test("a page path without the trailing slash redirects", async () => {
  const res = await call(setup(), "GET", `/p/${ID}`);
  assert.equal(res.status, 301);
  assert.equal(res.headers.get("Location"), `/p/${ID}/`);
});

test("HEAD returns headers without a body", async () => {
  const env = setup();
  await put(env, ID, "1", "<p>x</p>");
  const res = await call(env, "HEAD", `/p/${ID}/`);
  assert.equal(res.status, 200);
  assert.equal(await res.text(), "");
});

test("reads are read-only and the API only accepts PUT", async () => {
  const env = setup();
  const post = await call(env, "POST", `/p/${ID}/`, { body: "x" });
  assert.equal(post.status, 405);
  assert.equal(post.headers.get("Allow"), "GET, HEAD");
  const get = await call(env, "GET", `/api/pages/${ID}/revs/1`);
  assert.equal(get.status, 405);
  assert.equal(get.headers.get("Allow"), "PUT");
  assert.equal((await call(env, "GET", "/api/other")).status, 404);
});

test("raw paths with dot segments, encoded dots or slashes, or backslashes are rejected before routing", async () => {
  const env = setup();
  await put(env, ID, "1", "<p>secret</p>");
  for (const path of [
    `/api/%2e%2e/p/${ID}/`,
    `/api/%2E%2E/p/${ID}/`,
    `/api/%2e./p/${ID}/`,
    `/api/../p/${ID}/`,
    `/api/..%2fp/${ID}/`,
    `/api%2F..%2Fp/${ID}/`,
    `/p%2F${ID}/`,
    `/p%5C${ID}/`,
    `/api\\..\\p/${ID}/`,
    `/api\\p/${ID}/`,
  ]) {
    const raw = { url: BASE + path, method: "GET", headers: new Headers() };
    const res = await worker.fetch(raw, env);
    assert.equal(res.status, 400, path);
    assert.doesNotMatch(await res.text(), /secret/, path);
  }
  assert.equal((await call(env, "GET", `/p/${ID}/?q=..`)).status, 200, "query strings are not paths");
});
