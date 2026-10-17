// Package buildinfo holds the version and commit stamped into the binary.
package buildinfo

// Set by the build with -ldflags "-X
// github.com/darkraise/darkpanel/internal/buildinfo.Version=... -X
// github.com/darkraise/darkpanel/internal/buildinfo.Commit=...". The linker
// ignores -X for a variable that does not exist, so make verify-build checks
// that a built binary reports the commit.
var (
	Version = "dev"
	Commit  = "unknown"
)
