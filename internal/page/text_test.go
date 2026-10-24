package page

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/xaaha/kapi/internal/render"
)

func cell(row, col int, inner string) Part {
	return Part{ID: "aaaaaaaa", Row: &row, Col: col, Inner: inner}
}

func TestATextOpSetsCellsAndKeepsTheirTags(t *testing.T) {
	home, path := newPage(t, tablePage)
	s := open(t, home, path)
	sum := mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{
		cell(0, 1, "Row count"),
		cell(1, 0, `b&lt;eta&gt; &amp; "co" <code>x</code>`),
		cell(2, 1, "nine"),
	}})
	want := strings.NewReplacer(
		"<th>Rows</th>", "<th>Row count</th>",
		`<td class="name">beta</td>`, `<td class="name">b&lt;eta&gt; &amp; "co" <code>x</code></td>`,
		"<td>9</tr>", "<td>nine</tr>",
	).Replace(tablePage)
	if got := onDisk(t, path); got != want {
		t.Errorf("file after text op:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasPrefix(sum.Text, `edited table "Shard Row count`) || strings.Join(sum.IDs, ",") != "aaaaaaaa" {
		t.Errorf("summary = %+v", sum)
	}
}

func TestATextOpRefusesPartsItCannotEdit(t *testing.T) {
	const page = tablePage + "<kapi-plan data-kapi-id=\"cccccccc\"><kapi-step data-kapi-id=\"dddddddd\">one</kapi-step></kapi-plan>\n"
	for _, op := range []Op{
		{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(3, 0, "x")}},
		{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(0, 2, "x")}},
		{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(-1, 0, "x")}},
		{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(0, 0, "x"), cell(0, 0, "y")}},
		{Kind: "text", ID: "aaaaaaaa"},
		{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa", Inner: "x"}}},
		{Kind: "text", ID: "bbbbbbbb", Parts: []Part{{ID: "bbbbbbbb", Row: new(int), Inner: "x"}}},
		{Kind: "text", ID: "cccccccc", Parts: []Part{{ID: "cccccccc", Inner: "x"}}},
		{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "dddddddd", Inner: "x"}}},
		{Kind: "text", ID: "bbbbbbbb", Parts: []Part{{ID: "bbbbbbbb", Inner: "a</p><p>b"}}},
	} {
		home, path := newPage(t, page)
		s := open(t, home, path)
		if _, _, err := s.Apply(1, &op); err == nil {
			t.Errorf("%+v succeeded, want an error", op)
		}
		if got := onDisk(t, path); got != page {
			t.Errorf("refused op changed the file:\n%s", got)
		}
	}
}

func TestATextOpOnATableThatChangedIsStale(t *testing.T) {
	home, path := newPage(t, tablePage)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "archive", ID: "bbbbbbbb"})
	if _, _, err := s.Apply(1, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(1, 1, "11")}}); err != nil {
		t.Fatalf("text op at an old rev with the table unchanged: %v", err)
	}
	if _, _, err := s.Apply(2, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(1, 1, "12")}}); !errors.Is(err, ErrStale) {
		t.Errorf("text op at a rev before the table changed: err = %v, want ErrStale", err)
	}
}

func TestATextOpIsStaleOnlyWhenItsOwnTextChanged(t *testing.T) {
	const src = "<kapi-checklist data-kapi-id=\"aaaaaaaa\"><kapi-task data-kapi-id=\"bbbbbbbb\">one</kapi-task><kapi-task data-kapi-id=\"cccccccc\">two</kapi-task></kapi-checklist>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "check", ID: "cccccccc", Done: true})
	if _, _, err := s.Apply(1, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "cccccccc", Inner: "two more"}}}); err != nil {
		t.Fatalf("text op after a tick on the same task: %v", err)
	}
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "bbbbbbbb", Inner: "uno"}}})
	if _, _, err := s.Apply(3, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "bbbbbbbb", Inner: "eins"}}}); !errors.Is(err, ErrStale) {
		t.Errorf("text op over text that changed since: err = %v, want ErrStale", err)
	}
	if got, want := onDisk(t, path), "<kapi-checklist data-kapi-id=\"aaaaaaaa\"><kapi-task data-kapi-id=\"bbbbbbbb\">uno</kapi-task><kapi-task done data-kapi-id=\"cccccccc\">two more</kapi-task></kapi-checklist>\n"; got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestATextOpFollowsImpliedEndTags(t *testing.T) {
	const src = "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><td>a<td>b<tr><td>c<td>d</table></kapi-table>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(0, 1, "B")}})
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(1, 0, "C"), cell(1, 1, "D")}})
	if got, want := onDisk(t, path), "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><td>a<td>B<tr><td>C<td>D</table></kapi-table>\n"; got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestATextOpKeepsTheSourceOfPresetsInsideTheText(t *testing.T) {
	const src = "<kapi-table data-kapi-id=\"aaaaaaaa\"><table><tr><td>see <kapi-badge data-kapi-id=\"bbbbbbbb\"  tone='green'>merged</kapi-badge> <a href=\"#cccccccc\">c</a></td></tr></table></kapi-table>\n" +
		"<kapi-callout data-kapi-id=\"cccccccc\" kind=\"note\"><p>run</p>\n<kapi-code data-kapi-id=\"dddddddd\" lang=\"go\">if a < b {}\n</kapi-code></kapi-callout>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{cell(0, 0,
		`now <span class="kapi-badge kapi-tone-green" data-kapi-preset="badge" data-kapi-id="bbbbbbbb" contenteditable="false">merged</span> <a href="#cccccccc">c</a>`)}})
	mustApply(t, s, &Op{Kind: "text", ID: "cccccccc", Parts: []Part{{ID: "cccccccc",
		Inner: `<p data-kapi-selected="">run it</p>` + "\n" + `<figure class="kapi-code" data-kapi-preset="code" data-kapi-id="dddddddd"><pre>whatever the page shows</pre></figure>`}}})
	want := strings.NewReplacer("<td>see ", "<td>now ", "<p>run</p>", "<p>run it</p>").Replace(src)
	if got := onDisk(t, path); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestATextOpDropsWhatTheEditorAddsAndRemovesDeletedPresets(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a <kapi-badge data-kapi-id=\"bbbbbbbb\">x</kapi-badge></p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa", Inner: `<b style="color: red" contenteditable="true" data-kapi-row="1">bold</b> and <code>c</code>`}}})
	if got, want := onDisk(t, path), "<p data-kapi-id=\"aaaaaaaa\"><b>bold</b> and <code>c</code></p>\n"; got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestATextOpRefusesPresetsFromOutsideItsText(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a</p>\n<p data-kapi-id=\"cccccccc\"><kapi-badge data-kapi-id=\"bbbbbbbb\">x</kapi-badge></p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa", Inner: `<span data-kapi-preset="badge" data-kapi-id="bbbbbbbb">x</span>`}}}); err == nil {
		t.Error("a preset copied in from another block was accepted")
	}
}

func TestATextOpRefusesTextWithMarkupThePageHides(t *testing.T) {
	const src = "<kapi-callout data-kapi-id=\"aaaaaaaa\"><p onclick=\"x()\">a</p></kapi-callout>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa", Inner: "<p>b</p>"}}}); !errors.Is(err, ErrHiddenMarkup) {
		t.Errorf("err = %v, want ErrHiddenMarkup", err)
	}
	if got := onDisk(t, path); got != src {
		t.Errorf("refused op changed the file:\n%s", got)
	}
}

func TestATextOpRefusesARawPartInsideItsBlock(t *testing.T) {
	const src = "<kapi-callout data-kapi-id=\"aaaaaaaa\"><p>run</p><kapi-code data-kapi-id=\"bbbbbbbb\" lang=\"go\">x\n</kapi-code></kapi-callout>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	if _, _, err := s.Apply(1, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "bbbbbbbb", Inner: `<figcaption>go</figcaption><pre><code><span class="kapi-line">x</span></code></pre>`}}}); err == nil {
		t.Error("a raw part edited through its container was accepted")
	}
	if got := onDisk(t, path); got != src {
		t.Errorf("refused op changed the file:\n%s", got)
	}
}

func TestATextOpEditsProseThatHoldsABlockPreset(t *testing.T) {
	const src = "<div data-kapi-id=\"aaaaaaaa\"><p>intro</p><kapi-callout data-kapi-id=\"bbbbbbbb\" kind=\"note\">Freeze writes.</kapi-callout></div>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa",
		Inner: `<p>intro, edited</p><aside class="kapi-callout" data-kapi-preset="callout" data-kapi-id="bbbbbbbb" contenteditable="false">Freeze writes.</aside>`}}})
	if got, want := onDisk(t, path), strings.Replace(src, "<p>intro</p>", "<p>intro, edited</p>", 1); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestATextOpDropsOnlyTheZeroWidthSpaceAfterNewInlineCode(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a\u200bb</p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa", Inner: "a\u200bb <code>c</code>\u200b d"}}})
	if got, want := onDisk(t, path), "<p data-kapi-id=\"aaaaaaaa\">a\u200bb <code>c</code> d</p>\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestATextOpDropsIdsPastedFromElsewhereAndKeepsCommentsThatLookLikeItsMarks(t *testing.T) {
	const src = "<p data-kapi-id=\"aaaaaaaa\">a<!--kapi-keep:bbbbbbbb--></p>\n<p data-kapi-id=\"bbbbbbbb\">b <kapi-badge data-kapi-id=\"cccccccc\">x</kapi-badge></p>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa", Inner: `a<!--kapi-keep:bbbbbbbb--> <span data-kapi-id="bbbbbbbb">b</span>`}}})
	if got, want := onDisk(t, path), strings.Replace(src, "a<!--kapi-keep:bbbbbbbb--></p>", "a<!--kapi-keep:bbbbbbbb--> <span>b</span></p>", 1); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestATextOpWithManyPartsIsOneRevision(t *testing.T) {
	const src = "<kapi-checklist data-kapi-id=\"aaaaaaaa\"><kapi-task data-kapi-id=\"bbbbbbbb\">one</kapi-task><kapi-task data-kapi-id=\"cccccccc\" done>two</kapi-task></kapi-checklist>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "bbbbbbbb", Inner: "uno"}, {ID: "cccccccc", Inner: "dos"}}})
	if got, want := onDisk(t, path), strings.NewReplacer(">one<", ">uno<", ">two<", ">dos<").Replace(src); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestATextOpSetsRawBodiesByteForByte(t *testing.T) {
	const src = "<kapi-code data-kapi-id=\"aaaaaaaa\" lang=\"go\">x</kapi-code>\n"
	home, path := newPage(t, src)
	s := open(t, home, path)
	mustApply(t, s, &Op{Kind: "text", ID: "aaaaaaaa", Parts: []Part{{ID: "aaaaaaaa", Inner: "if a < b && c {\n}\n"}}})
	if got, want := onDisk(t, path), "<kapi-code data-kapi-id=\"aaaaaaaa\" lang=\"go\">if a < b && c {\n}\n</kapi-code>\n"; got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestEveryTextPartOfTheGalleryEditsInPlaceAndKeepsTheRest(t *testing.T) {
	gallery, err := os.ReadFile("../render/testdata/gallery.html")
	if err != nil {
		t.Fatal(err)
	}
	home, path := newPage(t, string(gallery))
	src, _ := open(t, home, path).Source()
	parts := renderedParts(t, src)
	kinds := map[render.TextPart]int{}
	for _, p := range parts {
		kinds[p.kind]++
	}
	for _, k := range []render.TextPart{render.BodyText, render.CellText, render.RawText} {
		if kinds[k] == 0 {
			t.Fatalf("the gallery has no text part of kind %d", k)
		}
	}
	for _, p := range parts {
		name := p.part.ID
		if p.part.Row != nil {
			name += "/" + strconv.Itoa(*p.part.Row) + "," + strconv.Itoa(p.part.Col)
		}
		t.Run(name, func(t *testing.T) {
			home, path := newPage(t, string(src))
			s := open(t, home, path)
			part := p.part
			mark := "edited" + strings.ReplaceAll(name, "/", "")
			if p.kind == render.RawText {
				inner, err := Inner(src, part.ID)
				if err != nil {
					t.Fatal(err)
				}
				part.Inner = string(inner)
			} else {
				part.Inner += " <em>" + mark + "</em>"
			}
			if _, _, err := s.Apply(1, &Op{Kind: "text", ID: part.ID, Parts: []Part{part}}); err != nil {
				t.Fatalf("editing %s: %v", name, err)
			}
			out, _ := s.Source()
			if p.kind != render.RawText {
				again := renderedParts(t, out)
				var found bool
				for _, q := range again {
					found = found || (q.part.ID == part.ID && rowOf(q.part) == rowOf(part) && q.part.Col == part.Col && strings.HasSuffix(q.part.Inner, "<em>"+mark+"</em>"))
				}
				if !found {
					t.Errorf("the edit does not show in its part after rendering")
				}
			} else if !bytes.Equal(out, src) {
				t.Errorf("saving a raw part unchanged changed the file")
			}
			before, after := attrsByID(src), attrsByID(out)
			for id, attrs := range before {
				if after[id] != attrs {
					t.Errorf("#%s: attributes %q became %q", id, attrs, after[id])
				}
			}
			if len(after) != len(before) {
				t.Errorf("ids: %d before, %d after", len(before), len(after))
			}
		})
	}
}

type renderedPart struct {
	part Part
	kind render.TextPart
}

func rowOf(p Part) int {
	if p.Row == nil {
		return -1
	}
	return *p.Row
}

func renderedParts(t *testing.T, src []byte) []renderedPart {
	t.Helper()
	out, err := render.Render(src)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := html.ParseFragment(bytes.NewReader(out), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		t.Fatal(err)
	}
	var parts []renderedPart
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if id := nodeAttr(n, "data-kapi-text"); n.Type == html.ElementNode && id != "" {
			var b bytes.Buffer
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if err := html.Render(&b, c); err != nil {
					t.Fatal(err)
				}
			}
			rp := renderedPart{part: Part{ID: id, Inner: b.String()}, kind: render.BodyText}
			if at := nodeAttr(n, "data-kapi-cell"); at != "" {
				r, c, _ := strings.Cut(at, ",")
				row, _ := strconv.Atoi(r)
				rp.part.Row = &row
				rp.part.Col, _ = strconv.Atoi(c)
				rp.kind = render.CellText
			}
			for _, a := range n.Attr {
				if a.Key == "data-kapi-raw" {
					rp.kind = render.RawText
				}
			}
			parts = append(parts, rp)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return parts
}

func attrsByID(src []byte) map[string]string {
	out := map[string]string{}
	for _, e := range parse(src) {
		if e.id == "" {
			continue
		}
		var b strings.Builder
		b.WriteString(e.name)
		for _, a := range e.attrs {
			b.WriteString(" " + a.Key + "=" + a.Val)
		}
		out[e.id] = b.String()
	}
	return out
}
