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

- Run `kapi` first.
- Treat `kapi --help`, command help, and `kapi preset` as authoritative.
- Write only documented HTML and presets.
- Reuse one page per task; add sections instead of creating related pages.
- Follow `kapi preset` writing rules: fact first, concrete numbers, no repetition.
- Use `kapi wait <page>` when its exit wakes the harness; restart it after each event. Use `kapi watch <page>` only when every line is delivered.
- Acknowledge events with the current `say --seen`, `say --reply`, `seen`, or `pending` flow.
- Stop waiting or watching when the session ends.

## Visuals

Add visuals without being asked when they clarify the work.

- Use `kapi-diagram` for flows, sequences, architecture, dependencies, states, and relationships. Include meaningful nodes and edges.
- Use `kapi-render` for HTML/CSS mockups, dashboards, cards, charts, timelines, and spatial layouts. Prefer Kapi theme variables.
- Use tables, comparisons, plans, key/value lists, and checklists for structured facts. Combine presets when useful.
- Use `kapi attach <page> <image...>` for images; multiple images form a gallery.
- Use ASCII only for literal source or terminal output, never instead of a diagram or interface.

## Constraints

- Do not require Node, npm, npx, or browser packages.
- Do not add scripts, iframes, or application JavaScript. Put CSS inside `kapi-render`.
- Do not copy stale CLI syntax from this skill when help differs.
- Do not detach a watcher that cannot wake the agent.

## Request

$ARGUMENTS

Named page: inspect it. No page: open unread work.
