package guard

import (
	"context"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// v1r19Path is an NPM exposure whose record names no public binding.
func v1r19Path() *core.ProxyPath {
	return &core.ProxyPath{ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
		ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}
}

func TestV1r19NPMExposureWithoutPublicBindingIsUnknown(t *testing.T) {
	in := Input{Plan: &core.GuardContext{Via: v1r19Path(), Complete: true}}
	got := buildFlows(in, facts{ifaces: gIfaces(), controller: 4242})
	want := []flow{
		{label: "exposure exp1 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.2", 0, "172.30.0.1", 8080, "tcp"))},
		{label: "exposure exp1 public", unknown: "the exposure has no public bindings"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flows\n%+v\nwant\n%+v", got, want)
	}
}

func TestV1r19CheckRefusesAnNPMExposureWithoutPublicBinding(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	in := Input{Plan: &core.GuardContext{Via: v1r19Path(), Complete: true}, Eval: ckEval(t, nil)}
	g, _, err := Checker{Host: &gHost{ifaces: ckIfaces()}}.Check(context.Background(), env, ckState(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := core.Guard{ID: GuardID, Message: "the change would cut exposure exp1 public: unknown (the exposure has no public bindings)"}
	if g != want {
		t.Errorf("guard = %+v, want %+v", g, want)
	}
}
