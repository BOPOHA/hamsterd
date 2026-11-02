//go:build darwin || linux

package firefox

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

const lockHelperEnv = "LEMMINGD_TEST_HOLD_FCNTL_LOCK"

// TestHelperHoldFcntlLock is not a test. It is a helper process that holds an
// fcntl lock, as a running Firefox does, until its standard input closes.
func TestHelperHoldFcntlLock(t *testing.T) {
	path := os.Getenv(lockHelperEnv)
	if path == "" {
		t.Skip("helper process only")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	if err := syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &lock); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("locked\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func holdFcntlLock(t *testing.T, path string) func() {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestHelperHoldFcntlLock$")
	command.Env = append(os.Environ(), lockHelperEnv+"="+path)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("lock helper did not lock the file: %q, %v", line, err)
	}
	return func() {
		_ = stdin.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("lock helper: %v", err)
		}
	}
}

func TestFirefoxProfileLockedNeedsAHeldLock(t *testing.T) {
	profile := t.TempDir()
	if locked, err := firefoxProfileLocked(profile); err != nil || locked {
		t.Fatalf("profile without lock file: locked = %v, err = %v", locked, err)
	}
	// Firefox does not delete .parentlock when it exits.
	parentLock := filepath.Join(profile, ".parentlock")
	if err := os.WriteFile(parentLock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if locked, err := firefoxProfileLocked(profile); err != nil || locked {
		t.Fatalf("lock file that no process holds: locked = %v, err = %v", locked, err)
	}
	release := holdFcntlLock(t, parentLock)
	if locked, err := firefoxProfileLocked(profile); err != nil || !locked {
		release()
		t.Fatalf("lock that another process holds: locked = %v, err = %v", locked, err)
	}
	release()
	if locked, err := firefoxProfileLocked(profile); err != nil || locked {
		t.Fatalf("lock after the other process exited: locked = %v, err = %v", locked, err)
	}
}

func TestSymlinkLockHeldOnlyWhileItsProcessExists(t *testing.T) {
	exited := exec.Command(os.Args[0], "-test.run=^$")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		target string
		want   bool
	}{
		{"live process", "127.0.0.1:+" + strconv.Itoa(os.Getpid()), true},
		{"exited process", "127.0.0.1:+" + strconv.Itoa(exited.Process.Pid), false},
		{"unknown format", "unknown", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := t.TempDir()
			if err := os.Symlink(test.target, filepath.Join(profile, "lock")); err != nil {
				t.Fatal(err)
			}
			if locked, err := firefoxProfileLocked(profile); err != nil || locked != test.want {
				t.Fatalf("locked = %v, err = %v; want %v", locked, err, test.want)
			}
		})
	}
	t.Run("regular file", func(t *testing.T) {
		profile := t.TempDir()
		if err := os.WriteFile(filepath.Join(profile, "lock"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if locked, err := firefoxProfileLocked(profile); err != nil || !locked {
			t.Fatalf("locked = %v, err = %v; want true", locked, err)
		}
	})
}
