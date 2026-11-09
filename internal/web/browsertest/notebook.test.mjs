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
const withSwitcher = (opts = {}) => browser.open(pageURL, { ready: "!!document.getElementById('kapi-chrome') && document.querySelectorAll('.kapi-switcher-row').length > 0", width: 1440, ...opts });
const refresh = async (p) => {
  await p.call("Page.bringToFront");
  await p.eval(`document.dispatchEvent(new Event("visibilitychange"))`);
};
const openSwitcher = (p) => p.eval(`document.querySelector(".kapi-switcher-launcher").click()`);

test("the token root without a trailing slash opens the notes home", async () => {
  const p = await home(base.replace(/\/$/, ""));
  try {
    assert.equal(await p.eval("location.pathname.endsWith('/')"), true);
    assert.equal(await p.eval("document.title"), "kapi");
    assert.equal(await p.eval("document.querySelector('h1').textContent"), "All notes");
    assert.ok(await p.eval("document.querySelectorAll('.nb-card').length") >= 3);
  } finally {
    await p.close();
  }
});

test("home filters the library by project and status", async () => {
  const p = await home();
  try {
    const titlesIn = async (name) => p.eval(`(() => { const s = document.querySelector(".nb-select"); s.value = [...s.options].find((o) => o.textContent === ${JSON.stringify(name)}).value; s.dispatchEvent(new Event("change", { bubbles: true })); return [...document.querySelectorAll(".nb-card-title")].map((t) => t.textContent).sort().join("|"); })()`);
    assert.equal(await titlesIn("Alpha"), "Rollout plan|Shared notes");
    assert.equal(await titlesIn("Beta"), "Bug list|Shared notes");
    await p.eval(`(() => { const s = document.querySelector(".nb-select"); s.value = ""; s.dispatchEvent(new Event("change", { bubbles: true })); })()`);
    assert.equal(await p.eval(`document.querySelectorAll(".nb-card").length`), 3);
  } finally {
    await p.close();
  }
});

test("the centered switcher shows current context and each destination once", async () => {
  const p = await withSwitcher();
  try {
    await openSwitcher(p);
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher").hidden`), false);
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher-current").textContent`), "Current · Rollout plan");
    assert.equal(await p.eval(`[...document.querySelectorAll(".kapi-switcher-row")].filter((r) => r.textContent.includes("Shared notes")).length`), 1);
    assert.equal(await p.eval(`getComputedStyle(document.querySelector(".kapi-switcher")).position`), "fixed");
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher-home").textContent.includes("Home")`), true);
  } finally {
    await p.close();
  }
});

test("searching the switcher narrows destinations and arrow keys enter the results", async () => {
  const p = await withSwitcher();
  try {
    await openSwitcher(p);
    await p.eval(`(() => { const s = document.querySelector(".kapi-switcher-search"); s.value = "bug"; s.dispatchEvent(new Event("input", { bubbles: true })); s.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })); })()`);
    assert.equal(await p.eval(`document.querySelectorAll(".kapi-switcher-row").length`), 1);
    assert.equal(await p.eval(`document.activeElement.textContent.includes("Bug list")`), true);
  } finally {
    await p.close();
  }
});

test("a refresh keeps keyboard focus on the same switcher link and home card", async () => {
  const s = await withSwitcher();
  const h = await home();
  try {
    await openSwitcher(s);
    const key = await s.eval(`(() => { const a = [...document.querySelectorAll(".kapi-switcher-row")].find((l) => l.textContent.includes("Bug list")); a.focus(); return a.dataset.key; })()`);
    const card = await h.eval(`(() => { const c = [...document.querySelectorAll(".nb-card")].find((l) => l.textContent.includes("Bug list")); c.focus(); return c.dataset.key; })()`);
    await refresh(s);
    await refresh(h);
    await new Promise((r) => setTimeout(r, 300));
    assert.equal(await s.eval("document.activeElement.dataset.key"), key);

    write(join(projectB, "more.html"), "More notes", "Added while focused.");
    kapi(projectB, "open", "more.html");
    await refresh(s);
    await s.waitFor(`[...document.querySelectorAll(".kapi-switcher-row")].some((t) => t.textContent.includes("More notes"))`);
    await refresh(h);
    await h.waitFor(`[...document.querySelectorAll(".nb-card-title")].some((t) => t.textContent === "More notes")`);
    assert.equal(await s.eval("document.activeElement.dataset.key"), key);
    assert.equal(await h.eval("document.activeElement.dataset.key"), card);
  } finally {
    await s.close();
    await h.close();
  }
});

test("printing a page leaves the switcher and launcher out", async () => {
  const p = await withSwitcher();
  try {
    await openSwitcher(p);
    await p.call("Emulation.setEmulatedMedia", { media: "print" });
    assert.equal(await p.eval(`getComputedStyle(document.querySelector(".kapi-switcher")).display`), "none");
    assert.equal(await p.eval(`getComputedStyle(document.querySelector(".kapi-switcher-launcher")).display`), "none");
    assert.equal(await p.eval(`getComputedStyle(document.documentElement).paddingLeft`), "0px");
  } finally {
    await p.close();
  }
});

test("on a phone the switcher is a bottom sheet and nothing scrolls sideways", async () => {
  const p = await withSwitcher({ width: 390, height: 844 });
  const h = await home(base, { width: 390, height: 844 });
  try {
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher").hidden`), true);
    await openSwitcher(p);
    assert.equal(await p.eval(`getComputedStyle(document.querySelector(".kapi-switcher")).bottom`), "8px");
    for (const page of [p, h]) assert.equal(await page.eval("document.documentElement.scrollWidth <= innerWidth"), true);
  } finally {
    await p.close();
    await h.close();
  }
});

test("deleted notes disappear from the switcher and home", async () => {
  const path = join(projectA, "temporary.html");
  write(path, "Temporary note", "Delete me.");
  kapi(projectA, "open", "temporary.html");
  const p = await withSwitcher();
  const h = await home();
  try {
    await refresh(p);
    await p.waitFor(`[...document.querySelectorAll(".kapi-switcher-row")].some((t) => t.textContent.includes("Temporary note"))`);
    unlinkSync(path);
    await refresh(p);
    await refresh(h);
    await p.waitFor(`![...document.querySelectorAll(".kapi-switcher-row")].some((t) => t.textContent.includes("Temporary note"))`);
    await h.waitFor(`![...document.querySelectorAll(".nb-card-title")].some((t) => t.textContent === "Temporary note")`);
  } finally {
    await p.close();
    await h.close();
  }
});

test("an ended session is red and can be isolated on home", async () => {
  const p = await withSwitcher();
  const h = await home();
  try {
    await p.eval(`(async () => {
      const pages = await fetch(${JSON.stringify(base)} + "api/notebook/pages").then((r) => r.json());
      const bugs = pages.find((item) => item.title === "Bug list");
      await fetch(${JSON.stringify(base)} + "p/" + bugs.id + "/end", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
    })()`);
    await refresh(p);
    await refresh(h);
    await p.waitFor(`[...document.querySelectorAll(".kapi-switcher-row[data-ended]")].some((a) => a.textContent.includes("Bug list"))`);
    assert.equal(await p.eval(`[...document.querySelectorAll(".kapi-switcher-row")].find((a) => a.textContent.includes("Bug list")).querySelector(".nb-dot").dataset.state`), "ended");
    await h.eval(`[...document.querySelectorAll(".nb-status-button")].find((b) => b.textContent === "Ended").click()`);
    assert.equal(await h.eval(`[...document.querySelectorAll(".nb-card-title")].map((n) => n.textContent).join("|")`), "Bug list");
  } finally {
    await p.close();
    await h.close();
  }
});
