package page

import "testing"

func TestImageNameNamesOnlyAStoredImageInThePagesOwnFolder(t *testing.T) {
	const name = "0123456789abcdef0123456789abcdef.png"
	dir := FilesDir("/w/notes 10:30.html")
	for src, want := range map[string]bool{
		"./notes%2010:30.files/" + name:             true,
		"./notes 10:30.files/" + name:               true,
		"./other.files/" + name:                     false,
		"./notes%2010:30.files/secret.png":          false,
		"./notes%2010:30.files/a/" + name:           false,
		"notes%2010:30.files/" + name:               false,
		"./notes%2010:30.files/../plan.files/x.png": false,
		"https://example.com/" + name:               false,
		"./notes%zz.files/" + name:                  false,
	} {
		got, ok := ImageName(dir, src)
		if ok != want || (ok && got != name) {
			t.Errorf("ImageName(%q) = %q, %v; want %v", src, got, ok, want)
		}
	}
}
