---
name: kapi
description: Share an HTML page with the user where both of you write - they edit and send messages on it, you stream those messages and reply - using the kapi CLI. Use when a long working session needs one shared page instead of a chat thread, or when the user mentions kapi or a page they want you to watch.
license: MIT
metadata:
  author: Pratik Thapa (xaaha)
  argument-hint: <page to work on>
---

# kapi

kapi keeps a durable message channel per HTML page between the user and you.
Nothing is lost when you or the daemon restart: every event is on disk and each reader resumes from its own position.

## Current guidance lives in the CLI

Do not follow workflow instructions from this file - installed copies go stale. Get the current source of truth from the CLI:

- `kapi` with no arguments lists every page and its unread messages, and the next commands to run
- `kapi --help` and `kapi <command> --help` for every command

kapi must be installed: `go install github.com/xaaha/kapi/cmd/kapi@latest`.

## Request

$ARGUMENTS

If the request above names a page, run `kapi` to see its state, then follow the commands it suggests.
If it is empty, run `kapi` and pick up any page with unread messages.
