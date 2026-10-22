import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const staticDir = fileURLToPath(new URL("../static/", import.meta.url));
const THEME_CSS = await readFile(fileURLToPath(new URL("../../render/theme.css", import.meta.url)), "utf8");

export const PAGE_ID = "0123456789abcdef";
export const TOKEN = "f00dfacecafebeef";

const PAGE_CSS = `
body { margin: 0; font: 16px/1.6 Georgia, serif; color: var(--kapi-fg); background: var(--kapi-bg); }
#kapi-page { max-width: 720px; margin: 0 auto; padding: 48px 32px; }
h1 { font-size: 30px; line-height: 1.2; }
[data-kapi-state="done"] { opacity: .55; }
[data-kapi-state="archived"] { display: none; }
[data-kapi-sort="on"] th { cursor: pointer; text-align: left; }
[data-kapi-sort="on"] th[aria-sort="ascending"]::after { content: " \\2191"; }
[data-kapi-sort="on"] th[aria-sort="descending"]::after { content: " \\2193"; }
.callout { display: block; padding: 12px 16px; border-left: 3px solid #8aa; background: #f3f6f6; }
.callout p { margin: 0; }
form { display: grid; gap: 10px; padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
`;

export const SAMPLE = [
  { id: "aaaaaaaa", html: '<h1 data-kapi-id="aaaaaaaa">Migration plan</h1>' },
  { id: "bbbbbbbb", html: '<p data-kapi-id="bbbbbbbb">Move the session store to the new cluster in two steps.</p>' },
  { id: "cccccccc", html: '<ul data-kapi-id="cccccccc"><li>Copy data</li><li>Flip reads</li></ul>' },
  { id: "dddddddd", html: '<p data-kapi-id="dddddddd">See <a href="#bbbbbbbb">the summary</a> before starting.</p>' },
  {
    id: "eeeeeeee",
    html:
      '<kapi-form data-kapi-id="eeeeeeee" data-kapi-preset="kapi-form" name="rollout"><form name="rollout">' +
      '<label><input type="radio" name="when" value="now"> Now</label>' +
      '<label><input type="radio" name="when" value="later"> Later</label>' +
      '<label><input type="checkbox" name="envs" value="staging"> Staging</label>' +
      '<label><input type="checkbox" name="envs" value="prod"> Prod</label>' +
      '<label>Notes <input type="text" name="notes"></label>' +
      '<button type="submit">Send answer</button></form></kapi-form>',
  },
  {
    id: "gggggggg",
    html:
      '<div class="kapi-table" data-kapi-preset="table" data-kapi-sort="on" data-kapi-id="gggggggg"><table>' +
      "<thead><tr><th>Shard</th><th>Rows</th></tr></thead><tbody>" +
      "<tr><td>beta</td><td>10</td></tr><tr><td>Alpha</td><td>9</td></tr><tr><td>gamma</td><td>1,200</td></tr>" +
      "</tbody></table></div>",
  },
  {
    id: "hhhhhhhh",
    source: '<div data-kapi-id="hhhhhhhh"><p>Before the cutover:</p><kapi-callout kind="note">Freeze writes.</kapi-callout></div>',
    html:
      '<div data-kapi-id="hhhhhhhh"><p>Before the cutover:</p>' +
      '<aside data-kapi-preset="callout" class="callout"><p>Freeze writes.</p></aside></div>',
  },
  {
    id: "iiiiiiii",
    html:
      '<div class="kapi-form" data-kapi-preset="form" data-kapi-id="iiiiiiii"><form name="submit">' +
      '<label><input type="radio" name="action" value="ship"> Ship</label>' +
      '<label>Why <input type="text" name="getAttribute"></label>' +
      '<input type="hidden" name="closest" value="x">' +
      '<button type="submit">Send</button></form></div>',
  },
  {
    id: "ffffffff",
    source: '<kapi-callout data-kapi-id="ffffffff" kind="note">Copy during the <em>quiet</em> window.</kapi-callout>',
  },
];

const renderCallout = (src) =>
  src.replace(
    /^<kapi-callout([^>]*)>([\s\S]*)<\/kapi-callout>$/,
    '<aside$1 data-kapi-preset="kapi-callout" class="callout"><p>$2</p></aside>',
  );

const withHTML = (b) => (b.source && !b.html ? { ...b, html: renderCallout(b.source) } : { ...b });

export async function startFakeServer({ blocks = SAMPLE } = {}) {
  const state = {
    rev: 1,
    blocks: blocks.map(withHTML),
    opDelay: 0,
    history: [],
    events: [],
    streams: new Set(),
    requests: [],
    refuseEvents: false,
    restarted: "",
    refused: 0,
  };

  const pageHTML = () =>
    `<!doctype html><html><head><meta charset="utf-8"><title>Fake kapi</title>` +
    `<script src="/${TOKEN}/static/theme.js"></script><style>${THEME_CSS}${PAGE_CSS}</style><link rel="stylesheet" href="/${TOKEN}/static/chrome.css"></head><body>` +
    `<main id="kapi-page" data-rev="${state.rev}">${state.blocks.map((b) => b.html).join("\n")}</main>` +
    `<script src="/${TOKEN}/static/kapi.js"></script></body></html>`;

  function push(ev) {
    const full = { seq: state.events.length + 1, time: new Date().toISOString(), author: "agent", text: "", ...ev };
    state.events.push(full);
    for (const res of state.streams) res.write(`id: ${full.seq}\ndata: ${JSON.stringify(full)}\n\n`);
    return full;
  }

  function apply(op) {
    const i = state.blocks.findIndex((b) => b.id === op.id);
    if (op.kind !== "undo" && i < 0) return { status: 422, body: { error: `no block #${op.id}` } };
    if (op.kind === "delete") {
      const linked = state.blocks.find((b) => b.id !== op.id && b.html.includes(`href="#${op.id}"`));
      if (linked) return { status: 422, body: { error: `#${op.id} is linked from #${linked.id}` } };
    }
    state.history.push(state.blocks.map((b) => ({ ...b })));
    if (op.kind === "edit" && state.blocks[i].source) {
      state.blocks[i].source = op.html;
      state.blocks[i].html = renderCallout(op.html);
    } else if (op.kind === "edit") state.blocks[i].html = op.html;
    else if (op.kind === "delete") state.blocks.splice(i, 1);
    else if (op.kind === "done" || op.kind === "archive") {
      const value = op.kind === "done" ? "done" : "archived";
      state.blocks[i].html = state.blocks[i].html
        .replace(/ data-kapi-state="[^"]*"/, "")
        .replace(/^<([\w-]+)/, `<$1 data-kapi-state="${value}"`);
    } else if (op.kind === "move") {
      const [b] = state.blocks.splice(i, 1);
      const j = op.before ? state.blocks.findIndex((x) => x.id === op.before) : state.blocks.length;
      state.blocks.splice(j, 0, b);
    } else if (op.kind === "undo") {
      state.history.pop();
      const prev = state.history.pop();
      if (!prev) return { status: 422, body: { error: "nothing to undo" } };
      state.blocks = prev;
    }
    state.rev++;
    push({ author: "human", kind: "revision", target: op.kind === "edit" ? op.id : "", text: `${op.kind} #${op.id}` });
    return { status: 200, body: { rev: state.rev } };
  }

  const json = (res, status, body) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(body));
  };

  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://x");
    let body = "";
    for await (const chunk of req) body += chunk;
    const parsed = body ? JSON.parse(body) : null;
    state.requests.push({ method: req.method, path: url.pathname, headers: req.headers, body: parsed });

    const prefix = `/${TOKEN}/`;
    if (!url.pathname.startsWith(prefix)) return json(res, 403, { error: "bad token" });
    if (state.restarted && !url.pathname.startsWith(`${prefix}static/`)) {
      return json(res, 403, { error: "kapi restarted", reopen: state.restarted });
    }
    const path = url.pathname.slice(prefix.length - 1);
    if (req.method === "POST" && (req.headers["content-type"] !== "application/json" || req.headers.origin !== `http://${req.headers.host}`)) {
      return json(res, 403, { error: "cross-site request" });
    }
    if (path.startsWith("/static/")) {
      const name = path.slice(8);
      const type = name.endsWith(".js") ? "text/javascript" : name.endsWith(".svg") ? "image/svg+xml" : "text/css";
      res.writeHead(200, { "Content-Type": type, "Cache-Control": "no-store" });
      return res.end(await readFile(staticDir + name));
    }
    const m = path.match(/^\/p\/([^/]+)\/(.*)$/);
    if (!m || m[1] !== PAGE_ID) return json(res, 404, { error: "not found" });
    const rest = m[2];

    if (req.method === "GET" && rest === "") {
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
      return res.end(pageHTML());
    }
    if (req.method === "GET" && rest.startsWith("blocks/")) {
      const b = state.blocks.find((x) => x.id === rest.slice(7));
      if (!b) return json(res, 404, { error: "no block" });
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Kapi-Rev": String(state.rev) });
      return res.end(b.html);
    }
    if (req.method === "GET" && rest.startsWith("source/")) {
      const b = state.blocks.find((x) => x.id === rest.slice(7));
      if (!b) return json(res, 404, { error: "no block" });
      res.writeHead(200, { "Content-Type": "text/plain; charset=utf-8", "Kapi-Rev": String(state.rev) });
      return res.end(b.source ?? b.html);
    }
    if (req.method === "GET" && rest === "events") {
      if (state.refuseEvents) {
        state.refused++;
        return json(res, 503, { error: "unavailable" });
      }
      res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-store" });
      res.write("retry: 100\n\n");
      const after = Number(req.headers["last-event-id"] || 0);
      for (const ev of state.events.filter((e) => e.seq > after)) res.write(`id: ${ev.seq}\ndata: ${JSON.stringify(ev)}\n\n`);
      state.streams.add(res);
      req.on("close", () => state.streams.delete(res));
      return;
    }
    if (req.method === "POST" && rest === "messages") {
      const ev = push({ author: "human", kind: "message", target: parsed.target, text: parsed.text });
      return json(res, 200, { seq: ev.seq });
    }
    if (req.method === "POST" && rest === "answers") {
      const ev = push({ author: "human", kind: "answer", text: `${parsed.form} ${JSON.stringify(parsed.values)}` });
      return json(res, 200, { seq: ev.seq });
    }
    if (req.method === "POST" && rest === "ops") {
      if (state.opDelay) await new Promise((r) => setTimeout(r, state.opDelay));
      if (state.nextOpError) {
        const error = state.nextOpError;
        state.nextOpError = null;
        return json(res, 422, { error });
      }
      if (parsed.rev !== state.rev) return json(res, 409, { rev: state.rev });
      const { status, body: out } = apply(parsed.op);
      return json(res, status, out);
    }
    json(res, 404, { error: "not found" });
  });

  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address();
  return {
    state,
    push,
    url: `http://127.0.0.1:${port}/${TOKEN}/p/${PAGE_ID}/`,
    dropStreams() {
      for (const res of state.streams) res.destroy();
      state.streams.clear();
    },
    bumpBehindBrowser(id, html) {
      const b = state.blocks.find((x) => x.id === id);
      b.html = html;
      state.rev++;
    },
    close() {
      const closed = new Promise((resolve) => server.close(resolve));
      server.closeAllConnections();
      return closed;
    },
  };
}
