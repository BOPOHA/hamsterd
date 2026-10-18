//go:build !unix

package control

import "os"

// checkOwner relies on the ACLs of the user's profile directory on systems
// without Unix permissions.
func checkOwner(string, os.FileInfo, os.FileMode) error {
	return nil
}

// processAlive cannot cheaply check a PID here; a dead endpoint is still
// detected when the connection is refused.
func processAlive(int) bool {
	return true
}
