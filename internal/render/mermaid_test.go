package render

import (
	"strings"
	"testing"
)

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
