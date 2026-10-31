package render

import (
	"bytes"
	"io/fs"
	"math"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var toneRoles = []string{"fill", "border", "text", "solid", "tint"}

var surfaceRoles = []string{"bg", "surface", "card", "panel", "raised", "lift", "subtle", "fg", "muted", "faint", "border", "border-strong", "shadow", "card-shadow", "shadow-ink", "on-solid"}

var shapeTokens = []string{"radius", "radius-sm", "radius-lg", "font", "font-body", "mono", "measure"}

func cssTokens(css []byte) map[string]string {
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*--kapi-([a-z0-9-]+):\s*(.+);$`).FindAllStringSubmatch(string(css), -1) {
		out[m[1]] = m[2]
	}
	return out
}

func themeTokens(t *testing.T) map[string]string {
	t.Helper()
	return cssTokens(themeCSS)
}

func paletteFiles(t *testing.T) map[string][]byte {
	t.Helper()
	names, err := fs.Glob(palettes, "palettes/*.css")
	if err != nil || len(names) == 0 {
		t.Fatalf("no palettes embedded: %v", err)
	}
	out := map[string][]byte{}
	for _, name := range names {
		b, err := palettes.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(path.Base(name), ".css")] = b
	}
	return out
}

func paletteTokens(t *testing.T) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{"default": themeTokens(t)}
	for id, css := range paletteFiles(t) {
		merged := themeTokens(t)
		for k, v := range cssTokens(css) {
			merged[k] = v
		}
		out[id] = merged
	}
	return out
}

func TestPalettesOverrideEveryColourAndNothingElse(t *testing.T) {
	base := themeTokens(t)
	for id, css := range paletteFiles(t) {
		if !strings.HasPrefix(string(css), `:root[data-palette="`+id+`"] {`) {
			t.Errorf(`palettes/%s.css must be one :root[data-palette="%s"] block`, id, id)
		}
		own := cssTokens(css)
		for name, val := range base {
			if _, colour := own[name]; strings.HasPrefix(val, "light-dark(#") != colour {
				t.Errorf("--kapi-%s: palettes/%s.css should override exactly the light-dark colours", name, id)
			}
		}
		for name, val := range own {
			lightDark(t, name, val)
		}
	}
}

func TestThemeMenuListsExactlyTheEmbeddedPalettes(t *testing.T) {
	js, err := os.ReadFile("../web/static/theme.js")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`\{ id: "([a-z0-9-]+)", label: "[^"]+" \}`).FindAllStringSubmatch(string(js), -1) {
		listed[m[1]] = true
	}
	if !listed["default"] {
		t.Error("theme.js does not list the default palette")
	}
	files := paletteFiles(t)
	for id := range files {
		if !listed[id] {
			t.Errorf("palettes/%s.css has no entry in theme.js PALETTES", id)
		}
	}
	for id := range listed {
		if _, ok := files[id]; !ok && id != "default" {
			t.Errorf("theme.js lists %q, but there is no palettes/%s.css", id, id)
		}
	}
}

func lightDark(t *testing.T, name, val string) (light, dark string) {
	t.Helper()
	m := regexp.MustCompile(`^light-dark\((#[0-9a-f]{6}), (#[0-9a-f]{6})\)$`).FindStringSubmatch(val)
	if m == nil {
		t.Fatalf("--kapi-%s is %q; want light-dark(#rrggbb, #rrggbb)", name, val)
	}
	return m[1], m[2]
}

func luminance(hex string) float64 {
	var c [3]float64
	for i := range c {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		s := float64(v) / 255
		if s <= 0.03928 {
			c[i] = s / 12.92
		} else {
			c[i] = math.Pow((s+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(a, b string) float64 {
	x, y := luminance(a), luminance(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}

func TestThemeDefinesExactlyTheSharedTones(t *testing.T) {
	tokens := themeTokens(t)
	want := map[string]bool{}
	for _, r := range surfaceRoles {
		want[r] = true
	}
	for _, s := range shapeTokens {
		want[s] = true
	}
	for _, tone := range tones {
		for _, r := range toneRoles {
			want[tone+"-"+r] = true
		}
	}
	for name := range tokens {
		if !want[name] {
			t.Errorf("theme.css defines --kapi-%s, which is neither a surface role nor one of the seven tones", name)
		}
	}
	for name := range want {
		if _, ok := tokens[name]; !ok {
			t.Errorf("theme.css lacks --kapi-%s", name)
		}
	}
	if len(tones) != 7 {
		t.Errorf("tones = %v, want the seven shared tones", tones)
	}
}

func TestToneTextIsReadableInBothThemes(t *testing.T) {
	for palette, tokens := range paletteTokens(t) {
		t.Run(palette, func(t *testing.T) { toneTextIsReadable(t, tokens) })
	}
}

func toneTextIsReadable(t *testing.T, tokens map[string]string) {
	pair := func(name string) [2]string {
		l, d := lightDark(t, name, tokens[name])
		return [2]string{l, d}
	}
	onSolid := tokens["on-solid"]
	check := func(what string, fg, bg [2]string, least float64) {
		for i, mode := range []string{"light", "dark"} {
			if c := contrast(fg[i], bg[i]); c < least {
				t.Errorf("%s in %s: contrast %.2f, want at least %.1f", what, mode, c, least)
			}
		}
	}
	for _, tone := range tones {
		text := pair(tone + "-text")
		for _, bg := range []string{tone + "-fill", tone + "-tint", "surface", "card", "raised", "subtle", "bg"} {
			check(tone+"-text on "+bg, text, pair(bg), 4.5)
		}
		check("on-solid on "+tone+"-solid", [2]string{onSolid, onSolid}, pair(tone+"-solid"), 4.5)
		check("fg on "+tone+"-fill", pair("fg"), pair(tone+"-fill"), 7)
		check("muted on "+tone+"-fill", pair("muted"), pair(tone+"-fill"), 4.5)
	}
	for _, role := range []string{"fg", "muted", "faint"} {
		for _, bg := range []string{"surface", "card", "panel", "raised", "subtle", "bg"} {
			check(role+" on "+bg, pair(role), pair(bg), 4.5)
		}
	}
}

func TestStylesUseOnlyThemeTokens(t *testing.T) {
	tokens := themeTokens(t)
	chrome, err := os.ReadFile("../web/static/chrome.css")
	if err != nil {
		t.Fatal(err)
	}
	var tokenRules bytes.Buffer
	tokenCSS(&tokenRules)
	files := map[string]string{"style.css": string(baseCSS), "chrome.css": string(chrome), "code token rules": tokenRules.String()}
	for name, css := range files {
		local := map[string]bool{}
		for _, m := range regexp.MustCompile(`(--kapi-[a-z0-9-]+)\s*:`).FindAllStringSubmatch(css, -1) {
			local[m[1]] = true
		}
		for _, m := range regexp.MustCompile(`var\((--kapi-[a-z0-9-]+)\)`).FindAllStringSubmatch(css, -1) {
			if _, ok := tokens[strings.TrimPrefix(m[1], "--kapi-")]; !ok && !local[m[1]] {
				t.Errorf("%s uses %s, which theme.css does not define", name, m[1])
			}
		}
		if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(css, -1); len(hex) > 0 {
			t.Errorf("%s hard-codes colours %v", name, hex)
		}
	}
	css := string(CSS())
	for _, tone := range tones {
		if !strings.Contains(css, ".kapi-tone-"+tone+" {") {
			t.Errorf("CSS has no .kapi-tone-%s class", tone)
		}
	}
}

func TestPurpleIsOnlyAToneAndADiagramColour(t *testing.T) {
	chrome, err := os.ReadFile("../web/static/chrome.css")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{".kapi-tone-purple": true}
	for name, css := range map[string]string{"style.css": string(baseCSS), "chrome.css": string(chrome)} {
		for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
			if !strings.Contains(m[2], "--kapi-purple-") {
				continue
			}
			if sel := strings.TrimSpace(m[1]); !allowed[sel] {
				t.Errorf("%s: %s uses a purple token; accents are blue", name, sel)
			}
		}
	}
}

func TestContentBlocksAreBorderlessCards(t *testing.T) {
	css := string(baseCSS)
	var m []string
	for _, r := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		if strings.Contains(r[2], "box-shadow: var(--kapi-card-shadow);") {
			m = r
			break
		}
	}
	if m == nil {
		t.Fatal("style.css has no rule giving cards the card shadow")
	}
	covers := func(sel string) bool {
		return regexp.MustCompile(`(^|[\s,(])` + regexp.QuoteMeta(sel) + `([\s,)]|$)`).MatchString(m[1])
	}
	for _, sel := range []string{".kapi-kv", ".kapi-code", ".kapi-diff", ".kapi-option", ".kapi-plan", ".kapi-form", ".kapi-diagram", "pre"} {
		if !covers(sel) {
			t.Errorf("the cards rule does not cover %s", sel)
		}
	}
	for _, sel := range []string{".kapi-table", ".kapi-plan-item", ".kapi-details", "details", ".kapi-opt"} {
		if covers(sel) {
			t.Errorf("%s should be a row or a plain line, not its own card", sel)
		}
	}
	rule := func(sel string) string {
		r := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + ` \{([^}]*)\}`).FindStringSubmatch(css)
		if len(r) < 2 {
			t.Fatalf("style.css has no %s rule", sel)
		}
		return r[1]
	}
	for _, sel := range []string{".kapi-plan-item + .kapi-plan-item", ".kapi-opt + .kapi-opt", ".kapi-details"} {
		if !strings.Contains(rule(sel), "border-") || !strings.Contains(rule(sel), "1px solid var(--kapi-border)") {
			t.Errorf("%s should be divided by a hairline", sel)
		}
	}
	if strings.Contains(rule(":is(.kapi-risk, .kapi-question)"), "tone-fill") {
		t.Error("risk and question rows should not be filled; their mark carries the meaning")
	}
	for _, decl := range []string{"border: 0;", "border-radius: var(--kapi-radius-lg);", "background: var(--kapi-card);", "box-shadow: var(--kapi-card-shadow);"} {
		if !strings.Contains(m[2], decl) {
			t.Errorf("the cards rule lacks %q", decl)
		}
	}
	tokens := themeTokens(t)
	if l, d := lightDark(t, "card", tokens["card"]); l != "#f5f5f7" || d != "#1c1c1e" {
		t.Errorf("--kapi-card = %s, %s; want #f5f5f7 in light and #1c1c1e in dark", l, d)
	}
	if l, d := lightDark(t, "bg", tokens["bg"]); l != "#ffffff" || d != "#000000" {
		t.Errorf("--kapi-bg = %s, %s; want white in light and black in dark", l, d)
	}
}

func TestNarrowTablesStackIntoLabelledCards(t *testing.T) {
	css := string(baseCSS)
	m := regexp.MustCompile(`(?s)@media \(max-width: 640px\) \{(.*?)\n\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatal("style.css has no @media (max-width: 640px) block")
	}
	if strings.Contains(css, "@container") {
		t.Error("narrow layouts must follow the window width, not a container that the chat panel shrinks")
	}
	for _, want := range []string{
		".kapi-table thead",
		".kapi-table :is(table, tbody, tr, td, th)",
		"display: block;",
		"content: attr(data-kapi-label);",
	} {
		if !strings.Contains(m[1], want) {
			t.Errorf("the narrow table rules lack %q", want)
		}
	}
}

func TestPageStylesLeaveTheChatStatusClassAlone(t *testing.T) {
	if regexp.MustCompile(`\.kapi-status\b`).MatchString(string(baseCSS)) {
		t.Error("style.css styles .kapi-status, which is the chat presence text")
	}
}

func TestHighlightedPreDropsTheCardShadow(t *testing.T) {
	m := regexp.MustCompile(`(?s)#kapi-page pre\.kapi-hl,\s*pre\.kapi-hl \{([^}]*)\}`).FindStringSubmatch(string(baseCSS))
	if len(m) < 2 || !strings.Contains(m[1], "box-shadow: none;") {
		t.Error("pre.kapi-hl inside a code card must set box-shadow: none, or it draws a second card shadow")
	}
}

func TestChatPanelTintStandsApartFromPageAndCards(t *testing.T) {
	for palette, tokens := range paletteTokens(t) {
		t.Run(palette, func(t *testing.T) { chatPanelStandsApart(t, tokens) })
	}
}

func chatPanelStandsApart(t *testing.T, tokens map[string]string) {
	pl, pd := lightDark(t, "panel", tokens["panel"])
	for _, other := range []struct {
		name  string
		least float64
	}{{"bg", 1.08}, {"card", 1.05}} {
		ol, od := lightDark(t, other.name, tokens[other.name])
		if c := contrast(pl, ol); c < other.least {
			t.Errorf("light panel %s against %s %s: %.3f, want at least %.2f", pl, other.name, ol, c, other.least)
		}
		if c := contrast(pd, od); c < other.least {
			t.Errorf("dark panel %s against %s %s: %.3f, want at least %.2f", pd, other.name, od, c, other.least)
		}
	}
	for _, tone := range []string{"blue-text", "red-text", "green-text"} {
		l, d := lightDark(t, tone, tokens[tone])
		if contrast(l, pl) < 4.5 || contrast(d, pd) < 4.5 {
			t.Errorf("%s is not readable on the panel", tone)
		}
	}
}

func TestRunningTextUsesTheSerifBodyFaceAndEverythingElseTheSans(t *testing.T) {
	css := string(CSS())
	for _, want := range []string{`--kapi-font: "Hanken Grotesk"`, `--kapi-font-body: "Source Serif 4"`, ":where(#kapi-page) > :is(p, ul, ol, blockquote):not([data-kapi-preset]),\n.kapi-callout-body,\n.kapi-details-body > :is(p, ul, ol, blockquote):not([data-kapi-preset]) {\n  font-family: var(--kapi-font-body);", ".kapi-badge {\n  font-family: var(--kapi-font);"} {
		if !strings.Contains(css, want) {
			t.Errorf("CSS lacks %q", want)
		}
	}
}
