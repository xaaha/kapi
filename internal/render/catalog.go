package render

func Presets() []Preset {
	return append([]Preset(nil), catalog...)
}

var catalog = []Preset{
	{
		Name:    "kapi-callout",
		Summary: "Boxed aside for a note, a warning, the key point, a danger or a success",
		Example: `<!-- kind: note (default, info tone), warning, key (accent tone), danger or success. Body: prose. -->
<kapi-callout kind="warning">
  <p>The cutover drops writes for about <strong>30 seconds</strong>. Schedule it outside business hours.</p>
</kapi-callout>`,
	},
	{
		Name:    "kapi-code",
		Summary: "Highlighted source with line numbers and an optional file label",
		Example: `<!-- lang: a language name (go, ts, python, sql, ...). file: label shown above, also picks the language when lang is missing.
     lines: first-last line numbers in the original file, like 10-24; numbering starts at the first.
     Body: the raw source exactly as it is; nothing needs escaping. Common indentation is removed. -->
<kapi-code lang="go" file="internal/page/store.go" lines="41-47">
func (s *Store) Source() ([]byte, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rev < 1 {
		return nil, 0
	}
	return s.src, s.rev
}
</kapi-code>`,
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
		Summary: "A styled plain table; sort=on marks it sortable by column",
		Example: `<!-- sort: on, or leave it out. Body: exactly one plain <table> with a <thead>. -->
<kapi-table sort="on">
  <table>
    <thead><tr><th>Endpoint</th><th>p50 ms</th><th>p99 ms</th></tr></thead>
    <tbody>
      <tr><td>GET /p/{id}/</td><td>4</td><td>11</td></tr>
      <tr><td>POST /p/{id}/ops</td><td>6</td><td>19</td></tr>
    </tbody>
  </table>
</kapi-table>`,
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
		Example: `<!-- Children: <kapi-step>, <kapi-risk> and <kapi-question>, each holding prose. Steps are numbered in order. -->
<kapi-plan>
  <kapi-step>Add the page store with revisions under <code>revs/</code>.</kapi-step>
  <kapi-step>Serve the rendered page and block endpoints.</kapi-step>
  <kapi-risk>Agents editing the file mid-request can race a browser op.</kapi-risk>
  <kapi-question>Should undo cross an agent edit?</kapi-question>
</kapi-plan>`,
	},
	{
		Name:    "kapi-diagram",
		Summary: "A flow chart or sequence diagram drawn from one edge per line",
		Example: `<!-- type: flow (default, top to bottom) or sequence (lifelines, messages in order).
     Body: one edge per line, "a -> b: label". The label is optional. "a --> b" draws a dashed line.
     A line with only a name declares a node (flow) or fixes participant order (sequence). Keep it under ~15 nodes. -->
<kapi-diagram type="flow">
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
}
