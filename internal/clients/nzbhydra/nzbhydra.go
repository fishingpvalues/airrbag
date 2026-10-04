// Package nzbhydra reads NZBHydra2's download history: which release an *Arr
// grabbed through Hydra, from which indexer, NZB or torrent. It never calls
// an indexer and never follows a download link; the links in Hydra's
// history carry indexer credentials and are dropped on decode.
//
// Two endpoints serve the same data:
//
//   - POST /api/history/downloads?apikey=... (Hydra's external API). It
//     accepts only the main API key.
//   - POST /internalapi/history/downloads, the endpoint Hydra's own web UI
//     uses. It needs no key when Hydra's auth type is NONE, or HTTP basic
//     auth when it is BASIC.
//
// The client tries the external API when a key is configured and falls back
// to the internal one.
package nzbhydra

import (
	"bytes"
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

// Client is safe for concurrent use.
type Client struct {
	base     string
	apiKey   string
	username string
	password string
	http     *http.Client
	// Limit caps how many history entries Downloads reads (default 20000).
	Limit int
}

// New creates a client. rt may be nil.
func New(base, apiKey, username, password string, timeout time.Duration, rt http.RoundTripper) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{base: strings.TrimRight(base, "/"), apiKey: apiKey, username: username, password: password,
		http: &http.Client{Timeout: timeout, Transport: rt}}
}

type entry struct {
	SearchResult struct {
		Title   string `json:"title"`
		Indexer struct {
			Name string `json:"name"`
		} `json:"indexer"`
		DownloadType string `json:"downloadType"`
	} `json:"searchResult"`
	Time      float64 `json:"time"`
	Status    string  `json:"status"`
	UserAgent string  `json:"userAgent"`
}

type page struct {
	Content    []entry `json:"content"`
	Last       bool    `json:"last"`
	TotalPages int     `json:"totalPages"`
}

func (c *Client) post(ctx context.Context, endpoint string, external bool, pageNo, limit int) (page, error) {
	body, _ := json.Marshal(map[string]any{
		"page": pageNo, "limit": limit,
		"sortModel":   map[string]any{"column": "time", "sortMode": 2},
		"filterModel": map[string]any{},
	})
	u := c.base + endpoint
	if external {
		u += "?apikey=" + url.QueryEscape(c.apiKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return page{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if !external && (c.username != "" || c.password != "") {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL may carry the API key; keep it out of the error.
		return page{}, fmt.Errorf("nzbhydra2: request to %s failed", endpoint)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return page{}, fmt.Errorf("nzbhydra2 %s: HTTP %d", endpoint, resp.StatusCode)
	}
	var p page
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(&p); err != nil {
		return page{}, fmt.Errorf("nzbhydra2 %s: decode: %w", endpoint, err)
	}
	return p, nil
}

// Downloads returns the download history, newest first.
func (c *Client) Downloads(ctx context.Context) ([]clients.IndexerDownload, error) {
	limit := c.Limit
	if limit <= 0 {
		limit = 20000
	}
	const size = 1000
	endpoint, external := "/internalapi/history/downloads", false
	if c.apiKey != "" {
		if _, err := c.post(ctx, "/api/history/downloads", true, 1, 1); err == nil {
			endpoint, external = "/api/history/downloads", true
		}
	}
	var out []clients.IndexerDownload
	for p := 1; len(out) < limit; p++ {
		pg, err := c.post(ctx, endpoint, external, p, size)
		if err != nil {
			return out, err
		}
		for _, e := range pg.Content {
			kind := "usenet"
			if strings.EqualFold(e.SearchResult.DownloadType, "TORRENT") {
				kind = "torrent"
			}
			sec := int64(e.Time)
			out = append(out, clients.IndexerDownload{
				Title: e.SearchResult.Title, Indexer: e.SearchResult.Indexer.Name, Kind: kind,
				Time: time.Unix(sec, 0).UTC(), UserAgent: e.UserAgent,
			})
		}
		if pg.Last || len(pg.Content) < size || (pg.TotalPages > 0 && p >= pg.TotalPages) {
			break
		}
	}
	return out, nil
}

var _ clients.IndexerHistory = (*Client)(nil)
