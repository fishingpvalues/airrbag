package nzbhydra

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// entryJSON is shaped like a real Hydra 8 history entry, link included, so
// the test proves the link (which carries indexer credentials) is dropped.
const entryJSON = `{"id":-1,"searchResult":{"id":"-7","indexer":{"id":-2,"name":"Treasure Maps"},
"title":"Artist-Album-16BIT-WEB-FLAC-2015-GRP","link":"https://indexer.example/getnzb/x?r=SECRET",
"downloadType":"NZB","pubDate":1726561387.0},"nzbAccessType":"REDIRECT","accessSource":"API",
"time":1791095138.96,"status":"CONTENT_DOWNLOAD_SUCCESSFUL","userAgent":"Lidarr"}`

func server(t *testing.T, externalOK bool) (*httptest.Server, *[]string) {
	t.Helper()
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Page int `json:"page"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/history/downloads":
			if !externalOK || r.URL.Query().Get("apikey") != "main-key" {
				w.WriteHeader(http.StatusInternalServerError) // Hydra's answer to a wrong key
				return
			}
		case "/internalapi/history/downloads":
		default:
			http.NotFound(w, r)
			return
		}
		if body.Page > 1 {
			_, _ = w.Write([]byte(`{"content":[],"last":true,"totalPages":1}`))
			return
		}
		torrent := strings.Replace(entryJSON, `"NZB"`, `"TORRENT"`, 1)
		_, _ = w.Write([]byte(`{"content":[` + entryJSON + `,` + torrent + `],"last":true,"totalPages":1}`))
	}))
	return srv, &hits
}

func TestDownloadsFallsBackToInternalAPI(t *testing.T) {
	srv, hits := server(t, false)
	defer srv.Close()
	c := New(srv.URL, "not-the-main-key", "", "", 5*time.Second, nil)
	got, err := c.Downloads(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("Downloads = %+v %v", got, err)
	}
	if got[0].Kind != "usenet" || got[1].Kind != "torrent" || got[0].Indexer != "Treasure Maps" ||
		got[0].UserAgent != "Lidarr" || got[0].Time.Unix() != 1791095138 {
		t.Fatalf("decoded %+v", got)
	}
	if (*hits)[len(*hits)-1] != "/internalapi/history/downloads" {
		t.Fatalf("expected fallback to the internal API, hits %v", *hits)
	}
}

func TestDownloadsExternalAPI(t *testing.T) {
	srv, hits := server(t, true)
	defer srv.Close()
	c := New(srv.URL, "main-key", "", "", 5*time.Second, nil)
	if _, err := c.Downloads(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, h := range *hits {
		if h == "/internalapi/history/downloads" {
			t.Fatalf("used the internal API although the external one works: %v", *hits)
		}
	}
}

func TestNoCredentialLeak(t *testing.T) {
	c := New("http://127.0.0.1:1", "secret-key", "", "", time.Second, nil)
	_, err := c.Downloads(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("error must not carry the key: %v", err)
	}
}
