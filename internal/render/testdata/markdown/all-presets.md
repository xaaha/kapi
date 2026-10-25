# Page store cutover plan

This page walks through moving page state into the Go daemon. It uses every kapi preset, so it doubles as the visual reference for the design system. See [the risky step](#k3f9a2bq) before you start, and [the repo](https://github.com/xaaha/kapi) for context.

- **Owner**: Track A, the server
- **Status**: `in review`
- **Rollback**: Flip `KAPI_STORE` back and restart the daemon.

> [!NOTE]
> Nothing here ships until the `kapi open` command lands on main.

> [!WARNING]
> The cutover drops writes for about **30 seconds**. Schedule it outside business hours.

> **Key point:** Go owns all state. The browser keeps nothing and refetches after any reconnect.

## Where the state lives

```kapi-diagram type=flow
agent -> page file: writes
page file -> daemon: mtime change
daemon -> event log: appends
daemon -> browser: revision event
browser -> daemon: op
daemon -> page file: applies
event log -> kapi watch: streams
```

An op from the browser takes this path:

```kapi-diagram type=sequence
browser
daemon
store
browser -> daemon: POST /p/{id}/ops
daemon -> store: Apply(rev, op)
store -> store: save revs/N.html
store --> daemon: new rev, summary
daemon --> browser: 200 {"rev":N}
daemon -> browser: revision event
```

## The code

`internal/page/store.go`, lines 41-52

```go
// Source returns the current page source and its revision.
func (s *Store) Source() ([]byte, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rev < 1 && len(s.src) == 0 {
		return nil, 0
	}
	return bytes.Clone(s.src), s.rev
}

const maxRevs = 200 // keep the last 200 revisions
```

`internal/page/store.go`

```diff
diff --git a/internal/page/store.go b/internal/page/store.go
--- a/internal/page/store.go
+++ b/internal/page/store.go
@@ -12,7 +12,8 @@ type Store struct {
 	mu   sync.Mutex
 	path string
-	rev  int
+	rev  uint64
+	mod  time.Time
 	src  []byte
 }

@@ -88,4 +89,4 @@ func (s *Store) Apply(rev uint64, op Op) (uint64, Summary, error) {
 	if rev != s.rev {
-		return 0, Summary{}, errors.New("stale")
+		return s.rev, Summary{}, ErrStale
 	}
```

## Latency so far

| Endpoint | p50 ms | p99 ms | Notes |
| --- | --- | --- | --- |
| `GET /p/{id}/` | 4 | 11 | Full render with CSS |
| `GET /p/{id}/blocks/{block}` | 1 | 3 | One element |
| `POST /p/{id}/ops` | 6 | 19 | Writes the file and a revision `slowest` |

<details>
<summary>Why the p99 for ops is high</summary>

Every op writes the page file and a revision copy, and both are synced to disk before the reply.

</details>

## Transport options

**Server-sent events**

One long GET per tab. Built into every browser and reconnects with `Last-Event-ID`.

- No new dependency
- Plain HTTP, easy to curl

**WebSocket**

Two-way, but needs its own reconnect and replay logic.

- Needs a library or hand-rolled framing

**Polling**

Simplest to build. Adds up to a second of lag and wakes the daemon for nothing.

## Steps

1. Add the page store with revisions under `revs/`.
2. Switch the daemon to serve rendered pages and single blocks.

- **Risk:** An agent editing the file mid-request can race a browser op. The store must compare mtimes before it writes.

3. Delete the old static page handler.

- **Question:** Should *undo* cross an agent edit, or stop at it?

<!-- -->

- [x] Write the page store tests
- [ ] Run the cutover on staging

## Your call

When should the cutover run?

- [ ] Tonight, 22:00
- [ ] Saturday morning

Who needs a heads-up?

- [ ] Support
- [ ] Sales
- [ ] The on-call engineer

Anything else? (*free text*)

## Mistakes render visibly

bar chart

This paragraph had an `onclick` and a `style` in its source; both are gone.

> Prose tags keep their meaning: quotes, *emphasis*, **strong text**, and lists.

1. First
2. Second, with a nested list
   - Nested item
