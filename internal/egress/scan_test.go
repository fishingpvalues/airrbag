package egress

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoUnguardedHTTP keeps the egress promise honest at the source level:
// package-level helpers and the default client skip the allowlist transport,
// so non-test code must not use them.
func TestNoUnguardedHTTP(t *testing.T) {
	forbidden := regexp.MustCompile(`http\.DefaultClient|http\.(Get|Post|PostForm|Head)\(|net\.Dial\(`)
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if forbidden.MatchString(line) {
				t.Errorf("%s:%d bypasses the egress allowlist: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
