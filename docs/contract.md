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
`done` and `archive` set `data-kapi-state` on the element. Every applied op
saves the previous source as `revs/<rev>.html` so `undo` restores it.

The agent edits the file directly too. The store notices a changed mtime,
assigns missing ids, and bumps the revision as if an `edit` happened.

## HTTP

The daemon also listens on `127.0.0.1` on a random port and writes
`http://127.0.0.1:<port>/?t=<token>` to `$KAPI_HOME/http.url`. `kapi open
<page>` starts the daemon if needed and opens `<url>/p/<pageid>/`.

Every request needs the token: the `t` query parameter on the first load sets
an `HttpOnly`, `SameSite=Strict` cookie. Requests whose `Host` is not
`127.0.0.1:<port>` get 403 (DNS rebinding).

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
