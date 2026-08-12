// Package buildinfo exposes compile-time metadata shared across the server.
package buildinfo

import (
	"os/exec"
	"strings"
	"time"
)

// The following variables are overridden via ldflags during release builds.
// Defaults cover local development builds.
var (
	// Version is the semantic version or git describe output of the binary.
	Version = "v7.1.45-s13"

	// Commit is the git commit SHA baked into the binary.
	Commit = "none"

	// BuildDate records when the binary was built in UTC.
	BuildDate = "unknown"
)

func init() {
	if Version == "dev" || Version == "" || strings.HasPrefix(Version, "dev-") {
		Version = "v7.1.45-s13"
	}
	if BuildDate == "unknown" {
		BuildDate = time.Now().UTC().Format(time.RFC3339)
	}
}

// gitDescribe runs git describe --tags --always --dirty to get version info.
func gitDescribe() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	if err != nil || len(out) == 0 {
		return Version
	}
	return strings.TrimSpace(string(out))
}
