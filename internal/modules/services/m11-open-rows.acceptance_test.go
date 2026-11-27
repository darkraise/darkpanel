package services

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

func TestA7_UnitFileTextIsAdminOnly(t *testing.T) {
	const text = "[Service]\nEnvironment=SECRET_TOKEN=hunter2\nExecStart=/usr/bin/app\n"
	r := newRig(t, unit("app.service", func(u *core.UnitInfo) {
		u.FragmentPath, u.FileText = "/etc/systemd/system/app.service", text
	}))
	readP := core.Principal{Actor: "user:ro", UserID: "ro", Source: "192.0.2.3", Access: core.Access{All: core.ScopeRead}}
	for name, p := range map[string]core.Principal{"read": readP, "operate": operatorP} {
		got, err := getHandler{}.Read(context.Background(), r.env(p), GetInput{Unit: "app.service"})
		if err != nil {
			t.Fatalf("%s scope: %v", name, err)
		}
		if got.UnitFileText != "" {
			t.Errorf("%s scope got the unit file text %q", name, got.UnitFileText)
		}
	}
	got, err := getHandler{}.Read(context.Background(), r.env(adminP), GetInput{Unit: "app.service"})
	if err != nil {
		t.Fatal(err)
	}
	if got.UnitFileText != text {
		t.Errorf("admin scope got %q, want %q", got.UnitFileText, text)
	}
}

func qaPlan(r *rig, o op, p core.Principal, name string) (core.PlanOutcome, error) {
	return r.plan(o, p, name)
}

func qaForbidden(t *testing.T, what string, err error, mention string) {
	t.Helper()
	e := errcode.From(err)
	if err == nil || e.Code != errcode.Forbidden {
		t.Errorf("%s: err = %v, want a %s refusal", what, err, errcode.Forbidden)
		return
	}
	if mention != "" && !strings.Contains(e.Detail, mention) {
		t.Errorf("%s: detail %q does not name %s", what, e.Detail, mention)
	}
}

func TestA8_StartOfAUnitThatPullsInAManagedUnitNeedsAdmin(t *testing.T) {
	r := newRig(t,
		unit("docker.service"), unit("nftables.service"),
		unit("app.service", func(u *core.UnitInfo) { u.Requires = []string{"docker.service"} }),
		unit("fw.service", func(u *core.UnitInfo) { u.Wants = []string{"nftables.service"} }),
		unit("plain.service"),
	)
	_, err := qaPlan(r, opStart, operatorP, "app.service")
	qaForbidden(t, "operator start of a unit that Requires=docker.service", err, "docker.service")

	out, err := qaPlan(r, opStart, adminP, "app.service")
	if err != nil {
		t.Fatalf("admin plan of app.service: %v", err)
	}
	if !slices.ContainsFunc(out.Diff, func(l core.DiffLine) bool { return l.Text == "starting app.service also starts docker.service" }) {
		t.Errorf("admin plan of app.service has no line %q; diff %+v", "starting app.service also starts docker.service", out.Diff)
	}

	_, err = qaPlan(r, opStart, operatorP, "fw.service")
	qaForbidden(t, "operator start of a unit that Wants=nftables.service", err, "nftables.service")

	// Control: a start of a unit with no managed dependency plans for an operator.
	if _, err := qaPlan(r, opStart, operatorP, "plain.service"); err != nil {
		t.Errorf("operator start of a plain unit: %v", err)
	}
}

func TestA9_StopAndRestartFollowManagedDependencies(t *testing.T) {
	r := newRig(t,
		unit("docker.service"), unit("docker.socket"), unit("systemd-networkd.service"),
		unit("netdep.service", func(u *core.UnitInfo) { u.RequiredBy = []string{"systemd-networkd.service"} }),
		unit("dockerdep.service", func(u *core.UnitInfo) { u.RequiredBy = []string{"docker.service"} }),
		unit("plain.service"),
	)
	_, err := qaPlan(r, opStop, operatorP, "docker.socket")
	qaForbidden(t, "operator stop of docker.socket", err, "")
	if _, err := qaPlan(r, opStop, adminP, "docker.socket"); err != nil {
		t.Errorf("control: admin stop of docker.socket: %v", err)
	}

	_, err = qaPlan(r, opRestart, operatorP, "dockerdep.service")
	qaForbidden(t, "operator restart of a unit that docker.service requires", err, "docker.service")

	out, err := qaPlan(r, opStop, adminP, "netdep.service")
	if err != nil {
		t.Fatalf("admin stop of a unit a network-stack unit requires: %v", err)
	}
	i := slices.IndexFunc(out.Guards, func(g core.Guard) bool { return g.ID == "services.network_stack" })
	if i < 0 || out.Guards[i].OK {
		t.Errorf("admin stop of netdep.service: guards %+v, want services.network_stack failing", out.Guards)
	}

	if _, err := qaPlan(r, opStop, operatorP, "plain.service"); err != nil {
		t.Errorf("operator stop of a plain unit: %v", err)
	}
}

func TestA10_AgentOnTailnetSocketCases(t *testing.T) {
	const pid = 777
	tcp := func(local, remote string, state netstat.State, pids ...int) netstat.Socket {
		s := netstat.Socket{Proto: netstat.ProtoTCP, Local: netip.MustParseAddrPort(local), State: state}
		if remote != "" {
			s.Remote = netip.MustParseAddrPort(remote)
		}
		for _, p := range pids {
			s.Processes = append(s.Processes, netstat.Process{PID: p})
		}
		return s
	}
	for _, c := range []struct {
		name  string
		socks []netstat.Socket
		want  bool
	}{
		{"agent PID, Tailscale IPv4 local address, outbound", []netstat.Socket{tcp("100.101.102.103:50000", "100.64.0.9:443", netstat.StateEstablished, pid)}, true},
		{"agent PID, Tailscale IPv6 local address, outbound", []netstat.Socket{tcp("[fd7a:115c:a1e0:ab12:4843:cd96:6270:6670]:50000", "[fd7a:115c:a1e0::9]:443", netstat.StateEstablished, pid)}, true},
		{"another PID, Tailscale IPv4 local address", []netstat.Socket{tcp("100.101.102.103:50000", "100.64.0.9:443", netstat.StateEstablished, pid+1)}, false},
		{"another PID, Tailscale IPv6 local address", []netstat.Socket{tcp("[fd7a:115c:a1e0::1]:50000", "[fd7a:115c:a1e0::9]:443", netstat.StateEstablished, pid+1)}, false},
		{"agent PID, LAN IPv4 local address", []netstat.Socket{tcp("192.168.1.20:50000", "192.168.1.1:443", netstat.StateEstablished, pid)}, false},
		{"agent PID, LAN IPv4 10/8 local address", []netstat.Socket{tcp("10.0.0.5:50000", "10.0.0.1:443", netstat.StateEstablished, pid)}, false},
		{"agent PID, LAN IPv6 local address", []netstat.Socket{tcp("[fd00::5]:50000", "[fd00::1]:443", netstat.StateEstablished, pid)}, false},
		{"inbound connection on a listening port, Tailscale address", []netstat.Socket{
			tcp("100.101.102.103:9443", "", netstat.StateListen, pid),
			tcp("100.101.102.103:9443", "100.64.0.9:51000", netstat.StateEstablished, pid)}, false},
		{"inbound connection on a listening port, Tailscale IPv6 address", []netstat.Socket{
			tcp("[fd7a:115c:a1e0::1]:9443", "", netstat.StateListen, pid),
			tcp("[fd7a:115c:a1e0::1]:9443", "[fd7a:115c:a1e0::9]:51000", netstat.StateEstablished, pid)}, false},
		{"no sockets", nil, false},
		{"a LAN socket and a tailnet socket of the agent", []netstat.Socket{
			tcp("192.168.1.20:50000", "192.168.1.1:443", netstat.StateEstablished, pid),
			tcp("100.101.102.103:50001", "100.64.0.9:443", netstat.StateEstablished, pid)}, true},
	} {
		got, err := agentOnTailnet(func() ([]netstat.Socket, error) { return c.socks, nil }, pid)
		if err != nil || got != c.want {
			t.Errorf("%s: agentOnTailnet = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}
