//go:build unix

package control

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// checkOwner refuses a file or directory that another user owns or that
// the group or others can access beyond mask.
func checkOwner(path string, info os.FileInfo, mask os.FileMode) error {
	if perm := info.Mode().Perm(); perm&mask != 0 {
		return fmt.Errorf("%s is accessible by other users (mode %04o)", path, perm)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if uid := uint32(os.Geteuid()); stat.Uid != uid && stat.Uid != 0 {
			return fmt.Errorf("%s is owned by another user (uid %d)", path, stat.Uid)
		}
	}
	return nil
}

// processAlive reports whether pid may still be running.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
