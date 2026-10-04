//go:build unix

package fsx

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// Stat returns the identity of path. A missing file is Info{Exists:false}
// with a nil error; any other failure is returned.
func (OS) Stat(path string) (Info, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Info{}, nil
		}
		return Info{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return Info{}, ErrUnsupported
	}
	return Info{
		Exists: true,
		Dev:    uint64(st.Dev), //nolint:unconvert // int32 on darwin, uint64 on linux
		Ino:    st.Ino,
		Nlink:  uint64(st.Nlink), //nolint:unconvert // uint16 on darwin, uint64 on linux
		Size:   fi.Size(),
	}, nil
}
