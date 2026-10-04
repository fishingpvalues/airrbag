//go:build unix

package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatHardlink(t *testing.T) {
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ia, _ := OS{}.Stat(a)
	ib, _ := OS{}.Stat(b)
	ic, _ := OS{}.Stat(c)
	if !ia.SameFile(ib) || ia.Nlink != 2 {
		t.Errorf("hardlinks must share an inode: %+v %+v", ia, ib)
	}
	if ia.SameFile(ic) || ic.Nlink != 1 {
		t.Errorf("a copy is a different inode: %+v %+v", ia, ic)
	}
	missing, err := OS{}.Stat(filepath.Join(dir, "nope"))
	if err != nil || missing.Exists {
		t.Errorf("missing file: %+v %v", missing, err)
	}
}
