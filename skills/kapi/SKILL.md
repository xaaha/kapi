---
name: kapi
description: Share a durable HTML page with the user where both of you can edit, annotate, send messages, and review visual work through the kapi CLI. Use when the user mentions kapi or when a long plan, report, comparison, diagram, or implementation review is easier to understand on one shared page than in chat.
license: MIT
metadata:
  author: Pratik Thapa (xaaha)
  argument-hint: <page to work on>
---

# kapi

Use kapi for substantial work that benefits from a shared visual page. The page file, event log, revisions, and each reader's position are durable across agent and daemon restarts.

## Rules

- Run `kapi` first. It lists active pages, unread work, and the next commands to use.
- Use `kapi --help`, `kapi <command> --help`, and `kapi preset` as the current source of truth for commands and page structure.
- Write only the plain HTML and kapi presets the CLI documents. Keep the page source reviewable and update it as the work changes.
- Use `kapi attach <page> <image...>` for images. Passing several images creates one side-by-side gallery and one previous/next preview sequence.
- Follow the `writing:` rules `kapi preset` prints: the fact first, numbers instead of adjectives, each thing said once.
- Keep one page per task. Add a section to the page you already have; make a new page only for unrelated work.
- Keep `kapi wait <page>` running as a background task when the harness reliably wakes you after it exits; start it again after each event and before going idle. Use `kapi watch <page>` only when the harness reliably delivers every streamed line.
- Acknowledge what you read with the CLI's current `say --seen`, `say --reply`, `seen`, or `pending` workflow.
- Stop watching after the user ends the session.

## Visual defaults

Do not wait for the user to ask for a visual. Choose the clearest Kapi representation while creating or updating the page:

- Use `kapi-diagram` for flows, sequences, architecture, dependencies, state changes, and relationships. Include the meaningful nodes and edges instead of reducing a complex system to bullets or an ASCII sketch.
- Use `kapi-render` with ordinary HTML and CSS for UI mockups, dashboards, cards, charts, timelines, spatial layouts, and other visuals where prose or a basic table loses important structure. Prefer Kapi theme variables, but preserve useful custom styling.
- Use semantic presets such as tables, comparisons, plans, key/value lists, and checklists for structured facts; combine them with diagrams or rendered HTML when that makes the page easier to understand.
- Use ASCII only when it is literal source material or terminal output, never as a substitute for a diagram or visual interface.

## Do not

- Do not require Node, npm, npx, or a separate browser package. The installed kapi binary contains and serves the browser JavaScript.
- Do not copy command flags or preset syntax from this skill when CLI help differs; installed skills can outlive the CLI version that wrote them.
- Do not add scripts, iframes, or arbitrary application JavaScript to a kapi page. CSS belongs inside a documented `kapi-render` block; Kapi isolates and sanitizes it.
- Do not detach a watcher in a way that cannot wake or resume this agent.

## Request

$ARGUMENTS

If a page is named, run `kapi` to inspect its state and then follow the current CLI guidance for that page. If no page is named, run `kapi` and pick up any page with unread messages.
