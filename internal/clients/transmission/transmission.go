// Package transmission is a read-only Transmission RPC client.
//
// Transmission guards its RPC endpoint with a CSRF token: the first request
// answers 409 with an X-Transmission-Session-Id header, and every request
// must echo the current id. The client learns the id on the fly and retries
// once whenever Transmission rotates it.
package transmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

const sessionHeader = "X-Transmission-Session-Id"

// Client is safe for concurrent use. It only calls torrent-get.
type Client struct {
	rpcURL   string
	username string
	password string
	http     *http.Client

	mu      sync.Mutex
	session string
}

// RPCURL turns a base URL into the RPC endpoint. The *Arr stores the URL base
// ("/transmission/"), so ".../transmission" becomes ".../transmission/rpc";
// a URL that already ends in /rpc is kept.
func RPCURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/rpc") {
		return base
	}
	if !strings.HasSuffix(base, "/transmission") {
		base += "/transmission"
	}
	return base + "/rpc"
}

// New creates a client. rt may be nil.
func New(base, username, password string, timeout time.Duration, rt http.RoundTripper) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{rpcURL: RPCURL(base), username: username, password: password,
		http: &http.Client{Timeout: timeout, Transport: rt}}
}

type rpcRequest struct {
	Method    string `json:"method"`
	Arguments any    `json:"arguments"`
}

type rpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

func (c *Client) call(ctx context.Context, args any, out any) error {
	body, err := json.Marshal(rpcRequest{Method: "torrent-get", Arguments: args})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		c.mu.Lock()
		if c.session != "" {
			req.Header.Set(sessionHeader, c.session)
		}
		c.mu.Unlock()
		if c.username != "" || c.password != "" {
			req.SetBasicAuth(c.username, c.password)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("transmission: request failed: %w", err)
		}
		if resp.StatusCode == http.StatusConflict {
			c.mu.Lock()
			c.session = resp.Header.Get(sessionHeader)
			c.mu.Unlock()
			_ = resp.Body.Close()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("transmission: HTTP %d", resp.StatusCode)
		}
		var r rpcResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<20)).Decode(&r); err != nil {
			return fmt.Errorf("transmission: decode: %w", err)
		}
		if r.Result != "success" {
			return fmt.Errorf("transmission: %s", r.Result)
		}
		return json.Unmarshal(r.Arguments, out)
	}
	return errors.New("transmission: session id handshake failed")
}

type tracker struct {
	Announce string `json:"announce"`
}

type torrent struct {
	HashString     string    `json:"hashString"`
	Name           string    `json:"name"`
	IsPrivate      bool      `json:"isPrivate"`
	Trackers       []tracker `json:"trackers"`
	UploadRatio    float64   `json:"uploadRatio"`
	SecondsSeeding int64     `json:"secondsSeeding"`
	DownloadDir    string    `json:"downloadDir"`
	Status         int       `json:"status"`
	Labels         []string  `json:"labels"`
	Files          []struct {
		Name string `json:"name"`
	} `json:"files"`
}

// Transmission's status numbers (tr_torrent_activity).
var statusNames = map[int]string{0: "stopped", 1: "check-wait", 2: "checking", 3: "download-wait",
	4: "downloading", 5: "seed-wait", 6: "seeding"}

var snapshotFields = []string{"hashString", "name", "isPrivate", "trackers", "uploadRatio",
	"secondsSeeding", "downloadDir", "status", "labels"}

// Snapshot lists every torrent in one call.
func (c *Client) Snapshot(ctx context.Context) (map[string]clients.Torrent, error) {
	var res struct {
		Torrents []torrent `json:"torrents"`
	}
	if err := c.call(ctx, map[string]any{"fields": snapshotFields}, &res); err != nil {
		return nil, err
	}
	out := make(map[string]clients.Torrent, len(res.Torrents))
	for _, t := range res.Torrents {
		h := strings.ToLower(t.HashString)
		priv := t.IsPrivate
		tr := ""
		if len(t.Trackers) > 0 {
			tr = t.Trackers[0].Announce
		}
		cat := ""
		if len(t.Labels) > 0 {
			cat = t.Labels[0]
		}
		ratio := t.UploadRatio
		if ratio < 0 { // -1 "not available", -2 "infinite"
			ratio = 0
		}
		out[h] = clients.Torrent{
			Hash: h, Name: t.Name, Private: &priv, Tracker: tr, Ratio: ratio,
			SeedingTime: time.Duration(t.SecondsSeeding) * time.Second, State: statusNames[t.Status],
			ContentPath: path.Join(t.DownloadDir, t.Name), SavePath: t.DownloadDir, Category: cat,
		}
	}
	return out, nil
}

func (c *Client) one(ctx context.Context, hash string, fields ...string) (torrent, error) {
	var res struct {
		Torrents []torrent `json:"torrents"`
	}
	if err := c.call(ctx, map[string]any{"ids": []string{hash}, "fields": fields}, &res); err != nil {
		return torrent{}, err
	}
	if len(res.Torrents) == 0 {
		return torrent{}, fmt.Errorf("transmission: torrent %s not found", hash)
	}
	return res.Torrents[0], nil
}

// Files returns absolute file paths (downloadDir joined with each file name).
func (c *Client) Files(ctx context.Context, hash string) ([]string, error) {
	t, err := c.one(ctx, hash, "downloadDir", "files")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(t.Files))
	for _, f := range t.Files {
		out = append(out, path.Join(t.DownloadDir, f.Name))
	}
	return out, nil
}

// Private reads isPrivate (Snapshot already carries it).
func (c *Client) Private(ctx context.Context, hash string) (bool, error) {
	t, err := c.one(ctx, hash, "isPrivate")
	return t.IsPrivate, err
}

// Trackers lists announce URLs.
func (c *Client) Trackers(ctx context.Context, hash string) ([]string, error) {
	t, err := c.one(ctx, hash, "trackers")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(t.Trackers))
	for _, tr := range t.Trackers {
		out = append(out, tr.Announce)
	}
	return out, nil
}

var _ clients.TorrentClient = (*Client)(nil)
