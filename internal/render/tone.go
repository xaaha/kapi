package render

import (
	"hash/fnv"
	"strings"
)

var tones = []string{"red", "orange", "yellow", "green", "blue", "purple", "grey"}

var toneAliases = map[string]string{
	"aqua": "blue", "cyan": "blue", "navy": "blue", "teal": "blue",
	"amber": "orange", "bronze": "orange", "brown": "orange", "gold": "yellow",
	"emerald": "green", "lime": "green", "mint": "green", "olive": "green",
	"crimson": "red", "rose": "red", "scarlet": "red",
	"indigo": "purple", "lavender": "purple", "lilac": "purple", "magenta": "purple", "pink": "purple", "violet": "purple",
	"black": "grey", "charcoal": "grey", "gray": "grey", "neutral": "grey", "silver": "grey", "slate": "grey", "white": "grey",
}

func isTone(name string) bool {
	for _, tone := range tones {
		if name == tone {
			return true
		}
	}
	return false
}

func namedTone(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if isTone(name) {
		return name
	}
	if tone := toneAliases[name]; tone != "" {
		return tone
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return tones[int(h.Sum32())%len(tones)]
}

func hexColour(value string) bool {
	if len(value) != 4 && len(value) != 7 {
		return false
	}
	if value[0] != '#' {
		return false
	}
	for _, c := range value[1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func customToneStyle(colour string) string {
	return "--kapi-tone-fill:color-mix(in srgb," + colour + " 12%,var(--kapi-surface));" +
		"--kapi-tone-border:color-mix(in srgb," + colour + " 45%,var(--kapi-border-strong));" +
		"--kapi-tone-text:color-mix(in srgb," + colour + " 45%,var(--kapi-fg));" +
		"--kapi-tone-solid:" + colour + ";" +
		"--kapi-tone-tint:color-mix(in srgb," + colour + " 18%,var(--kapi-surface))"
}
