import { findChrome, launch } from "./chrome.mjs";

const url = process.argv[2];
if (!findChrome()) {
  console.log(JSON.stringify({ skip: "Chrome not found; set CHROME_PATH" }));
  process.exit(0);
}

const browser = await launch();
let page;
const CHROME = "[data-kapi-raw], .kapi-callout-label, .kapi-plan-mark, .kapi-form-actions, svg";

async function settle() {
  let last = await page.eval(`document.getElementById("kapi-page").innerHTML.length + ":" + document.getElementById("kapi-page").dataset.rev`);
  for (let i = 0; i < 40; i++) {
    await new Promise((r) => setTimeout(r, 100));
    const now = await page.eval(`document.getElementById("kapi-page").innerHTML.length + ":" + document.getElementById("kapi-page").dataset.rev`);
    if (now === last) return;
    last = now;
  }
}

async function doubleClick(target) {
  await settle();
  await page.key("Escape");
  const at = await page.eval(`(() => {
    for (const d of document.querySelectorAll("details")) d.open = true;
    const n = ${target};
    n.scrollIntoView({ block: "center" });
    const r = [...n.getClientRects()].find((x) => x.width > 4 && x.height > 4) || n.getBoundingClientRect();
    return { x: r.x + Math.min(r.width / 2, 24), y: r.y + Math.min(r.height / 2, 12) };
  })()`);
  for (const clickCount of [1, 2]) {
    await page.call("Input.dispatchMouseEvent", { type: "mousePressed", ...at, button: "left", clickCount });
    await page.call("Input.dispatchMouseEvent", { type: "mouseReleased", ...at, button: "left", clickCount });
  }
}
const report = { edited: [], raw: [], blocks: [], errors: [] };
try {
  page = await browser.open(url, { width: 1280, height: 900 });
  const keyOf = `(n) => n.dataset.kapiText + (n.dataset.kapiCell ? "/" + n.dataset.kapiCell : "") + (n.dataset.kapiAttr ? "@" + n.dataset.kapiAttr : "") + (n.hasAttribute("data-kapi-lead") ? "@lead" : "")`;
  const find = (key) => `[...document.querySelectorAll("[data-kapi-text]")].find((n) => (${keyOf})(n) === ${JSON.stringify(key)})`;
  const keys = await page.eval(`[...document.querySelectorAll("[data-kapi-text]")].map(${keyOf})`);
  for (const [i, key] of keys.entries()) {
    const mark = `zq${i}`;
    try {
      await doubleClick(find(key));
      if (await page.eval(`${find(key)}.hasAttribute("data-kapi-raw")`)) {
        await page.waitFor(`document.activeElement?.classList.contains("kapi-source")`);
        const shown = await page.eval(`document.activeElement.value`);
        const source = await page.eval(`fetch(location.pathname + "source/" + ${find(key)}.dataset.kapiText + "?part=inner").then((r) => r.text())`);
        if (shown !== source) throw new Error(`the text box shows ${JSON.stringify(shown)}, the source is ${JSON.stringify(source)}`);
        await page.key("Escape");
        await page.waitFor(`!document.querySelector(".kapi-source")`);
        report.raw.push(key);
        continue;
      }
      await page.waitFor(`!!${find(key)}?.isContentEditable`);
      await page.eval(`(host => { window.editedHost = host; if (!host.contains(getSelection().anchorNode)) { host.focus(); getSelection().selectAllChildren(host); getSelection().collapseToEnd(); } })(${find(key)})`);
      await page.type(` ${mark}`);
      await page.key("Enter", { ctrl: true });
      await page.waitFor(`(host => !document.querySelector("[contenteditable]") && host !== window.editedHost && host.textContent.includes(${JSON.stringify(mark)}))(${find(key)})`, 5000);
      const notice = await page.eval(`(n => n.hidden ? "" : n.textContent)(document.querySelector(".kapi-notice"))`);
      if (notice) throw new Error(notice);
      report.edited.push({ key, mark });
    } catch (err) {
      const notice = await page.eval(`(n => n.hidden ? "" : n.textContent)(document.querySelector(".kapi-notice"))`).catch(() => "");
      const state = await page.eval(`JSON.stringify({ editable: document.querySelectorAll("[contenteditable]").length, same: ${find(key)} === window.editedHost, text: ${find(key)}?.innerHTML, bar: [...document.querySelectorAll(".kapi-bar button")].map((b) => b.dataset.action), rev: document.getElementById("kapi-page").dataset.rev })`).catch((e) => e.message);
      report.errors.push(`${key}: ${notice || err.message.slice(0, 120)} ${state}`);
      await page.key("Escape").catch(() => {});
    }
  }
  const blocks = await page.eval(`[...document.querySelectorAll("#kapi-page > [data-kapi-id]")].filter((b) => !b.matches("[data-kapi-raw]")).map((b) => b.dataset.kapiId)`);
  for (const id of blocks) {
    const block = `document.querySelector('[data-kapi-id="${id}"]')`;
    try {
      await page.eval(`${block}.click()`);
      await page.eval(`${block}.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }))`);
      await page.waitFor(`!!document.querySelector("[contenteditable=true]")`);
      const leak = await page.eval(`[...${block}.querySelectorAll("[data-kapi-raw]")].filter((n) => n.isContentEditable).length + document.querySelectorAll(".kapi-source").length`);
      if (leak) throw new Error(`${leak} raw parts or text boxes opened while editing the whole block`);
      const stuck = await page.eval(`(() => {
        const out = [];
        const walk = document.createTreeWalker(${block}, NodeFilter.SHOW_TEXT);
        while (walk.nextNode()) {
          const n = walk.currentNode;
          if (!n.data.trim() || n.parentElement.isContentEditable || n.parentElement.closest(${JSON.stringify(CHROME)})) continue;
          out.push(n.data.trim().slice(0, 40));
        }
        return out;
      })()`);
      if (stuck.length) throw new Error(`text that cannot be edited: ${JSON.stringify(stuck)}`);
      report.blocks.push(id);
    } catch (err) {
      report.errors.push(`block ${id}: ${err.message.slice(0, 160)}`);
    }
    await page.key("Escape");
    await page.waitFor(`!document.querySelector("[contenteditable]")`).catch(() => report.errors.push(`block ${id}: Escape left the editor open`));
  }
  report.errors.push(...page.errors);
} finally {
  await browser.close();
}
console.log(JSON.stringify(report));
