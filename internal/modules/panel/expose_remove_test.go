package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/npm"
)

const (
	erExposureID = "01JD0000000000000000000010"
	erHostID     = "h1"
	erHostname   = "panel.example.com"
	erTunnel     = "0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"
	erRemovalID  = "01JD0000000000000000000011"
)

// erRow is the live exposure of the controller's host over via.
func erRow(via string) exposureRow {
	r := exposureRow{
		ID: erExposureID, Via: via, Hostname: erHostname, HostID: erHostID,
		Subnet: netip.MustParsePrefix("172.31.255.248/29"), Gateway: netip.MustParseAddr("172.31.255.249"),
		Proxy: netip.MustParseAddr("172.31.255.250"), Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(erExposureID),
		State: StateOpen, CreatedAt: fdNow, UpdatedAt: fdNow,
	}
	if via == ViaTunnel {
		r.Tunnel = erTunnel
	}
	return r
}

// erDB is a controller database holding the local host and rows.
func erDB(t *testing.T, rows ...exposureRow) *store.DB {
	t.Helper()
	db := fdDB(t)
	fdInsertHost(t, db, erHostID, true)
	for _, r := range rows {
		fdInsertExposure(t, db, r)
	}
	return db
}

// erSteps asks the removal for its steps the way the engine does, through the erased handler.
func erSteps(t *testing.T, db *store.DB) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(RemoveInput{})
	if err != nil {
		t.Fatal(err)
	}
	return exposureRemoveAction().Handler.Steps(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), raw)
}

// erStrict decodes raw into a T and refuses a member T does not have.
func erStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// erWant fails the test unless s is the step id on host running action after the steps after, retiring retires.
func erWant(t *testing.T, s core.Step, id, host, action string, after []string, retires string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) || s.Retires != retires {
		t.Errorf("step %+v, want %s: %s on host %q after %v retiring %q", s, id, action, host, after, retires)
	}
}

func erIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// erPorts decodes the input of the firewall step once its one reference, to the revoke step's rule id, is resolved.
func erPorts(t *testing.T, s core.Step, ruleID string) firewall.RulesApplyInput {
	t.Helper()
	const ref = "${steps.revoke.output.ruleId}"
	if strings.Count(string(s.Input), "${") != 1 || !strings.Contains(string(s.Input), ref) {
		t.Fatalf("ports input %s, want exactly one reference, to %s", s.Input, ref)
	}
	return erStrict[firewall.RulesApplyInput](t, json.RawMessage(strings.ReplaceAll(string(s.Input), ref, ruleID)))
}

func erCode(err error) errcode.Code {
	var e *errcode.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// TestErDeclaration: the removal is a controller-side forward composite under the panel's composite lock, for admins.
func TestErDeclaration(t *testing.T) {
	def := exposureRemoveAction()
	s := def.Spec
	if s.Name != ActionRemove || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
		s.Unwind != core.UnwindForward || !slices.Equal(s.LockKeys, []string{"composite:panel"}) ||
		s.MinScope != core.ScopeAdmin || core.RequiredScope(s) != core.ScopeAdmin {
		t.Errorf("spec %+v, want panel.exposure.remove v1 with a summary, a controller-side forward composite, lock composite:panel, scope admin", s)
	}
	if def.Handler.Steps == nil || def.Handler.Output == nil {
		t.Error("the handler lacks Steps or Output")
	}
	if def.InputType != reflect.TypeFor[RemoveInput]() || def.OutputType != reflect.TypeFor[RemoveOutput]() {
		t.Errorf("types %v and %v, want RemoveInput and RemoveOutput", def.InputType, def.OutputType)
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
}

// TestErSteps: the listener is revoked first and retires the exposure; the proxy host or hostname, the attachment, the
// network and the firewall rule follow in order, each on the exposure's host, with the inputs the other modules take.
func TestErSteps(t *testing.T) {
	for _, via := range []string{ViaNPM, ViaTunnel} {
		t.Run(via, func(t *testing.T) {
			steps, err := erSteps(t, erDB(t, erRow(via)))
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"revoke", "proxy", "attach", "network", "ports"}; !slices.Equal(erIDs(steps), want) {
				t.Fatalf("steps %v, want %v", erIDs(steps), want)
			}
			erWant(t, steps[0], "revoke", "", "panel.listener.revoke/v1", nil, erExposureID)
			if got := erStrict[ListenerRevokeInput](t, steps[0].Input); got != (ListenerRevokeInput{}) {
				t.Errorf("revoke input %+v, want none", got)
			}
			if via == ViaNPM {
				erWant(t, steps[1], "proxy", erHostID, "npm.proxyhost.remove/v1", []string{"revoke"}, "")
				if got := erStrict[npm.ProxyHostRemoveInput](t, steps[1].Input); got != (npm.ProxyHostRemoveInput{Domain: erHostname}) {
					t.Errorf("proxy input %+v, want the hostname as the domain and no id", got)
				}
				erWant(t, steps[2], "attach", erHostID, "npm.network.detach/v1", []string{"proxy"}, "")
				if got := erStrict[npm.DetachInput](t, steps[2].Input); got != (npm.DetachInput{Network: NetworkName, Claimant: Claimant}) {
					t.Errorf("attach input %+v, want the panel network and the panel claimant", got)
				}
			} else {
				erWant(t, steps[1], "proxy", "", "cloudflare.hostname.remove/v1", []string{"revoke"}, "")
				if got := erStrict[cloudflare.HostnameInput](t, steps[1].Input); got != (cloudflare.HostnameInput{Hostname: erHostname}) {
					t.Errorf("proxy input %+v, want the hostname", got)
				}
				erWant(t, steps[2], "attach", erHostID, "cloudflare.network.detach/v1", []string{"proxy"}, "")
				want := cloudflare.NetworkDetachInput{Tunnel: erTunnel, Network: NetworkName, Claimant: Claimant}
				if got := erStrict[cloudflare.NetworkDetachInput](t, steps[2].Input); got != want {
					t.Errorf("attach input %+v, want %+v", got, want)
				}
			}
			erWant(t, steps[3], "network", erHostID, "panel.network.remove/v1", []string{"attach"}, "")
			if got := erStrict[NetworkRemoveInput](t, steps[3].Input); got != (NetworkRemoveInput{}) {
				t.Errorf("network input %+v, want none", got)
			}
			erWant(t, steps[4], "ports", erHostID, "firewall.rules.apply/v1", []string{"network"}, "")
			want := firewall.RulesApplyInput{Label: "panel exposure " + erHostname, Delete: []string{RuleID(erExposureID)}}
			if got := erPorts(t, steps[4], RuleID(erExposureID)); !reflect.DeepEqual(got, want) {
				t.Errorf("ports input %+v, want %+v", got, want)
			}
			for _, s := range steps[:4] {
				if strings.Contains(string(s.Input), "${") {
					t.Errorf("step %s input %s holds a reference, want only the ports step to", s.ID, s.Input)
				}
			}
		})
	}
}

// TestErRemovableStates: an exposure whose listener the break-glass command revoked, one that a restore marked
// needs_repair and one whose removal has not completed are still live, so each plans the same removal.
func TestErRemovableStates(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *exposureRow)
		removal string // the state of the removal change, "" for none
	}{
		{"open", func(*exposureRow) {}, ""},
		{"revoked by the break-glass command", func(r *exposureRow) { r.State = StateRevoked }, ""},
		{"needs repair", func(r *exposureRow) { r.State = StateNeedsRepair }, ""},
		{"revoked by a removal that needs repair", func(r *exposureRow) { r.State = StateRevoked; r.RemovalChangeID = erRemovalID }, core.StateNeedsRepair},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := erRow(ViaNPM)
			tc.mutate(&row)
			db := erDB(t)
			if tc.removal != "" {
				fdInsertChange(t, db, erRemovalID, ActionRemove, tc.removal)
			}
			fdInsertExposure(t, db, row)
			steps, err := erSteps(t, db)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"revoke", "proxy", "attach", "network", "ports"}; !slices.Equal(erIDs(steps), want) || steps[0].Retires != erExposureID {
				t.Errorf("steps %v retiring %q, want %v retiring %s", erIDs(steps), steps[0].Retires, want, erExposureID)
			}
		})
	}
}

// TestErRefusals: with no live exposure the removal is refused with panel.not_exposed before any step is planned, and
// an exposure that a completed removal revoked is gone.
func TestErRefusals(t *testing.T) {
	gone := erRow(ViaNPM)
	gone.State, gone.RemovalChangeID = StateRevoked, erRemovalID
	tests := []struct {
		name string
		seed func(t *testing.T) *store.DB
	}{
		{"no exposure", func(t *testing.T) *store.DB { return erDB(t) }},
		{"a removed exposure", func(t *testing.T) *store.DB {
			db := erDB(t)
			fdInsertChange(t, db, erRemovalID, ActionRemove, core.StateCompleted)
			fdInsertExposure(t, db, gone)
			return db
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := erSteps(t, tc.seed(t))
			if erCode(err) != errcode.PanelNotExposed || !core.IsDefinite(err) || len(steps) != 0 {
				t.Errorf("steps %+v, error %v, want a definite %s refusal", steps, err, errcode.PanelNotExposed)
			}
		})
	}
}

// TestErPlansTheLiveExposure: a removed exposure of the past does not hide or replace the live one.
func TestErPlansTheLiveExposure(t *testing.T) {
	old := erRow(ViaNPM)
	old.ID, old.Hostname, old.State, old.RemovalChangeID = "01JD0000000000000000000001", "old.example.com", StateRevoked, erRemovalID
	db := erDB(t)
	fdInsertChange(t, db, erRemovalID, ActionRemove, core.StateCompleted)
	fdInsertExposure(t, db, old)
	fdInsertExposure(t, db, erRow(ViaTunnel))
	steps, err := erSteps(t, db)
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].Retires != erExposureID || steps[1].Action.Name != "cloudflare.hostname.remove" {
		t.Errorf("steps retire %q and remove through %s, want the live tunnel exposure %s", steps[0].Retires, steps[1].Action.Name, erExposureID)
	}
	if got := erStrict[cloudflare.HostnameInput](t, steps[1].Input); got.Hostname != erHostname {
		t.Errorf("proxy input %+v, want the live exposure's hostname %s", got, erHostname)
	}
}

// TestErLabelFitsTheFirewall: the firewall step's label is the rule comment, which the firewall limits to 64
// characters, so the label is cut to 64 characters, as the add composite cuts it, and the removal is still planned.
func TestErLabelFitsTheFirewall(t *testing.T) {
	comment := regexp.MustCompile(`^[A-Za-z0-9 :._/-]{0,64}$`)
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{"short", erHostname, "panel exposure " + erHostname},
		{"the longest that fits", strings.Repeat("a", 37) + ".example.com", "panel exposure " + strings.Repeat("a", 37) + ".example.com"},
		{"one more", strings.Repeat("a", 38) + ".example.com", ("panel exposure " + strings.Repeat("a", 38) + ".example.com")[:64]},
		{"long", strings.Repeat("a", 60) + ".example.com", ("panel exposure " + strings.Repeat("a", 60))[:64]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := erRow(ViaNPM)
			row.Hostname = tc.hostname
			steps, err := erSteps(t, erDB(t, row))
			if err != nil {
				t.Fatal(err)
			}
			got := erPorts(t, steps[4], RuleID(erExposureID)).Label
			if got != tc.want || !comment.MatchString(got) {
				t.Errorf("label %q, want %q, which the firewall accepts as a comment", got, tc.want)
			}
		})
	}
}

// TestErOutput: the output names the exposure the revoke step revoked and its hostname, which stays stored once the
// exposure is gone.
func TestErOutput(t *testing.T) {
	def := exposureRemoveAction()
	in, err := json.Marshal(RemoveInput{})
	if err != nil {
		t.Fatal(err)
	}
	revoke := func(id string) json.RawMessage {
		raw, err := json.Marshal(ListenerRevokeOutput{ExposureID: id, ListenerID: "l1", RuleID: RuleID(erExposureID), Revoked: true})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	gone := erRow(ViaNPM)
	gone.State, gone.RemovalChangeID = StateRevoked, erRemovalID
	tests := []struct {
		name    string
		removal string // the state of the removal change, "" for none
		row     exposureRow
		steps   map[string]json.RawMessage
		want    *RemoveOutput // nil: an error naming wantErr
		wantErr string
	}{
		{"an open exposure", "", erRow(ViaNPM), map[string]json.RawMessage{"revoke": revoke(erExposureID)},
			&RemoveOutput{ExposureID: erExposureID, Hostname: erHostname}, ""},
		{"a removed exposure", core.StateCompleted, gone, map[string]json.RawMessage{"revoke": revoke(erExposureID)},
			&RemoveOutput{ExposureID: erExposureID, Hostname: erHostname}, ""},
		{"no revoke step", "", erRow(ViaNPM), map[string]json.RawMessage{}, nil, "revoke"},
		{"a revoke step that names no exposure", "", erRow(ViaNPM), map[string]json.RawMessage{"revoke": revoke("")}, nil, "exposure"},
		{"an exposure that is not stored", "", erRow(ViaNPM), map[string]json.RawMessage{"revoke": revoke("01JD0000000000000000000099")}, nil, "01JD0000000000000000000099"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := erDB(t)
			if tc.removal != "" {
				fdInsertChange(t, db, erRemovalID, ActionRemove, tc.removal)
			}
			fdInsertExposure(t, db, tc.row)
			out, err := def.Handler.Output(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), in, tc.steps)
			if tc.want == nil {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("output %s, error %v, want an error naming %q", out, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := erStrict[RemoveOutput](t, out); got != *tc.want {
				t.Errorf("output %+v, want %+v", got, *tc.want)
			}
		})
	}
}
