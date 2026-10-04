// Package fsx reads the filesystem identity of a file: device, inode and link
// count. Two names with the same device and inode are the same bytes on disk.
package fsx

import "errors"

// Info is the identity of one file.
type Info struct {
	Exists bool
	Dev    uint64
	Ino    uint64
	Nlink  uint64
	Size   int64
}

// SameFile reports whether a and b are the same inode on the same device.
func (a Info) SameFile(b Info) bool {
	return a.Exists && b.Exists && a.Dev == b.Dev && a.Ino == b.Ino
}

// Statter looks up file identity. Tests replace it with a fake.
type Statter interface {
	Stat(path string) (Info, error)
}

// ErrUnsupported is returned where inode identity is not available.
var ErrUnsupported = errors.New("inode identity not supported on this platform")

// OS is the real filesystem.
type OS struct{}
