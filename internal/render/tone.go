package render

var tones = []string{"red", "orange", "yellow", "green", "blue", "purple", "grey"}

const toneList = "red, orange, yellow, green, blue, purple or grey"

func isTone(name string) bool {
	for _, t := range tones {
		if t == name {
			return true
		}
	}
	return false
}
