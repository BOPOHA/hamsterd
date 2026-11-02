//go:build unix

package firefox

import "syscall"

// processAttributes puts Firefox in a new process group, so that a terminal
// signal for lemmingd does not also go to Firefox.
func processAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
