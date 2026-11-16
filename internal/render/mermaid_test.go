package render

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAHugeMermaidFanOutShowsAsCodeInsteadOfDrawing(t *testing.T) {
	var as, bs []string
	for i := range 120 {
		as = append(as, fmt.Sprintf("a%d", i))
		bs = append(bs, fmt.Sprintf("b%d", i))
	}
	src := "<pre class=\"mermaid\">flowchart TD\n  " + strings.Join(as, "&") + " --> " + strings.Join(bs, "&") + "</pre>"
	start := time.Now()
	out := render(t, src)
	if took := time.Since(start); took > 2*time.Second || strings.Contains(out, "<svg") || len(out) > 100_000 {
		t.Fatalf("a 14,400 edge fan-out took %v and %d bytes; want a plain code block", took, len(out))
	}
	notes, err := Repairs([]byte(src))
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "too many nodes or edges") || len(notes[0]) > 200 {
		t.Errorf("notes = %q, %v; want one saying the diagram has too many edges", notes, err)
	}
}

func TestMermaidFlowchartsOfferTheFlip(t *testing.T) {
	src := "graph LR\n  a[Web] --> b[(Orders)]"
	for _, pre := range []string{`<pre class="mermaid">` + src + `</pre>`, `<pre><code class="language-mermaid">` + src + `</code></pre>`} {
		if out := render(t, pre); !strings.Contains(out, `data-kapi-preset="diagram"`) || !strings.Contains(out, `data-kapi-dir="lr"`) {
			t.Errorf("a Mermaid flowchart in a <pre> should carry data-kapi-dir so the reader can flip it:\n%.300s", out)
		}
	}
	if out := render(t, "<pre class=\"mermaid\">sequenceDiagram\n  a->>b: hi</pre>"); strings.Contains(out, "data-kapi-dir") {
		t.Errorf("a sequence diagram cannot be flipped:\n%.300s", out)
	}
	if out := render(t, `<kapi-diagram>`+src+`</kapi-diagram>`); !strings.Contains(out, `data-kapi-dir="lr"`) {
		t.Errorf("Mermaid in a <kapi-diagram> should keep data-kapi-dir from its header:\n%.300s", out)
	}
	if out := render(t, `<kapi-diagram dir="tb">`+src+`</kapi-diagram>`); !strings.Contains(out, `data-kapi-dir="tb"`) {
		t.Errorf("a dir attribute set by orient should win over the Mermaid header:\n%.300s", out)
	}
}
