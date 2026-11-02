//go:build darwin || linux

package firefox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// firefoxProfileLocked tells if a running Firefox holds the profile. Firefox
// keeps an fcntl write lock on .parentlock while it runs. The file stays after
// Firefox exits, so the lock, not the file, shows that the profile is in use.
// On Linux, Firefox also makes a "lock" symbolic link to "address:+PID". The
// link stays after a crash, so it shows use only while that process exists.
func firefoxProfileLocked(profile string) (bool, error) {
	locked, err := fcntlLockHeld(filepath.Join(profile, ".parentlock"))
	if err != nil || locked {
		return locked, err
	}
	return symlinkLockHeld(filepath.Join(profile, "lock"))
}

func fcntlLockHeld(path string) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect Firefox profile lock %q: %w", path, err)
	}
	// Closing the file also releases the test lock below.
	defer file.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	err = syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &lock)
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect Firefox profile lock %q: %w", path, err)
	}
	return false, nil
}

func symlinkLockHeld(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect Firefox profile lock %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return true, nil // Not a Firefox lock link. Do not take the risk.
	}
	target, err := os.Readlink(path)
	if err != nil {
		return false, fmt.Errorf("inspect Firefox profile lock %q: %w", path, err)
	}
	_, pidText, found := strings.Cut(target, ":")
	pid, err := strconv.Atoi(strings.TrimPrefix(pidText, "+"))
	if !found || err != nil || pid < 1 {
		return true, nil // Unknown format. Do not take the risk.
	}
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM), nil
}
