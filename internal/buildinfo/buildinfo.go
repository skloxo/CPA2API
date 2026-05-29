// Package buildinfo exposes compile-time metadata shared across the server.
package buildinfo

// The following variables are overridden via ldflags during release builds.
// Defaults cover local development builds.
var (
	// Version is the semantic version or git describe output of the binary.
	Version = "dev"

	// Commit is the git commit SHA baked into the binary.
	Commit = "none"

	// BuildDate records when the binary was built in UTC.
	BuildDate = "unknown"
)

func init() {
	if Version == "dev" {
		Version = "v7.2.2-s.4-dev"
	}
	if BuildDate == "unknown" {
		BuildDate = "2026-05-30T08:08:59Z"
	}
}
