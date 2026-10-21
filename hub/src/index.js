const PAGE_ID = /^[0-9a-f]{16}$/;
const REV = /^[0-9]{1,20}$/;
const MAX_BYTES = 10 * 1024 * 1024;
const MAX_TITLE = 200;

export const CSP = [
  "default-src 'none'",
  "style-src 'unsafe-inline'",
  "img-src data:",
  "font-src data:",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
].join("; ");

export default {
  fetch(request, env) {
    return route(request, env);
  },
};

async function route(request, env) {
  const path = new URL(request.url).pathname;
  let m;
  if ((m = /^\/api\/pages\/([^/]+)\/revs\/([^/]+)$/.exec(path))) {
    if (request.method !== "PUT") return methodNotAllowed("PUT");
    return putRevision(request, env, m[1], m[2]);
  }
  if (path.startsWith("/api/")) return text(404, "not found");
  if (request.method !== "GET" && request.method !== "HEAD") return methodNotAllowed("GET, HEAD");
  const head = request.method === "HEAD";
  if (path === "/") return listPages(env, head);
  if ((m = /^\/p\/([^/]+)$/.exec(path))) {
    return new Response(null, { status: 301, headers: { Location: `/p/${m[1]}/` } });
  }
  if ((m = /^\/p\/([^/]+)\/$/.exec(path))) return serveLatest(env, m[1], head);
  if ((m = /^\/p\/([^/]+)\/revs\/$/.exec(path))) return listRevisions(env, m[1], head);
  if ((m = /^\/p\/([^/]+)\/revs\/([^/]+)\/$/.exec(path))) return serveRevision(env, m[1], m[2], head);
  return text(404, "not found");
}

async function putRevision(request, env, id, rev) {
  if (!env.KAPI_HUB_TOKEN) return text(503, "hub token is not configured");
  if (!(await authorized(request.headers.get("Authorization"), env.KAPI_HUB_TOKEN))) {
    return text(401, "unauthorized", { "WWW-Authenticate": "Bearer" });
  }
  if (!PAGE_ID.test(id)) return text(400, "page id must be 16 lowercase hex characters");
  if (!REV.test(rev)) return text(400, "revision must be a decimal number");
  const type = request.headers.get("Content-Type") || "";
  if (!/^text\/html\b/i.test(type)) return text(415, "content type must be text/html");
  if (Number(request.headers.get("Content-Length") || 0) > MAX_BYTES) return text(413, "page is too large");
  const title = parseTitle(request.headers.get("Kapi-Title"));
  if (title === null) return text(400, "Kapi-Title must be percent-encoded UTF-8");
  const body = await request.arrayBuffer();
  if (body.byteLength > MAX_BYTES) return text(413, "page is too large");

  await env.PAGES.put(revisionKey(id, rev), body, {
    httpMetadata: { contentType: "text/html; charset=utf-8" },
    customMetadata: { title },
  });
  await env.PAGES.put(indexKey(id), "", { customMetadata: { title, rev } });
  return json(201, { id, rev });
}

async function authorized(header, secret) {
  const m = /^Bearer (.+)$/.exec(header || "");
  if (!m) return false;
  const [got, want] = await Promise.all([digest(m[1]), digest(secret)]);
  let diff = 0;
  for (let i = 0; i < want.length; i++) diff |= got[i] ^ want[i];
  return diff === 0;
}

async function digest(s) {
  return new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s)));
}

function parseTitle(raw) {
  let title;
  try {
    title = decodeURIComponent(raw || "");
  } catch {
    return null;
  }
  return title.replace(/[\u0000-\u001f\u007f]/g, " ").trim().slice(0, MAX_TITLE);
}

async function listPages(env, head) {
  const entries = await listAll(env.PAGES, "index/");
  const pages = entries
    .map((o) => ({ id: o.key.slice("index/".length), at: o.uploaded, ...o.customMetadata }))
    .filter((p) => PAGE_ID.test(p.id))
    .sort((a, b) => b.at - a.at);
  const items = pages.map(
    (p) =>
      `<li><a href="/p/${p.id}/">${escape(p.title || p.id)}</a> <small>${p.at.toISOString()}</small> <a href="/p/${p.id}/revs/">revisions</a></li>`,
  );
  return htmlPage("kapi pages", items.length ? `<ul>${items.join("")}</ul>` : "<p>No pages published yet.</p>", head);
}

async function serveLatest(env, id, head) {
  if (!PAGE_ID.test(id)) return text(404, "page not found");
  const entry = await env.PAGES.head(indexKey(id));
  if (!entry) return text(404, "page not found");
  return serveRevision(env, id, entry.customMetadata.rev, head);
}

async function serveRevision(env, id, rev, head) {
  if (!PAGE_ID.test(id) || !REV.test(rev)) return text(404, "revision not found");
  const obj = await env.PAGES.get(revisionKey(id, rev));
  if (!obj) return text(404, "revision not found");
  return new Response(head ? null : obj.body, { headers: htmlHeaders() });
}

async function listRevisions(env, id, head) {
  if (!PAGE_ID.test(id)) return text(404, "page not found");
  const entry = await env.PAGES.head(indexKey(id));
  if (!entry) return text(404, "page not found");
  const prefix = `pages/${id}/revs/`;
  const revs = (await listAll(env.PAGES, prefix))
    .map((o) => ({ rev: o.key.slice(prefix.length, -".html".length), at: o.uploaded }))
    .filter((r) => REV.test(r.rev))
    .sort((a, b) => b.at - a.at || compareRevs(b.rev, a.rev));
  const items = revs.map(
    (r) => `<li><a href="./${r.rev}/">revision ${r.rev}</a> <small>${r.at.toISOString()}</small></li>`,
  );
  const title = entry.customMetadata.title || id;
  return htmlPage(
    `${title}: revisions`,
    `<p><a href="/p/${id}/">latest</a> · <a href="/">all pages</a></p><ul>${items.join("")}</ul>`,
    head,
  );
}

function compareRevs(a, b) {
  const x = BigInt(a);
  const y = BigInt(b);
  return x < y ? -1 : x > y ? 1 : 0;
}

async function listAll(bucket, prefix) {
  const objects = [];
  let cursor;
  do {
    const page = await bucket.list({ prefix, cursor, include: ["customMetadata"] });
    objects.push(...page.objects);
    cursor = page.truncated ? page.cursor : undefined;
  } while (cursor);
  return objects;
}

function revisionKey(id, rev) {
  return `pages/${id}/revs/${rev}.html`;
}

function indexKey(id) {
  return `index/${id}`;
}

function htmlHeaders() {
  return {
    "Content-Type": "text/html; charset=utf-8",
    "Content-Security-Policy": CSP,
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy": "no-referrer",
    "Cache-Control": "private, no-cache",
  };
}

function htmlPage(title, body, head) {
  const doc = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escape(title)}</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:48rem;margin:2rem auto;padding:0 1rem}small{color:#666}</style>
</head>
<body>
<h1>${escape(title)}</h1>
${body}
</body>
</html>
`;
  return new Response(head ? null : doc, { headers: htmlHeaders() });
}

function escape(s) {
  return String(s).replace(
    /[&<>"']/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c],
  );
}

function text(status, message, headers = {}) {
  return new Response(message + "\n", {
    status,
    headers: { "Content-Type": "text/plain; charset=utf-8", "X-Content-Type-Options": "nosniff", ...headers },
  });
}

function json(status, value) {
  return new Response(JSON.stringify(value) + "\n", {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function methodNotAllowed(allow) {
  return text(405, "method not allowed", { Allow: allow });
}
