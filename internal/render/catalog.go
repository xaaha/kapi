package render

func Presets() []Preset {
	return append([]Preset(nil), catalog...)
}

var catalog = []Preset{
	{
		Name:    "kapi-callout",
		Summary: "Boxed aside for a note, a warning, the key point, a danger or a success",
		Example: `<!-- kind: note (default, blue), warning (yellow), key (purple), danger or its alias alert (red), or success (green). Body: prose. -->
<kapi-callout kind="warning">
  <p>The cutover drops writes for about <strong>30 seconds</strong>. Schedule it outside business hours.</p>
</kapi-callout>`,
	},
	{
		Name:    "kapi-code",
		Summary: "An ordinary pre and code block with syntax highlighting, line numbers and copy controls",
		Example: `<!-- Write ordinary <pre><code> and put the language in class="language-...". Kapi supplies highlighting, line numbers and copy controls. -->
<pre><code class="language-go">func main() {
    fmt.Println("hello")
}</code></pre>`,
	},
	{
		Name:    "kapi-diff",
		Summary: "A unified diff with added, removed and context lines, hunk headers and highlighting",
		Example: `<!-- Body: a unified diff exactly as git diff prints it; nothing needs escaping.
     file: label shown above; defaults to the +++ path. lang: highlight the code; defaults to the file's language. -->
<kapi-diff file="internal/page/store.go">
@@ -12,6 +12,7 @@ type Store struct {
 	mu   sync.Mutex
 	path string
-	rev  int
+	rev  uint64
+	mod  time.Time
 	src  []byte
 }
</kapi-diff>`,
	},
	{
		Name:    "kapi-table",
		Summary: "A plain HTML table with Kapi styling, sorting, responsive layout and expansion",
		Example: `<!-- Write an ordinary table with a <thead>. Kapi gives every table the same formatting and controls; kapi-table remains supported for older pages. -->
<table>
  <thead><tr><th>Endpoint</th><th>p50 ms</th><th>p99 ms</th></tr></thead>
  <tbody>
    <tr><td>GET /p/{id}/</td><td>4</td><td>11</td></tr>
    <tr><td>POST /p/{id}/ops</td><td>6</td><td>19</td></tr>
  </tbody>
</table>`,
	},
	{
		Name:    "kapi-gallery",
		Summary: "Images side by side; double-click one to open it with previous and next controls",
		Example: `<!-- Body: figures containing images and optional captions. Use kapi attach with several image paths to create this automatically. -->
<kapi-gallery>
  <figure><img src="./review.files/before.png" alt="Before"><figcaption>Before</figcaption></figure>
  <figure><img src="./review.files/after.png" alt="After"><figcaption>After</figcaption></figure>
</kapi-gallery>`,
	},
	{
		Name:    "kapi-render",
		Summary: "A forgiving HTML and CSS mockup, isolated from the rest of the page and themed by Kapi",
		Example: `<!-- title: label above the preview. Body: ordinary HTML and CSS. Scripts and unsafe attributes are removed; malformed or unknown markup degrades to visible content.
     Use it for UI options too: one kapi-render per variant, a fixed-width box (about 390px) for a phone, CSS animation for motion.
     Theme variables follow the reader's light or dark mode: var(--kapi-bg), var(--kapi-fg), var(--kapi-muted), var(--kapi-surface), var(--kapi-subtle),
     var(--kapi-border), var(--kapi-border-strong), var(--kapi-radius), var(--kapi-radius-lg), var(--kapi-shadow), var(--kapi-font), var(--kapi-mono),
     and var(--kapi-<colour>-text|fill|border|solid) for blue, green, yellow, orange, red, purple and grey. -->
<kapi-render title="Dashboard mockup">
  <style>
    .cards { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px }
    .card { padding:20px; border:1px solid var(--kapi-border); border-radius:var(--kapi-radius-lg); background:var(--kapi-surface) }
  </style>
  <main>
    <h1>Funding</h1>
    <section class="cards">
      <article class="card"><h2>Current</h2><p>No enrollment fee card.</p></article>
      <article class="card"><h2>Proposed</h2><p>Submit a fee.</p><button>Submit a fee</button></article>
    </section>
  </main>
</kapi-render>`,
	},
	{
		Name:    "kapi-compare",
		Summary: "Options side by side as cards, one per kapi-option",
		Example: `<!-- Children: <kapi-option title="..."> each holding prose. Two to four options read best. -->
<kapi-compare>
  <kapi-option title="Server-sent events">
    <p>One long GET per tab. Built into every browser, reconnects with Last-Event-ID.</p>
  </kapi-option>
  <kapi-option title="WebSocket">
    <p>Two-way, but needs its own reconnect and replay logic.</p>
  </kapi-option>
</kapi-compare>`,
	},
	{
		Name:    "kapi-plan",
		Summary: "Numbered steps with risks and open questions in line",
		Example: `<!-- Children: <kapi-step>, <kapi-risk> and <kapi-question>, each holding prose. Steps are numbered in order.
     state on a step (done, failed, blocked, progress, waiting or info) shows its status icon in place of the number. -->
<kapi-plan>
  <kapi-step state="done">Add the page store with revisions under <code>revs/</code>.</kapi-step>
  <kapi-step state="progress">Serve the rendered page and block endpoints.</kapi-step>
  <kapi-risk>Agents editing the file mid-request can race a browser op.</kapi-risk>
  <kapi-question>Should undo cross an agent edit?</kapi-question>
</kapi-plan>`,
	},
	{
		Name:    "kapi-diagram",
		Summary: "A flow chart, sequence diagram or architecture diagram drawn from one line per node or edge",
		Example: `<!-- type: flow (default) or sequence (lifelines, messages in order).
     dir: tb (top to bottom, the default for flow) or lr (left to right). Sequence diagrams ignore dir.
     Body: one edge per line, "a -> b: label". The label is optional. "a --> b" draws a dashed line.
     A line with only a name declares a node (flow) or fixes participant order (sequence). Keep it under ~15 nodes.
     In a flow, "name: tags" gives a node a kind and a state (see architecture below). -->
<kapi-diagram type="flow">
  page file: file
  daemon: service
  agent -> page file: writes
  page file -> daemon: mtime change
  daemon -> browser: revision event
  browser -> daemon: op
  daemon -> page file: applies
</kapi-diagram>

<kapi-diagram type="sequence">
  browser -> daemon: POST /ops
  daemon -> store: Apply(rev, op)
  store --> daemon: new rev
  daemon --> browser: 200 {"rev":N}
</kapi-diagram>

<!-- type: architecture (left to right by default; dir="tb" stacks it top to bottom).
     Node lines: "id: Title | subtitle | subtitle | tags". id is letters, digits, - _ or .
     tags, space separated, all optional:
       a kind, drawn as a large icon over the label: agent, database, user, service, page, file, queue, timer or external;
       a state, the only thing drawn in colour (border and a small icon): done, failed, blocked, progress, waiting or info;
       planned, drawn dashed.
     Nodes are neutral otherwise. Edges: "a -> b: label", "a <-> b: label" (both ways), "a --> b: label" (dashed).
     Every edge names declared ids. A legend for the states in use is added. -->
<kapi-diagram type="architecture">
  agent: Agent | kapi watch: one line per event | user
  daemon: kapi daemon | event log, page store | service progress
  disk: ~/.kapi | pages, cursors, revisions | database done
  hub: Private hub | R2 + Worker | external planned
  agent <-> daemon: socket
  daemon -> disk: append
  daemon --> hub: publish
</kapi-diagram>`,
	},
	{
		Name:    "kapi-form",
		Summary: "Questions the reader answers with choices and free text; answers reach kapi watch",
		Example: `<!-- name: the form's name, reported with the answers.
     Children: <kapi-choice name="..."> (add multiple for checkboxes) whose loose text is the question and whose
     <kapi-opt value="...">label</kapi-opt> children are the options, and <kapi-text name="...">label</kapi-text>
     for free text. Do not self-close these elements. -->
<kapi-form name="cutover">
  <kapi-choice name="window">When should the cutover run?
    <kapi-opt value="tonight">Tonight, 22:00</kapi-opt>
    <kapi-opt value="weekend">Saturday morning</kapi-opt>
  </kapi-choice>
  <kapi-choice name="notify" multiple>Who needs a heads-up?
    <kapi-opt value="support">Support</kapi-opt>
    <kapi-opt value="sales">Sales</kapi-opt>
  </kapi-choice>
  <kapi-text name="notes">Anything else?</kapi-text>
</kapi-form>`,
	},
	{
		Name:    "kapi-badge",
		Summary: "A small coloured label or status icon, inside prose or a table cell",
		Example: `<!-- tone: red, orange, yellow, green, blue, purple or grey (default); gray and common colour names work too.
     Any other name gets a stable theme colour. Hex colours such as #7c3aed derive readable light and dark variants. Body: a word or two.
     state: done (green check), failed or blocked (red x), progress (orange), waiting (grey clock) or info (blue).
     It leads the label with its icon and sets the tone unless tone is given. With no body it is the icon alone.
     Use it inline: inside a <p>, an <li> or a table cell. -->
<p>PR #19 is <kapi-badge tone="green">merged</kapi-badge>, #18 is <kapi-badge tone="yellow">in review</kapi-badge>.</p>
<p>Copy <kapi-badge state="done"></kapi-badge> Compare <kapi-badge state="progress"></kapi-badge> Flip <kapi-badge state="waiting">next</kapi-badge></p>`,
	},
	{
		Name:    "kapi-kv",
		Summary: "An ordinary definition list formatted as key and value pairs",
		Example: `<!-- Write an ordinary <dl> containing alternating <dt> keys and <dd> values. -->
<dl>
  <dt>Owner</dt><dd>Pratik</dd>
  <dt>Status</dt><dd><kapi-badge tone="yellow">in review</kapi-badge></dd>
  <dt>Rollback</dt><dd>Flip <code>KAPI_STORE</code> back and restart the daemon.</dd>
</dl>`,
	},
	{
		Name:    "kapi-details",
		Summary: "An ordinary details and summary section formatted for information readers can skip",
		Example: `<!-- Write ordinary <details><summary> markup. Add open to start expanded. -->
<details>
  <summary>Why copy before flipping reads?</summary>
  <p>Copying first lets us compare row counts before any reads move, so a bad copy never serves traffic.</p>
</details>`,
	},
	{
		Name:    "kapi-checklist",
		Summary: "An ordinary list of checkbox tasks whose checked state is written back to the file",
		Example: `<!-- Write an ordinary <ul>; begin every <li> with <input type="checkbox">. Add checked to completed tasks. -->
<ul>
  <li><input type="checkbox" checked>Copy the session store to the new cluster</li>
  <li><input type="checkbox">Compare row counts</li>
  <li><input type="checkbox">Flip reads</li>
</ul>`,
	},
}
