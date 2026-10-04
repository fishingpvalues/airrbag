package hub

import (
	"fmt"
	"testing"
	"time"
)

func TestEventsNewestFirstAndBounded(t *testing.T) {
	h := New("test", nil)
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < maxEvents+7; i++ {
		h.Record(GuardEvent{Time: base.Add(time.Duration(i) * time.Second), Path: fmt.Sprint(i)})
	}
	ev := h.Events()
	if len(ev) != maxEvents {
		t.Fatalf("len = %d, want %d", len(ev), maxEvents)
	}
	if ev[0].Path != fmt.Sprint(maxEvents+6) || ev[len(ev)-1].Path != "7" {
		t.Fatalf("order wrong: first %s last %s", ev[0].Path, ev[len(ev)-1].Path)
	}
}

func TestInstancesSorted(t *testing.T) {
	h := New("test", nil)
	h.Register(&Instance{Name: "sonarr"})
	h.Register(&Instance{Name: "lidarr"})
	h.Register(&Instance{Name: "radarr"})
	got := h.Instances()
	if got[0].Name != "lidarr" || got[2].Name != "sonarr" {
		t.Fatalf("not sorted: %v %v %v", got[0].Name, got[1].Name, got[2].Name)
	}
	if _, ok := h.Instance("radarr"); !ok {
		t.Fatal("lookup failed")
	}
}
