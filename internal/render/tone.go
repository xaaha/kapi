package render

var tones = []string{"neutral", "info", "success", "warning", "danger", "accent"}

const toneList = "neutral, info, success, warning, danger or accent"

func isTone(name string) bool {
	for _, t := range tones {
		if t == name {
			return true
		}
	}
	return false
}
