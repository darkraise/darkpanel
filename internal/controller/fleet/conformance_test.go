package fleet_test

import (
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/fleet"
)

// Package fleet must not import dispatch or coreactions, which import it,
// so the registry's conformance to their interfaces is checked from here.
var (
	_ dispatch.ClientSource = (*fleet.Registry)(nil)
	_ coreactions.Sessions  = (*fleet.Registry)(nil)
)
