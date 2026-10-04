package webassets

import "testing"

func TestAssetRefusesTraversalAndUnknownTypes(t *testing.T) {
	for _, n := range []string{"../webassets.go", "dist/../webassets.go", "README.md", "static/../../go.mod", "x/y.js"} {
		if _, _, ok := Asset(n); ok {
			t.Fatalf("Asset(%q) served", n)
		}
	}
}

func TestStaticLogoEmbedded(t *testing.T) {
	b, ct, ok := Asset("static/logo.svg")
	if !ok || ct != "image/svg+xml" || len(b) == 0 {
		t.Fatalf("logo missing: ok=%v ct=%q len=%d", ok, ct, len(b))
	}
}
