package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func PortPath(home string) string { return filepath.Join(home, "port") }

func TokenPath(home string) string { return filepath.Join(home, "http.token") }

// SetPort saves the port the daemon listens on from its next start; 0 goes back to a sticky random port.
func SetPort(home string, port int) error {
	if port < 0 || port > 65535 {
		return fmt.Errorf("port %d is out of range; use 1-65535, or 0 for a random port", port)
	}
	if port == 0 {
		if err := os.Remove(PortPath(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return writeAtomic(PortPath(home), []byte(strconv.Itoa(port)+"\n"))
}

func fixedPort(home string) (int, error) {
	b, err := os.ReadFile(PortPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s holds %q, not a port; fix it with kapi daemon --port", PortPath(home), b)
	}
	return port, nil
}

var savedToken = regexp.MustCompile(`^([0-9a-f]{32})\n$`)

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func keptToken(home string) (token string, fresh bool, err error) {
	if b, err := os.ReadFile(TokenPath(home)); err == nil {
		if m := savedToken.FindSubmatch(b); m != nil {
			return string(m[1]), false, nil
		}
	}
	if token, err = newToken(); err != nil {
		return "", false, err
	}
	return token, true, writeAtomic(TokenPath(home), []byte(token+"\n"))
}
