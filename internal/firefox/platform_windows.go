package firefox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// errorSharingViolation is the Windows ERROR_SHARING_VIOLATION code. The
// syscall package does not define it.
const errorSharingViolation = syscall.Errno(32)

// firefoxProfileLocked tells if a running Firefox holds the profile. Firefox
// opens parent.lock with no sharing while it runs. Thus, an open of the file
// fails only while Firefox has it open. The file can stay after Firefox exits.
func firefoxProfileLocked(profile string) (bool, error) {
	path := filepath.Join(profile, "parent.lock")
	file, err := os.Open(path)
	if err == nil {
		_ = file.Close()
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if errors.Is(err, errorSharingViolation) || errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		return true, nil
	}
	return false, fmt.Errorf("inspect Firefox profile lock %q: %w", path, err)
}

// processAttributes does not change the process. Firefox is a GUI program and
// does not get Ctrl+C from the console of lemmingd.
func processAttributes() *syscall.SysProcAttr {
	return nil
}

// requestQuit asks Firefox to close its windows, as the window close button
// does. Without /F, taskkill sends WM_CLOSE and does not stop the process.
func requestQuit(process *os.Process) error {
	output, err := exec.Command("taskkill", "/PID", strconv.Itoa(process.Pid)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
