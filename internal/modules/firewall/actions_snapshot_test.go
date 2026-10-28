package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// snCapture takes a snapshot of the fake host now, as an earlier change's
// Snapshot would, and returns its id.
func snCapture(t *testing.T, h *fakeHost, f fw) string {
	t.Helper()
	env := h.env()
	env.ChangeID = ""
	raw, err := capture(context.Background(), env, f.d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestSnapshotRestore: the plan lists what the restore changes, a manual
// rule it brings back needs manual: true, and the apply brings the
// snapshot's files, state and sysctl values back.
func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	id := snCapture(t, h, f)
	s := h.state()
	s.Rules4, s.Input = s.Rules4[:1], "ACCEPT"
	h.setState(s)
	h.sysctl["net/ipv4/ip_forward"] = "1"
	raw := `{"id":"` + id + `"}`

	_, err := f.snapshotRestore().Handler.Plan(ctx, env, json.RawMessage(raw))
	if errcode.From(err).Code != errcode.FirewallManualRequired {
		t.Errorf("plan without manual = %v, want manual required for the manual rule it adds", err)
	}
	raw = `{"id":"` + id + `","manual":true}`
	plan, out := ruApply(t, env, f.snapshotRestore(), raw)
	manual := model.Merge(h.config().Rules4, nil)[1]
	want := []string{
		"+ rule " + manual.ID + " (manual): allow in proto tcp from 10.0.0.0/8 to 0.0.0.0/0 port 3000",
		"- default incoming ACCEPT", "+ default incoming DROP",
		"- sysctl net/ipv4/ip_forward=1", "+ sysctl net/ipv4/ip_forward=0",
	}
	if !reflect.DeepEqual(acDiff(plan), want) {
		t.Errorf("diff\n%q\nwant\n%q", acDiff(plan), want)
	}
	if out.Snapshot == "" || out.Snapshot == id {
		t.Errorf("output snapshot %q, want the restore's own snapshot", out.Snapshot)
	}
	if got := h.state(); len(got.Rules4) != 2 || got.Input != "DROP" || h.sysctl["net/ipv4/ip_forward"] != "0" {
		t.Errorf("state %+v ip_forward %s, want the snapshot's", got, h.sysctl["net/ipv4/ip_forward"])
	}
}

// TestSnapshotRestoreMode: a state file whose bytes are the snapshot's but
// whose mode drifted is written back too.
func TestSnapshotRestoreMode(t *testing.T) {
	h, f, env := ruHost(t)
	id := snCapture(t, h, f)
	s := h.state()
	s.Input = "ACCEPT"
	h.setState(s)
	path := filepath.Join(h.root, "etc/ufw/user.rules")
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	ruApply(t, env, f.snapshotRestore(), `{"id":"`+id+`"}`)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("user.rules mode = %v after the restore, want the snapshot's 0600", fi.Mode().Perm())
	}
}

// TestSnapshotRestoreLogsAVanishedKey: a snapshot's sysctl key the kernel
// no longer has is skipped and logged.
func TestSnapshotRestoreLogsAVanishedKey(t *testing.T) {
	h, f, env := ruHost(t)
	id := snCapture(t, h, f)
	s := h.state()
	s.Input = "ACCEPT"
	h.setState(s)
	delete(h.sysctl, "net/ipv6/conf/eth0/accept_ra")
	var log bytes.Buffer
	env.Log = slog.New(slog.NewTextHandler(&log, nil))
	ruApply(t, env, f.snapshotRestore(), `{"id":"`+id+`"}`)
	if !strings.Contains(log.String(), "net/ipv6/conf/eth0/accept_ra") {
		t.Errorf("log %q, want the skipped key", log.String())
	}
}

// TestSnapshotRestoreRefuses: an unknown snapshot is invalid input; verify
// fails when UFW's state is not the snapshot's.
func TestSnapshotRestoreRefuses(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	_, err := f.snapshotRestore().Handler.Plan(ctx, env, json.RawMessage(`{"id":"01J000000000000000000000ZZ"}`))
	if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/id" {
		t.Errorf("plan of an unknown snapshot = %v, want an invalid input at /id", err)
	}
	id := snCapture(t, h, f)
	s := h.state()
	s.Enabled = false
	h.setState(s)
	err = f.snapshotRestore().Handler.Verify(ctx, env, json.RawMessage(`{"id":"`+id+`"}`), json.RawMessage(`{}`))
	if errcode.From(err).Code != errcode.FirewallPredictionMismatch {
		t.Errorf("verify with UFW disabled = %v, want a prediction mismatch", err)
	}
	if def := f.snapshotRestore(); def.Spec.Name != "firewall.snapshot.restore" || def.Spec.Class != core.ClassRevertible {
		t.Errorf("spec %+v", def.Spec)
	}
}

// TestSnapshotRestoreOldestRetained: the restore's own capture counts toward
// retention, so the oldest retained snapshot must survive its own restore.
func TestSnapshotRestoreOldestRetained(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	snCapture(t, h, f) // the host's first snapshot is the baseline, never pruned
	h.clock.Advance(time.Second)
	id := snCapture(t, h, f)
	for i := 1; i < keepSnapshots; i++ {
		h.clock.Advance(time.Second)
		snCapture(t, h, f)
	}
	h.clock.Advance(time.Second)
	if ids := stTestIDs(t, h.db); len(ids) != keepSnapshots+1 || !ids[id] {
		t.Fatalf("%d snapshots before the restore, want the baseline and %d retained including %s", len(ids), keepSnapshots, id)
	}
	s := h.state()
	s.Rules4, s.Input = s.Rules4[:1], "ACCEPT"
	h.setState(s)
	ruApply(t, env, f.snapshotRestore(), `{"id":"`+id+`","manual":true}`)
	got, err := getSnapshot(ctx, h.db, id)
	if err != nil || got.Pinned {
		t.Errorf("target after the restore: pinned %v, %v; want kept and unpinned", got.Pinned, err)
	}
	if h.state().Input != "DROP" {
		t.Errorf("state %+v, want the snapshot's", h.state())
	}
}
