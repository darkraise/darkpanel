package users

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// FixtureModule is the module reading the rig's fixture host files instead of
// the host's: /etc/shadow is readable only by root, and CI runs unprivileged.
func FixtureModule(t *testing.T) core.Module { return newRig(t).module() }
