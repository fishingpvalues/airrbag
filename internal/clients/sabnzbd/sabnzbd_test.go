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
