# Browser page contract

The four phase 2 tracks build against this file. Change it only on `main`, and
tell the other tracks.

Decisions behind it: D18 Go owns all state, D19 server-sent events plus POST,
D20 a fixed preset set with one design system, D21 four parallel tracks.

## Ownership

| Track | Owns | Must not touch |
|---|---|---|
| A server | `internal/page`, `internal/web/*.go`, `kapi open`, daemon wiring | `internal/web/static`, `internal/render` |
| B browser | `internal/web/static/kapi.js`, `internal/web/static/chrome.css` | Go code |
| C presets | `internal/render`, `kapi preset` command | `internal/web`, `internal/page` |
| D hub | `hub/` (Worker), `internal/publish`, `kapi publish` command | everything else |

`cmd/kapi/main.go` is shared: each track adds its own subcommand in its own
`newXxxCmd` function and one line in `SubCommands`. Merge conflicts there are
resolved by keeping every line.

## Page source

A page is an HTML fragment file the agent writes, for example `plan.html`. It
contains plain prose tags (`h1` to `h3`, `p`, `ul`, `ol`, `li`, `a`, `code`,
`em`, `strong`, `table`) and the preset elements below. No `<script>`, no
`<style>`, no `style=` attributes: the renderer drops them.

Every top-level element and every preset element carries `data-kapi-id`. The
agent may omit it; the page store adds one (8 lowercase base32 chars) on first
load and writes the file back. Ids never change after that.

### Presets

| Element | Attributes | Body |
|---|---|---|
| `<kapi-callout>` | `kind` = `note`, `warning`, `key` | prose |
| `<kapi-code>` | `lang`, optional `file`, optional `lines` like `10-24` | raw source, HTML-escaped by the agent or inside CDATA-free text |
| `<kapi-diff>` | optional `lang`, optional `file` | a unified diff |
| `<kapi-table>` | optional `sort` = `on` | one plain `<table>` |
| `<kapi-compare>` | none | `<kapi-option title="...">` children, each prose |
| `<kapi-plan>` | none | `<kapi-step>`, `<kapi-risk>`, `<kapi-question>` children, each prose |
| `<kapi-diagram>` | `type` = `flow` or `sequence` | one edge per line: `a -> b: label` (flow) or `a -> b: message` (sequence) |
| `<kapi-form>` | `name` | `<kapi-choice name="..." multiple?>` with `<kapi-opt value="...">label</kapi-opt>`, and `<kapi-text name="...">label</kapi-text>` |

Unknown `kapi-*` elements render as a visible error box, never silently.

The renderer puts `data-kapi-preset="<name>"` on the outer element of every
rendered preset. Prose elements render to the same HTML as their source.

### Editing

Prose elements (no `data-kapi-preset`) are edited in place and saved as an
`edit` op with the element's outer HTML. Preset elements are edited as source:
the browser fetches `GET /p/{id}/source/{block}`, shows it in a textarea, and
saves the textarea as the `edit` op's `HTML`. Rendered preset HTML is never
sent back.

## Go interfaces

```go
// package render
func Render(src []byte) ([]byte, error)          // page source to body HTML; keeps data-kapi-id on each outer element
func Block(src []byte, id string) ([]byte, error) // one element by id, same output as inside Render
func CSS() []byte                                 // the design system, light theme
func Presets() []Preset                           // name, one-line summary, example source
type Preset struct{ Name, Summary, Example string }

// package page
type Store struct{ /* one per page, under $KAPI_HOME/pages/<id>/ */ }
func Open(home, path string) (*Store, error)
func (s *Store) Source() ([]byte, uint64)        // current source and revision
func (s *Store) Apply(rev uint64, op Op) (uint64, Summary, error) // ErrStale when rev != current
type Op struct {
    Kind string // edit, delete, done, archive, move, undo
    ID   string
    HTML string // edit: new outer HTML of the element
    Before string // move: id to insert before, "" means end
}
type Summary struct{ Text string; IDs []string } // "edited #k3f9a2bq" etc., goes into the event log
var ErrStale, ErrNotFound, ErrInboundLinks error
```

`delete` fails with `ErrInboundLinks` when another element links to `#<id>`.
`edit` follows the same rule: it fails with `ErrDanglingLinks` (422, naming
the ids) when it adds a link to a missing id or removes an element that other
elements link to.
`done` and `archive` set `data-kapi-state` on the element. Every applied op
saves the previous source as `revs/<rev>.html` so `undo` restores it.

An op whose `rev` is older than the current revision still applies when its
target element's outer source at that `rev` (from `revs/<rev>.html`) equals
its current outer source, so edits to other blocks never refuse a save.
This applies to `edit`, `done`, `archive` and `delete` only. `move` and `undo`
depend on the whole page, so they need the exact current `rev`. `ErrStale`
(409) means the target element itself changed, or a `move` or `undo` came at
an old `rev`. Every op except `undo` needs a non-empty `id`; an empty one is
422.

`undo` reverts only the browser's own ops. When the file changed outside the
browser after the op being undone, `undo` fails with 422 instead of discarding
that change. Before writing, `Apply` compares the file's bytes (not only its
mtime and size) with the source it read; a mismatch is treated as an
external edit first.

The agent edits the file directly too. The store notices a changed mtime,
assigns missing ids, and bumps the revision as if an `edit` happened.

## HTTP

The daemon also listens on `127.0.0.1` and writes
`http://127.0.0.1:<port>/<token>/` to `$KAPI_HOME/http.url` (mode 0600).
`kapi open <page>` starts the daemon if needed and opens `<url>p/<pageid>/`.

On start the daemon reuses the port from a well-formed `http.url` when that
port is free, and picks a random one otherwise. The token is new on every
start, never reused: while the daemon is down any local program can bind the
old port and read a reconnecting tab's token. A request with a wrong token to
`p/{id}/`, `p/{id}/events`, `p/{id}/messages`, `p/{id}/ops` or
`p/{id}/answers` gets 403 with
`{"error":"kapi restarted","reopen":"kapi open <page path>"}`. The page path
is filled in only for a page id kapi knows and only when the segment is one of
the last 3 tokens earlier runs issued (their SHA-256 hashes are kept in
`$KAPI_HOME/previous-tokens`, mode 0600); otherwise `reopen` is
`kapi open <page>`. For such a token, the body also has `"reopened":true`
when this start of the daemon reopened that page in the browser (see below).
The body never holds a token.

### Reopening tabs after a restart

When the first browser connects to a page's `p/{id}/events`, the daemon
creates the empty file `$KAPI_HOME/pages/<id>/viewing` (mode 0600). It
removes the file when the last one disconnects, but not when the daemon itself
stops, and a crash leaves it too. So on the next start, `viewing` marks the
pages whose tab was open when the daemon went away. The file holds no token.

On start, before HTTP serves, the daemon collects the pages with `viewing`
and deletes every `viewing` file, so a restart loop with no tab reconnecting
reopens each page at most once. When a kapi command starts the daemon on
demand it runs `kapi daemon --reopen`. Once HTTP is serving, that daemon
opens the new URL of each collected page whose file still exists, newest
`viewing` first, at most 5, with the same opener as `kapi open`. It logs
each reopen to `$KAPI_HOME/daemon.log` by page path. It reopens nothing when
`KAPI_NO_BROWSER`, `CI`, `SSH_CONNECTION` or `SSH_TTY` is set, on Linux when neither `DISPLAY` nor
`WAYLAND_DISPLAY` is set, or when run as plain `kapi daemon` in the
foreground. A page whose opener fails is not reported as `reopened`.
When `kapi open <page>` is what starts the daemon, the client adds
`--reopen-except <page id>`, so that page opens once, from the CLI.

The old tab shows "Kapi restarted and reopened this page in a new tab. You
can close this one." when the 403 says `reopened`, and "Kapi restarted. Run
`<reopen>` to continue." with a Copy button otherwise. Either way it stops
reconnecting and keeps any unsent message and any open edit in place, so the
text can still be copied.

The token is the first path segment of every URL, including static files. No
cookies: browsers send cookies to every port on `127.0.0.1`, so a cookie would
leak to any other local server. Every path in the table below is relative to
`/<token>/`; `kapi.js` derives that base from `location.pathname` and never
hard-codes it. Every response sets `Referrer-Policy: no-referrer`.

Requests are refused with 403 when the token is wrong, when `Host` is not
`127.0.0.1:<port>` (DNS rebinding), or, for any POST, when `Origin` is not
exactly `http://127.0.0.1:<port>` or `Content-Type` is not `application/json`
(cross-site requests from other local pages).

| Method and path | Body | Response |
|---|---|---|
| `GET /p/{id}/` | | full HTML: `render.CSS()`, `chrome.css`, the rendered body inside `<main id="kapi-page" data-rev="N">`, `kapi.js` |
| `GET /p/{id}/blocks/{block}` | | `render.Block` output, header `Kapi-Rev` |
| `GET /p/{id}/source/{block}` | | the element's source outer HTML, `text/plain`, header `Kapi-Rev` |
| `GET /p/{id}/events` | | `text/event-stream`, see below |
| `POST /p/{id}/messages` | `{"kind":"message","target":"<block id or empty>","text":"..."}` | `{"seq":N}` |
| `POST /p/{id}/ops` | `{"rev":N,"op":{...Op}}` | `{"rev":N}`; 409 `{"rev":current}` when stale; 422 `{"error":"..."}` |
| `POST /p/{id}/answers` | `{"form":"name","values":{"field":["v"]}}` | `{"seq":N}` |
| `GET /static/kapi.js`, `GET /static/chrome.css` | | the embedded files |

`{id}` is the same 16-hex page id the daemon already uses for
`pages/<id>/`.

### Events stream

Each line of the event log is one SSE message with `id: <seq>`. The browser
reconnects with `Last-Event-ID` and gets everything after it. Event `data` is
the `eventlog.Event` JSON. Kinds the browser cares about:

| `kind` | Meaning | Browser does |
|---|---|---|
| `message` | chat message from `human` or an agent | append to the chat panel |
| `answer` | form answer | show as sent |
| `revision` | page changed; `text` is the summary, `target` lists changed ids comma-separated | refetch those blocks, or the page when `target` is empty |
| `presence` | `text` is `watching`, `busy` or `none` | set the status dot |

The browser keeps nothing: after any reconnect it refetches the page.

## Agent side

Ops reach the agent through the existing event log, so `kapi watch` shows
`#12 human revision on #k3f9a2bq: deleted` with no new command. Agent-side
`kapi op` and `kapi get` are phase 3.

## Out of scope for phase 2

Slides, the editable whiteboard, attachments, `kapi op`/`kapi get`.
