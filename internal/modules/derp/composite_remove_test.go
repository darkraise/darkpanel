package derp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

// cmHost is the DERP host of these tests; the agent-side test host's own id ("h1") is deliberately another.
const cmHost = "h7"

// cmSteps asks the composite for its steps the way the engine does, through the erased handler, on the controller's
// database.
func cmSteps(t *testing.T, d dp, h *dpHost) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(ServerRemoveInput{HostID: cmHost})
	if err != nil {
		t.Fatal(err)
	}
	return d.serverRemove().Handler.Steps(context.Background(), h.ctlEnv(""), raw)
}

func cmJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

// cmSeed records, in the controller's database, a server on cmHost whose region is regionID (0: not published).
func cmSeed(t *testing.T, h *dpHost, regionID int) {
	t.Helper()
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: cmHost, Mode: server.ModeIP, HostName: "203.0.113.9", RegionID: regionID, RegionCode: "dp-eu",
		DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cmWantStep fails the test unless s is the step id on host running action after the steps after.
func cmWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

// cmAction returns the declaration of action name, version 1, of module m.
func cmAction(t *testing.T, m core.Module, name string) core.ActionDef {
	t.Helper()
	for _, a := range m.Manifest().Actions {
		if a.Spec.Name == name && a.Spec.Version == 1 {
			return a
		}
	}
	t.Fatalf("%s is not declared", name)
	return core.ActionDef{}
}

func TestCMDeclaration(t *testing.T) {
	s := newDPHost(t).dp().serverRemove().Spec
	if s.Name != "derp.server.remove" || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
		s.Unwind != core.UnwindForward || !slices.Equal(s.LockKeys, []string{"composite:derp"}) {
		t.Errorf("spec %+v, want version 1, a summary, a controller-side composite, unwind forward, lock composite:derp", s)
	}
}

func TestCMSteps(t *testing.T) {
	tests := []struct {
		name      string
		seed      bool
		regionID  int
		connected bool
		ids       []string
	}{
		{"a published region on a connected tailnet is removed first", true, 905, true, []string{"edits", "region", "uninstall", "unpin", "ports"}},
		{"no region", true, 0, true, []string{"uninstall", "unpin", "ports"}},
		{"a tailnet that is not connected has no policy to edit", true, 905, false, []string{"uninstall", "unpin", "ports"}},
		{"a server the controller does not know is still removed", false, 0, true, []string{"uninstall", "unpin", "ports"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			h.tailnet = tc.connected
			cmServe(h, ServerStatus{Installed: true, Mode: server.ModeIP, HostName: "203.0.113.9"}, `{}`)
			if tc.seed {
				cmSeed(t, h, tc.regionID)
			}
			steps, err := cmSteps(t, h.dp(), h)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, s := range steps {
				ids = append(ids, s.ID)
			}
			if !slices.Equal(ids, tc.ids) {
				t.Fatalf("steps %v, want %v", ids, tc.ids)
			}
			var after []string
			if len(steps) == 5 {
				cmWantStep(t, steps[0], "edits", "", "derp.region.edits/v1", nil)
				cmWantStep(t, steps[1], "region", "", "tailnet.policy.patch/v1", []string{"edits"})
				if got, want := cmJSON(t, steps[0].Input), (map[string]any{"op": "remove", "regionId": float64(tc.regionID)}); !reflect.DeepEqual(got, want) {
					t.Errorf("edits input %s, want %v: the record's region", steps[0].Input, want)
				}
				if got, want := cmJSON(t, steps[1].Input), (map[string]any{"edits": "${steps.edits.output.edits}"}); !reflect.DeepEqual(got, want) {
					t.Errorf("region input %s, want %v", steps[1].Input, want)
				}
				after, steps = []string{"region"}, steps[2:]
			}
			cmWantStep(t, steps[0], "uninstall", cmHost, "derp.server.uninstall/v1", after)
			cmWantStep(t, steps[1], "unpin", cmHost, "tailscale.package.unpin/v1", []string{"uninstall"})
			cmWantStep(t, steps[2], "ports", cmHost, "firewall.rules.apply/v1", []string{"unpin"})
			for _, s := range steps[:2] {
				if got := cmJSON(t, s.Input); !reflect.DeepEqual(got, map[string]any{}) {
					t.Errorf("step %s input %s, want {}", s.ID, s.Input)
				}
			}
			want := map[string]any{"delete": []any{server.RuleID(cmHost, "80/tcp"), server.RuleID(cmHost, "443/tcp"), server.RuleID(cmHost, "3478/udp")}}
			if got := cmJSON(t, steps[2].Input); !reflect.DeepEqual(got, want) {
				t.Errorf("ports input %s, want %v: only the three rules of host %s", steps[2].Input, want, cmHost)
			}
		})
	}
}

// cmServe makes cmHost's agent report s and the tailnet policy read policy.
func cmServe(h *dpHost, s ServerStatus, policy string) {
	h.agent(cmHost).Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return s, nil }
	h.policy = tailnet.Policy{Text: policy}
}

// TestCMStepsWithoutARecord: for a host the controller has no record of yet, the removal asks the host's agent for
// its server and withdraws the policy's region whose node that server is; a host whose status cannot be read is
// refused rather than removed with its region left published, and without a connected tailnet nothing is asked.
func TestCMStepsWithoutARecord(t *testing.T) {
	const policy = `{"derpMap": {"Regions": {"905": {"RegionID": 905, "RegionCode": "dp-eu", "RegionName": "EU",
		"Nodes": [{"Name": "905a", "RegionID": 905, "HostName": "203.0.113.9"}]}}}}`
	installed := ServerStatus{Installed: true, Mode: server.ModeIP, HostName: "203.0.113.9"}
	for _, tc := range []struct {
		name      string
		status    *ServerStatus
		policy    string
		connected bool
		ids       []string
		region    float64
	}{
		{"the policy publishes the host's server", &installed, policy, true, []string{"edits", "region", "uninstall", "unpin", "ports"}, 905},
		{"the policy publishes another server", &installed, `{}`, true, []string{"uninstall", "unpin", "ports"}, 0},
		{"the host has no server", &ServerStatus{}, policy, true, []string{"uninstall", "unpin", "ports"}, 0},
		{"no connected tailnet", nil, policy, false, []string{"uninstall", "unpin", "ports"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			h.tailnet = tc.connected
			h.policy = tailnet.Policy{Text: tc.policy}
			if tc.status != nil {
				cmServe(h, *tc.status, tc.policy)
			}
			steps, err := cmSteps(t, h.dp(), h)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, s := range steps {
				ids = append(ids, s.ID)
			}
			if !slices.Equal(ids, tc.ids) {
				t.Fatalf("steps %v, want %v", ids, tc.ids)
			}
			if tc.region != 0 {
				if got, want := cmJSON(t, steps[0].Input), (map[string]any{"op": "remove", "regionId": tc.region}); !reflect.DeepEqual(got, want) {
					t.Errorf("edits input %s, want %v", steps[0].Input, want)
				}
			}
		})
	}
	t.Run("the host's status cannot be read", func(t *testing.T) {
		h := newDPHost(t)
		h.tailnet = true
		h.policy = tailnet.Policy{Text: policy}
		_, err := cmSteps(t, h.dp(), h)
		if coded := errcode.From(err); err == nil || coded.Code != errcode.DERPNotReady || !core.IsDefinite(err) || !strings.Contains(coded.Detail, cmHost) {
			t.Errorf("err = %#v, want a definite derp.not_ready naming %s", err, cmHost)
		}
	})
}

// TestCMConnectedIsAskedOnlyForARegion:a server with no region never needs the tailnet account, and an error while
// asking whether it is connected fails the steps rather than skipping the region.
func TestCMConnectedIsAskedOnlyForARegion(t *testing.T) {
	boom := errors.New("boom")
	asked := 0
	h := newDPHost(t)
	d := h.dp()
	d.d.connected = func(context.Context, *store.DB) (bool, error) { asked++; return false, boom }
	cmSeed(t, h, 0)
	if _, err := cmSteps(t, d, h); err != nil || asked != 0 {
		t.Errorf("no region: error %v after %d questions, want none and none", err, asked)
	}
	cmSeed(t, h, 905)
	if _, err := cmSteps(t, d, h); !errors.Is(err, boom) || asked != 1 {
		t.Errorf("a region: error %v after %d questions, want the connected error after one", err, asked)
	}
}

// TestCMStepsMatchTheActions: the composite is forward, so every non-read step's action converges, the one revertible
// step is the last, and an agent action has a host; each input is valid for its action's schema.
func TestCMStepsMatchTheActions(t *testing.T) {
	h := newDPHost(t)
	h.tailnet = true
	cmSeed(t, h, 905)
	steps, err := cmSteps(t, h.dp(), h)
	if err != nil {
		t.Fatal(err)
	}
	foreign := map[string]core.ActionDef{
		"tailnet.policy.patch":    cmAction(t, tailnet.Module(), "tailnet.policy.patch"),
		"tailscale.package.unpin": cmAction(t, tailscale.Module(), "tailscale.package.unpin"),
		"firewall.rules.apply":    cmAction(t, firewall.Module(), "firewall.rules.apply"),
	}
	validator := schema.NewValidator()
	for _, s := range steps {
		def, ok := foreign[s.Action.Name]
		if !ok {
			continue
		}
		switch {
		case !def.Spec.Converges:
			t.Errorf("step %s: %s does not converge, which a forward composite's steps must", s.ID, s.Action)
		case def.Spec.Side == core.SideController && s.Host != "", def.Spec.Side == core.SideAgent && s.Host == "":
			t.Errorf("step %s: %s is %s-side but the step names host %q", s.ID, s.Action, def.Spec.Side, s.Host)
		}
		input := json.RawMessage(strings.ReplaceAll(string(s.Input), `"${steps.edits.output.edits}"`, `[]`))
		if err := validator.ValidateInput(def, input); err != nil {
			t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
		}
	}
	if last := steps[len(steps)-1]; last.Action.Name != "firewall.rules.apply" || foreign["firewall.rules.apply"].Spec.Class != core.ClassRevertible {
		t.Errorf("the last step is %s, want the revertible firewall.rules.apply", last.Action)
	}
	dec := json.NewDecoder(bytes.NewReader(steps[0].Input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(new(RegionEditsInput)); err != nil {
		t.Errorf("edits input %s: %v", steps[0].Input, err)
	}
}

func TestCMOutput(t *testing.T) {
	h := newDPHost(t)
	def := h.dp().serverRemove()
	in := json.RawMessage(`{"hostId":"h7"}`)
	const grant = "the visibility grant of region 900's server stays in the policy: its tag could not be read"
	const removed = `["derp.unit:darkpanel-derper.service","derp.user:darkpanel-derp"]`
	const edits = `[{"op":"delete","path":"/derpMap/Regions/900"}]`
	tests := []struct {
		name      string
		uninstall string
		edits     string // the edits step's output; "" when the removal has no edits step
		want      string
	}{
		{"what was removed", `{"removed":` + removed + `}`, "", `{"removed":` + removed + `,"warnings":[]}`},
		{"nothing was installed", `{"removed":[]}`, "", `{"removed":[],"warnings":[]}`},
		{"no list at all is still a list", `{}`, "", `{"removed":[],"warnings":[]}`},
		{"the region was withdrawn cleanly", `{"removed":` + removed + `}`, `{"edits":` + edits + `,"warnings":[]}`,
			`{"removed":` + removed + `,"warnings":[]}`},
		{"the grant of the region's server stays in the policy", `{"removed":` + removed + `}`,
			`{"edits":` + edits + `,"warnings":["` + grant + `"]}`, `{"removed":` + removed + `,"warnings":["` + grant + `"]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps := map[string]json.RawMessage{"uninstall": json.RawMessage(tc.uninstall), "unpin": json.RawMessage(`{"version":"1.104.1","held":false}`), "ports": json.RawMessage(`{}`)}
			if tc.edits != "" {
				steps["edits"] = json.RawMessage(tc.edits)
				steps["region"] = json.RawMessage(`{"etag":"x"}`)
			}
			out, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, steps)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cmJSON(t, out), cmJSON(t, json.RawMessage(tc.want)); !reflect.DeepEqual(got, want) {
				t.Errorf("output %s, want %s", out, tc.want)
			}
		})
	}
	if _, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, map[string]json.RawMessage{"ports": json.RawMessage(`{}`)}); err == nil || !strings.Contains(err.Error(), "uninstall") {
		t.Errorf("output without the uninstall step: %v, want an error naming uninstall", err)
	}
}
