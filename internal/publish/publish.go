package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xaaha/kapi/internal/render"
)

var pageIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

var defaultBackoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}

type Client struct {
	cfg     Config
	http    *http.Client
	backoff []time.Duration
	wait    func(context.Context, time.Duration) error
}

func New(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		backoff: defaultBackoff,
		wait:    sleep,
	}
}

func FromHome(home string, getenv func(string) string) (*Client, error) {
	cfg, err := LoadConfig(home, getenv)
	if err != nil {
		return nil, err
	}
	return New(cfg), nil
}

func PageID(absPath string) string {
	sum := sha256.Sum256([]byte(absPath))
	return hex.EncodeToString(sum[:8])
}

func (c *Client) PageURL(pageID string) string {
	return c.cfg.URL + "/p/" + pageID + "/"
}

func (c *Client) Publish(ctx context.Context, pageID string, rev uint64, title string, src []byte, filesDir string) (string, error) {
	doc, err := Document(title, src, filesDir)
	if err != nil {
		return "", err
	}
	if err := c.Put(ctx, pageID, rev, title, doc); err != nil {
		return "", err
	}
	return c.PageURL(pageID), nil
}

const MaxDocBytes = 10 << 20

var pageImage = regexp.MustCompile(`src="\./[^"/]+\.files/([0-9a-f]{32}\.(png|jpg|gif|webp))"`)

var imageTypes = map[string]string{"png": "image/png", "jpg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}

type embedded struct {
	name string
	size int
}

func embedImages(body []byte, filesDir string) ([]byte, []embedded, error) {
	var used []embedded
	var failed error
	out := pageImage.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := pageImage.FindSubmatch(m)
		data, err := os.ReadFile(filepath.Join(filesDir, string(sub[1])))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && failed == nil {
				failed = err
			}
			return m
		}
		used = append(used, embedded{string(sub[1]), len(data)})
		return []byte(`src="data:` + imageTypes[string(sub[2])] + `;base64,` + base64.StdEncoding.EncodeToString(data) + `"`)
	})
	return out, used, failed
}

func Document(title string, src []byte, filesDir string) ([]byte, error) {
	body, err := render.Render(src)
	if err != nil {
		return nil, fmt.Errorf("render page: %w", err)
	}
	body, images, err := embedImages(body, filesDir)
	if err != nil {
		return nil, fmt.Errorf("embed images: %w", err)
	}
	var b bytes.Buffer
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n<style>\n")
	b.Write(render.CSS())
	b.WriteString("\n</style>\n</head>\n<body>\n<main id=\"kapi-page\">\n")
	b.Write(body)
	b.WriteString("\n</main>\n</body>\n</html>\n")
	if b.Len() > MaxDocBytes {
		slices.SortFunc(images, func(a, b embedded) int { return b.size - a.size })
		var big []string
		for _, im := range images[:min(3, len(images))] {
			big = append(big, fmt.Sprintf("%s (%.1f MB)", im.name, float64(im.size)/(1<<20)))
		}
		msg := fmt.Sprintf("the published page would be %.1f MB, over the hub's %d MB limit", float64(b.Len())/(1<<20), MaxDocBytes>>20)
		if len(big) > 0 {
			msg += "; the largest images are " + strings.Join(big, ", ")
		}
		return nil, errors.New(msg)
	}
	return b.Bytes(), nil
}

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func (c *Client) Put(ctx context.Context, pageID string, rev uint64, title string, doc []byte) error {
	if !pageIDPattern.MatchString(pageID) {
		return fmt.Errorf("page id %q must be 16 lowercase hex characters", pageID)
	}
	endpoint := c.cfg.URL + "/api/pages/" + pageID + "/revs/" + strconv.FormatUint(rev, 10)
	for attempt := 1; ; attempt++ {
		err := c.put(ctx, endpoint, title, doc)
		if err == nil {
			return nil
		}
		var perm *permanentError
		if errors.As(err, &perm) || attempt > len(c.backoff) || c.wait(ctx, c.backoff[attempt-1]) != nil {
			return fmt.Errorf("publish %s rev %d: attempt %d: %w", pageID, rev, attempt, err)
		}
	}
}

func (c *Client) put(ctx context.Context, endpoint, title string, doc []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(doc))
	if err != nil {
		return &permanentError{err}
	}
	req.Header.Set("Authorization", "Bearer "+string(c.cfg.Token))
	req.Header.Set("Content-Type", "text/html; charset=utf-8")
	req.Header.Set("Kapi-Title", url.PathEscape(title))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(strings.ReplaceAll(string(snippet), string(c.cfg.Token), "[redacted]"))
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return nil
	case code >= 300 && code < 400:
		return &permanentError{fmt.Errorf("hub redirected the upload to %q; if the hub sits behind an access proxy, exempt /api/ from it", resp.Header.Get("Location"))}
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500:
		return fmt.Errorf("hub answered %s: %s", resp.Status, msg)
	default:
		return &permanentError{fmt.Errorf("hub answered %s: %s", resp.Status, msg)}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
