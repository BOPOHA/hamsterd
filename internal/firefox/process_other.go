//go:build !unix && !windows

package firefox

import (
	"errors"
	"os"
	"syscall"
)

func processAttributes() *syscall.SysProcAttr {
	return nil
}

// requestQuit has no quit request on this system. Close then stops Firefox.
func requestQuit(*os.Process) error {
	return errors.New("a quit request is not available on this system")
}
