package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const rcAllow1 = "01J00000000000000000000001"

func rcAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

// rcFacts is one running container, web, that publishes host port 8080 to
// its port 80 on the default bridge.
func rcFacts() dockerfw.Facts {
	return dockerfw.Facts{
		Containers: []dockerfw.Container{{
			ID: "id-web", Name: "web",
			Ports:     []dockerfw.Published{{HostAddr: rcAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"}},
			Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: rcAddr("172.17.0.2")}},
		}},
		Networks: []dockerfw.Network{{
			ID: "id-bridge", Name: "bridge", Driver: "bridge", Bridge: "docker0",
			Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")},
		}},
	}
}

// rcState is the state of a supported daemon with the integration on,
// holding rows (an allowance of web's binding 8080/tcp when withRow is set)
// and the default private sources plus extra, realized against rcFacts.
func rcState(withRow bool, extra ...string) dockerState {
	s := dockerfw.DefaultSettings()
	for _, e := range extra {
		s.PrivateSources = append(s.PrivateSources, netip.MustParsePrefix(e))
	}
	var rows []dockerfw.ContainerRule
	if withRow {
		rows = []dockerfw.ContainerRule{{ID: rcAllow1, Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}, Kind: dockerfw.Allow}}
	}
	facts := rcFacts()
	return dockerState{
		available: true,
		det:       dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft", RawProtection: true},
		facts:     facts,
		cfg:       dockerConfig{Enabled: true, Settings: s},
		rows:      rows,
		realized:  dockerfw.Realize(rows, facts, s),
	}
}

// rcApplied is one call of the pass's Apply: the action and its input as JSON.
type rcApplied struct{ action, input string }

// rcHost is a fake host with its Docker fake, a ufw that reloads, and the
// integration flag stored on, or not stored at all.
func rcHost(t *testing.T, enabled bool) (*fakeHost, *fakeDocker, fw) {
	t.Helper()
	h := newFakeHost(t)
	d := newFakeDocker()
	h.ufw["reload"] = func(*fhState) int { return 0 }
	f, _ := newFW(h)
	f.d = fdDeps(h, d)
	if enabled {
		err := h.db.Write(t.Context(), func(tx *sql.Tx) error {
			return writeDockerConfig(t.Context(), tx, dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return h, d, f
}

// rcTask is the TaskContext of one pass: the system principal, no change,
// and an Apply that records its calls and returns err.
func rcTask(h *fakeHost, calls *[]rcApplied, err error) core.TaskContext {
	env := h.env()
	env.Effects, env.ChangeID = nil, ""
	env.Principal = core.Principal{Actor: "system:firewall.reconcile", Scope: core.ScopeAdmin}
	return core.TaskContext{Env: env, Apply: func(_ context.Context, action string, input any) (core.ChangeView, error) {
		raw, mErr := json.Marshal(input)
		if mErr != nil {
			return core.ChangeView{}, mErr
		}
		*calls = append(*calls, rcApplied{action, string(raw)})
		return core.ChangeView{}, err
	}}
}

// rcWriteBlock writes the block of st into the host's after files, as an
// applied change leaves them.
func rcWriteBlock(t *testing.T, h *fakeHost, st dockerState) {
	t.Helper()
	files, err := blockFiles(h.config(), st, true)
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range files {
		h.writeFile(p, data)
	}
}

// TestRcTaskDeclaration: the task is firewall.reconcile, every five minutes
// under the firewall lock, with a pass and a Docker watch.
func TestRcTaskDeclaration(t *testing.T) {
	task := fw{}.reconcileTask()
	if task.ID != "firewall.reconcile" || task.Every != 5*time.Minute || !slices.Equal(task.LockKeys, []string{"firewall"}) {
		t.Errorf("task %q every %v locks %q, want firewall.reconcile every 5m under the firewall lock", task.ID, task.Every, task.LockKeys)
	}
	if task.Run == nil || task.Watch == nil {
		t.Errorf("Run %v Watch %v, want both set", task.Run != nil, task.Watch != nil)
	}
}

// TestRcEvent: exactly the events spec 2.8 names wake the task.
func TestRcEvent(t *testing.T) {
	for _, tc := range []struct {
		typ, action string
		want        bool
	}{
		{"container", "start", true}, {"container", "die", true}, {"container", "destroy", true},
		{"network", "connect", true}, {"network", "disconnect", true}, {"network", "create", true}, {"network", "destroy", true},
		{"container", "create", false}, {"container", "kill", false}, {"container", "exec_start: sh", false},
		{"container", "health_status: healthy", false}, {"network", "remove", false},
		{"volume", "create", false}, {"image", "pull", false}, {"", "start", false},
	} {
		if got := reconcileEvent(dockerapi.Event{Type: tc.typ, Action: tc.action}); got != tc.want {
			t.Errorf("reconcileEvent(%s %s) = %v, want %v", tc.typ, tc.action, got, tc.want)
		}
	}
}

// rcDocker is the Docker fake with the options of its event stream recorded.
type rcDocker struct {
	*fakeDocker
	opts dockerapi.EventsOptions
}

func (r *rcDocker) Events(ctx context.Context, o dockerapi.EventsOptions) (dockerEvents, error) {
	r.opts = o
	return r.fakeDocker.Events(ctx, o)
}

// TestRcWatch: the watch asks Docker for container and network events only,
// wakes the task for each event of spec 2.8 and no other, and returns the
// stream's end as an error so the runtime restarts it.
func TestRcWatch(t *testing.T) {
	h, d, f := rcHost(t, false)
	rd := &rcDocker{fakeDocker: d}
	f.d.docker = rd
	var woke atomic.Int32
	done := make(chan error, 1)
	go func() { done <- f.reconcileWatch(t.Context(), h.env(), func() { woke.Add(1) }) }()
	for _, ev := range []dockerapi.Event{
		{Type: "container", Action: "start"}, {Type: "container", Action: "exec_start: sh"}, {Type: "container", Action: "die"},
		{Type: "network", Action: "connect"}, {Type: "volume", Action: "create"}, {Type: "network", Action: "destroy"},
		{Type: "container", Action: "create"},
	} {
		d.events <- ev
	}
	close(d.events)
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Errorf("watch returned %v, want an error wrapping io.EOF", err)
	}
	if got := woke.Load(); got != 4 {
		t.Errorf("woke %d times, want 4 (start, die, connect, destroy)", got)
	}
	sorted := func(s []string) []string { s = slices.Clone(s); slices.Sort(s); return s }
	if got, want := sorted(rd.opts.Filters["type"]), []string{"container", "network"}; !slices.Equal(got, want) {
		t.Errorf("type filter %q, want %q", got, want)
	}
	if got, want := sorted(rd.opts.Filters["event"]), []string{"connect", "create", "destroy", "die", "disconnect", "start"}; !slices.Equal(got, want) {
		t.Errorf("event filter %q, want %q", got, want)
	}
}

// TestRcWatchStops: a watch whose context ends returns nil, and one that
// cannot reach Docker returns the error.
func TestRcWatchStops(t *testing.T) {
	h, d, f := rcHost(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.reconcileWatch(ctx, h.env(), func() {}) }()
	cancel()
	if err := <-done; err != nil {
		t.Errorf("watch after cancel returned %v, want nil", err)
	}

	d.err = fmt.Errorf("dockerapi: %w", dockerapi.ErrUnavailable)
	if err := f.reconcileWatch(t.Context(), h.env(), func() {}); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("watch without a daemon returned %v, want dockerapi.ErrUnavailable", err)
	}
}

// TestRcPass: one pass over a supported daemon applies firewall.docker.repair
// when UFW's loaded Darkpanel chains differ from the files, firewall.containers.apply
// when only the files or the container rules differ from what the stored rows
// realize, and nothing otherwise or when the difference is not the integration's.
func TestRcPass(t *testing.T) {
	user := fwstate.ChainDiff{Family: fwstate.V4, Table: "filter", Chain: "DOCKER-USER"}
	deny := fwstate.ChainDiff{Family: fwstate.V4, Table: "filter", Chain: "darkpanel-docker-deny"}
	binding := fwstate.ChainDiff{Family: fwstate.V6, Table: "filter", Chain: "dp-b-0123456789ab"}
	ufw := fwstate.ChainDiff{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input"}
	nat := fwstate.ChainDiff{Family: fwstate.V4, Table: "nat", Chain: "darkpanel-nat"}
	plain, withRow, custom := rcState(false), rcState(true), rcState(false, "203.0.113.0/24")
	gone := plain
	gone.available, gone.reason = false, "the Docker daemon is not running"
	nftables := plain
	nftables.det.Driver = "nftables"
	apply := []rcApplied{{"firewall.containers.apply", `{"ops":[]}`}}
	repair := []rcApplied{{"firewall.docker.repair", `{}`}}

	cases := []struct {
		name    string
		st      dockerState  // what the pass reads
		written *dockerState // the state whose block the files hold; nil: no block
		diffs   []fwstate.ChainDiff
		want    []rcApplied
	}{
		{"block missing from the files", plain, nil, nil, apply},
		{"files hold an older block", custom, &plain, nil, apply},
		{"container rules not in UFW yet", withRow, &withRow, nil, apply},
		{"everything in step", plain, &plain, nil, nil},
		{"Darkpanel's loaded chain differs", plain, &plain, []fwstate.ChainDiff{deny}, repair},
		{"loaded chains of both families differ", plain, &plain, []fwstate.ChainDiff{user, binding}, repair},
		{"loaded chain differs and the files are old too", custom, &plain, []fwstate.ChainDiff{user}, repair},
		{"the difference reaches beyond the integration", plain, &plain, []fwstate.ChainDiff{deny, ufw}, nil},
		{"only a UFW chain differs", plain, &plain, []fwstate.ChainDiff{ufw}, nil},
		{"a Darkpanel chain of another table differs", plain, &plain, []fwstate.ChainDiff{nat}, nil},
		{"Docker unreachable", gone, nil, nil, nil},
		{"unsupported combination", nftables, nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, f := rcHost(t, true)
			if tc.written != nil {
				rcWriteBlock(t, h, *tc.written)
			}
			h.diffs = tc.diffs
			var calls []rcApplied
			if err := f.reconcileWith(t.Context(), rcTask(h, &calls, nil), h.config(), h.loaded, tc.st); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(calls, tc.want) {
				t.Errorf("applied %v, want %v", calls, tc.want)
			}
		})
	}

	t.Run("a failed apply fails the pass", func(t *testing.T) {
		h, _, f := rcHost(t, true)
		boom := errors.New("rctest: guard firewall.lockout failed")
		var calls []rcApplied
		err := f.reconcileWith(t.Context(), rcTask(h, &calls, boom), h.config(), h.loaded, plain)
		if !errors.Is(err, boom) || len(calls) != 1 {
			t.Errorf("err %v after %d applies, want %v after one", err, len(calls), boom)
		}
	})
}

// TestRcGates: a pass does nothing, and never talks to Docker, while the
// integration is off, UFW is disabled or DOCKER-USER holds a foreign rule;
// with Docker unreachable it does nothing and does not fail.
func TestRcGates(t *testing.T) {
	foreign := "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n" +
		"-A DOCKER-USER -s 203.0.113.0/24 -j DROP\nCOMMIT\n"
	cases := []struct {
		name    string
		enabled bool
		setup   func(t *testing.T, h *fakeHost, d *fakeDocker)
		touches bool // the pass asks Docker
	}{
		{"integration off", false, func(*testing.T, *fakeHost, *fakeDocker) {}, false},
		{"UFW disabled", true, func(_ *testing.T, h *fakeHost, _ *fakeDocker) {
			s := h.state()
			s.Enabled = false
			h.setState(s)
		}, false},
		{"foreign DOCKER-USER rule", true, func(t *testing.T, h *fakeHost, _ *fakeDocker) { h.loaded.V4 = fhParse(t, foreign) }, false},
		{"Docker unreachable", true, func(_ *testing.T, _ *fakeHost, d *fakeDocker) {
			d.err = fmt.Errorf("dockerapi: %w", dockerapi.ErrUnavailable)
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, d, f := rcHost(t, tc.enabled)
			tc.setup(t, h, d)
			var calls []rcApplied
			if err := f.reconcile(t.Context(), rcTask(h, &calls, nil)); err != nil {
				t.Errorf("pass failed: %v", err)
			}
			if len(calls) != 0 {
				t.Errorf("applied %v, want nothing", calls)
			}
			if !tc.touches && len(d.calls) != 0 {
				t.Errorf("Docker was asked %q, want no call", d.calls)
			}
		})
	}
}

// TestRcFinishesCleanup: a pass finishes an owned-chain cleanup that a crash
// left, whatever the integration flag says.
func TestRcFinishesCleanup(t *testing.T) {
	h, d, f := rcHost(t, false)
	if err := beginCleanup(t.Context(), h.db, "c9", h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	var calls []rcApplied
	if err := f.reconcile(t.Context(), rcTask(h, &calls, nil)); err != nil {
		t.Fatal(err)
	}
	if pending, err := pendingCleanups(t.Context(), h.db); err != nil || len(pending) != 0 {
		t.Errorf("pending cleanups %q, err %v; want none", pending, err)
	}
	if len(calls) != 0 || len(d.calls) != 0 {
		t.Errorf("applied %v, Docker asked %q; want neither", calls, d.calls)
	}
}
