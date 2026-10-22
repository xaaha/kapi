package render

import (
	"bytes"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var toneRoles = []string{"fill", "border", "text", "solid", "tint"}

var surfaceRoles = []string{"bg", "surface", "subtle", "fg", "muted", "faint", "border", "border-strong", "shadow", "on-solid"}

var shapeTokens = []string{"radius", "radius-sm", "font", "mono", "measure"}

func themeTokens(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*--kapi-([a-z0-9-]+):\s*(.+);$`).FindAllStringSubmatch(string(themeCSS), -1) {
		out[m[1]] = m[2]
	}
	return out
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
	tokens := themeTokens(t)
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
		for _, bg := range []string{tone + "-fill", tone + "-tint", "surface", "subtle", "bg"} {
			check(tone+"-text on "+bg, text, pair(bg), 4.5)
		}
		check("on-solid on "+tone+"-solid", [2]string{onSolid, onSolid}, pair(tone+"-solid"), 4.5)
		check("fg on "+tone+"-fill", pair("fg"), pair(tone+"-fill"), 7)
		check("muted on "+tone+"-fill", pair("muted"), pair(tone+"-fill"), 4.5)
	}
	for _, role := range []string{"fg", "muted", "faint"} {
		for _, bg := range []string{"surface", "subtle", "bg"} {
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
