package firefox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// requestQuit asks this Firefox process to quit, as File > Quit does. On
// macOS, Firefox stops immediately on SIGTERM. Thus, lemmingd sends the quit
// request through NSRunningApplication. The process ID selects this Firefox
// only, not other Firefox instances of the user.
func requestQuit(process *os.Process) error {
	script := "ObjC.import('AppKit');" +
		"var app = $.NSRunningApplication.runningApplicationWithProcessIdentifier(" +
		strconv.Itoa(process.Pid) + ");" +
		"(!app.isNil() && app.terminate) ? 'ok' : 'failed'"
	output, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).CombinedOutput()
	result := strings.TrimSpace(string(output))
	if err != nil {
		return fmt.Errorf("osascript: %w: %s", err, result)
	}
	if result != "ok" {
		return errors.New("macOS did not accept the quit request")
	}
	return nil
}
