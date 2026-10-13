//go:build !darwin

package main

import (
	"os"
	"time"
)

// birthtime approximates creation time with mtime on platforms without a
// portable birth-time stat field.
func birthtime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
