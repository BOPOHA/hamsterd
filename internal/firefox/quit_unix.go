//go:build unix && !darwin

package firefox

import (
	"os"
	"syscall"
)

// requestQuit asks Firefox to quit with SIGTERM, the usual stop request on
// Linux and other Unix systems.
func requestQuit(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
