package publish

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var ErrNotConfigured = errors.New("no hub configured: set KAPI_HUB_URL and KAPI_HUB_TOKEN, or write $KAPI_HOME/hub.json with url and token_file")

type Token string

func (Token) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, "[redacted]") }

type Config struct {
	URL   string
	Token Token
}

type fileConfig struct {
	URL       string `json:"url"`
	TokenFile string `json:"token_file"`
}

func LoadConfig(home string, getenv func(string) string) (Config, error) {
	var fc fileConfig
	b, err := os.ReadFile(filepath.Join(home, "hub.json"))
	switch {
	case err == nil:
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&fc); err != nil {
			return Config{}, fmt.Errorf("hub.json: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return Config{}, err
	}

	c := Config{URL: fc.URL}
	if v := getenv("KAPI_HUB_URL"); v != "" {
		c.URL = v
	}
	if v := getenv("KAPI_HUB_TOKEN"); v != "" {
		c.Token = Token(strings.TrimSpace(v))
	} else if fc.TokenFile != "" {
		if c.Token, err = readToken(home, fc.TokenFile); err != nil {
			return Config{}, err
		}
	}

	switch {
	case c.URL == "" && c.Token == "":
		return Config{}, ErrNotConfigured
	case c.URL == "":
		return Config{}, errors.New("hub url missing: set url in hub.json or KAPI_HUB_URL")
	case c.Token == "":
		return Config{}, errors.New("hub token missing: set token_file in hub.json or KAPI_HUB_TOKEN")
	}
	if c.URL, err = checkURL(c.URL); err != nil {
		return Config{}, err
	}
	return c, nil
}

func readToken(home, path string) (Token, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(h, rest)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hub token file: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", fmt.Errorf("hub token file %s is empty", path)
	}
	return Token(t), nil
}

func checkURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("hub url %q must look like https://host[/path]", raw)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return "", fmt.Errorf("hub url %q must use https; plain http is only allowed for localhost", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
