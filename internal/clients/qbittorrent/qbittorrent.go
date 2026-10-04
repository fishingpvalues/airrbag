// Package qbittorrent is a read-only qBittorrent WebAPI v2 client.
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
	SeedingTime int64   `json:"seeding_time"`
	State       string  `json:"state"`
	ContentPath string  `json:"content_path"`
	SavePath    string  `json:"save_path"`
	Category    string  `json:"category"`
}

// Snapshot lists every torrent in one call.
func (c *Client) Snapshot(ctx context.Context) (map[string]clients.Torrent, error) {
	var list []info
	if err := c.get(ctx, "/api/v2/torrents/info", nil, &list); err != nil {
		return nil, err
	}
	out := make(map[string]clients.Torrent, len(list))
	for _, t := range list {
		h := strings.ToLower(t.Hash)
		out[h] = clients.Torrent{
			Hash: h, Name: t.Name, Private: t.Private, Tracker: t.Tracker, Ratio: t.Ratio,
			SeedingTime: time.Duration(t.SeedingTime) * time.Second, State: t.State,
			ContentPath: t.ContentPath, SavePath: t.SavePath, Category: t.Category,
		}
	}
	return out, nil
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

// Private reads is_private from the properties endpoint (qBittorrent 4.6+).
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
	return false, errors.New("qbittorrent did not report the private flag")
}

// Trackers lists announce URLs, skipping the DHT/PeX/LSD pseudo entries.
func (c *Client) Trackers(ctx context.Context, hash string) ([]string, error) {
	var ts []struct {
		URL string `json:"url"`
	}
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

var _ clients.TorrentClient = (*Client)(nil)
