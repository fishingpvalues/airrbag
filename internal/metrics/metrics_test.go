package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestWrite(t *testing.T) {
	r := New()
	r.Describe("x_total", "counter", "things")
	r.Add("x_total", 1, "instance", "radarr")
	r.Add("x_total", 2, "instance", "radarr")
	r.Set("g", 5, "a", `q"uote`)
	var b bytes.Buffer
	r.Write(&b)
	out := b.String()
	for _, want := range []string{"# HELP x_total things", "# TYPE x_total counter", `x_total{instance="radarr"} 3`, `g{a="q\"uote"} 5`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
