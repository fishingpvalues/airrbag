package deluge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fake behaves like the Deluge 2 Web UI: auth.login sets a session cookie,
// the UI starts disconnected from the daemon, and status calls need both.
func fake(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	connected := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
			ID     int    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, req.Method)
		reply := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": req.ID, "result": result, "error": nil})
		}
		authed := func() bool {
			c, err := r.Cookie("_session_id")
			return err == nil && c.Value == "s"
		}
		switch req.Method {
		case "auth.login":
			if len(req.Params) == 1 && req.Params[0] == "deluge" {
				http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "s", Path: "/"})
				reply(true)
				return
			}
			reply(false)
		case "web.connected":
			reply(connected)
		case "web.get_hosts":
			reply([][]any{{"host-id-1", "127.0.0.1", 58846, "localclient"}})
		case "web.connect":
			connected = true
			reply([]string{"core.get_torrents_status"})
		case "core.get_torrents_status", "core.get_torrent_status":
			if !authed() {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": req.ID, "result": nil,
					"error": map[string]any{"message": "Not authenticated", "code": 1}})
				return
			}
			st := map[string]any{"name": "Show.S01", "private": true, "tracker_host": "tracker.example.org",
				"ratio": 0.8, "seeding_time": 3600, "save_path": "/data/tv", "state": "Seeding", "label": "sonarr",
				"trackers": []map[string]any{{"url": "https://tracker.example.org/announce"}},
				"files":    []map[string]any{{"path": "Show.S01/e01.mkv"}, {"path": "Show.S01/e02.mkv"}}}
			if req.Method == "core.get_torrent_status" {
				reply(st)
				return
			}
			reply(map[string]any{"ABCDEF0123456789ABCDEF0123456789ABCDEF01": st})
		default:
			reply(nil)
		}
	}))
	return srv, &calls
}

func TestSnapshotConnectsDaemon(t *testing.T) {
	srv, calls := fake(t)
	defer srv.Close()
	c := New(srv.URL, "deluge", 5*time.Second, nil)
	ctx := context.Background()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tor, ok := snap["abcdef0123456789abcdef0123456789abcdef01"]
	if !ok {
		t.Fatalf("missing torrent %v", snap)
	}
	if !*tor.Private || tor.Ratio != 0.8 || tor.SeedingTime != time.Hour || tor.State != "seeding" ||
		tor.ContentPath != "/data/tv/Show.S01" || tor.Tracker != "tracker.example.org" {
		t.Fatalf("unexpected %+v", tor)
	}
	connectSeen := false
	for _, m := range *calls {
		if m == "web.connect" {
			connectSeen = true
		}
	}
	if !connectSeen {
		t.Fatal("client did not connect the disconnected web UI to its daemon")
	}
	files, err := c.Files(ctx, tor.Hash)
	if err != nil || len(files) != 2 || files[1] != "/data/tv/Show.S01/e02.mkv" {
		t.Fatalf("files %v %v", files, err)
	}
}

func TestWrongPassword(t *testing.T) {
	srv, _ := fake(t)
	defer srv.Close()
	c := New(srv.URL, "nope", 5*time.Second, nil)
	if _, err := c.Snapshot(context.Background()); err == nil {
		t.Fatal("expected login failure")
	}
}
