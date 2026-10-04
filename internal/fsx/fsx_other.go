//go:build !unix

package fsx

import (
	"errors"
	"io/fs"
	"os"
)

// Stat on platforms without inode numbers reports existence and size only.
// Verdicts then fall back to path containment.
func (OS) Stat(path string) (Info, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Info{}, nil
		}
		return Info{}, err
	}
	return Info{Exists: true, Size: fi.Size()}, ErrUnsupported
}
