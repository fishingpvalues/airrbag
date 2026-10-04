// Package deluge is a read-only client for the Deluge Web UI JSON-RPC API
// (the endpoint the *Arrs use, /json).
//
// The Web UI is a proxy in front of the Deluge daemon: after auth.login it
// may still be disconnected, in which case the client connects it to the
// first configured daemon host. Only status-reading methods are called.
package deluge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

// Client is safe for concurrent use.
type Client struct {
	jsonURL  string
	password string
	http     *http.Client
	id       atomic.Int64

	mu    sync.Mutex
	ready bool
}

// JSONURL turns a base URL into the JSON-RPC endpoint ("/json").
func JSONURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/json") {
		return base
	}
	return base + "/json"
}

// New creates a client. Deluge's Web UI has a password and no user name.
func New(base, password string, timeout time.Duration, rt http.RoundTripper) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	jar, _ := cookiejar.New(nil)
	return &Client{jsonURL: JSONURL(base), password: password,
		http: &http.Client{Timeout: timeout, Jar: jar, Transport: rt}}
}

type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

var errNotAuthed = errors.New("deluge: not authenticated")

func (c *Client) raw(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"method": method, "params": params, "id": c.id.Add(1)})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.jsonURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("deluge: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deluge: HTTP %d", resp.StatusCode)
	}
	var r rpcResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<20)).Decode(&r); err != nil {
		return fmt.Errorf("deluge: decode: %w", err)
	}
	if r.Error != nil {
		// Code 1 is "Not authenticated" in the Web UI.
		if r.Error.Code == 1 {
			return errNotAuthed
		}
		return fmt.Errorf("deluge: %s", r.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

func (c *Client) ensure(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return nil
	}
	var ok bool
	if err := c.raw(ctx, "auth.login", []any{c.password}, &ok); err != nil {
		return err
	}
	if !ok {
		return errors.New("deluge: login refused (wrong password)")
	}
	var connected bool
	if err := c.raw(ctx, "web.connected", []any{}, &connected); err != nil {
		return err
	}
	if !connected {
		var hosts [][]any
		if err := c.raw(ctx, "web.get_hosts", []any{}, &hosts); err != nil {
			return err
		}
		if len(hosts) == 0 || len(hosts[0]) == 0 {
			return errors.New("deluge: web UI has no daemon host configured")
		}
		if err := c.raw(ctx, "web.connect", []any{hosts[0][0]}, nil); err != nil {
			return err
		}
	}
	c.ready = true
	return nil
}

func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensure(ctx); err != nil {
			return err
		}
		err := c.raw(ctx, method, params, out)
		if errors.Is(err, errNotAuthed) {
			c.mu.Lock()
			c.ready = false
			c.mu.Unlock()
			continue
		}
		return err
	}
	return errNotAuthed
}

type status struct {
	Name        string  `json:"name"`
	Private     bool    `json:"private"`
	TrackerHost string  `json:"tracker_host"`
	Ratio       float64 `json:"ratio"`
	SeedingTime int64   `json:"seeding_time"`
	SavePath    string  `json:"save_path"`
	State       string  `json:"state"`
	Label       string  `json:"label"`
	Trackers    []struct {
		URL string `json:"url"`
	} `json:"trackers"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

var snapshotKeys = []string{"name", "private", "tracker_host", "ratio", "seeding_time", "save_path", "state", "label"}

// Snapshot lists every torrent in one call.
func (c *Client) Snapshot(ctx context.Context) (map[string]clients.Torrent, error) {
	var res map[string]status
	if err := c.call(ctx, "core.get_torrents_status", []any{map[string]any{}, snapshotKeys}, &res); err != nil {
		return nil, err
	}
	out := make(map[string]clients.Torrent, len(res))
	for h, s := range res {
		h = strings.ToLower(h)
		priv := s.Private
		ratio := s.Ratio
		if ratio < 0 { // -1 when nothing was downloaded yet
			ratio = 0
		}
		out[h] = clients.Torrent{
			Hash: h, Name: s.Name, Private: &priv, Tracker: s.TrackerHost, Ratio: ratio,
			SeedingTime: time.Duration(s.SeedingTime) * time.Second, State: strings.ToLower(s.State),
			ContentPath: path.Join(s.SavePath, s.Name), SavePath: s.SavePath, Category: s.Label,
		}
	}
	return out, nil
}

func (c *Client) one(ctx context.Context, hash string, keys ...string) (status, error) {
	var s status
	err := c.call(ctx, "core.get_torrent_status", []any{hash, keys}, &s)
	return s, err
}

// Files returns absolute file paths (save_path joined with each file path).
func (c *Client) Files(ctx context.Context, hash string) ([]string, error) {
	s, err := c.one(ctx, hash, "save_path", "files")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(s.Files))
	for _, f := range s.Files {
		out = append(out, path.Join(s.SavePath, f.Path))
	}
	return out, nil
}

// Private reads the private flag (Snapshot already carries it).
func (c *Client) Private(ctx context.Context, hash string) (bool, error) {
	s, err := c.one(ctx, hash, "private")
	return s.Private, err
}

// Trackers lists announce URLs.
func (c *Client) Trackers(ctx context.Context, hash string) ([]string, error) {
	s, err := c.one(ctx, hash, "trackers")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(s.Trackers))
	for _, t := range s.Trackers {
		out = append(out, t.URL)
	}
	return out, nil
}

var _ clients.TorrentClient = (*Client)(nil)
