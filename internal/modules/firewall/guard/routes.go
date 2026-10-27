package guard

import "github.com/darkraise/darkpanel/internal/platform/netstat"

// routeDumper is implemented by a Host that lists every route of a family.
// It is not part of Host: only a check that must read the routes needs it,
// and that check fails when its host cannot list them.
type routeDumper interface {
	Routes(family netstat.Family) ([]netstat.Route, error)
}

// Routes lists every route of every table for family.
func (LiveHost) Routes(family netstat.Family) ([]netstat.Route, error) { return netstat.Routes(family) }
