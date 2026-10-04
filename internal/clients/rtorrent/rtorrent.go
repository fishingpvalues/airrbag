// Package rtorrent is a read-only rTorrent XML-RPC client, talking to the
// same HTTP endpoint the *Arrs use (an RPC2 mount behind nginx or
// ruTorrent's /plugins/httprpc/action.php). Only d.*, t.* and f.* getters
// are called.
package rtorrent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

// Client is safe for concurrent use.
type Client struct {
	rpcURL   string
	username string
	password string
	http     *http.Client
	now      func() time.Time
}

// RPCURL returns the XML-RPC endpoint: the URL as given when it already
// names an endpoint, else base + "/RPC2" (rTorrent's conventional mount).
func RPCURL(base string) string {
	base = strings.TrimRight(base, "/")
	low := strings.ToLower(base)
	if strings.HasSuffix(low, "/rpc2") || strings.HasSuffix(low, ".php") {
		return base
	}
	return base + "/RPC2"
}

// New creates a client. rt may be nil.
func New(base, username, password string, timeout time.Duration, rt http.RoundTripper) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{rpcURL: RPCURL(base), username: username, password: password,
		http: &http.Client{Timeout: timeout, Transport: rt}, now: time.Now}
}

func (c *Client) call(ctx context.Context, method string, params ...string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(encodeCall(method, params...)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rtorrent: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rtorrent: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, err
	}
	return decodeResponse(raw)
}

// multicallFields are the d.multicall2 getters, in this order.
var multicallFields = []string{"d.hash=", "d.name=", "d.is_private=", "d.ratio=", "d.timestamp.finished=",
	"d.base_path=", "d.directory=", "d.is_multi_file=", "d.state=", "d.complete=", "d.custom1="}

// Snapshot lists every torrent in the "main" view in one call.
func (c *Client) Snapshot(ctx context.Context) (map[string]clients.Torrent, error) {
	params := append([]string{"", "main"}, multicallFields...)
	res, err := c.call(ctx, "d.multicall2", params...)
	if err != nil {
		return nil, err
	}
	rows, _ := res.([]any)
	out := make(map[string]clients.Torrent, len(rows))
	now := c.now()
	for _, r := range rows {
		f, _ := r.([]any)
		if len(f) < len(multicallFields) {
			continue
		}
		h := strings.ToLower(asString(f[0]))
		name := asString(f[1])
		priv := asInt(f[2]) == 1
		finished := asInt(f[4])
		var seeding time.Duration
		if finished > 0 {
			seeding = now.Sub(time.Unix(finished, 0))
		}
		dir := asString(f[6])
		content := asString(f[5])
		if content == "" { // base_path is empty while a torrent is closed
			content = dir
			if asInt(f[7]) == 0 {
				content = path.Join(dir, name)
			}
		}
		state := "stopped"
		switch {
		case asInt(f[8]) == 1 && asInt(f[9]) == 1:
			state = "seeding"
		case asInt(f[8]) == 1:
			state = "downloading"
		case asInt(f[9]) == 1:
			state = "complete"
		}
		saveDir := dir
		if asInt(f[7]) == 1 {
			saveDir = path.Dir(dir)
		}
		out[h] = clients.Torrent{
			Hash: h, Name: name, Private: &priv,
			// d.ratio is the ratio in thousandths.
			Ratio:       float64(asInt(f[3])) / 1000,
			SeedingTime: seeding, State: state,
			ContentPath: content, SavePath: saveDir, Category: asString(f[10]),
		}
	}
	return out, nil
}

// Files returns absolute file paths (d.directory joined with each f.path).
func (c *Client) Files(ctx context.Context, hash string) ([]string, error) {
	dirV, err := c.call(ctx, "d.directory", strings.ToUpper(hash))
	if err != nil {
		return nil, err
	}
	dir := asString(dirV)
	res, err := c.call(ctx, "f.multicall", strings.ToUpper(hash), "", "f.path=")
	if err != nil {
		return nil, err
	}
	rows, _ := res.([]any)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if f, ok := r.([]any); ok && len(f) > 0 {
			out = append(out, path.Join(dir, asString(f[0])))
		}
	}
	return out, nil
}

// Private reads d.is_private (Snapshot already carries it).
func (c *Client) Private(ctx context.Context, hash string) (bool, error) {
	v, err := c.call(ctx, "d.is_private", strings.ToUpper(hash))
	return asInt(v) == 1, err
}

// Trackers lists announce URLs (t.url of every tracker).
func (c *Client) Trackers(ctx context.Context, hash string) ([]string, error) {
	res, err := c.call(ctx, "t.multicall", strings.ToUpper(hash), "", "t.url=")
	if err != nil {
		return nil, err
	}
	rows, _ := res.([]any)
	var out []string
	for _, r := range rows {
		if f, ok := r.([]any); ok && len(f) > 0 {
			if u := asString(f[0]); u != "" && !strings.HasPrefix(u, "dht://") {
				out = append(out, u)
			}
		}
	}
	return out, nil
}

var _ clients.TorrentClient = (*Client)(nil)
