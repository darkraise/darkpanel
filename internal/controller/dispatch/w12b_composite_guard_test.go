package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestW12bCompositeStepGuardCarriesTheRequester(t *testing.T) {
	d, seen, agent := newDg(t, func() Topology { return dgTopology })
	client := core.Peer{Addr: dgProxied.Client}
	ctx := core.WithProxied(core.WithClientPeer(context.Background(), client), dgProxied)
	kids := d.Children()
	call := &core.CallContext{ParentChangeID: "01COMPOSITE", ParentAction: "cfx.deploy.add", StepID: "a"}
	v, err := kids.Plan(ctx, alice, testHost, refDgAgent, json.RawMessage(`{"name":"a"}`), core.PlanOptions{Call: call})
	if err != nil {
		t.Fatal(err)
	}
	via := dgProxied.Path
	want := dgJSON(t, &core.GuardContext{Client: &client, Via: &via, PanelListen: dgTopology.PanelListen,
		AgentListen: dgTopology.AgentListen, AgentPeers: dgTopology.AgentPeers})
	if got := dgJSON(t, seen.guard(core.SideAgent)); got != want {
		t.Errorf("step plan guard %s, want %s", got, want)
	}
	if _, err := kids.Apply(ctx, alice, testHost, refDgAgent, v.PlanID, core.ApplyOptions{Call: call}); err != nil {
		t.Fatal(err)
	}
	if got := agent.lastOpts(); got.Call == nil || got.Call.ParentChangeID != call.ParentChangeID || dgJSON(t, got.Call.GuardRefresh) != want {
		t.Errorf("step apply call %+v, want the composite's call with the guard refresh %s", got.Call, want)
	}
}
