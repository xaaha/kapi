package render

import (
	"strings"
	"testing"
)

func TestOnlyAKapiDiagramOffersTheFlip(t *testing.T) {
	src := "graph LR\n  a[Web] --> b[(Orders)]"
	if out := render(t, `<pre class="mermaid">`+src+`</pre>`); !strings.Contains(out, `data-kapi-preset="diagram"`) || strings.Contains(out, "data-kapi-dir") {
		t.Errorf("Mermaid in a <pre> should draw without data-kapi-dir, since orient cannot rewrite it:\n%.300s", out)
	}
	if out := render(t, `<kapi-diagram>`+src+`</kapi-diagram>`); !strings.Contains(out, `data-kapi-dir="lr"`) {
		t.Errorf("Mermaid in a <kapi-diagram> should keep data-kapi-dir from its header:\n%.300s", out)
	}
	if out := render(t, `<kapi-diagram dir="tb">`+src+`</kapi-diagram>`); !strings.Contains(out, `data-kapi-dir="tb"`) {
		t.Errorf("a dir attribute set by orient should win over the Mermaid header:\n%.300s", out)
	}
}
