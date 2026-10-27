// Package guard is the firewall's lockout guard (spec 2.3, 1.5): it
// gathers this host's live facts, builds every protected flow from them and
// from the controller's guard context, evaluates each flow on every
// eligible in-interface against a predicted or loaded state, and reports
// the firewall.lockout guard.
package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// moduleID is the module every env.Files call names.
const moduleID = "firewall"

// controllerUnit is the controller's systemd unit; the firewall's manifest
// declares it as an external unit with the state operation (owner ruling
// R3).
const controllerUnit = "darkpanel-controller.service"

// Host reads the live facts of the host the agent runs on. LiveHost is the
// production implementation.
type Host interface {
	Sockets() ([]netstat.Socket, error)
	Interfaces() ([]netstat.Interface, error)
	RouteGet(q netstat.RouteQuery) (netstat.Route, error)
	PID() int // the agent's own process id
}

// LiveHost reads this host through internal/platform/netstat.
type LiveHost struct {
	ProcRoot string // the proc root sockets are read from; "" is /proc
}

// Sockets reads the socket table under ProcRoot.
func (h LiveHost) Sockets() ([]netstat.Socket, error) {
	root := h.ProcRoot
	if root == "" {
		root = "/proc"
	}
	return netstat.Table{Root: root}.Sockets()
}

// Interfaces lists the host's interfaces.
func (LiveHost) Interfaces() ([]netstat.Interface, error) { return netstat.Interfaces() }

// RouteGet asks the kernel for the route of q.
func (LiveHost) RouteGet(q netstat.RouteQuery) (netstat.Route, error) { return netstat.RouteGet(q) }

// PID is os.Getpid.
func (LiveHost) PID() int { return os.Getpid() }

// facts are the live facts one guard check uses.
type facts struct {
	sockets    []netstat.Socket
	ifaces     []netstat.Interface
	rpFilter   map[string]int // live net/ipv4/conf/<name>/rp_filter for "all" and every up, non-loopback interface
	controller int            // MainPID of a running local darkpanel-controller.service; 0 when none
	pid        int            // the agent's own process id
}

// gatherFacts reads the socket table, the interfaces, the live rp_filter
// values and the local controller's main process. Any read that fails is an
// error, so a guard never passes on facts it could not read; in particular a
// proc root without socket tables (netstat.ErrNoSocketTables) is not a host
// without sessions.
func gatherFacts(ctx context.Context, env core.Env, h Host) (facts, error) {
	if env.Files == nil || env.Units == nil {
		return facts{}, errors.New("firewall: the guard needs the file writer and the unit client")
	}
	socks, err := h.Sockets()
	if err != nil {
		return facts{}, fmt.Errorf("firewall: read the socket table: %w", err)
	}
	ifaces, err := h.Interfaces()
	if err != nil {
		return facts{}, fmt.Errorf("firewall: read the interfaces: %w", err)
	}
	f := facts{sockets: socks, ifaces: ifaces, rpFilter: map[string]int{}, pid: h.PID()}
	names := []string{"all"}
	for _, i := range ifaces {
		if i.Up && !i.Loopback {
			names = append(names, i.Name)
		}
	}
	for _, name := range names {
		key := "net/ipv4/conf/" + name + "/rp_filter"
		v, err := env.Files.ReadSysctl(ctx, moduleID, key)
		if err != nil {
			return facts{}, fmt.Errorf("firewall: read %s: %w", key, err)
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return facts{}, fmt.Errorf("firewall: %s is %q, not a number", key, v)
		}
		f.rpFilter[name] = n
	}
	st, err := env.Units.State(ctx, controllerUnit)
	if err != nil {
		return facts{}, fmt.Errorf("firewall: state of %s: %w", controllerUnit, err)
	}
	if st.LoadState != "not-found" && st.ActiveState != "inactive" {
		f.controller = int(st.MainPID)
	}
	return f, nil
}
