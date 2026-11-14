import { after, before, test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
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

const home = (url = base, opts = {}) => browser.open(url, { ready: "!!document.querySelector('.nb-home') && document.querySelector('.nb-count').textContent !== ''", ...opts });
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
    assert.deepEqual(await p.eval(`[...document.querySelectorAll(".nb-status-button")].map((button) => [button.textContent, button.getAttribute("aria-pressed")])`), [["Active", "false"], ["Inactive", "false"], ["Ended", "false"], ["All", "true"]]);
    await p.eval(`[...document.querySelectorAll(".nb-status-button")].find((button) => button.textContent === "All").click()`);
    assert.ok(await p.eval("document.querySelectorAll('.nb-card').length") >= 3);
    assert.equal(await p.eval("document.querySelector('.nb-glyph') !== null"), true);
  } finally {
    await p.close();
  }
});

test("home has the shared search trigger and universal theme control", async () => {
  const p = await home();
  try {
    assert.equal(await p.eval(`document.querySelector(".nb-top .kapi-theme-button") !== null`), true);
    assert.equal(await p.eval(`!!document.querySelector(".nb-top .kapi-search-button")`), true);
    assert.equal(await p.eval(`document.querySelector(".nb-top input")`), null);
    await p.eval(`document.querySelector(".nb-top .kapi-theme-button").click()`);
    await p.waitFor(`!document.querySelector(".nb-top .kapi-theme-menu").hidden`);
    await p.eval(`document.querySelector('.nb-top [data-theme-choice="dark"]').click()`);
    await p.waitFor(`document.documentElement.dataset.theme === "dark"`);
    await p.eval(`document.querySelector(".nb-top .kapi-theme-button").click(); document.querySelector('.nb-top [data-theme-choice="system"]').click()`);
  } finally {
    await p.close();
  }
});

test("a theme chosen on home reaches an open page at once, survives a reload, and shows in a fresh browser", async () => {
  const h = await home();
  const p = await withSwitcher();
  const fresh = await launch();
  try {
    await h.eval(`document.querySelector(".nb-top .kapi-theme-button").click()`);
    await h.eval(`document.querySelector('.nb-top [data-theme-choice="dark"]').click()`);
    await h.eval(`document.querySelector(".nb-top .kapi-theme-button").click()`);
    await h.eval(`document.querySelector('.nb-top [data-palette="paper"]').click()`);
    await p.waitFor(`document.documentElement.dataset.theme === "dark" && document.documentElement.dataset.palette === "paper"`, 5000);
    const saved = JSON.parse(readFileSync(join(dir, "home", "settings.json"), "utf8"));
    assert.equal(saved.mode, "dark");
    assert.equal(saved.theme, "paper");
    await p.call("Page.reload");
    await p.waitFor(`document.readyState === "complete" && document.documentElement.dataset.theme === "dark" && document.documentElement.dataset.palette === "paper"`, 5000);
    const other = await fresh.open(pageURL);
    try {
      assert.equal(await other.eval(`document.documentElement.dataset.theme + " " + document.documentElement.dataset.palette`), "dark paper");
    } finally {
      await other.close();
    }
  } finally {
    await h.eval(`window.kapiTheme.setPalette("default"); window.kapiTheme.set("system")`);
    await p.waitFor(`document.documentElement.dataset.themeChoice === "system" && !document.documentElement.dataset.palette`, 5000);
    await fresh.close();
    await h.close();
    await p.close();
  }
});

test("home filters the library by project and status", async () => {
  const p = await home();
  try {
    await p.eval(`[...document.querySelectorAll(".nb-status-button")].find((button) => button.textContent === "All").click()`);
    const titlesIn = async (name) => p.eval(`(() => { const s = document.querySelector(".nb-select"); s.value = [...s.options].find((o) => o.textContent === ${JSON.stringify(name)}).value; s.dispatchEvent(new Event("change", { bubbles: true })); return [...document.querySelectorAll(".nb-card-title")].map((t) => t.textContent).sort().join("|"); })()`);
    assert.equal(await titlesIn("Alpha"), "Rollout plan|Shared notes");
    assert.equal(await titlesIn("Beta"), "Bug list|Shared notes");
    assert.equal(await p.eval(`[...document.querySelectorAll(".nb-card")].find((card) => card.textContent.includes("Shared notes")).querySelector(".nb-card-project").textContent`), "Beta");
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
    await p.eval(`document.querySelector(".kapi-switcher-home").focus()`);
    await p.key("Tab");
    assert.equal(await p.eval(`document.activeElement.classList.contains("kapi-switcher-search")`), true);
    await p.key("Tab", { shift: true });
    assert.equal(await p.eval(`document.activeElement.className`), "kapi-switcher-home");
  } finally {
    await p.close();
  }
});

test("Cmd or Ctrl+K then Enter returns to the most recently viewed note", async () => {
  const p = await withSwitcher();
  try {
    await openSwitcher(p);
    const bug = await p.eval(`[...document.querySelectorAll(".kapi-switcher-row")].find((row) => row.textContent.includes("Bug list")).href`);
    await p.eval(`location.href = ${JSON.stringify(bug)}`);
    await p.waitFor(`document.querySelector(".kapi-switcher-current")?.textContent === "Current · Bug list"`, 5000);
    const mod = (await p.eval(`/Mac|iPhone|iPad/.test(navigator.platform)`)) ? { meta: true } : { ctrl: true };
    await p.key("k", mod);
    await p.waitFor(`!document.querySelector(".kapi-switcher").hidden`);
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher-section h2").textContent`), "Recently viewed");
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher-row strong").textContent`), "Rollout plan");
    await p.key("Enter");
    await p.waitFor(`document.querySelector(".kapi-switcher-current")?.textContent === "Current · Rollout plan"`, 5000);
  } finally {
    await p.close();
  }
});

test("the switcher traps focus and makes the page inert", async () => {
  const p = await withSwitcher();
  try {
    await openSwitcher(p);
    assert.equal(await p.eval(`document.querySelector("#kapi-page").inert`), true);
    await p.eval(`(() => { const last = document.querySelector(".kapi-switcher-home"); last.focus(); last.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true })); })()`);
    assert.equal(await p.eval(`document.activeElement.classList.contains("kapi-switcher-search")`), true);
    await p.eval(`(() => { const first = document.querySelector(".kapi-switcher-search"); first.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true })); })()`);
    assert.equal(await p.eval(`document.activeElement.classList.contains("kapi-switcher-home")`), true);
    await p.eval(`document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }))`);
    assert.equal(await p.eval(`document.querySelector("#kapi-page").inert`), false);
  } finally {
    await p.close();
  }
});

test("home and switcher share the note search component", async () => {
  const h = await home();
  const s = await withSwitcher();
  try {
    await h.eval(`document.querySelector(".kapi-search-button").click()`);
    await openSwitcher(s);
    const shape = `(box => ({children: [...box.children].map((child) => child.tagName), height: getComputedStyle(box).height}))(document.querySelector(".kapi-search-input-row"))`;
    assert.deepEqual(await h.eval(shape), await s.eval(shape));
    for (const p of [h, s]) {
      assert.equal(await p.eval(`document.querySelectorAll('.kapi-search-dialog input').length`), 1);
      assert.equal(await p.eval(`document.querySelector('.kapi-search-button kbd').textContent.endsWith('K')`), true);
    }
    assert.deepEqual(await h.eval(`[...document.querySelectorAll('[data-search-scope]')].map(b => b.textContent)`), ["Notes"]);
    assert.deepEqual(await s.eval(`[...document.querySelectorAll('[data-search-scope]')].map(b => b.textContent)`), ["Notes", "This page"]);
  } finally {
    await h.close();
    await s.close();
  }
});

test("one Cmd/Ctrl+K dialog defaults to Notes, shares its input across scopes, and keeps Ctrl+N/P navigation", async () => {
  for (const width of [390, 1440]) {
    const p = await withSwitcher({ width, height: 900 });
    try {
      await p.key("k", { ctrl: true });
      await p.waitFor(`!document.querySelector('.kapi-search-dialog').hidden`);
      assert.equal(await p.eval(`document.querySelector('[data-search-scope="notes"]').getAttribute('aria-pressed')`), "true");
      await p.eval(`window.sharedSearchInput = document.querySelector('.kapi-search-input'); window.navigationEvents = []; addEventListener('keydown', e => e.ctrlKey && navigationEvents.push(e), true)`);
      const first = await p.eval(`sharedSearchInput.getAttribute('aria-activedescendant')`);
      await p.key("n", { ctrl: true });
      assert.notEqual(await p.eval(`sharedSearchInput.getAttribute('aria-activedescendant')`), first);
      await p.key("p", { ctrl: true });
      assert.equal(await p.eval(`sharedSearchInput.getAttribute('aria-activedescendant')`), first);
      assert.deepEqual(await p.eval(`navigationEvents.map(e => [e.key, e.defaultPrevented])`), [["n", true], ["p", true]]);
      await p.eval(`document.querySelector('[data-search-scope="page"]').click()`);
      await p.type("two");
      await p.waitFor(`!!document.querySelector('.kapi-search-hit')`);
      assert.equal(await p.eval(`sharedSearchInput === document.querySelector('.kapi-search-input') && document.querySelectorAll('.kapi-search-dialog input').length === 1`), true);
      const hit = await p.eval(`sharedSearchInput.getAttribute('aria-activedescendant')`);
      await p.key("p", { ctrl: true });
      assert.notEqual(await p.eval(`sharedSearchInput.getAttribute('aria-activedescendant')`), hit);
      await p.key("n", { ctrl: true });
      assert.equal(await p.eval(`sharedSearchInput.getAttribute('aria-activedescendant')`), hit);
      await p.eval(`document.querySelector('[data-search-scope="notes"]').click()`);
      assert.equal(await p.eval(`document.querySelector('.kapi-search').hidden`), true);
      assert.equal(await p.eval(`sharedSearchInput === document.querySelector('.kapi-search-input')`), true);
      await p.key("Escape");
      await p.key("k", { ctrl: true });
      assert.equal(await p.eval(`document.querySelector('[data-search-scope="notes"]').getAttribute('aria-pressed')`), "true");
      assert.deepEqual(p.errors, []);
    } finally { await p.close(); }
  }
});

test("note search uses word matching and selects the first result for Enter", async () => {
  const p = await withSwitcher();
  try {
    await openSwitcher(p);
    await p.eval(`(() => { const s = document.querySelector(".kapi-switcher-search"); s.value = "projects opened"; s.dispatchEvent(new Event("input", { bubbles: true })); })()`);
    assert.equal(await p.eval(`document.querySelectorAll(".kapi-switcher-row").length`), 1);
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher-row").textContent.includes("Shared notes")`), true);
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher-row").getAttribute("aria-selected")`), "true");
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher-search").getAttribute("aria-activedescendant") === document.querySelector(".kapi-switcher-row").id`), true);
  } finally {
    await p.close();
  }
});

test("Enter from unified home search opens the first matching note", async () => {
  const p = await home();
  try {
    await p.eval(`document.querySelector('.kapi-search-button').click()`);
    await p.eval(`(() => { const s = document.querySelector(".kapi-search-input"); s.value = "list bug"; s.dispatchEvent(new Event("input", { bubbles: true })); })()`);
    const destination = await p.eval(`document.querySelector('.kapi-switcher-row[aria-selected="true"]').href`);
    await p.key("Enter");
    await p.waitFor(`location.href === ${JSON.stringify(destination)} && !!document.querySelector('#kapi-page')`);
  } finally {
    await p.close();
  }
});

test("a refresh keeps keyboard focus on the same switcher link and home card", async () => {
  const s = await withSwitcher();
  const h = await home();
  try {
    await openSwitcher(s);
    await h.eval(`[...document.querySelectorAll(".nb-status-button")].find((button) => button.textContent === "All").click()`);
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
    const groups = await s.eval(`[...document.querySelectorAll(".kapi-switcher-section")].map((section) => [section.querySelector("h2").textContent, [...section.querySelectorAll(".kapi-switcher-row")].map((row) => row.textContent)])`);
    const newAt = groups.findIndex(([title]) => title === "New");
    const workingAt = groups.findIndex(([title]) => title === "Working now");
    assert.ok(newAt >= 0 && (workingAt < 0 || newAt < workingAt), JSON.stringify(groups));
    assert.equal(groups[newAt][1].some((text) => text.includes("More notes")), true);
    assert.equal(groups.some(([title]) => title === "Recent"), false);
    assert.equal(groups.some(([title]) => title === "Other notes"), true);
    await refresh(h);
    await h.waitFor(`[...document.querySelectorAll(".nb-card-title")].some((t) => t.textContent === "More notes")`);
    assert.equal(await s.eval("document.activeElement.dataset.key"), key);
    assert.equal(await h.eval("document.activeElement.dataset.key"), card);
  } finally {
    await s.close();
    await h.close();
  }
});

test("the Kapi brand on a note links home", async () => {
  const p = await withSwitcher();
  try {
    assert.equal(await p.eval(`document.querySelector(".kapi-site-home").getAttribute("href")`), new URL(base).pathname);
  } finally {
    await p.close();
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

test("on a phone unified search fits above the keyboard without sideways scrolling", async () => {
  const p = await withSwitcher({ width: 390, height: 844 });
  const h = await home(base, { width: 390, height: 844 });
  try {
    assert.equal(await p.eval(`document.querySelector(".kapi-switcher").hidden`), true);
    await openSwitcher(p);
    assert.equal(await p.eval(`((r) => r.left >= 0 && r.right <= innerWidth && r.top >= 0 && r.bottom <= innerHeight)(document.querySelector('.kapi-search-dialog').getBoundingClientRect())`), true);
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
    await h.eval(`[...document.querySelectorAll(".nb-status-button")].find((button) => button.textContent === "All").click()`);
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

test("home switches between cards and list, remembers the choice, and keeps filters", async () => {
  const p = await home();
  try {
    const before = await p.eval(`document.querySelectorAll(".nb-card").length`);
    await p.eval(`[...document.querySelectorAll(".nb-view-button")].find((b) => b.textContent === "List").click()`);
    assert.equal(await p.eval(`document.querySelector(".nb-grid").dataset.view`), "list");
    assert.equal(await p.eval(`document.querySelectorAll(".nb-card").length`), before);
    assert.equal(await p.eval(`getComputedStyle(document.querySelector(".nb-grid")).gridTemplateColumns.split(" ").length`), 1);
    await p.eval(`location.reload()`);
    await p.waitFor(`document.querySelector(".nb-grid")?.dataset.view === "list" && document.querySelector(".nb-count").textContent !== ""`);
    await p.eval(`document.querySelector('[data-status="inactive"]').click()`);
    assert.equal(await p.eval(`document.querySelector(".nb-filter-context strong").textContent`), "Quiet for now");
    await p.eval(`[...document.querySelectorAll(".nb-view-button")].find((b) => b.textContent === "Cards").click()`);
    assert.equal(await p.eval(`document.querySelector('[data-status="inactive"]').getAttribute("aria-pressed")`), "true");
    assert.equal(await p.eval(`document.querySelector(".nb-grid").dataset.view`), "cards");
    assert.deepEqual(p.errors, []);
  } finally { await p.close(); }
});

test("home exposes palette selection and keeps the logo and folder caret aligned in both themes", async () => {
  for (const width of [360, 760, 1440]) {
    const p = await home(base, { width, height: 900 });
    try {
      for (const mode of ["light", "dark"]) {
        await p.eval(`window.kapiTheme.set(${JSON.stringify(mode)})`);
        assert.equal(await p.eval(`((a) => getComputedStyle(a).color !== getComputedStyle(a).backgroundColor)(document.querySelector(".kapi-site-home"))`), true);
        assert.equal(await p.eval(`document.querySelector(".kapi-site-name").checkVisibility()`), true);
        assert.equal(await p.eval(`document.documentElement.scrollWidth <= innerWidth`), true);
      }
      await p.eval(`document.querySelector(".kapi-theme-button").click()`);
      await p.eval(`document.querySelector('.kapi-theme-menu [data-palette="paper"]').click()`);
      assert.equal(await p.eval(`window.kapiTheme.palette()`), "paper");
      const [arrow, select] = await p.eval(`[".nb-select-caret", ".nb-select"].map((s) => { const r = document.querySelector(s).getBoundingClientRect(); return { mid: r.top + r.height / 2, right: r.right }; })`);
      assert.ok(Math.abs(arrow.mid - select.mid) < 3);
      assert.ok(select.right - arrow.right >= 12);
      await p.eval(`window.kapiTheme.setPalette("default"); window.kapiTheme.set("system")`);
      assert.deepEqual(p.errors, []);
    } finally { await p.close(); }
  }
});

test("home uses switcher recency priority without dropping older notes", async () => {
  const p = await home();
  try {
    const id = await p.eval(`(() => { const a = [...document.querySelectorAll(".nb-card")].at(-1); const id = a.getAttribute("href").match(/p\\/([^/]+)/)[1]; localStorage.setItem("kapi-notebook-visits", JSON.stringify({ since: Date.now(), ids: { [id]: Date.now() } })); dispatchEvent(new StorageEvent("storage", { key: "kapi-notebook-visits" })); return id; })()`);
    assert.equal(await p.eval(`document.querySelector(".nb-card").getAttribute("href").includes(${JSON.stringify(id)})`), true);
    assert.ok(await p.eval(`document.querySelectorAll(".nb-card").length`) >= 3);
  } finally { await p.close(); }
});

test("organizer rename inputs fill their row and the Phosphor X closes the dialog", async () => {
  for (const width of [390, 1440]) {
    const p = await home(base, { width, height: 900 });
    try {
      await p.eval(`document.querySelector(".nb-organize").click()`);
      assert.equal(await p.eval(`document.querySelector(".kapi-organizer-close").textContent`), "");
      assert.equal(await p.eval(`!!document.querySelector(".kapi-organizer-close svg path")`), true);
      for (const selector of [".kapi-organizer-page", ".kapi-organizer-folder-head"]) {
        await p.eval(`document.querySelector(${JSON.stringify(selector)} + " .kapi-organizer-rename").click()`);
        assert.ok(await p.eval(`(() => { const input = document.querySelector(".kapi-organizer-rename-input"); return input.getBoundingClientRect().width / input.parentElement.getBoundingClientRect().width; })()`) > 0.8);
        await p.type("A visible new name");
        assert.equal(await p.eval(`document.querySelector(".kapi-organizer-rename-input").value`), "A visible new name");
        await p.key("Escape");
        await p.waitFor(`!document.querySelector(".kapi-organizer-rename-input")`);
        assert.equal(await p.eval(`document.querySelector(".kapi-organizer").hidden`), false);
        assert.equal(await p.eval(`document.activeElement.matches(".kapi-organizer-rename") && document.activeElement.closest(${JSON.stringify(selector)}) !== null`), true);
      }
      await p.eval(`document.querySelector(".kapi-organizer-close").click()`);
      assert.equal(await p.eval(`document.querySelector(".kapi-organizer").hidden`), true);
      assert.deepEqual(p.errors, []);
    } finally { await p.close(); }
  }
});

test("home organization can rename, collapse, and reorder notes", async () => {
  const hidden = join(projectA, ".kapi");
  const first = join(hidden, "first.html");
  const second = join(hidden, "second.html");
  mkdirSync(hidden);
  write(first, "First hidden note", "One.");
  write(second, "Second hidden note", "Two.");
  kapi(hidden, "open", "first.html");
  kapi(hidden, "open", "second.html");
  const p = await home();
  try {
    await p.eval(`[...document.querySelectorAll(".nb-status-button")].find((button) => button.textContent === "All").click()`);
    await refresh(p);
    await p.waitFor(`[...document.querySelectorAll(".nb-card-title")].some((n) => n.textContent === "Second hidden note")`);
    await p.eval(`document.querySelector(".nb-organize").click()`);
    await p.waitFor(`[...document.querySelectorAll(".kapi-organizer-folder-toggle strong")].some((n) => n.textContent === "Alpha pages")`);
    await p.eval(`(() => { const head = [...document.querySelectorAll(".kapi-organizer-folder-head")].find((el) => el.textContent.includes("Alpha pages")); head.querySelector(".kapi-organizer-rename").click(); const input = head.querySelector(".kapi-organizer-rename-input"); input.value = "Agent work"; input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); })()`);
    await p.waitFor(`[...document.querySelectorAll(".kapi-organizer-folder-toggle strong")].some((n) => n.textContent === "Agent work")`);

    await p.eval(`(() => { const group = [...document.querySelectorAll(".kapi-organizer-folder")].find((el) => el.textContent.includes("Agent work")); const rows = [...group.querySelectorAll(".kapi-organizer-page")]; const source = rows[1]; const target = rows[0]; const moved = source.querySelector(".kapi-organizer-page-title").textContent; const expected = rows.map((row) => row.querySelector(".kapi-organizer-page-title").textContent).filter((title) => title !== moved); expected.splice(0, 0, moved); window.organizerOrder = JSON.stringify(expected); window.organizerMoved = { moved: source.dataset.key.split("\\n").at(-1), target: target.dataset.key.split("\\n").at(-1) }; const transfer = new DataTransfer(); source.dispatchEvent(new DragEvent("dragstart", { bubbles: true, dataTransfer: transfer })); target.dispatchEvent(new DragEvent("dragover", { bubbles: true, cancelable: true, clientY: target.getBoundingClientRect().top, dataTransfer: transfer })); target.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, clientY: target.getBoundingClientRect().top, dataTransfer: transfer })); })()`);
    await p.waitFor(`(() => { const group = [...document.querySelectorAll(".kapi-organizer-folder")].find((el) => el.textContent.includes("Agent work")); return JSON.stringify([...group.querySelectorAll(".kapi-organizer-page-title")].map((n) => n.textContent)) === window.organizerOrder; })()`);

    await p.eval(`(() => { const row = [...document.querySelectorAll(".kapi-organizer-page")].find((el) => el.textContent.includes("First hidden note")); row.querySelector(".kapi-organizer-rename").click(); const input = row.querySelector(".kapi-organizer-rename-input"); input.value = "My first note"; input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); })()`);
    await p.waitFor(`[...document.querySelectorAll(".kapi-organizer-page-title")].some((n) => n.textContent === "My first note")`);
    assert.equal(await p.eval(`document.activeElement.matches(".kapi-organizer-rename") && document.activeElement.closest(".kapi-organizer-page")?.textContent.includes("My first note")`), true);
    await p.eval(`(() => { const button = [...document.querySelectorAll(".kapi-organizer-folder-toggle strong")].find((n) => n.textContent === "Agent work").closest("button"); button.focus(); button.click(); })()`);
    assert.equal(await p.eval(`[...document.querySelectorAll(".kapi-organizer-folder")].find((el) => el.textContent.includes("Agent work")).querySelector(".kapi-organizer-pages").hidden`), true);
    assert.equal(await p.eval(`document.activeElement.matches(".kapi-organizer-folder-toggle") && document.activeElement.textContent.includes("Agent work")`), true);
    await p.eval(`document.querySelector(".kapi-organizer-close").click()`);
    assert.equal(await p.eval(`(() => { const cards = [...document.querySelectorAll(".nb-card")]; const moved = cards.findIndex((card) => card.href.includes(window.organizerMoved.moved)); const target = cards.findIndex((card) => card.href.includes(window.organizerMoved.target)); return moved >= 0 && moved < target; })()`), true);
  } finally {
    await p.close();
    rmSync(hidden, { recursive: true, force: true });
  }
});
