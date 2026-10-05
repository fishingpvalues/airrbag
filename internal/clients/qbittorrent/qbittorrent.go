// Package qbittorrent is a read-only qBittorrent WebAPI v2 client.
//
// It supports qBittorrent 4.1 (WebAPI 2.0) through 5.x. Fields that newer
// releases put in torrents/info are read from older sources when missing:
//
//   - private: torrents/info "private" (5.0), torrents/properties
//     "is_private" (4.6), else the DHT/PeX/LSD rows of torrents/trackers,
//     whose message is "This torrent is private" for a private torrent.
//   - seeding_time: torrents/info (4.4) else torrents/properties (4.1).
//   - content_path: torrents/info (4.3.2) else save_path joined with the name.
//
// Login answers "Ok." (200) on 4.x and 204 on 5.x; the session cookie is SID
// or QBT_SID_<port>, which the cookie jar handles without knowing the name.
package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

// Client is safe for concurrent use. It only issues GETs after logging in.
type Client struct {
	base     string
	username string
	password string
	http     *http.Client

	mu       sync.Mutex
	loggedIn bool
	api      string // WebAPI version, read once after login
}

// New creates a client. Empty credentials work when qBittorrent bypasses
// authentication for the caller's subnet. rt may be nil.
func New(base, username, password string, timeout time.Duration, rt http.RoundTripper) *Client {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	jar, _ := cookiejar.New(nil)
	return &Client{
		base:     strings.TrimRight(base, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: timeout, Jar: jar, Transport: rt},
	}
}

var errAuth = errors.New("qbittorrent: authentication failed")

func (c *Client) login(ctx context.Context) error {
	form := url.Values{"username": {c.username}, "password": {c.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// qBittorrent's CSRF check compares Referer/Origin with its own host.
	req.Header.Set("Referer", c.base)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	// 4.x answers 200 "Ok.", 5.x answers 204 with the SID cookie.
	if resp.StatusCode == http.StatusNoContent || (resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "Ok.") {
		c.loggedIn = true
		return nil
	}
	return fmt.Errorf("%w (HTTP %d)", errAuth, resp.StatusCode)
}

func (c *Client) get(ctx context.Context, p string, q url.Values, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		c.mu.Lock()
		if !c.loggedIn && c.username != "" {
			if err := c.login(ctx); err != nil {
				c.mu.Unlock()
				return err
			}
		}
		c.mu.Unlock()

		u := c.base + p
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Referer", c.base)
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			_ = resp.Body.Close()
			c.mu.Lock()
			c.loggedIn = false
			if c.username == "" {
				c.mu.Unlock()
				return fmt.Errorf("%w: no credentials configured", errAuth)
			}
			c.mu.Unlock()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("qbittorrent %s: HTTP %d", p, resp.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 512<<20)).Decode(out)
	}
	return errAuth
}

type info struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Private     *bool   `json:"private"` // qBittorrent 5.x
	Tracker     string  `json:"tracker"`
	Ratio       float64 `json:"ratio"`
	SeedingTime *int64  `json:"seeding_time"` // 4.4+
	State       string  `json:"state"`
	ContentPath string  `json:"content_path"` // 4.3.2+
	SavePath    string  `json:"save_path"`
	Category    string  `json:"category"`
}

// minAPI is WebAPI 2.0, qBittorrent 4.1. Older releases speak the legacy
// API (/query/torrents), which Airrbag does not implement.
const minAPI = "2.0"

// APIVersion returns the WebAPI version (e.g. "2.8.3"), cached.
func (c *Client) APIVersion(ctx context.Context) (string, error) {
	c.mu.Lock()
	v := c.api
	c.mu.Unlock()
	if v != "" {
		return v, nil
	}
	body, err := c.getText(ctx, "/api/v2/app/webapiVersion")
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(body)
	if !versionAtLeast(v, minAPI) {
		return v, fmt.Errorf("qbittorrent WebAPI %q is older than %s (qBittorrent 4.1); not supported", v, minAPI)
	}
	c.mu.Lock()
	c.api = v
	c.mu.Unlock()
	return v, nil
}

// versionAtLeast compares dotted numeric versions ("2.8.3" >= "2.0").
func versionAtLeast(v, min string) bool {
	a, b := strings.Split(v, "."), strings.Split(min, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			if _, err := fmt.Sscanf(a[i], "%d", &x); err != nil {
				return false
			}
		}
		if i < len(b) {
			_, _ = fmt.Sscanf(b[i], "%d", &y)
		}
		if x != y {
			return x > y
		}
	}
	return true
}

// Snapshot lists every torrent in one call.
func (c *Client) Snapshot(ctx context.Context) (map[string]clients.Torrent, error) {
	if _, err := c.APIVersion(ctx); err != nil {
		return nil, err
	}
	var list []info
	if err := c.get(ctx, "/api/v2/torrents/info", nil, &list); err != nil {
		return nil, err
	}
	out := make(map[string]clients.Torrent, len(list))
	for _, t := range list {
		h := strings.ToLower(t.Hash)
		tt := clients.Torrent{
			Hash: h, Name: t.Name, Private: t.Private, Tracker: t.Tracker, Ratio: t.Ratio,
			State: t.State, ContentPath: t.ContentPath, SavePath: t.SavePath, Category: t.Category,
		}
		if t.SeedingTime != nil {
			tt.SeedingTime = time.Duration(*t.SeedingTime) * time.Second
		} else {
			tt.SeedingTimeUnknown = true
		}
		if tt.ContentPath == "" && t.SavePath != "" && t.Name != "" {
			// Before 4.3.2: the torrent's root is save_path/name for the
			// default layouts. Files() is still the authoritative list.
			tt.ContentPath = path.Join(t.SavePath, t.Name)
		}
		out[h] = tt
	}
	return out, nil
}

// SeedingTime reads one torrent's seeding time from torrents/properties,
// for qBittorrent releases whose torrents/info lacks it.
func (c *Client) SeedingTime(ctx context.Context, hash string) (time.Duration, error) {
	var props struct {
		SeedingTime *int64 `json:"seeding_time"`
	}
	if err := c.get(ctx, "/api/v2/torrents/properties", url.Values{"hash": {hash}}, &props); err != nil {
		return 0, err
	}
	if props.SeedingTime == nil {
		return 0, errors.New("qbittorrent did not report the seeding time")
	}
	return time.Duration(*props.SeedingTime) * time.Second, nil
}

// Files returns absolute file paths (save_path joined with each file name).
func (c *Client) Files(ctx context.Context, hash string) ([]string, error) {
	var props struct {
		SavePath string `json:"save_path"`
	}
	if err := c.get(ctx, "/api/v2/torrents/properties", url.Values{"hash": {hash}}, &props); err != nil {
		return nil, err
	}
	var files []struct {
		Name string `json:"name"`
	}
	if err := c.get(ctx, "/api/v2/torrents/files", url.Values{"hash": {hash}}, &files); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, path.Join(props.SavePath, f.Name))
	}
	return out, nil
}

// Private reads the private flag: torrents/properties "is_private" (4.6+),
// else the DHT/PeX/LSD rows of torrents/trackers. qBittorrent disables those
// three for a private torrent and says so in their message.
func (c *Client) Private(ctx context.Context, hash string) (bool, error) {
	var props struct {
		IsPrivate *bool `json:"is_private"`
		Private   *bool `json:"private"`
	}
	if err := c.get(ctx, "/api/v2/torrents/properties", url.Values{"hash": {hash}}, &props); err != nil {
		return false, err
	}
	switch {
	case props.IsPrivate != nil:
		return *props.IsPrivate, nil
	case props.Private != nil:
		return *props.Private, nil
	}
	var ts []tracker
	if err := c.get(ctx, "/api/v2/torrents/trackers", url.Values{"hash": {hash}}, &ts); err != nil {
		return false, err
	}
	return privateFromTrackers(ts)
}

type tracker struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Msg    string `json:"msg"`
}

// privateFromTrackers reads the pseudo rows "** [DHT] **", "** [PeX] **" and
// "** [LSD] **". A private torrent has them disabled with the message "This
// torrent is private"; without the rows there is no answer.
func privateFromTrackers(ts []tracker) (bool, error) {
	seen := false
	for _, t := range ts {
		if !strings.HasPrefix(t.URL, "** [") {
			continue
		}
		seen = true
		if strings.Contains(strings.ToLower(t.Msg), "private") {
			return true, nil
		}
	}
	if !seen {
		return false, errors.New("qbittorrent did not report the private flag")
	}
	return false, nil
}

// getText GETs a plain-text endpoint (app/webapiVersion answers "2.8.3",
// which is not JSON), logging in again once on 401/403.
func (c *Client) getText(ctx context.Context, p string) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		c.mu.Lock()
		if !c.loggedIn && c.username != "" {
			if err := c.login(ctx); err != nil {
				c.mu.Unlock()
				return "", err
			}
		}
		c.mu.Unlock()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+p, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Referer", c.base)
		resp, err := c.http.Do(req)
		if err != nil {
			return "", err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
			return string(b), nil
		case (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) && c.username != "":
			c.mu.Lock()
			c.loggedIn = false
			c.mu.Unlock()
			continue
		default:
			return "", fmt.Errorf("qbittorrent %s: HTTP %d", p, resp.StatusCode)
		}
	}
	return "", errAuth
}

// Trackers lists announce URLs, skipping the DHT/PeX/LSD pseudo entries.
func (c *Client) Trackers(ctx context.Context, hash string) ([]string, error) {
	var ts []tracker
	if err := c.get(ctx, "/api/v2/torrents/trackers", url.Values{"hash": {hash}}, &ts); err != nil {
		return nil, err
	}
	var out []string
	for _, t := range ts {
		if !strings.HasPrefix(t.URL, "** [") {
			out = append(out, t.URL)
		}
	}
	return out, nil
}

var (
	_ clients.TorrentClient = (*Client)(nil)
	_ clients.SeedTimer     = (*Client)(nil)
)
