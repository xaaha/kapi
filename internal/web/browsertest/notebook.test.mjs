import { after, before, test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { launch } from "./chrome.mjs";

const repo = fileURLToPath(new URL("../../../", import.meta.url));
const dir = mkdtempSync("/tmp/kn-");
const bin = join(dir, "kapi");
const env = { ...process.env, KAPI_HOME: join(dir, "home"), KAPI_NO_BROWSER: "1" };
const projectA = join(dir, "alpha");
const projectB = join(dir, "beta");
const kapi = (cwd, ...args) => execFileSync(bin, args, { cwd, env }).toString();
const write = (path, title, text) => writeFileSync(path, `<h1>${title}</h1>\n<p>${text}</p>\n`);

let browser;
let base;
let pageURL;

before(async () => {
  execFileSync("go", ["build", "-o", bin, "./cmd/kapi"], { cwd: repo });
  mkdirSync(projectA);
  mkdirSync(projectB);
  write(join(projectA, "plan.html"), "Rollout plan", "Two steps.");
  write(join(projectA, "shared.html"), "Shared notes", "Opened from both projects.");
  write(join(projectB, "bugs.html"), "Bug list", "Three open.");
  pageURL = kapi(projectA, "open", "plan.html").split("\n")[0];
  kapi(projectA, "open", "shared.html");
  kapi(projectB, "open", "../alpha/shared.html");
  kapi(projectB, "open", "bugs.html");
  base = pageURL.replace(/p\/[0-9a-f]+\/$/, "");
  browser = await launch();
});

after(async () => {
  await browser?.close();
  try {
    execFileSync("pkill", ["-f", bin]);
  } catch {}
  for (let i = 0; i < 100; i++) {
    try {
      execFileSync("pgrep", ["-f", bin]);
    } catch {
      break;
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
});

const home = (url = base, opts = {}) => browser.open(url, { ready: "document.querySelectorAll('.nb-card').length > 0", ...opts });
const withSidebar = (opts = {}) => browser.open(pageURL, { ready: "!!document.getElementById('kapi-chrome') && document.querySelectorAll('.kapi-nb-page').length > 0", width: 1440, ...opts });
const refresh = async (p) => {
  await p.call("Page.bringToFront");
  await p.eval(`document.dispatchEvent(new Event("visibilitychange"))`);
};

test("the token root without a trailing slash opens the notebook home", async () => {
  const p = await home(base.replace(/\/$/, ""));
  try {
    assert.equal(await p.eval("location.pathname.endsWith('/')"), true);
    assert.ok(await p.eval("document.querySelectorAll('.nb-card').length") >= 3);
  } finally {
    await p.close();
  }
});

test("a page opened from two projects shows under both", async () => {
  const p = await home();
  try {
    const titlesIn = async (project) => {
      await p.eval(`[...document.querySelectorAll(".nb-chip")].find((c) => c.textContent === ${JSON.stringify(project)}).click()`);
      return p.eval(`[...document.querySelectorAll(".nb-card-title")].map((t) => t.textContent).sort().join("|")`);
    };
    assert.equal(await titlesIn("alpha"), "Rollout plan|Shared notes");
    assert.equal(await titlesIn("beta"), "Bug list|Shared notes");
    await p.eval(`[...document.querySelectorAll(".nb-chip")].find((c) => c.textContent === "All").click()`);
    assert.equal(await p.eval(`document.querySelectorAll(".nb-card").length`), 3, "All lists the shared page once");
  } finally {
    await p.close();
  }
  const s = await withSidebar();
  try {
    const groups = await s.eval(`JSON.stringify([...document.querySelectorAll(".kapi-nb-group")].map((g) => [g.querySelector("h2").textContent, [...g.querySelectorAll(".kapi-nb-title")].map((t) => t.textContent).sort()]))`);
    assert.deepEqual(Object.fromEntries(JSON.parse(groups)), { alpha: ["Rollout plan", "Shared notes"], beta: ["Bug list", "Shared notes"] });
  } finally {
    await s.close();
  }
});

test("a refresh keeps keyboard focus on the same sidebar link and home card", async () => {
  const s = await withSidebar();
  const h = await home();
  try {
    const key = await s.eval(`(() => { const a = [...document.querySelectorAll(".kapi-nb-page")].find((l) => l.textContent.includes("Bug list")); a.focus(); return a.dataset.key; })()`);
    const card = await h.eval(`(() => { const c = [...document.querySelectorAll(".nb-card")].find((l) => l.textContent.includes("Bug list")); c.focus(); return c.dataset.key; })()`);
    await refresh(s);
    await refresh(h);
    await new Promise((r) => setTimeout(r, 300));
    assert.equal(await s.eval("document.activeElement.dataset.key"), key, "an unchanged list leaves focus alone");

    write(join(projectB, "more.html"), "More notes", "Added while focused.");
    kapi(projectB, "open", "more.html");
    await refresh(s);
    await s.waitFor(`[...document.querySelectorAll(".kapi-nb-title")].some((t) => t.textContent === "More notes")`);
    await refresh(h);
    await h.waitFor(`[...document.querySelectorAll(".nb-card-title")].some((t) => t.textContent === "More notes")`);
    assert.equal(await s.eval("document.activeElement.dataset.key"), key, "the sidebar link keeps focus after the list changes");
    assert.equal(await h.eval("document.activeElement.dataset.key"), card, "the home card keeps focus after the list changes");
  } finally {
    await s.close();
    await h.close();
  }
});

test("printing a page leaves the notebook out", async () => {
  const s = await withSidebar();
  try {
    assert.equal(await s.eval(`getComputedStyle(document.querySelector(".kapi-nb")).display`), "flex");
    await s.call("Emulation.setEmulatedMedia", { media: "print" });
    assert.equal(await s.eval(`getComputedStyle(document.querySelector(".kapi-nb")).display`), "none");
    assert.equal(await s.eval(`getComputedStyle(document.querySelector(".kapi-nb-toggle")).display`), "none");
    assert.equal(await s.eval(`getComputedStyle(document.documentElement).paddingLeft`), "0px");
  } finally {
    await s.close();
  }
});

test("on a phone the sidebar starts closed, opens over the page and nothing scrolls sideways", async () => {
  const s = await withSidebar({ width: 390, height: 844 });
  const h = await home(base, { width: 390, height: 844 });
  try {
    assert.equal(await s.eval(`document.documentElement.hasAttribute("data-kapi-notebook")`), false);
    await s.eval(`document.querySelector(".kapi-nb-toggle").click()`);
    assert.equal(await s.eval(`getComputedStyle(document.querySelector(".kapi-nb")).position`), "fixed");
    assert.equal(await s.eval(`getComputedStyle(document.documentElement).paddingLeft`), "0px");
    for (const p of [s, h]) assert.equal(await p.eval("document.documentElement.scrollWidth <= innerWidth"), true);
  } finally {
    await s.close();
    await h.close();
  }
});

test("deleted pages disappear from the sidebar instead of opening an error", async () => {
  const path = join(projectA, "temporary.html");
  write(path, "Temporary page", "Delete me.");
  kapi(projectA, "open", "temporary.html");
  const s = await withSidebar();
  try {
    await refresh(s);
    await s.waitFor(`[...document.querySelectorAll(".kapi-nb-title")].some((t) => t.textContent === "Temporary page")`);
    unlinkSync(path);
    await refresh(s);
    await s.waitFor(`![...document.querySelectorAll(".kapi-nb-title")].some((t) => t.textContent === "Temporary page")`);
  } finally {
    await s.close();
  }
});

test("an ended session mutes its sidebar title and turns its dot red", async () => {
  const s = await withSidebar();
  try {
    await s.eval(`(async () => {
      const pages = await fetch(${JSON.stringify(base)} + "api/notebook/pages").then((r) => r.json());
      const bugs = pages.find((p) => p.title === "Bug list");
      await fetch(${JSON.stringify(base)} + "p/" + bugs.id + "/end", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
    })()`);
    await refresh(s);
    await s.waitFor(`[...document.querySelectorAll(".kapi-nb-page[data-ended]")].some((a) => a.textContent.includes("Bug list"))`);
    assert.equal(await s.eval(`[...document.querySelectorAll(".kapi-nb-page")].find((a) => a.textContent.includes("Bug list")).querySelector(".nb-dot").dataset.state`), "ended");
  } finally {
    await s.close();
  }
});
