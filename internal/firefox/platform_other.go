//go:build !darwin && !linux && !windows

package firefox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// firefoxProfileLocked has no lock test on this system. It treats any Firefox
// lock file as in use. Delete the file manually after Firefox exits.
func firefoxProfileLocked(profile string) (bool, error) {
	for _, name := range []string{"lock", ".parentlock", "parent.lock"} {
		_, err := os.Lstat(filepath.Join(profile, name))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect Firefox profile lock %q (%s): %w", profile, name, err)
		}
	}
	return false, nil
}
