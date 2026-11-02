package derp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func sdTables(t *testing.T, h *dpHost, ctl bool, like string) []string {
	t.Helper()
	db := h.db
	if ctl {
		db = h.ctlDB
	}
	rows, err := db.Read().Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE ? ORDER BY name`, like)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestSdHostDefaults(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()

	if e := h.env(""); e.Effects != nil || e.ChangeID != "" {
		t.Errorf("env(\"\") = change %q, journal %v, want neither", e.ChangeID, e.Effects)
	}
	env := h.env("c1")
	if env.HostID != "h1" || env.ChangeID != "c1" || env.DB != h.db || env.Exec == nil || env.Files == nil || env.Units == nil ||
		env.Effects == nil || env.Clock == nil || env.Log == nil || env.Principal.Scope != core.ScopeAdmin {
		t.Fatalf("env(c1) = %+v", env)
	}
	if got := env.Clock.Now(); !got.Equal(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("clock reads %v", got)
	}
	ctl := h.ctlEnv("c2")
	if ctl.HostID != "" || ctl.ChangeID != "c2" || ctl.DB != h.ctlDB || ctl.Agents == nil || ctl.Effects == nil || ctl.Sealer == nil || ctl.Clock == nil || ctl.Units != nil {
		t.Fatalf("ctlEnv(c2) = %+v", ctl)
	}
	if e := h.ctlEnv(""); e.Effects != nil {
		t.Error("ctlEnv(\"\") has an effects journal")
	}

	if got := sdTables(t, h, false, "derp_%"); !reflect.DeepEqual(got, []string{"derp_server", "derp_updates"}) {
		t.Errorf("agent tables %v", got)
	}
	if got := sdTables(t, h, true, "derp_%"); !reflect.DeepEqual(got, []string{"derp_servers"}) {
		t.Errorf("controller tables %v", got)
	}

	d := h.dp().d
	st, err := d.local().Status(ctx)
	if err != nil || st.BackendState != "Running" || st.Version != "1.104.1" || st.Self == nil || !reflect.DeepEqual(st.Self.Tags, []string{"tag:derp"}) ||
		len(st.TailscaleIPs) != 1 || st.TailscaleIPs[0] != netip.MustParseAddr("100.64.0.10") {
		t.Errorf("Status = %+v, %v; want a running node tagged tag:derp at 100.64.0.10", st, err)
	}
	if d.version != "1.4.0" || d.arch != "amd64" {
		t.Errorf("version %q, arch %q", d.version, d.arch)
	}
	if _, err := d.readFile("/nonexistent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readFile of a missing path = %v, want fs.ErrNotExist", err)
	}
	if _, _, ok, err := d.lookupUser(server.ServiceUser); err != nil || ok {
		t.Errorf("lookupUser before useradd = ok %v, %v", ok, err)
	}
	if v, err := d.linkViaTailnet(); err != nil || v {
		t.Errorf("linkViaTailnet = %v, %v", v, err)
	}
	if s, err := d.sockets(); err != nil || len(s) != 0 {
		t.Errorf("sockets = %v, %v", s, err)
	}
	if v, err := d.connected(ctx, h.ctlDB); err != nil || v {
		t.Errorf("connected = %v, %v", v, err)
	}
	if _, _, err := d.fetch(ctx, "1.4.0", "v1.104.1", "amd64"); err == nil {
		t.Error("fetch answered before a test set fetchBin")
	}
	if _, err := d.tlsProbe(ctx, probeTarget{HostName: "x.example.com"}); err == nil {
		t.Error("tlsProbe answered before a test set tls")
	}
	if _, err := d.stun(ctx, netip.MustParseAddrPort("127.0.0.1:3478")); err == nil {
		t.Error("stun answered before a test set stun")
	}
	if _, err := d.resolve(ctx, "derp.example.com"); err == nil {
		t.Error("resolve answered before a test set resolved")
	}
}

func TestSdHostDeps(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	d := h.dp().d
	boom := errors.New("boom")

	h.fetchBin = []byte("derper")
	bin, sha, err := d.fetch(ctx, "1.4.0", "v1.104.1", "amd64")
	sum := sha256.Sum256([]byte("derper"))
	if err != nil || string(bin) != "derper" || sha != hex.EncodeToString(sum[:]) {
		t.Errorf("fetch = %q, %q, %v", bin, sha, err)
	}
	h.fetchErr = boom
	if _, _, err := d.fetch(ctx, "1.4.0", "v1.104.1", "arm64"); !errors.Is(err, boom) {
		t.Errorf("fetch with fetchErr = %v", err)
	}
	if want := []string{"1.4.0 v1.104.1 amd64", "1.4.0 v1.104.1 arm64"}; !reflect.DeepEqual(h.fetches, want) {
		t.Errorf("fetches %v, want %v", h.fetches, want)
	}

	h.tls = map[string]dpProbe{"203.0.113.10": {d: 20 * time.Millisecond}, "derp.example.com": {err: boom}}
	if got, err := d.tlsProbe(ctx, probeTarget{HostName: "derp.example.com", Address: "203.0.113.10"}); err != nil || got != 20*time.Millisecond {
		t.Errorf("tlsProbe by address = %v, %v; the address key wins over the name", got, err)
	}
	if _, err := d.tlsProbe(ctx, probeTarget{HostName: "derp.example.com"}); !errors.Is(err, boom) {
		t.Errorf("tlsProbe by name = %v", err)
	}
	server1 := netip.MustParseAddrPort("203.0.113.10:3478")
	h.stun = map[netip.AddrPort]dpProbe{server1: {d: 5 * time.Millisecond}}
	if got, err := d.stun(ctx, server1); err != nil || got != 5*time.Millisecond {
		t.Errorf("stun = %v, %v", got, err)
	}
	if _, err := d.stun(ctx, netip.MustParseAddrPort("203.0.113.11:3478")); err == nil {
		t.Error("stun answered for an address nobody set")
	}
	h.resolved = map[string][]netip.Addr{"derp.example.com": {netip.MustParseAddr("203.0.113.10")}}
	if got, err := d.resolve(ctx, "derp.example.com"); err != nil || len(got) != 1 || got[0].String() != "203.0.113.10" {
		t.Errorf("resolve = %v, %v", got, err)
	}

	h.policy = tailnet.Policy{Text: "{}", ETag: "e1"}
	if p, err := d.policy(ctx, h.ctlEnv("")); err != nil || p.ETag != "e1" {
		t.Errorf("policy = %+v, %v", p, err)
	}
	h.policyErr = boom
	if _, err := d.policy(ctx, h.ctlEnv("")); !errors.Is(err, boom) {
		t.Errorf("policy with policyErr = %v", err)
	}
	h.devices = []tailnet.Device{{ID: "n1", Name: "web"}}
	if got, err := d.devices(ctx, h.ctlEnv("")); err != nil || len(got) != 1 || got[0].ID != "n1" {
		t.Errorf("devices = %v, %v", got, err)
	}
	h.tailnet, h.link = true, true
	h.sockets = []netstat.Socket{{Proto: netstat.ProtoTCP, State: netstat.StateListen}}
	h.users[server.ServiceUser] = [2]int{5, 6}
	if v, err := d.connected(ctx, h.ctlDB); err != nil || !v {
		t.Errorf("connected = %v, %v", v, err)
	}
	if v, err := d.linkViaTailnet(); err != nil || !v {
		t.Errorf("linkViaTailnet = %v, %v", v, err)
	}
	if s, err := d.sockets(); err != nil || len(s) != 1 {
		t.Errorf("sockets = %v, %v", s, err)
	}
	if uid, gid, ok, err := d.lookupUser(server.ServiceUser); err != nil || !ok || uid != 5 || gid != 6 {
		t.Errorf("lookupUser = %d, %d, %v, %v", uid, gid, ok, err)
	}
	h.reads[server.CertDir+"/203.0.113.10.crt"] = []byte("CERT")
	if b, err := d.readFile(server.CertDir + "/203.0.113.10.crt"); err != nil || string(b) != "CERT" {
		t.Errorf("readFile = %q, %v", b, err)
	}
}

func TestSdHostRun(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	useradd := core.Cmd{Path: "/usr/sbin/useradd", Args: []string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", server.ServiceUser}}
	userdel := core.Cmd{Path: "/usr/sbin/userdel", Args: []string{server.ServiceUser}}

	if res, err := h.Run(ctx, useradd); err != nil || res.ExitCode != 0 {
		t.Fatalf("useradd = %+v, %v", res, err)
	}
	if ids := h.users[server.ServiceUser]; ids != [2]int{990, 990} {
		t.Errorf("useradd recorded %v, want uid and gid 990", ids)
	}
	if _, err := h.Run(ctx, userdel); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.users[server.ServiceUser]; ok {
		t.Error("userdel left the user")
	}

	h.script("/usr/sbin/useradd --system --no-create-home --shell /usr/sbin/nologin "+server.ServiceUser,
		dpRun{res: core.CmdResult{ExitCode: 9, Stderr: []byte("user exists")}}, dpRun{err: errors.New("boom")})
	if res, err := h.Run(ctx, useradd); err != nil || res.ExitCode != 9 {
		t.Errorf("scripted useradd = %+v, %v", res, err)
	}
	if _, err := h.Run(ctx, useradd); err == nil || err.Error() != "boom" {
		t.Errorf("second scripted useradd error = %v", err)
	}
	if _, ok := h.users[server.ServiceUser]; ok {
		t.Error("a failed useradd still added the user")
	}

	for _, cmd := range []core.Cmd{
		{Path: "/usr/bin/tailscale", Args: []string{"up"}},
		{Path: "/usr/bin/systemctl", Args: []string{"restart", server.UnitName}},
		{Path: server.BinaryPath, Args: []string{"-h"}},
		{Path: "/usr/sbin/useradd", Args: []string{"--system", "--no-create-home", "--shell", "/usr/bin/bash", server.ServiceUser}},
		{Path: "/usr/sbin/userdel", Args: []string{"root"}},
	} {
		if _, err := h.Run(ctx, cmd); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%v: error %v, want a refusal by the production allowlist", cmd, err)
		}
	}
	if _, err := h.Run(ctx, core.Cmd{Path: "/usr/sbin/useradd", Args: []string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "darkpanel"}}); err == nil {
		t.Error("the fake added the user darkpanel; only the module's user may be added")
	}
	if len(h.ran) != 10 || h.ran[0][0] != "/usr/sbin/useradd" || h.ran[1][0] != "/usr/sbin/userdel" {
		t.Errorf("ran %v", h.ran)
	}
}

func TestSdHostFiles(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()

	if err := h.WriteFile(ctx, ID, server.BinaryPath, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.WriteFile(ctx, ID, server.UnitPath, []byte("unit"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{server.BinaryPath: "bin", server.UnitPath: "unit"} {
		if string(h.files[path]) != want {
			t.Errorf("files[%s] = %q", path, h.files[path])
		}
		if b, err := h.dp().d.readFile(path); err != nil || string(b) != want {
			t.Errorf("readFile(%s) = %q, %v; deps.readFile must see what Files wrote", path, b, err)
		}
		if got, err := h.Owns(ctx, ID, path); err != nil || !got {
			t.Errorf("Owns(%s) = %v, %v", path, got, err)
		}
	}
	h.reads[server.BinaryPath] = []byte("seeded")
	if b, _ := h.dp().d.readFile(server.BinaryPath); string(b) != "seeded" {
		t.Errorf("readFile = %q; a seeded read wins over a written file", b)
	}
	if got, _ := h.Owns(ctx, ID, server.BinaryNewPath); got {
		t.Error("an unwritten path is owned")
	}
	if _, err := h.Owns(ctx, "docker", server.BinaryPath); err == nil {
		t.Error("Owns accepted another module")
	}

	refused := map[string]error{
		"another module":              h.WriteFile(ctx, "docker", server.BinaryPath, nil, 0o755),
		"an undeclared path":          h.WriteFile(ctx, ID, "/etc/passwd", nil, 0o644),
		"removing an undeclared unit": h.Remove(ctx, ID, "/etc/systemd/system/other.service"),
		"a service dir as a file":     h.WriteFile(ctx, ID, server.StateDir, nil, 0o644),
		"the user as a file":          h.WriteFile(ctx, ID, server.ServiceUser, nil, 0o644),
		"a managed file":              h.WriteManaged(ctx, ID, server.StateDir, "x", nil, 0o600, 0, 0),
		"a block":                     h.ReplaceBlock(ctx, ID, server.BinaryPath, "b", nil),
		"a restore":                   h.Restore(ctx, ID, core.RestoreEntry{Path: server.BinaryPath}),
	}
	for name, err := range refused {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := h.Remove(ctx, ID, server.UnitPath); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(ctx, ID, server.UnitPath); err != nil {
		t.Errorf("removing an absent unit = %v; a missing file is not an error", err)
	}
	if _, ok := h.files[server.UnitPath]; ok || h.owned[server.UnitPath] {
		t.Error("Remove left the unit file or its record")
	}
}

func TestSdHostRenames(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()

	h.files[server.BinaryPath] = []byte("old")
	h.files[server.BinaryNewPath] = []byte("new")
	if err := h.Rename(ctx, ID, server.BinaryPath, server.BinaryPrevPath); err != nil {
		t.Fatal(err)
	}
	if err := h.Rename(ctx, ID, server.BinaryNewPath, server.BinaryPath); err != nil {
		t.Fatal(err)
	}
	if string(h.files[server.BinaryPath]) != "new" || string(h.files[server.BinaryPrevPath]) != "old" || len(h.files[server.BinaryNewPath]) != 0 {
		t.Errorf("after the swap: %q, %q, %q", h.files[server.BinaryPath], h.files[server.BinaryPrevPath], h.files[server.BinaryNewPath])
	}
	if err := h.Rename(ctx, ID, server.BinaryPrevPath, server.BinaryPath); err != nil || string(h.files[server.BinaryPath]) != "old" {
		t.Errorf("renaming prev back over the binary = %v, %q; a rename replaces its target", err, h.files[server.BinaryPath])
	}
	if err := h.Rename(ctx, ID, server.BinaryNewPath, server.BinaryPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("renaming a missing file = %v, want fs.ErrNotExist", err)
	}
	h.files[server.UnitPath] = []byte("unit")
	if err := h.Rename(ctx, ID, server.UnitPath, server.BinaryPath); err == nil {
		t.Error("a unit path was renamed over a binary")
	}
	if err := h.Rename(ctx, ID, server.BinaryPath, "/usr/local/lib/darkpanel/other"); err == nil {
		t.Error("a rename to an undeclared path was accepted")
	}

	if err := h.EnsureServiceDir(ctx, ID, server.StateDir); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.Owns(ctx, ID, server.StateDir); !got {
		t.Error("EnsureServiceDir did not record the directory")
	}
	if err := h.EnsureServiceDir(ctx, ID, "/var/lib/darkpanel/other"); err == nil {
		t.Error("EnsureServiceDir accepted an undeclared directory")
	}

	crt := server.CertDir + "/203.0.113.10.crt"
	h.files[crt] = []byte("cert") // derper writes this, never Darkpanel
	if err := h.RenameIn(ctx, ID, server.StateDir, "certs/203.0.113.10.crt", "certs/203.0.113.10.crt.prev"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.files[crt]; ok || string(h.files[crt+".prev"]) != "cert" {
		t.Errorf("after RenameIn: %v", h.files)
	}
	if _, err := h.dp().d.readFile(crt); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readFile of the renamed certificate = %v, want fs.ErrNotExist", err)
	}
	for name, err := range map[string]error{
		"a missing source":     h.RenameIn(ctx, ID, server.StateDir, "certs/none.crt", "certs/none.crt.prev"),
		"a parent segment":     h.RenameIn(ctx, ID, server.StateDir, "certs/../derper.key", "certs/x"),
		"an undeclared subdir": h.RenameIn(ctx, ID, server.StateDir, "other/a.crt", "other/b.crt"),
		"another directory":    h.RenameIn(ctx, ID, "/var/lib/darkpanel/other", "a", "b"),
		"a name off pattern":   h.RenameIn(ctx, ID, server.StateDir, "certs/203.0.113.10.crt.prev", "certs/.hidden"),
	} {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := h.RemoveIn(ctx, ID, server.StateDir, "certs/203.0.113.10.crt.prev"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.files[crt+".prev"]; ok {
		t.Error("RemoveIn left the file")
	}

	h.files[server.KeyPath] = []byte("key")
	if err := h.RemoveServiceDir(ctx, ID, server.StateDir); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.files[server.KeyPath]; ok || h.owned[server.StateDir] {
		t.Errorf("RemoveServiceDir left %v and owned=%v", h.files, h.owned[server.StateDir])
	}
	if string(h.files[server.BinaryPath]) != "old" {
		t.Error("RemoveServiceDir removed a file outside the directory")
	}
}

func TestSdFakeLocal(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	api := h.dp().d.local()
	addr := netip.MustParseAddr("100.64.0.7")

	h.local.WhoIs = map[netip.Addr]*tslocal.WhoIs{addr: {Node: &tslocal.WhoIsNode{Name: "web.example.ts.net.", Tags: []string{"tag:server"}}}}
	if w, err := api.WhoIs(ctx, addr); err != nil || w.Node.Name != "web.example.ts.net." {
		t.Errorf("WhoIs = %+v, %v", w, err)
	}
	if _, err := api.WhoIs(ctx, netip.MustParseAddr("100.64.0.8")); !errors.Is(err, tslocal.ErrPeerNotFound) {
		t.Errorf("WhoIs of an unknown address = %v, want tslocal.ErrPeerNotFound", err)
	}
	h.local.Regions = map[string]*tslocal.DERPRegionReport{"fra-x": {Info: []string{"ok"}}}
	if r, err := api.DebugDERPRegion(ctx, "fra-x"); err != nil || len(r.Info) != 1 {
		t.Errorf("DebugDERPRegion = %+v, %v", r, err)
	}
	var apiErr *tslocal.APIError
	if _, err := api.DebugDERPRegion(ctx, "nope"); !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		t.Errorf("DebugDERPRegion of an unknown region = %v, want an APIError 404", err)
	}

	boom := errors.New("boom")
	h.local.Errs = map[string]error{"Status": boom, "WhoIs": boom, "DebugDERPRegion": boom}
	_, e1 := api.Status(ctx)
	_, e2 := api.WhoIs(ctx, addr)
	_, e3 := api.DebugDERPRegion(ctx, "fra-x")
	for i, err := range []error{e1, e2, e3} {
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

func TestSdFakeUnits(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	u := h.env("").Units

	if st, err := u.State(ctx, server.UnitName); err != nil || st.LoadState != "not-found" {
		t.Errorf("State before install = %+v, %v", st, err)
	}
	h.units.States[server.UnitName] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}
	if st, _ := u.State(ctx, server.UnitName); st.ActiveState != "active" {
		t.Errorf("State after a change = %+v", st)
	}
	for _, err := range []error{u.Reload(ctx), u.Enable(ctx, server.UnitName), u.Start(ctx, server.UnitName), u.Restart(ctx, server.UnitName),
		u.Stop(ctx, server.UnitName), u.Disable(ctx, server.UnitName)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"reload", "enable " + server.UnitName, "start " + server.UnitName, "restart " + server.UnitName, "stop " + server.UnitName, "disable " + server.UnitName}
	if !reflect.DeepEqual(h.units.Calls, want) {
		t.Errorf("Calls %v, want %v", h.units.Calls, want)
	}
	if st, _ := u.State(ctx, server.UnitName); st.ActiveState != "active" {
		t.Error("Stop changed the state: the fake keeps the state a test set")
	}
	for name, err := range map[string]error{
		"State of tailscaled":    func() error { _, err := u.State(ctx, "tailscaled.service"); return err }(),
		"Start of tailscaled":    u.Start(ctx, "tailscaled.service"),
		"Enable of another unit": u.Enable(ctx, "darkpanel-other.service"),
		"a transient service":    u.StartTransientService(ctx, "darkpanel-task-x-apply-1.service", core.TransientService{}),
		"Kill":                   u.Kill(ctx, server.UnitName, 15),
		"ResetFailed":            u.ResetFailed(ctx, server.UnitName),
	} {
		if err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
	if len(h.units.Calls) != len(want) {
		t.Errorf("a refused call was recorded: %v", h.units.Calls)
	}
}

func TestSdJournal(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()
	env := h.env("c1")
	seq, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "derp.user:" + server.ServiceUser}, []byte("secret"))
	if err != nil || seq != 1 {
		t.Fatalf("Intend = %d, %v", seq, err)
	}
	if err := env.Effects.Made(ctx, seq, "derp.user:"+server.ServiceUser, core.StateDoc{}); err != nil {
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

func TestSdControllerSide(t *testing.T) {
	h := newDPHost(t)
	ctx := context.Background()

	h.addHost("h1", "controller", true)
	h.addHost("h2", "web-1", false)
	rows, err := h.ctlDB.Read().Query(`SELECT id, name, is_local FROM core_hosts ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for rows.Next() {
		var id, name string
		var local int
		if err := rows.Scan(&id, &name, &local); err != nil {
			t.Fatal(err)
		}
		hosts = append(hosts, id+":"+name+":"+string(rune('0'+local))) //nolint:gosec // G115: is_local is 0 or 1
	}
	_ = rows.Close()
	if !reflect.DeepEqual(hosts, []string{"h1:controller:1", "h2:web-1:0"}) {
		t.Errorf("core_hosts %v", hosts)
	}

	first := h.addChange("derp.server.deploy", "completed", map[string]any{"hostId": "h2"})
	h.clock.Advance(time.Minute)
	second := h.addChange("derp.server.deploy", "failed", map[string]any{"hostId": "h2"})
	if first == "" || first == second {
		t.Fatalf("change ids %q and %q must be distinct and not empty", first, second)
	}
	var action, state, input, created string
	if err := h.ctlDB.Read().QueryRow(`SELECT c.action, c.state, c.input_json, c.created_at FROM core_changes c JOIN core_plans p ON p.id = c.plan_id WHERE c.id = ?`, first).
		Scan(&action, &state, &input, &created); err != nil {
		t.Fatal(err)
	}
	if action != "derp.server.deploy" || state != "completed" || input != `{"hostId":"h2"}` || created != "2026-11-02T12:00:00.000000000Z" {
		t.Errorf("change %s = %q %q %q %q", first, action, state, input, created)
	}
	var newest string
	if err := h.ctlDB.Read().QueryRow(`SELECT id FROM core_changes ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&newest); err != nil || newest != second {
		t.Errorf("newest change %q (%v), want %q", newest, err, second)
	}

	boom := errors.New("boom")
	ag := h.agent("h2")
	if h.agent("h2") != ag {
		t.Error("agent(h2) did not return the same fake twice")
	}
	ag.Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) {
		return ServerStatus{Installed: true, Mode: "ip", Tags: []string{}, Addresses: []string{}, Visible: []Visibility{}}, nil
	}
	ag.Reads["derp.node.probe/v1"] = func(json.RawMessage) (any, error) { return nil, boom }
	ag.Resources["tailscale.status"] = map[string]any{"preferredDerp": "fra-x"}

	env := h.ctlEnv("")
	client, err := env.Agents.Agent(ctx, "h2")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.Read(ctx, env.Principal, core.ActionRef{Name: "derp.server.status", Version: 1}, json.RawMessage(`{"whois":["100.64.0.7"]}`))
	var st ServerStatus
	if err != nil || json.Unmarshal(raw, &st) != nil || !st.Installed || st.Mode != "ip" {
		t.Fatalf("Read = %s, %v", raw, err)
	}
	if _, err := client.Read(ctx, env.Principal, core.ActionRef{Name: "derp.node.probe", Version: 1}, json.RawMessage(`{}`)); !errors.Is(err, boom) {
		t.Errorf("a failing read = %v, want the read's own error", err)
	}
	if _, err := client.Read(ctx, env.Principal, core.ActionRef{Name: "derp.unknown", Version: 1}, json.RawMessage(`{}`)); err == nil {
		t.Error("a read nobody set was answered")
	}
	wantCalls := []string{`derp.server.status/v1 {"whois":["100.64.0.7"]}`, `derp.node.probe/v1 {}`, `derp.unknown/v1 {}`}
	if !reflect.DeepEqual(ag.Calls, wantCalls) {
		t.Errorf("Calls %v, want %v", ag.Calls, wantCalls)
	}
	res, err := client.Resource(ctx, env.Principal, "tailscale.status")
	if err != nil || string(res) != `{"preferredDerp":"fra-x"}` {
		t.Errorf("Resource = %s, %v", res, err)
	}
	if _, err := client.Resource(ctx, env.Principal, "tailscale.latency"); err == nil {
		t.Error("a resource nobody set was answered")
	}
	var coded *errcode.Error
	if _, err := env.Agents.Agent(ctx, "h9"); !errors.As(err, &coded) || coded.Code != errcode.NotFound {
		t.Errorf("Agent of an unknown host = %v, want not_found", err)
	}
}
