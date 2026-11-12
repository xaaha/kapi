package render

import "strings"

var tones = []string{"red", "orange", "yellow", "green", "blue", "purple", "grey"}

const toneList = "red, orange, yellow, green, blue, purple or grey"

var toneAliases = map[string]string{
	"gray": "grey", "slate": "grey", "neutral": "grey", "black": "grey", "white": "grey", "brown": "grey",
	"amber": "orange", "gold": "yellow", "lime": "green", "teal": "blue", "cyan": "blue", "navy": "blue",
	"indigo": "purple", "violet": "purple", "magenta": "purple", "pink": "purple",
}

func normalizeTone(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range tones {
		if t == name {
			return t
		}
	}
	if tone := toneAliases[name]; tone != "" {
		return tone
	}
	return "grey"
}

func isTone(name string) bool { return normalizeTone(name) == name }
