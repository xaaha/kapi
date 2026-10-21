import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const CANDIDATES = [
  process.env.CHROME_PATH,
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
];

export function findChrome() {
  return CANDIDATES.find((p) => p && existsSync(p));
}

export async function launch() {
  const bin = findChrome();
  if (!bin) throw new Error("Chrome not found; set CHROME_PATH");
  const profile = await mkdtemp(join(tmpdir(), "kapi-chrome-"));
  const proc = spawn(bin, [
    "--headless=new",
    "--remote-debugging-port=0",
    `--user-data-dir=${profile}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-gpu",
    "--no-sandbox",
    "about:blank",
  ], { stdio: ["ignore", "ignore", "pipe"] });
  let stderr = "";
  proc.stderr.on("data", (d) => (stderr = (stderr + d).slice(-4000)));

  let port;
  for (let i = 0; i < 600 && !port && proc.exitCode === null; i++) {
    await sleep(50);
    port = await readFile(join(profile, "DevToolsActivePort"), "utf8").then((s) => s.split("\n")[0], () => null);
  }
  if (!port) {
    proc.kill("SIGKILL");
    throw new Error(`Chrome at ${bin} did not start (exit ${proc.exitCode}):\n${stderr}`);
  }
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  const ws = new WebSocket(version.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = reject;
  });

  let nextId = 1;
  const waiting = new Map();
  const listeners = new Set();
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && waiting.has(msg.id)) {
      const { resolve, reject } = waiting.get(msg.id);
      waiting.delete(msg.id);
      msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
    } else for (const fn of listeners) fn(msg);
  };
  const send = (method, params = {}, sessionId) =>
    new Promise((resolve, reject) => {
      const id = nextId++;
      const timer = setTimeout(() => {
        waiting.delete(id);
        reject(new Error(`${method} got no reply in 5s`));
      }, 5000);
      const settle = (fn) => (v) => {
        clearTimeout(timer);
        fn(v);
      };
      waiting.set(id, { resolve: settle(resolve), reject: settle(reject) });
      ws.send(JSON.stringify({ id, method, params, sessionId }));
    });

  return {
    async open(url, { width = 1280, height = 800 } = {}) {
      const { targetId } = await send("Target.createTarget", { url: "about:blank" });
      const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
      const call = (method, params) => send(method, params, sessionId);
      const errors = [];
      listeners.add((msg) => {
        if (msg.sessionId !== sessionId) return;
        if (msg.method === "Runtime.exceptionThrown") errors.push(msg.params.exceptionDetails.exception?.description);
      });
      await call("Runtime.enable");
      await call("Page.enable");
      await call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 2, mobile: false });
      await call("Page.navigate", { url });

      const page = {
        errors,
        call,
        async eval(expression) {
          const r = await call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
          if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
          return r.result.value;
        },
        async waitFor(expression, timeout = 3000) {
          const end = Date.now() + timeout;
          let last;
          while (Date.now() < end) {
            last = await page.eval(expression).catch((e) => e.message);
            if (last === true) return;
            await sleep(25);
          }
          throw new Error(`timed out waiting for ${expression} (last: ${last})`);
        },
        async key(key, { ctrl = false } = {}) {
          const code = key === "Enter" ? 13 : key === "Escape" ? 27 : 0;
          const base = { key, code: key, windowsVirtualKeyCode: code, modifiers: ctrl ? 2 : 0 };
          await call("Input.dispatchKeyEvent", { type: "rawKeyDown", ...base });
          await call("Input.dispatchKeyEvent", { type: "keyUp", ...base });
        },
        async type(text) {
          await call("Input.insertText", { text });
        },
        async screenshot() {
          const { data } = await call("Page.captureScreenshot", { format: "png" });
          return Buffer.from(data, "base64");
        },
        close: () => send("Target.closeTarget", { targetId }),
      };
      await page.waitFor("document.readyState === 'complete' && !!document.getElementById('kapi-chrome')", 5000);
      return page;
    },
    async close() {
      ws.close();
      if (proc.exitCode === null) {
        const exited = new Promise((resolve) => proc.once("exit", resolve));
        proc.kill();
        await exited;
      }
      await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
    },
  };
}
