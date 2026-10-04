package sabnzbd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "k" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"history":{"slots":[{"nzo_id":"nzo_1","name":"X","status":"Completed"}]}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k", time.Second, nil)
	j, ok, err := c.Job(context.Background(), "SABnzbd_nzo_1")
	if err != nil || !ok || j.Status != "Completed" {
		t.Fatalf("Job = %+v %v %v", j, ok, err)
	}
	bad := New(srv.URL, "wrong", time.Second, nil)
	_, _, err = bad.Job(context.Background(), "nzo_1")
	if err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("errors must not leak the api key: %v", err)
	}
}

func TestHistoryAndCompleteDirs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("mode") {
		case "history":
			if q.Get("start") == "0" {
				_, _ = w.Write([]byte(`{"history":{"noofslots":2,"slots":[
{"nzo_id":"SABnzbd_nzo_a","name":"Movie.2020.1080p-GRP","nzb_name":"Movie.2020.1080p-GRP.nzb","status":"Completed","storage":"/downloads/movies/Movie.2020.1080p-GRP","category":"movies","bytes":123},
{"nzo_id":"SABnzbd_nzo_b","name":"Show.S01E01","status":"Failed","storage":"","category":"tv","bytes":0}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"history":{"noofslots":2,"slots":[]}}`))
		case "get_config":
			_, _ = w.Write([]byte(`{"config":{"misc":{"complete_dir":"/downloads"},"categories":[
{"name":"*","dir":""},{"name":"movies","dir":"movies"},{"name":"abs","dir":"/elsewhere/abs"}]}}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k", time.Second, nil)
	jobs, err := c.History(context.Background())
	if err != nil || len(jobs) != 2 || jobs[0].Storage != "/downloads/movies/Movie.2020.1080p-GRP" || jobs[0].NZBName != "Movie.2020.1080p-GRP.nzb" {
		t.Fatalf("History = %+v %v", jobs, err)
	}
	dirs, err := c.CompleteDirs(context.Background())
	want := []string{"/downloads", "/downloads/movies", "/elsewhere/abs"}
	if err != nil || strings.Join(dirs, ",") != strings.Join(want, ",") {
		t.Fatalf("CompleteDirs = %v %v", dirs, err)
	}
}
