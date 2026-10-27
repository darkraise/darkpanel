package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

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
