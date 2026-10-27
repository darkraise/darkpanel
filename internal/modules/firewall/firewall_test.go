package firewall

import (
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// mfUnits answers State for any unit and records the calls that reach it.
type mfUnits struct {
	core.Units
	asked []string
}

func (u *mfUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	u.asked = append(u.asked, unit)
	return core.UnitState{LoadState: "not-found"}, nil
}

func (u *mfUnits) Start(_ context.Context, unit string) error {
	u.asked = append(u.asked, "start "+unit)
	return nil
}

// TestManifestDeclaresTheHost: the module is registered, enabled by
// default, migrates its snapshot tables, runs its preflight, and declares
// UFW's state files and sysctl file (kind restore), the sysctl keys of
// spec 2.1 (kind sysctl) and the controller's unit (state only).
func TestManifestDeclaresTheHost(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("manifest %s v%d enabled %v, want firewall v1 enabled by default", m.ID, m.Version, m.DefaultEnabled)
	}
	if mig := m.Migrations[core.SideAgent]; mig == nil || len(m.Migrations) != 1 {
		t.Errorf("migrations %v, want the agent's only", m.Migrations)
	} else if _, err := fs.Stat(mig, "0001_firewall.sql"); err != nil {
		t.Errorf("agent migrations: %v", err)
	}
	var ids []string
	for _, c := range m.Preflight {
		ids = append(ids, c.ID)
	}
	if want := []string{"ufw", "ufw-version", "iptables-tools"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("preflight %v, want %v", ids, want)
	}
	want := []core.OwnedPath{
		{Path: "/etc/ufw/user.rules", Kind: core.OwnedRestore},
		{Path: "/etc/ufw/user6.rules", Kind: core.OwnedRestore},
		{Path: "/etc/ufw/before.rules", Kind: core.OwnedRestore},
		{Path: "/etc/ufw/before6.rules", Kind: core.OwnedRestore},
		{Path: "/etc/ufw/after.rules", Kind: core.OwnedRestore},
		{Path: "/etc/ufw/after6.rules", Kind: core.OwnedRestore},
		{Path: "/etc/ufw/ufw.conf", Kind: core.OwnedRestore},
		{Path: "/etc/default/ufw", Kind: core.OwnedRestore},
		{Path: "/etc/ufw/sysctl.conf", Kind: core.OwnedRestore},
		{Path: "net/ipv4/ip_forward", Kind: core.OwnedSysctl, Pattern: "^[01]$"},
		{Path: "net/ipv6/conf/<iface>/forwarding", Kind: core.OwnedSysctl, Pattern: "^[01]$"},
		{Path: "net/ipv6/conf/<iface>/accept_ra", Kind: core.OwnedSysctl, Pattern: "^[012]$"},
		{Path: "net/ipv4/conf/<iface>/rp_filter", Kind: core.OwnedSysctl, Pattern: "^[012]$"},
		{Path: "net/ipv4/conf/<iface>/accept_source_route", Kind: core.OwnedSysctl, Pattern: "^[01]$"},
	}
	if !reflect.DeepEqual(m.OwnedPaths, want) {
		t.Errorf("owned paths\n%+v\nwant\n%+v", m.OwnedPaths, want)
	}
	units := []core.ExternalUnit{{Name: "darkpanel-controller.service", Ops: []string{core.UnitOpState}}}
	if !reflect.DeepEqual(m.ExternalUnits, units) {
		t.Errorf("external units %+v, want %+v", m.ExternalUnits, units)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

// TestManifestScopesTheHostClients: the agent's file writer and unit
// client, built from the manifest, let the firewall read and write every
// supported sysctl key within its pattern, for all, default and any
// interface, and read the controller unit's state, and nothing more.
func TestManifestScopesTheHostClients(t *testing.T) {
	ctx := context.Background()
	m := Module().Manifest()
	procSys := t.TempDir()
	keys := map[string]string{
		"net/ipv4/ip_forward": "0", "net/ipv6/conf/all/forwarding": "0", "net/ipv6/conf/eth0.5/accept_ra": "1",
		"net/ipv4/conf/default/rp_filter": "2", "net/ipv4/conf/br-1a2b/accept_source_route": "0",
		"net/ipv4/conf/all/rp_filter": "1", "net/ipv4/conf/eth0/rp_filter": "0",
		"net/ipv4/conf/all/forwarding": "0",
	}
	for k, v := range keys {
		p := filepath.Join(procSys, k)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := platform.NewFiles(platform.FilesConfig{Owned: map[string][]core.OwnedPath{ID: m.OwnedPaths}, ProcSys: procSys, TestMode: true})
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		v := keys[k]
		got, err := files.ReadSysctl(ctx, ID, k)
		if k == "net/ipv4/conf/all/forwarding" {
			if err == nil {
				t.Errorf("ReadSysctl(%s) succeeds; the key is not declared", k)
			}
			continue
		}
		if err != nil || got != v {
			t.Errorf("ReadSysctl(%s) = %q, %v; want %q", k, got, err, v)
		}
	}
	if err := files.WriteSysctl(ctx, ID, "net/ipv6/conf/eth0.5/accept_ra", "2"); err != nil {
		t.Errorf("WriteSysctl accept_ra 2: %v", err)
	}
	for _, kv := range [][2]string{{"net/ipv4/ip_forward", "2"}, {"net/ipv4/conf/default/rp_filter", "3"}, {"net/ipv6/conf/all/forwarding", "2"}} {
		k, v := kv[0], kv[1]
		if err := files.WriteSysctl(ctx, ID, k, v); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Errorf("WriteSysctl(%s, %s) = %v, want a pattern refusal", k, v, err)
		}
	}

	u := &mfUnits{}
	scoped := platform.ScopeUnits(u, m)
	if _, err := scoped.State(ctx, "darkpanel-controller.service"); err != nil {
		t.Errorf("State of the controller unit: %v", err)
	}
	if err := scoped.Start(ctx, "darkpanel-controller.service"); err == nil {
		t.Error("Start of the controller unit is allowed")
	}
	if _, err := scoped.State(ctx, "darkpanel-agent.service"); err == nil {
		t.Error("State of the agent unit is allowed")
	}
	if !reflect.DeepEqual(u.asked, []string{"darkpanel-controller.service"}) {
		t.Errorf("calls reaching the unit client %v, want the controller's state only", u.asked)
	}
}
