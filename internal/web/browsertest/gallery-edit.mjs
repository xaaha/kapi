import { findChrome, launch } from "./chrome.mjs";

const url = process.argv[2];
if (!findChrome()) {
  console.log(JSON.stringify({ skip: "Chrome not found; set CHROME_PATH" }));
  process.exit(0);
}

const browser = await launch();
const report = { edited: [], raw: [], errors: [] };
try {
  const page = await browser.open(url, { width: 1280, height: 900 });
  const keyOf = `(n) => n.dataset.kapiText + (n.dataset.kapiCell ? "/" + n.dataset.kapiCell : "")`;
  const find = (key) => `[...document.querySelectorAll("[data-kapi-text]")].find((n) => (${keyOf})(n) === ${JSON.stringify(key)})`;
  const keys = await page.eval(`[...document.querySelectorAll("[data-kapi-text]")].map(${keyOf})`);
  for (const [i, key] of keys.entries()) {
    const mark = `zq${i}`;
    try {
      await page.eval(`(() => {
        for (const d of document.querySelectorAll("details")) d.open = true;
        const host = ${find(key)};
        host.scrollIntoView({ block: "center" });
        host.click();
        document.querySelector("[data-kapi-selected]").dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
      })()`);
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
      await page.eval(`(host => { window.editedHost = host; host.focus(); getSelection().selectAllChildren(host); getSelection().collapseToEnd(); })(${find(key)})`);
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
  report.errors.push(...page.errors);
} finally {
  await browser.close();
}
console.log(JSON.stringify(report));
