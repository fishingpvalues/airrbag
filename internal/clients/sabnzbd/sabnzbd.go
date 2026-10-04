// Package sabnzbd is a read-only SABnzbd API client.
package sabnzbd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

// Client queries SABnzbd's history and folder configuration.
type Client struct {
	base   string
	apiKey string
	http   *http.Client
	// HistoryLimit caps how many history slots History reads (default 20000).
	HistoryLimit int
}

// New creates a client. rt may be nil.
func New(base, apiKey string, timeout time.Duration, rt http.RoundTripper) *Client {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	return &Client{base: strings.TrimRight(base, "/"), apiKey: apiKey, http: &http.Client{Timeout: timeout, Transport: rt}}
}

func (c *Client) get(ctx context.Context, q url.Values, out any) error {
	q.Set("output", "json")
	q.Set("apikey", c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL carries the API key; never return it inside an error.
		return fmt.Errorf("sabnzbd: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sabnzbd: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(out)
}

type slot struct {
	NzoID    string `json:"nzo_id"`
	Name     string `json:"name"`
	NZBName  string `json:"nzb_name"`
	Status   string `json:"status"`
	Storage  string `json:"storage"`
	Category string `json:"category"`
	Bytes    int64  `json:"bytes"`
}

func (s slot) job() clients.UsenetJob {
	return clients.UsenetJob{ID: s.NzoID, Name: s.Name, NZBName: s.NZBName, Status: s.Status,
		Storage: s.Storage, Category: s.Category, Bytes: s.Bytes}
}

type historyBody struct {
	History struct {
		NoOfSlots int    `json:"noofslots"`
		Slots     []slot `json:"slots"`
	} `json:"history"`
}

// Job looks up a finished job by nzo id. *Arr download ids carry a
// "SABnzbd_" prefix that SABnzbd itself does not use.
func (c *Client) Job(ctx context.Context, id string) (clients.UsenetJob, bool, error) {
	id = strings.TrimPrefix(id, "SABnzbd_")
	var body historyBody
	if err := c.get(ctx, url.Values{"mode": {"history"}, "nzo_ids": {id}}, &body); err != nil {
		return clients.UsenetJob{}, false, err
	}
	for _, s := range body.History.Slots {
		if s.NzoID == id {
			return s.job(), true, nil
		}
	}
	return clients.UsenetJob{}, false, nil
}

// History pages through the whole history, newest first.
func (c *Client) History(ctx context.Context) ([]clients.UsenetJob, error) {
	limit := c.HistoryLimit
	if limit <= 0 {
		limit = 20000
	}
	const page = 1000
	var out []clients.UsenetJob
	for start := 0; start < limit; start += page {
		var body historyBody
		q := url.Values{"mode": {"history"}, "start": {strconv.Itoa(start)}, "limit": {strconv.Itoa(page)}}
		if err := c.get(ctx, q, &body); err != nil {
			return out, err
		}
		for _, s := range body.History.Slots {
			out = append(out, s.job())
		}
		if len(body.History.Slots) < page || start+page >= body.History.NoOfSlots {
			break
		}
	}
	return out, nil
}

// CompleteDirs returns the complete folder and every category folder below
// it, as absolute paths in SABnzbd's own view.
func (c *Client) CompleteDirs(ctx context.Context) ([]string, error) {
	var body struct {
		Config struct {
			Misc struct {
				CompleteDir string `json:"complete_dir"`
			} `json:"misc"`
			Categories []struct {
				Dir string `json:"dir"`
			} `json:"categories"`
		} `json:"config"`
	}
	if err := c.get(ctx, url.Values{"mode": {"get_config"}}, &body); err != nil {
		return nil, err
	}
	root := body.Config.Misc.CompleteDir
	if root == "" {
		return nil, nil
	}
	out := []string{root}
	for _, cat := range body.Config.Categories {
		if cat.Dir == "" {
			continue
		}
		d := cat.Dir
		if !path.IsAbs(d) {
			d = path.Join(root, d)
		}
		out = append(out, d)
	}
	return out, nil
}

var (
	_ clients.UsenetClient  = (*Client)(nil)
	_ clients.UsenetHistory = (*Client)(nil)
)
