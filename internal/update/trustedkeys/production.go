//go:build !harnesskeys

package trustedkeys

// keys is empty until the owner adds the production release key; until then
// every update and install.sh refuses to verify a release.
var keys = []Key{}

const (
	testBuild = false
	baseURL   = "https://github.com/Darkraise/darkpanel/releases/download"
)
