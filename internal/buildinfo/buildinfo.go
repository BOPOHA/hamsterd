package buildinfo

import "runtime/debug"

// Version is set with -ldflags by make, RPM, and release builds.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	// go install module@version records the module version in the binary.
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}
}
