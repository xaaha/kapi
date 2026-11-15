package settings

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultLibrary = "~/kapi"
	FolderDepth    = 4
)

var ErrBadFolder = errors.New("bad folder name")

func checkLibrary(s string) error {
	if s == "~" || strings.HasPrefix(s, "~/") || filepath.IsAbs(s) {
		return nil
	}
	return fmt.Errorf("%q is not an absolute folder or one under ~/", s)
}

func cleanLibrary(s string) string {
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		if rest = filepath.Clean(rest); rest == "." {
			return "~"
		}
		return "~/" + filepath.ToSlash(filepath.Clean(rest))
	}
	if s == "~" {
		return s
	}
	return filepath.Clean(s)
}

type Folders struct {
	Root, Library, Drafts string
}

func (s *Settings) Folders() (Folders, error) {
	root := cmp.Or(s.Library, DefaultLibrary)
	if root == "~" || strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return Folders{}, fmt.Errorf("library %s: %w", root, err)
		}
		root = filepath.Join(home, strings.TrimPrefix(root, "~"))
	}
	return Folders{Root: root, Library: filepath.Join(root, "library"), Drafts: filepath.Join(root, "drafts")}, nil
}

func (f *File) library(raw json.RawMessage) string {
	if raw == nil {
		return DefaultLibrary
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || string(raw) == "null" {
		f.warn("library", fmt.Sprintf("%s is not a string", raw), strconv.Quote(DefaultLibrary))
		return DefaultLibrary
	}
	if err := checkLibrary(s); err != nil {
		f.warn("library", err.Error(), strconv.Quote(DefaultLibrary))
		return DefaultLibrary
	}
	return cleanLibrary(s)
}

func CheckFolder(folder string) (string, error) {
	folder = strings.Trim(strings.TrimSpace(folder), "/")
	if folder == "" {
		return "", fmt.Errorf("%w: name the folder", ErrBadFolder)
	}
	parts := strings.Split(folder, "/")
	if len(parts) > FolderDepth {
		return "", fmt.Errorf("%w: %q is more than %d folders deep", ErrBadFolder, folder, FolderDepth)
	}
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
		if err := CheckFolderName(parts[i]); err != nil {
			return "", fmt.Errorf("%w: %q %w", ErrBadFolder, folder, err)
		}
	}
	return filepath.Join(parts...), nil
}

func CheckFolderName(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return errors.New("has an empty or relative part")
	case strings.HasPrefix(name, "."):
		return errors.New("starts with a dot")
	case strings.HasSuffix(strings.ToLower(name), ".files"):
		return errors.New("ends in .files, which kapi keeps for page images")
	case len(name) > 100:
		return errors.New("is longer than 100 bytes")
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		return errors.New("ends in a dot or a space, which Windows drops")
	case reservedOnWindows(name):
		return errors.New("is a device name Windows reserves")
	case strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || strings.ContainsRune(`\:*?"<>|`, r) }):
		return errors.New(`holds a control character or one of \ : * ? " < > |`)
	}
	return nil
}

func reservedOnWindows(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	stem = strings.TrimRight(stem, " ")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '0' && stem[3] <= '9'
	}
	if rest, ok := strings.CutPrefix(stem, "COM"); ok && strings.ContainsAny(rest, "¹²³") && len([]rune(rest)) == 1 {
		return true
	}
	if rest, ok := strings.CutPrefix(stem, "LPT"); ok && strings.ContainsAny(rest, "¹²³") && len([]rune(rest)) == 1 {
		return true
	}
	return false
}
