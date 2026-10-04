// Package sabnzbd is a read-only SABnzbd API client.
package sabnzbd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fishingpvalues/airrbag/internal/clients"
)

// Client queries SABnzbd's history.
type Client struct {
	base   string
	apiKey string
	http   *http.Client
}

// New creates a client. rt may be nil.
func New(base, apiKey string, timeout time.Duration, rt http.RoundTripper) *Client {
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	return &Client{base: strings.TrimRight(base, "/"), apiKey: apiKey, http: &http.Client{Timeout: timeout, Transport: rt}}
}

// Job looks up a finished job by nzo id. *Arr download ids carry a
// "SABnzbd_" prefix that SABnzbd itself does not use.
func (c *Client) Job(ctx context.Context, id string) (clients.UsenetJob, bool, error) {
	id = strings.TrimPrefix(id, "SABnzbd_")
	q := url.Values{"mode": {"history"}, "output": {"json"}, "apikey": {c.apiKey}, "nzo_ids": {id}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api?"+q.Encode(), nil)
	if err != nil {
		return clients.UsenetJob{}, false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL carries the API key; never return it inside an error.
		return clients.UsenetJob{}, false, fmt.Errorf("sabnzbd: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return clients.UsenetJob{}, false, fmt.Errorf("sabnzbd: HTTP %d", resp.StatusCode)
	}
	var body struct {
		History struct {
			Slots []struct {
				NzoID  string `json:"nzo_id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"slots"`
		} `json:"history"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&body); err != nil {
		return clients.UsenetJob{}, false, err
	}
	for _, s := range body.History.Slots {
		if s.NzoID == id {
			return clients.UsenetJob{ID: s.NzoID, Name: s.Name, Status: s.Status}, true, nil
		}
	}
	return clients.UsenetJob{}, false, nil
}

var _ clients.UsenetClient = (*Client)(nil)
