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
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// cmdliner is implemented by a Host that reads a process's command line. It
// is optional: without it, where Docker's userland proxy relays is unknown.
type cmdliner interface {
	Cmdline(pid int) ([]string, error)
}

// Cmdline reads the NUL-separated command line of pid under ProcRoot.
func (h LiveHost) Cmdline(pid int) ([]string, error) {
	root := h.ProcRoot
	if root == "" {
		root = "/proc"
	}
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "cmdline")) //nolint:gosec // G304: the proc root and a process id name the file
	if err != nil {
		return nil, err
	}
	args := strings.Split(string(b), "\x00")
	if n := len(args); args[n-1] == "" {
		args = args[:n-1]
	}
	return args, nil
}

// dockerProxy is the comm of Docker's userland proxy.
const dockerProxy = "docker-proxy"

// cmdline is a process's command line, or why it could not be read.
type cmdline struct {
	args []string
	err  error
}

// facts are the live facts one guard check uses.
type facts struct {
	sockets    []netstat.Socket
	ifaces     []netstat.Interface // without an interface that vanished before its rp_filter read
	rpFilter   map[string]int      // live net/ipv4/conf/<name>/rp_filter for "all" and every up, non-loopback interface
	controller int                 // MainPID of a running local darkpanel-controller.service; 0 when none
	pid        int                 // the agent's own process id
	cmdlines   map[int]cmdline     // the command line of each docker-proxy that holds a listening TCP socket
}

// gatherFacts reads the socket table, the interfaces, the live rp_filter
// values and the local controller's main process. Any read that fails is an
// error, so a guard never passes on facts it could not read; in particular a
// proc root without socket tables (netstat.ErrNoSocketTables) is not a host
// without sessions. The one exception is an interface whose rp_filter key
// no longer exists: it vanished after the interfaces were listed, so no
// packet can arrive on it, and it is left out. A docker-proxy command line
// that cannot be read is kept as its error: the flow it would give is then
// unknown rather than the whole check failing.
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
	f := facts{sockets: socks, rpFilter: map[string]int{}, pid: h.PID()}
	if c, ok := h.(cmdliner); ok {
		for _, so := range socks {
			if so.Proto != netstat.ProtoTCP || so.State != netstat.StateListen {
				continue
			}
			for _, p := range so.Processes {
				if _, done := f.cmdlines[p.PID]; done || p.Comm != dockerProxy {
					continue
				}
				if f.cmdlines == nil {
					f.cmdlines = map[int]cmdline{}
				}
				args, err := c.Cmdline(p.PID)
				f.cmdlines[p.PID] = cmdline{args: args, err: err}
			}
		}
	}
	if f.rpFilter["all"], err = readRPFilter(ctx, env, "all"); err != nil {
		return facts{}, err
	}
	for _, i := range ifaces {
		if i.Up && !i.Loopback {
			n, err := readRPFilter(ctx, env, i.Name)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return facts{}, err
			}
			f.rpFilter[i.Name] = n
		}
		f.ifaces = append(f.ifaces, i)
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

// readRPFilter reads the live net/ipv4/conf/<name>/rp_filter. A read error
// is wrapped, so a key that does not exist still matches fs.ErrNotExist.
func readRPFilter(ctx context.Context, env core.Env, name string) (int, error) {
	key := "net/ipv4/conf/" + name + "/rp_filter"
	v, err := env.Files.ReadSysctl(ctx, moduleID, key)
	if err != nil {
		return 0, fmt.Errorf("firewall: read %s: %w", key, err)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("firewall: %s is %q, not a number", key, v)
	}
	return n, nil
}
