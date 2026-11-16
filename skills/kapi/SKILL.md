---
name: kapi
description: Use kapi to share durable, editable HTML pages for plans, reports, comparisons, diagrams, and visual reviews. Use whenever the user mentions kapi or a shared page is clearer than chat.
license: MIT
metadata:
  author: Pratik Thapa (xaaha)
  argument-hint: <page to work on>
---

# kapi

## Workflow

- Run `kapi` first. `kapi --help` and `kapi preset` are authoritative.
- Create pages with `kapi new <title>`. One page per task; add sections, not related pages. Find pages with `kapi ls --json`.
- Pages are temporary until the person saves one. Never move a page yourself. When `kapi wait` exits naming a new path, use it.
- Write the fact first, with numbers. Say each thing once.
- Use `kapi wait <page>` when its exit wakes the harness; restart it after each event. Use `kapi watch <page>` only when every line is delivered.
- Acknowledge events with `say --seen`, `say --reply`, `seen`, or `pending`.
- A `repaired` event means kapi fixed your markup. Write it the fixed way next time.
- Stop waiting or watching when the session ends.

## Writing pages

Write ordinary HTML; kapi styles it:

- `<table>` with `<thead>`, `<dl>`, `<details><summary>`, `<blockquote>`, `<figure><img><figcaption>`, `<pre><code class="language-…">`, and `<ul>` items starting with checkbox inputs.
- Add visuals without being asked when they clarify the work.
- Use `kapi-diagram` for architecture, or Mermaid `flowchart` and `sequenceDiagram` in `<pre class="mermaid">` for flows and sequences.
- Use `kapi-render` for mockups, dashboards and charts.
- Use other `kapi-*` presets only for what HTML cannot say: callout, badge, plan, compare, form, diff.
- Use ASCII only for literal output, never instead of a diagram or interface.
- Images: `kapi attach <page> <image...>`; several form a gallery.

## Constraints

- No scripts, iframes, or application JavaScript. CSS only inside `kapi-render`.
- No Node, npm, npx, or browser packages.
- Do not detach a watcher that cannot wake the agent.

## Request

$ARGUMENTS

Named page: inspect it. No page: open unread work.
