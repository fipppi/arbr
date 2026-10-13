//go:build darwin

package main

import (
	"os"
	"syscall"
	"time"
)

// birthtime returns the file's creation time (falling back to mtime).
func birthtime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
	}
	return fi.ModTime()
}
