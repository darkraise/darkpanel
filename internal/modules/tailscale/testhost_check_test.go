package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func TestTHHostDefaults(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()

	if e := h.env(""); e.Effects != nil || e.ChangeID != "" {
		t.Errorf("env(\"\") = change %q, journal %v, want neither", e.ChangeID, e.Effects)
	}
	env := h.env("c1")
	if env.HostID != "h1" || env.ChangeID != "c1" || env.DB != h.db || env.Exec == nil || env.Files == nil || env.Units == nil ||
		env.Effects == nil || env.Clock == nil || env.Log == nil || env.Principal.Scope != core.ScopeAdmin {
		t.Fatalf("env(c1) = %+v", env)
	}
	if got := env.Clock.Now(); !got.Equal(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("clock reads %v", got)
	}

	var tables []string
	rows, err := h.db.Read().Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'tailscale_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if want := []string{"tailscale_installs", "tailscale_pin_steps", "tailscale_pins"}; !reflect.DeepEqual(tables, want) {
		t.Errorf("tables %v, want %v", tables, want)
	}

	d := h.ts().d
	st, err := d.local().Status(ctx)
	if err != nil || st.BackendState != "Running" || st.Version != "1.104.1" || st.Self == nil {
		t.Errorf("Status = %+v, %v; want a running node on 1.104.1", st, err)
	}
	if info, err := d.osRelease(); err != nil || info != (osInfo{ID: "ubuntu", Codename: "noble"}) {
		t.Errorf("osRelease = %+v, %v; want ubuntu noble", info, err)
	}
	if name, err := d.hostname(); err != nil || name != thHostname || d.arch != "amd64" {
		t.Errorf("hostname = %q, %v, arch %q", name, err, d.arch)
	}
	if v, err := h.ts().installedVersion(ctx, env); err != nil || v != "1.104.1" {
		t.Errorf("installedVersion = %q, %v; the host starts with tailscale 1.104.1 installed", v, err)
	}
	if _, err := d.stun(ctx, netip.MustParseAddrPort("127.0.0.1:3478")); err == nil {
		t.Error("stun answered before a test set it")
	}
	if _, err := d.https(ctx, "https://example.invalid/derp/latency-check"); err == nil {
		t.Error("https answered before a test set it")
	}
	if _, err := d.readFile("/nonexistent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readFile of a missing path = %v, want fs.ErrNotExist", err)
	}
}

func TestTHHostRun(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	showhold := core.Cmd{Path: "/usr/bin/apt-mark", Args: []string{"showhold"}}

	if res, err := h.Run(ctx, showhold); err != nil || res.ExitCode != 0 || len(res.Stdout) != 0 {
		t.Errorf("an unscripted run = %+v, %v; want exit 0 and no output", res, err)
	}
	h.script("/usr/bin/apt-mark showhold",
		tsRun{res: core.CmdResult{Stdout: []byte("tailscale\n")}},
		tsRun{err: errors.New("boom")},
	)
	if res, err := h.Run(ctx, showhold); err != nil || string(res.Stdout) != "tailscale\n" {
		t.Errorf("first scripted run = %+v, %v", res, err)
	}
	if _, err := h.Run(ctx, showhold); err == nil || err.Error() != "boom" {
		t.Errorf("second scripted run error = %v, want boom", err)
	}
	if res, err := h.Run(ctx, showhold); err != nil || len(res.Stdout) != 0 {
		t.Errorf("after the queue = %+v, %v; want the unscripted answer", res, err)
	}

	for _, cmd := range []core.Cmd{
		{Path: "/usr/bin/apt-get", Args: []string{"remove", "-y", "curl"}},
		{Path: "/usr/bin/systemctl", Args: []string{"restart", "tailscaled"}},
		{Path: "/usr/bin/tailscale", Args: []string{"up"}},
	} {
		if _, err := h.Run(ctx, cmd); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%v: error %v, want a refusal by the production allowlist", cmd, err)
		}
	}
	if _, err := h.Run(ctx, core.Cmd{Path: "/usr/bin/apt-mark", Args: []string{"hold", "tailscale"}}); err != nil {
		t.Errorf("apt-mark hold tailscale refused: %v", err)
	}
	want := [][]string{
		{"/usr/bin/apt-mark", "showhold"}, {"/usr/bin/apt-mark", "showhold"}, {"/usr/bin/apt-mark", "showhold"},
		{"/usr/bin/apt-mark", "showhold"},
		{"/usr/bin/apt-get", "remove", "-y", "curl"}, {"/usr/bin/systemctl", "restart", "tailscaled"},
		{"/usr/bin/tailscale", "up"}, {"/usr/bin/apt-mark", "hold", "tailscale"},
	}
	if !reflect.DeepEqual(h.ran, want) {
		t.Errorf("ran %v, want %v", h.ran, want)
	}
}

func TestTHHostFiles(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	deb := "tailscale_1.104.1_amd64.deb"

	if err := h.WriteFile(ctx, ID, keyringPath, []byte("key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.WriteManaged(ctx, ID, debDir, deb, []byte("deb"), 0o600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if string(h.files[keyringPath]) != "key" || string(h.files[debDir+"/"+deb]) != "deb" {
		t.Errorf("files %v", h.files)
	}
	for path, want := range map[string]string{keyringPath: "key", debDir + "/" + deb: "deb"} {
		if b, err := h.ts().d.readFile(path); err != nil || string(b) != want {
			t.Errorf("readFile(%s) = %q, %v; deps.readFile must see what Files wrote", path, b, err)
		}
	}
	h.reads[keyringPath] = []byte("seeded")
	if b, _ := h.ts().d.readFile(keyringPath); string(b) != "seeded" {
		t.Errorf("readFile(%s) = %q; a seeded read wins over a written file", keyringPath, b)
	}
	for path, want := range map[string]bool{keyringPath: true, debDir + "/" + deb: true, sourcesPath: false} {
		if got, err := h.Owns(ctx, ID, path); err != nil || got != want {
			t.Errorf("Owns(%s) = %v, %v; want %v", path, got, err, want)
		}
	}

	refused := map[string]error{
		"another module":          h.WriteFile(ctx, "docker", keyringPath, nil, 0o644),
		"an undeclared path":      h.WriteFile(ctx, ID, "/etc/passwd", nil, 0o644),
		"a managed dir as a file": h.WriteFile(ctx, ID, debDir, nil, 0o644),
		"another directory":       h.WriteManaged(ctx, ID, "/var/lib/other", deb, nil, 0o600, 0, 0),
		"a name off pattern":      h.WriteManaged(ctx, ID, debDir, "evil.deb", nil, 0o600, 0, 0),
		"a path in a name":        h.WriteManaged(ctx, ID, debDir, "../tailscale_1.0_amd64.deb", nil, 0o600, 0, 0),
		"a block":                 h.ReplaceBlock(ctx, ID, keyringPath, "b", nil),
	}
	for name, err := range refused {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := h.RemoveManaged(ctx, ID, debDir, deb); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(ctx, ID, keyringPath); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.files[debDir+"/"+deb]; ok || len(h.files) != 0 {
		t.Errorf("files after removal %v", h.files)
	}
	if got, _ := h.Owns(ctx, ID, keyringPath); got {
		t.Error("a removed file is still owned")
	}
}

func TestTHFakeLocal(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	api := h.ts().d.local()

	yes, no := true, false
	routes := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	exit := "n123"
	p, err := api.EditPrefs(ctx, tslocal.PrefsPatch{WantRunning: &no, RunSSH: &yes, ExitNodeID: &exit, AdvertiseRoutes: &routes})
	if err != nil {
		t.Fatal(err)
	}
	if p.WantRunning || !p.RunSSH || p.ExitNodeID != "n123" || !reflect.DeepEqual(p.AdvertiseRoutes, routes) || !p.CorpDNS {
		t.Errorf("EditPrefs answered %+v; it applies the fields the patch sets and keeps the rest", p)
	}
	got, err := api.Prefs(ctx)
	if err != nil || got.WantRunning || !got.RunSSH {
		t.Errorf("Prefs after the edit = %+v, %v", got, err)
	}
	if len(h.local.Edits) != 1 || *h.local.Edits[0].RunSSH != true {
		t.Errorf("Edits %+v", h.local.Edits)
	}

	if err := api.Start(ctx, tslocal.StartOptions{AuthKey: "k"}); err != nil || len(h.local.Starts) != 1 || h.local.Starts[0].AuthKey != "k" {
		t.Errorf("Start: %v, recorded %+v", err, h.local.Starts)
	}
	if err := api.Logout(ctx); err != nil || h.local.Logouts != 1 {
		t.Errorf("Logout: %v, count %d", err, h.local.Logouts)
	}
	if err := api.LoginInteractive(ctx); err != nil || h.local.LoginInteractives != 1 {
		t.Errorf("LoginInteractive: %v, count %d", err, h.local.LoginInteractives)
	}
	if st, _ := api.Status(ctx); st.BackendState != "Running" {
		t.Error("Start and Logout must only be recorded: the fake does not change the status")
	}

	h.local.Cert = [2][]byte{[]byte("CERT"), []byte("KEY")}
	cert, key, err := api.CertPair(ctx, "ts-host-1.example.ts.net")
	if err != nil || string(cert) != "CERT" || string(key) != "KEY" || !reflect.DeepEqual(h.local.CertCalls, []string{"ts-host-1.example.ts.net"}) {
		t.Errorf("CertPair = %q, %q, %v; calls %v", cert, key, err, h.local.CertCalls)
	}

	if m, err := api.DERPMap(ctx); err != nil || len(m.Regions) != 0 {
		t.Errorf("DERPMap with none set = %+v, %v; want an empty map", m, err)
	}
	h.local.DERP = &tslocal.DERPMap{Regions: map[int]*tslocal.DERPRegion{1: {RegionID: 1, RegionCode: "nyc"}}}
	if m, err := api.DERPMap(ctx); err != nil || m.Regions[1].RegionCode != "nyc" {
		t.Errorf("DERPMap = %+v, %v", m, err)
	}

	boom := errors.New("boom")
	h.local.Errs = map[string]error{"Status": boom, "Prefs": boom, "EditPrefs": boom, "Start": boom, "LoginInteractive": boom, "Logout": boom, "DERPMap": boom, "CertPair": boom}
	_, e1 := api.Status(ctx)
	_, e2 := api.Prefs(ctx)
	_, e3 := api.EditPrefs(ctx, tslocal.PrefsPatch{})
	e4 := api.Start(ctx, tslocal.StartOptions{})
	e5 := api.Logout(ctx)
	_, e6 := api.DERPMap(ctx)
	_, _, e7 := api.CertPair(ctx, "x.example.ts.net")
	e8 := api.LoginInteractive(ctx)
	for i, err := range []error{e1, e2, e3, e4, e5, e6, e7, e8} {
		if !errors.Is(err, boom) {
			t.Errorf("method %d answered %v, want the queued error", i, err)
		}
	}

	h.local.update(func() {
		h.local.Errs = nil
		h.local.Status = nil
	})
	if _, err := api.Status(ctx); !errors.Is(err, tslocal.ErrUnavailable) {
		t.Errorf("Status with none set = %v, want tslocal.ErrUnavailable", err)
	}
}

func TestTHFakeLink(t *testing.T) {
	h := newTSHost(t)
	l := h.ts().d.link
	dst := netip.MustParseAddr("100.64.0.9")
	h.link.Routes = map[netip.Addr]netstat.Route{dst: {OutIface: "tailscale0"}}
	h.link.Sockets = []netstat.Socket{{Proto: netstat.ProtoTCP, State: netstat.StateEstablished}}

	if r, err := l.RouteGet(netstat.RouteQuery{Dst: dst}); err != nil || r.OutIface != "tailscale0" {
		t.Errorf("RouteGet = %+v, %v", r, err)
	}
	if _, err := l.RouteGet(netstat.RouteQuery{Dst: netip.MustParseAddr("192.0.2.99")}); !errors.Is(err, netstat.ErrNoRoute) {
		t.Errorf("RouteGet of an unknown destination = %v, want netstat.ErrNoRoute", err)
	}
	if s, err := l.Sockets(); err != nil || len(s) != 1 || s[0].State != netstat.StateEstablished {
		t.Errorf("Sockets = %+v, %v", s, err)
	}
	if ifs, err := l.Interfaces(); err != nil || len(ifs) != 3 || ifs[2].Name != "tailscale0" {
		t.Errorf("Interfaces = %+v, %v", ifs, err)
	}
	if l.PID() != 4242 {
		t.Errorf("PID = %d", l.PID())
	}
}

func TestTHFakeUnits(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	u := h.env("").Units

	if st, err := u.State(ctx, tailscaledUnit); err != nil || st.ActiveState != "active" {
		t.Errorf("State(tailscaled) = %+v, %v", st, err)
	}
	if st, err := u.State(ctx, controllerUnit); err != nil || st.LoadState != "not-found" {
		t.Errorf("State(controller) = %+v, %v", st, err)
	}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
	if st, _ := u.State(ctx, controllerUnit); st.ActiveState != "active" {
		t.Errorf("State(controller) after a change = %+v", st)
	}
	if err := u.Restart(ctx, tailscaledUnit); err != nil || !reflect.DeepEqual(h.units.Restarts, []string{tailscaledUnit}) {
		t.Errorf("Restart(tailscaled): %v, recorded %v", err, h.units.Restarts)
	}
	for name, err := range map[string]error{
		"State of docker":           func() error { _, err := u.State(ctx, "docker.service"); return err }(),
		"Restart of the controller": u.Restart(ctx, controllerUnit),
		"Stop of tailscaled":        u.Stop(ctx, tailscaledUnit),
		"Start of tailscaled":       u.Start(ctx, tailscaledUnit),
		"a transient service":       u.StartTransientService(ctx, "darkpanel-task-x-apply-1.service", core.TransientService{}),
		"Reload":                    u.Reload(ctx),
	} {
		if err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
}

func TestTHJournal(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	env := h.env("c1")
	seq, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "tailscale.install"}, []byte("secret"))
	if err != nil || seq != 1 {
		t.Fatalf("Intend = %d, %v", seq, err)
	}
	if err := env.Effects.Made(ctx, seq, "tailscale.install", core.StateDoc{}); err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Skipped(ctx, seq); err == nil {
		t.Error("an effect already made was skipped")
	}
	got := h.journal.effects()
	if len(got) != 1 || got[0].ChangeID != "c1" || got[0].State != core.EffectMade || got[0].Post == nil {
		t.Errorf("effects %+v", got)
	}
	if s, err := env.Effects.Secret(ctx, seq); err != nil || string(s) != "secret" {
		t.Errorf("Secret = %q, %v", s, err)
	}
}
