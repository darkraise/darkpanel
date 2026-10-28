package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// drsStateA is the Docker state a snapshot captures: the integration on,
// custom private sources and an exemption, and three rows.
func drsStateA() (dockerConfig, []dockerfw.ContainerRule) {
	web := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	cfg := dockerConfig{Enabled: true, Settings: dockerfw.Settings{
		PrivateSources: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10")},
		ForwardExemptions: []dockerfw.Exemption{{
			OrigDestination: netip.MustParseAddr("192.0.2.10"), OrigPort: 2222,
			Destination: netip.MustParseAddr("10.9.0.5"), Proto: "tcp", Port: 22,
		}},
	}}
	return cfg, []dockerfw.ContainerRule{
		{ID: "01J00000000000000000000010", Binding: web, Kind: dockerfw.Allow},
		{ID: "01J00000000000000000000011", Kind: dockerfw.Allow, From: netip.MustParsePrefix("198.51.100.0/24"),
			Binding: dockerfw.Binding{Selector: "shop/api", HostAddr: netip.MustParseAddr("192.0.2.10"), HostPort: 8443, Proto: "tcp"}},
		{ID: "01J00000000000000000000012", Binding: web, Kind: dockerfw.Deny, From: netip.MustParsePrefix("203.0.113.0/24")},
	}
}

// drsStateB is the state the host holds when the snapshot is restored: the
// integration off, the default settings and one row the snapshot lacks.
func drsStateB() (dockerConfig, []dockerfw.ContainerRule) {
	return dockerConfig{Settings: dockerfw.DefaultSettings()}, []dockerfw.ContainerRule{
		{ID: "01J00000000000000000000020", Kind: dockerfw.Allow, Binding: dockerfw.Binding{Selector: "db", HostPort: 5432, Proto: "tcp"}},
	}
}

func drsSeed(t *testing.T, h *fakeHost, cfg dockerConfig, rows []dockerfw.ContainerRule) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := writeDockerConfig(ctx, tx, cfg); err != nil {
			return err
		}
		return replaceContainerRules(ctx, tx, rows, h.clock.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// drsWant fails the test unless the host's flag, settings and rows are those.
func drsWant(t *testing.T, h *fakeHost, cfg dockerConfig, rows []dockerfw.ContainerRule) {
	t.Helper()
	ctx := context.Background()
	gotCfg, err := readDockerConfig(ctx, h.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotCfg, cfg) {
		t.Errorf("flag and settings = %+v, want %+v", gotCfg, cfg)
	}
	gotRows, err := listContainerRules(ctx, h.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotRows) != len(rows) || (len(rows) > 0 && !reflect.DeepEqual(gotRows, rows)) {
		t.Errorf("rows = %+v, want %+v", gotRows, rows)
	}
}

// drsFailingFiles is the host's file writer with Restore failing on demand.
type drsFailingFiles struct {
	*fakeHost
	fail bool
}

func (f *drsFailingFiles) Restore(ctx context.Context, module string, e core.RestoreEntry) error {
	if f.fail {
		return errors.New("disk full")
	}
	return f.fakeHost.Restore(ctx, module, e)
}

func drsRestoreHost(t *testing.T) (*fakeHost, snapshot) {
	t.Helper()
	h := newFakeHost(t)
	cfgA, rowsA := drsStateA()
	drsSeed(t, h, cfgA, rowsA)
	snap := rsTestSnap(t, h, false)
	cfgB, rowsB := drsStateB()
	drsSeed(t, h, cfgB, rowsB)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	return h, snap
}

func TestDrsRestoreBringsBackTheDockerState(t *testing.T) {
	ctx := context.Background()
	h, snap := drsRestoreHost(t)
	if _, err := restore(ctx, h.env(), h.deps(), snap); err != nil {
		t.Fatal(err)
	}
	cfgA, rowsA := drsStateA()
	drsWant(t, h, cfgA, rowsA)
	if _, ok, _ := pendingRestore(ctx, h.db); ok {
		t.Error("a finished restore left its record")
	}
}

func TestDrsRestoreOfASnapshotFromBeforeTheIntegration(t *testing.T) {
	ctx := context.Background()
	h, snap := drsRestoreHost(t)
	snap.State.Docker, snap.State.DockerSettings, snap.State.ContainerRules = false, nil, nil
	cfgB, rowsB := drsStateB()
	cfgOn := dockerConfig{Enabled: true, Settings: cfgB.Settings}
	drsSeed(t, h, cfgOn, rowsB)
	if _, err := restore(ctx, h.env(), h.deps(), snap); err != nil {
		t.Fatal(err)
	}
	drsWant(t, h, dockerConfig{Settings: dockerfw.DefaultSettings()}, nil)
}

func TestDrsRowsAndRecordShareOneTransaction(t *testing.T) {
	ctx := context.Background()
	h, snap := drsRestoreHost(t)
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DROP TABLE firewall_container_rules")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h.calls = nil
	if _, err := restore(ctx, h.env(), h.deps(), snap); err == nil {
		t.Fatal("restore succeeded without a table for the container rows")
	}
	if _, ok, _ := pendingRestore(ctx, h.db); ok {
		t.Error("a restore that could not write the rows left its record")
	}
	if slices.ContainsFunc(h.calls, func(c string) bool { return strings.HasPrefix(c, "restore ") || strings.HasPrefix(c, "run ") }) {
		t.Errorf("a restore that could not write the rows went on: %v", h.calls)
	}
}

func TestDrsRowsAreWrittenBeforeTheFilesAndAgainOnRetry(t *testing.T) {
	ctx := context.Background()
	h, snap := drsRestoreHost(t)
	env := h.env()
	files := &drsFailingFiles{fakeHost: h, fail: true}
	env.Files = files
	if _, err := restore(ctx, env, h.deps(), snap); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("restore = %v, want the file failure", err)
	}
	cfgA, rowsA := drsStateA()
	drsWant(t, h, cfgA, rowsA)
	if _, ok, _ := pendingRestore(ctx, h.db); !ok {
		t.Error("a cut-off restore has no record")
	}
	cfgB, rowsB := drsStateB()
	drsSeed(t, h, cfgB, rowsB)
	files.fail = false
	if _, err := restore(ctx, env, h.deps(), snap); err != nil {
		t.Fatal(err)
	}
	drsWant(t, h, cfgA, rowsA)
	if _, ok, _ := pendingRestore(ctx, h.db); ok {
		t.Error("the finished retry left its record")
	}
}

func TestDrsRecordedRestoreWritesTheRows(t *testing.T) {
	for name, emergency := range map[string]json.RawMessage{"normal": nil, "emergency": json.RawMessage(`{}`)} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			h, snap := drsRestoreHost(t)
			if err := beginRestore(ctx, h.db, snap.ID, fwstate.TransReload, h.clock.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := restoreOver(ctx, h.env(), h.deps(), snap, emergency); err != nil {
				t.Fatal(err)
			}
			cfgA, rowsA := drsStateA()
			drsWant(t, h, cfgA, rowsA)
		})
	}
}
