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
| `<kapi-callout>` | `kind` = `note` (blue), `warning` (yellow), `key` (purple), `danger` (red), `success` (green) | prose |
| `<kapi-code>` | `lang`, optional `file`, optional `lines` like `10-24` | raw source, HTML-escaped by the agent or inside CDATA-free text |
| `<kapi-diff>` | optional `lang`, optional `file` | a unified diff |
| `<kapi-table>` | optional `sort` = `on` | one plain `<table>` |
| `<kapi-compare>` | none | `<kapi-option title="...">` children, each prose |
| `<kapi-plan>` | none | `<kapi-step>`, `<kapi-risk>`, `<kapi-question>` children, each prose |
| `<kapi-diagram>` | `type` = `flow`, `sequence` or `architecture`; optional `dir` = `tb` (flow default) or `lr` (architecture default), ignored by `sequence` | one edge per line: `a -> b: label` (flow) or `a -> b: message` (sequence); architecture adds node lines `id: Title \| subtitle \| state`, where state is `built` (purple), `review` (yellow), `planned` (grey, dashed) or a colour, and edges `a -> b`, `a <-> b`, `a --> b` (dashed) |
| `<kapi-badge>` | optional `tone` = `red`, `orange`, `yellow`, `green`, `blue`, `purple` or `grey` (default) | short inline text; allowed inside prose and table cells |
| `<kapi-kv>` | none | `<kapi-item key="...">` children, each prose; renders as a definition list |
| `<kapi-details>` | `summary`, optional `open` | prose, hidden until the reader opens it; closed unless `open` is set |
| `<kapi-checklist>` | none | `<kapi-task>` children, each prose; `<kapi-task done>` is ticked. Ticking a box in the browser sends a `check` op |
| `<kapi-form>` | `name` | `<kapi-choice name="..." multiple?>` with `<kapi-opt value="...">label</kapi-opt>`, and `<kapi-text name="...">label</kapi-text>` |

The colours are the seven shared tones: `red`, `orange`, `yellow`, `green`,
`blue`, `purple` and `grey`. Each is a `--kapi-<colour>-fill`, `-border`,
`-text`, `-solid` and `-tint` token in `theme.css`, for light and dark.

Unknown `kapi-*` elements render as a visible error box, never silently.

The renderer puts `data-kapi-preset="<name>"` on the outer element of every
rendered preset. A rendered flow diagram also carries `data-kapi-dir` with the
direction it was drawn in. Prose elements render to the same HTML as their source.

### Editing

Readers edit what they see, never markup:

| Block | Edited as | Saved as |
|---|---|---|
| prose (no `data-kapi-preset`) | the element itself, in place | `edit` with the element's outer HTML |
| `kapi-table` | each rendered `th` and `td`, in place, as plain text | `cells` with only the changed cells |
| `kapi-callout`, `kapi-option`, `kapi-step`, `kapi-risk`, `kapi-question` | the rendered body, in place | `edit` with `inner` set to the body |
| `kapi-code`, `kapi-diff`, `kapi-diagram`, `kapi-form`, and any other block that holds presets | a textarea with `GET /p/{id}/source/{block}?part=inner` | `edit` with `inner` set to the textarea |

A prose preset is edited in place only when its rendered body round-trips to
its source inner (the browser parses the source inner and compares it with
the body's HTML); otherwise it falls back to the textarea. In every mode the
element's opening tag, its attributes and its closing tag are never shown and
never change, and rendered chrome (labels, marks, headings) is not editable.

A `cells` op is `{"kind":"cells","id":"<table id>","cells":[{"row":r,"col":c,"text":"..."}]}`.
`row` counts the `tr` elements of the source table in source order, header
rows included; `col` counts the `th` and `td` children of that row. The store
sets each cell's text, HTML-escaped, between its existing tags, so the cell's
tag and attributes stay as they were. A cell outside the table, a cell given
twice, an empty `cells` list or a target that is not a `kapi-table` is 422.
Adding or removing rows and columns is out of scope. A `cells` op follows the
same staleness, dangling-link and no-change rules as `edit`.

A `kapi-badge` may sit inside prose, since a badge stays inline. The `HTML` of
a prose element may hold `<kapi-badge>` elements; any other `kapi-*` element
or a `data-kapi-preset` attribute in it is refused with 422.

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
func (s *Store) Apply(rev uint64, op *Op) (uint64, Summary, error) // ErrStale when rev != current
type Op struct {
    Kind string // edit, cells, delete, archive, unarchive, move, undo, check, orient
    ID   string
    HTML string // edit: new outer HTML of the element
    Inner *string // edit, instead of HTML: new source between the element's start and end tags
    Cells []Cell  // cells: {Row, Col, Text} for each changed table cell
    Before string // move: id to insert before, "" means end
    Done bool // check: true adds done to the kapi-task, false removes it
    Dir string // orient: tb or lr
}
type Summary struct{ Text string; IDs []string } // `deleted callout "Freeze writes."` etc., goes into the event log
var ErrStale, ErrNotFound, ErrInboundLinks error
```

`delete` fails with `ErrInboundLinks` when another element links to `#<id>`.
`edit` follows the same rule: it fails with `ErrDanglingLinks` (422, naming
the ids) when it adds a link to a missing id or removes an element that other
elements link to.
An `edit` with `inner` keeps the element's start tag and end tag byte for byte
and replaces only the source between them. It follows every rule an `edit`
with `html` does, and it fails with `ErrInnerEscapes` (422) when the new body
would close the element or leave it unclosed. An op with both `html` and
`inner` is 422.
An op whose result is byte for byte the current source changes nothing: it
returns 200 with the current `rev`, writes no `revs/` file, adds no undo step
and logs no event (`Apply` returns an empty `Summary`).
`archive` sets `data-kapi-state="archived"` on the element and `unarchive`
removes `data-kapi-state`. An archived block renders dimmed and collapsed to
one line: a small "archived" label and a text excerpt. Its toolbar offers
Unarchive in place of Edit and Archive.
`check`, sent as `{"kind":"check","id":"<task id>","done":true}`, adds or
removes the `done` attribute on a `<kapi-task>` and is refused with 422 for
any other element; its summary is `checked` or `unchecked`. `orient`, sent as
`{"kind":"orient","id":"<diagram id>","dir":"lr"}`, sets `dir` on a flow or
architecture `<kapi-diagram>`; a sequence diagram, any other element, or a
`dir` other than `tb` or `lr` is refused with 422. The browser shows a Flip
action for diagrams that carry `data-kapi-dir`, except an archived one.
Every applied op saves the previous source as `revs/<rev>.html` so `undo`
restores it.

An op whose `rev` is older than the current revision still applies when its
target element's outer source at that `rev` (from `revs/<rev>.html`) equals
its current outer source, so edits to other blocks never refuse a save.
This applies to `edit`, `cells`, `archive`, `unarchive`, `delete`, `check` and
`orient` only. `move` and `undo` depend on the whole page, so they need the exact current `rev`. `ErrStale`
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
The agent's write always wins over a kapi write, whether the agent
renames a file into place or writes into the file. Before kapi writes
the file back, it rereads it 2ms later and starts over if the bytes
changed. kapi does not delete the file its swap moved aside. It keeps a
hard link to it in `pages/<id>/held/` and deletes the temp next to the page at
once, so nothing stays in the page's folder. Each swap adds its own entry to
`held` in `state.json`, holding the size, mtime and a hash of the moved-aside
file, the same for the file kapi left in place, and an expiry 10s later.
ctime is never compared, so a chmod or an xattr changes nothing. A held file
"changed" when its bytes, size or mtime differ. kapi checks every held file
before each commit, including retries, on each refresh and op, and every
second for each open page, with or without a browser. If a held file changed
and the page still holds what kapi last put there, a write landed after the
swap, so kapi swaps it back. Before that swap it records `back` (the stamps
going in and coming out) in the entry, so a kill mid-swap is settled on the
next open. If the page changed too, the page stays live and the held bytes
are renamed to `.<name>.kapi-kept-<digits>` next to the page, never deleted.
A held file that has not changed is deleted when its entry expires. When the
daemon stops, and when a page opens, every held file is settled the same way
and then deleted. If the page's folder is on another file system than
`$KAPI_HOME`, the hard link fails and nothing is held.
When two writes carry the same mtime, the order comes from what kapi
last put in place, not from the clock.

A kill or power cut at any point leaves the page file, `revs/` and
`pages/<id>/state.json` whole. Every write goes to a temp file named
`.<name>.kapi-<digits>` in the same folder, which is fsynced, renamed or
swapped into place, and followed by an fsync of the folder. `state.json`
holds `{"rev":N,"undo":[...]}`. Before swapping the page, the store writes
`revs/<N+1>.html` and adds `"next":{"rev":N+1,"undo":[...],"temp":"<name>"}`
to `state.json`. `temp` names the temp file the swap will use, which the
store creates only after recording it. Once the swap lands, it rewrites
`state.json` without `next`. On open, the store moves to `next` only when
the page file holds exactly `revs/<next rev>.html`. Otherwise it keeps `rev`,
so a revision never goes back. Once open has handled the temp files, it rewrites
`state.json` without `next`. Open also deletes its own temp files under
`pages/<id>/`. A temp file next to the page whose bytes equal `rev` or `next`
is deleted. Only the temp file that `next` names can go back into the page,
and only when the page holds exactly `next`, which means the swap landed and
the temp file holds a displaced agent write. When the page still holds `rev`,
the named temp file is kapi's unfinished write, so it is deleted. Any other
temp file is never swapped in. It is renamed to `.<name>.kapi-kept-<digits>`,
so its bytes are not lost and open does not consider it again.

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
demand it runs `kapi daemon --spawn`, which starts `kapi daemon --reopen` in
its own session, with stdin from `/dev/null` and output appended to
`$KAPI_HOME/daemon.log`, and exits at once. The daemon is then no longer a
child of the command, so stopping the command or killing its process tree
leaves the daemon running.
The command waits up to 3 seconds in all for the spawn and the socket, and
fails with the spawn's error if the spawn fails. Once HTTP is serving, that daemon
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
| `GET /p/{id}/source/{block}` | | the element's source outer HTML, `text/plain`, header `Kapi-Rev`; with `?part=inner`, only the source between its start and end tags, byte for byte (`kapi-code` and `kapi-diff` bodies are raw); any other `part` is 400 |
| `GET /p/{id}/events` | | `text/event-stream`, see below |
| `POST /p/{id}/messages` | `{"kind":"message","target":"<block id or empty>","text":"..."}` | `{"seq":N}` |
| `POST /p/{id}/ops` | `{"rev":N,"op":{...Op}}` | `{"rev":N}`; 409 `{"rev":current}` when stale; 422 `{"error":"..."}` |
| `POST /p/{id}/answers` | `{"form":"name","values":{"field":["v"]}}` | `{"seq":N}` |
| `GET /static/kapi.js`, `GET /static/chrome.css` | | the embedded files |

`{id}` is the same 16-hex page id the daemon already uses for
`pages/<id>/`.

### Events stream

The event log is `pages/<id>/events.ndjson`, one JSON event per line, and
each append is fsynced before it is reported. A final line without its
newline is a torn write: opening the log drops it and truncates the file to
the last whole line. `positions.json` is replaced through a fsynced temp
file and a rename, followed by an fsync of the folder. An empty or
unreadable `positions.json` or `cursors.json` counts as missing, so the log
still opens. Events past the last readable position are then delivered
again rather than lost.

Each line of the event log is one SSE message with `id: <seq>`. The browser
reconnects with `Last-Event-ID` and gets everything after it. Event `data` is
the `eventlog.Event` JSON. Kinds the browser cares about:

| `kind` | Meaning | Browser does |
|---|---|---|
| `message` | chat message from `human` or an agent | append to the chat panel |
| `answer` | form answer | show as sent |
| `revision` | page changed; `text` is the summary, `target` lists changed ids comma-separated | refetch those blocks, or the page when `target` is empty |
| `presence` | `text` is `watching` or `none`: whether a `kapi watch` or `kapi wait` is attached | set the status dot |

Read receipts are not in the event log. Each reader has two positions:
queued, the highest seq handed to its harness (printed by `kapi watch` or
`kapi wait`, or given to a hook), and seen, the highest seq it confirmed. When
either advances, and once right after a stream connects, the stream sends a
message with no `id:` line, so `Last-Event-ID` resume is unaffected:
`{"kind":"queued","reader":"agent","seq":N}` for the queued position and
`{"kind":"read","reader":"agent","seq":N}` for the seen one. The browser shows
a small Queued label on every human message or answer with a queued seq at or
above it, and 👀 in its place once a seen seq covers it, for any reader.

The status line under the newest message follows the newest human message
when no agent message came after it: "Queued for the agent" while it is
queued but not seen, then "Agent is working" once it is seen. Otherwise it
follows presence.

The browser keeps nothing: after any reconnect it refetches the page.

## Agent side

Ops reach the agent through the existing event log, so `kapi watch` shows
`#12 human revision on #k3f9a2bq: deleted "Move the session store to the new…"`
with no new command. A revision's `text` quotes the block: its text with
whitespace collapsed, cut to 60 characters at a word with an ellipsis, after
the preset name without `kapi-` for a preset (`archived callout "Freeze
writes."`). An edit quotes the new text. A file edit that changed several
blocks quotes up to 3 of them and counts the rest (`edited in the file: "a",
"b", callout "c", and 1 more`). `target` still lists the ids. Agent-side
`kapi op` and `kapi get` are phase 3.

`kapi say` writes its message with the reader name as author (`--reader
codex` writes author `codex`; the default is `agent`), so agents watching
under different reader names see each other's messages, while sessions that
share one reader name skip each other's. Edits made to the file itself are
logged with author `file`. `human`, `kapi` and `file` are reserved:
`watch`, `wait`, `pending`, `seen` and `say` refuse them, and the events stream sends
no read position for them.

A reader never gets its own events back: `kapi watch` skips events whose
author is the reader and queues them once everything before them is queued,
and `kapi pending` and the home view's unread counts leave them out.

### Delivery and seen

A harness may hold or drop a line it was handed, so printing an event never
marks it seen. `kapi wait` and the hooks below leave out the agent's own
page edits (authors `file` and `kapi`); `kapi watch` and `kapi pending` still
show them. Events reach the agent three ways:

- `kapi wait <page>` blocks until the page has an unseen event that is not
  queued yet, prints every unseen event, queues them and exits. The agent runs
  it as a background command whose exit wakes it, and starts it again after
  each exit. It is attached, so presence is `watching`, only while it blocks.
- `kapi watch <page>` streams events and queues each after printing it. It
  starts from the seen position, so a restarted watch prints every queued but
  unseen event again.
- `kapi setup hooks` adds Claude Code `UserPromptSubmit`, `PostToolUse` and
  `Stop` hooks that run `kapi hook-messages`. It reads the hook's JSON on
  stdin and hands the unseen events of reader `agent` on the pages under the
  hook's `cwd` to the model: as `additionalContext` for `PostToolUse` and
  `UserPromptSubmit`, and for `Stop` as `{"decision":"block","reason":...}`,
  so the agent does not go idle on them. The rule, in two lines:
  each hook shows, in order, only events past the reader's hook position, a too-long one as a short prefix with its `#n` and `kapi pending <page>`, recorded as cut.
  after writing its text the hook sends one `handover` that, in a single write, moves queued and the hook position through what it showed, records the cut events, and moves seen up to the first unread cut event; `Stop` blocks only when something passed the hook position.
  Every reader position (seen, queued, hook position, cut events) lives in
  `pages/<id>/positions.json`; an older `cursors.json` is read as seen.
  The hook position is separate from the queued position, so a message
  `kapi watch` printed still reaches the model. A cut event is never shown
  again; `UserPromptSubmit` adds a one-line reminder while it is unread. The
  text stays under 9,000 characters, the point where Claude Code swaps
  context for a short preview; events that do not fit wait for the next
  hook. A hook whose input has `agent_id` fired
  inside a subagent and hands over nothing, so messages reach the main agent.
  `kapi hook-messages` never starts the daemon, gives each daemon call 1
  second, and always exits 0; with no daemon running or answering it prints
  nothing.

The seen position moves only to what provably reached the model: a hook as
above, `kapi pending`, which prints every unseen event and marks it seen, or
an event number the agent states. `kapi seen <page> <n>` and
`kapi say --seen <n>` mark every event through `#n` seen; the agent takes `n`
from the printed lines. An `n` above the reader's queued position names an
event nothing handed over, so it is refused. A plain `kapi say` moves nothing.

## Out of scope for phase 2

Slides, the editable whiteboard, attachments, `kapi op`/`kapi get`.
