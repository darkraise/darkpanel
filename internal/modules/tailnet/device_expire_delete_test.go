package tailnet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

const (
	dxID     = "nlaptop1"
	dxName   = "laptop.example.ts.net"
	dxExpiry = "2099-01-01T00:00:00Z"
)

// dxRig is a connected account, a fake tailnet holding one device, and the module under test.
type dxRig struct {
	h   *tnHost
	srv *fake.Server
	t   tn
}

func dxNew(t *testing.T) dxRig {
	t.Helper()
	h := newTNHost(t)
	srv := fake.New(t)
	acConnect(t, h, srv)
	srv.AddDevice(fake.Device{
		ID: dxID, Name: dxName, Hostname: "laptop", Addresses: []string{"100.64.0.7"}, Authorized: true, Expires: dxExpiry,
	})
	return dxRig{h: h, srv: srv, t: tnWith(srv)}
}

func dxInput(device string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"device":%q}`, device))
}

func (r dxRig) plan(def core.ActionDef, device string) (core.PlanOutcome, error) {
	return def.Handler.Plan(context.Background(), r.h.env(""), dxInput(device))
}

func (r dxRig) apply(def core.ActionDef, changeID, device string) (DeviceRef, error) {
	raw, err := def.Handler.Apply(context.Background(), r.h.env(changeID), dxInput(device))
	var ref DeviceRef
	if err == nil {
		if err := json.Unmarshal(raw, &ref); err != nil {
			return DeviceRef{}, err
		}
	}
	return ref, err
}

func dxSameJSON(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %q is not JSON: %v", what, got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func dxCode(err error) errcode.Code {
	if err == nil {
		return ""
	}
	return errcode.From(err).Code
}

func TestDxDeclarations(t *testing.T) {
	r := dxNew(t)
	for _, tc := range []struct {
		def  core.ActionDef
		name string
	}{
		{r.t.deviceExpire(), "tailnet.device.expire"},
		{r.t.deviceDelete(), "tailnet.device.delete"},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideController ||
			!slices.Equal(s.LockKeys, []string{"tailnet"}) {
			t.Errorf("%s: spec %+v", tc.name, s)
		}
		if tc.def.InputType != reflect.TypeFor[DeviceInput]() || tc.def.OutputType != reflect.TypeFor[DeviceRef]() {
			t.Errorf("%s: input %v, output %v", tc.name, tc.def.InputType, tc.def.OutputType)
		}
		if tc.def.Handler.Observe == nil || tc.def.Handler.Inverse != nil {
			t.Errorf("%s: want an Observe handler and no Inverse handler", tc.name)
		}
	}
}

func TestDxPlan(t *testing.T) {
	r := dxNew(t)
	other := r.srv.AddDevice(fake.Device{ID: "nphone2", Name: "phone.example.ts.net", Authorized: true, Expires: dxExpiry})
	for _, tc := range []struct {
		name string
		def  core.ActionDef
		ops  string
	}{
		{"expire", r.t.deviceExpire(), "-+"},
		{"delete", r.t.deviceDelete(), "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := r.plan(tc.def, dxID)
			if err != nil {
				t.Fatal(err)
			}
			if out.Confirmation != dxName {
				t.Errorf("confirmation %q, want the device name %q", out.Confirmation, dxName)
			}
			var ops string
			for _, l := range out.Diff {
				ops += l.Op
				if !strings.Contains(l.Text, dxName) || !strings.Contains(l.Text, dxID) {
					t.Errorf("diff line %q names neither the device name nor its id", l.Text)
				}
			}
			if ops != tc.ops {
				t.Errorf("diff ops %q, want %q", ops, tc.ops)
			}
			again, err := r.plan(tc.def, dxID)
			if err != nil || again.StateHash == "" || again.StateHash != out.StateHash {
				t.Errorf("state hash %q then %q (err %v), want the same non-empty hash", out.StateHash, again.StateHash, err)
			}
			if o, err := r.plan(tc.def, other); err != nil || o.StateHash == out.StateHash {
				t.Errorf("another device: hash %q (err %v), want one different from %q", o.StateHash, err, out.StateHash)
			}
		})
	}
}

func TestDxPlanRefusals(t *testing.T) {
	r := dxNew(t)
	bare := newTNHost(t)
	for _, def := range []core.ActionDef{r.t.deviceExpire(), r.t.deviceDelete()} {
		for _, tc := range []struct {
			name   string
			env    core.Env
			device string
			want   errcode.Code
		}{
			{"empty device", r.h.env(""), "", errcode.InvalidInput},
			{"unknown device", r.h.env(""), "nmissing", errcode.NotFound},
			{"no account", bare.env(""), dxID, errcode.CloudNotConnected},
		} {
			_, err := def.Handler.Plan(context.Background(), tc.env, dxInput(tc.device))
			if got := dxCode(err); got != tc.want {
				t.Errorf("%s, %s: code %q (err %v), want %q", def.Spec.Name, tc.name, got, err, tc.want)
			}
		}
	}
}

func TestDxApplyExpire(t *testing.T) {
	r := dxNew(t)
	def := r.t.deviceExpire()
	before, err := r.plan(def, dxID)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.apply(def, "chg-expire", dxID)
	if err != nil {
		t.Fatal(err)
	}
	if ref != (DeviceRef{ID: dxID, Name: dxName}) {
		t.Errorf("output %+v", ref)
	}
	d, ok := r.srv.Device(dxID)
	if !ok || d.Expires == dxExpiry {
		t.Fatalf("provider device %+v (found %v), want its expiry changed from %s", d, ok, dxExpiry)
	}
	effs := r.h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("%d effects, want 1", len(effs))
	}
	e := effs[0]
	if e.Kind != core.EffectModify || e.Resource != "tailnet.device:"+dxID || e.State != core.EffectMade || e.Local ||
		e.CommitWait != cloud.CommitWait {
		t.Errorf("effect %+v", e)
	}
	dxSameJSON(t, "before", e.Before.State, `{"expires":"`+dxExpiry+`"}`)
	if e.Post == nil {
		t.Fatal("no post-state recorded")
	}
	dxSameJSON(t, "post", e.Post.State, `{"expires":"`+d.Expires+`"}`)
	if after, err := r.plan(def, dxID); err != nil || after.StateHash == before.StateHash {
		t.Errorf("plan after the expiry: hash %q (err %v), want one different from %q", after.StateHash, err, before.StateHash)
	}
}

func TestDxApplyDelete(t *testing.T) {
	r := dxNew(t)
	ref, err := r.apply(r.t.deviceDelete(), "chg-delete", dxID)
	if err != nil {
		t.Fatal(err)
	}
	if ref != (DeviceRef{ID: dxID, Name: dxName}) {
		t.Errorf("output %+v", ref)
	}
	if _, ok := r.srv.Device(dxID); ok {
		t.Error("the provider still has the device")
	}
	effs := r.h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("%d effects, want 1", len(effs))
	}
	e := effs[0]
	if e.Kind != core.EffectDelete || e.Resource != "tailnet.device:"+dxID || e.State != core.EffectMade || e.Local ||
		e.CommitWait != cloud.CommitWait {
		t.Errorf("effect %+v", e)
	}
	dxSameJSON(t, "before", e.Before.State, `{"id":"`+dxID+`","name":"`+dxName+`"}`)
	if e.After == nil || e.Post == nil {
		t.Fatalf("after %v, post %v, want both recorded", e.After, e.Post)
	}
	dxSameJSON(t, "after", e.After.State, `null`)
	dxSameJSON(t, "post", e.Post.State, `null`)
}

func TestDxRefusedWrite(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		remove             bool
	}{
		{"expire", http.MethodPost, "/api/v2/device/" + dxID + "/expire", false},
		{"delete", http.MethodDelete, "/api/v2/device/" + dxID, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := dxNew(t)
			def := r.t.deviceExpire()
			if tc.remove {
				def = r.t.deviceDelete()
			}
			r.srv.Cloud().Fail(tc.method, tc.path, cloudfake.Fault{Status: http.StatusForbidden})
			_, err := r.apply(def, "chg-refused", dxID)
			if dxCode(err) != errcode.CloudPermissionMissing || !core.IsDefinite(err) {
				t.Fatalf("error %v (definite %v), want a definite cloud.permission_missing", err, core.IsDefinite(err))
			}
			effs := r.h.journal.effects()
			if len(effs) != 1 || effs[0].State != core.EffectSkipped {
				t.Errorf("effects %+v, want one skipped effect", effs)
			}
			if d, ok := r.srv.Device(dxID); !ok || d.Expires != dxExpiry {
				t.Errorf("provider device %+v (found %v), want it untouched", d, ok)
			}
		})
	}
}

func TestDxLostResponse(t *testing.T) {
	t.Run("expire stays uncertain", func(t *testing.T) {
		r := dxNew(t)
		def := r.t.deviceExpire()
		r.srv.Cloud().Fail(http.MethodPost, "/api/v2/device/"+dxID+"/expire", cloudfake.Fault{LoseResponse: true})
		_, err := r.apply(def, "chg-lost", dxID)
		if dxCode(err) != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
			t.Fatalf("error %v (definite %v), want an indefinite cloud.uncertain_outcome", err, core.IsDefinite(err))
		}
		effs := r.h.journal.effects()
		if len(effs) != 1 || effs[0].State != core.EffectIntended {
			t.Fatalf("effects %+v, want one effect left intended", effs)
		}
		obs, err := def.Handler.Observe(context.Background(), r.h.env("chg-lost"), effs[0])
		if err != nil {
			t.Fatal(err)
		}
		d, _ := r.srv.Device(dxID)
		dxSameJSON(t, "observed", obs.Current.State, `{"expires":"`+d.Expires+`"}`)
		if d.Expires == dxExpiry {
			t.Error("the lost request should have committed")
		}
	})
	t.Run("delete converges", func(t *testing.T) {
		r := dxNew(t)
		r.srv.Cloud().Fail(http.MethodDelete, "/api/v2/device/"+dxID, cloudfake.Fault{LoseResponse: true})
		ref, err := r.apply(r.t.deviceDelete(), "chg-lost", dxID)
		if err != nil || ref.ID != dxID {
			t.Fatalf("ref %+v, err %v, want the delete to succeed once the retry finds the device gone", ref, err)
		}
		if effs := r.h.journal.effects(); len(effs) != 1 || effs[0].State != core.EffectMade {
			t.Errorf("effects %+v, want one made effect", effs)
		}
	})
}

func TestDxObserve(t *testing.T) {
	r := dxNew(t)
	for _, tc := range []struct {
		name string
		kind core.EffectKind
		id   string
		want string
	}{
		{"expire present", core.EffectModify, dxID, `{"expires":"` + dxExpiry + `"}`},
		{"delete present", core.EffectDelete, dxID, `{"id":"` + dxID + `","name":"` + dxName + `"}`},
		{"expire gone", core.EffectModify, "ngone", `null`},
		{"delete gone", core.EffectDelete, "ngone", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := r.t.deviceExpire()
			if tc.kind == core.EffectDelete {
				def = r.t.deviceDelete()
			}
			obs, err := def.Handler.Observe(context.Background(), r.h.env("chg-o"),
				core.Effect{ChangeID: "chg-o", Kind: tc.kind, Resource: "tailnet.device:" + tc.id})
			if err != nil {
				t.Fatal(err)
			}
			dxSameJSON(t, "state", obs.Current.State, tc.want)
		})
	}
	_, err := r.t.deviceExpire().Handler.Observe(context.Background(), newTNHost(t).env("chg-o"),
		core.Effect{ChangeID: "chg-o", Kind: core.EffectModify, Resource: "tailnet.device:" + dxID})
	if dxCode(err) != errcode.CloudNotConnected {
		t.Errorf("without an account: error %v, want cloud.not_connected", err)
	}
}

func TestDxVerify(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove bool
	}{{"expire", false}, {"delete", true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := dxNew(t)
			def := r.t.deviceExpire()
			if tc.remove {
				def = r.t.deviceDelete()
			}
			ctx := context.Background()
			if err := def.Handler.Verify(ctx, r.h.env("chg-v"), dxInput(dxID), json.RawMessage(`{}`)); err == nil {
				t.Error("verify passed with no recorded call")
			}
			if _, err := r.apply(def, "chg-v", dxID); err != nil {
				t.Fatal(err)
			}
			if err := def.Handler.Verify(ctx, r.h.env("chg-v"), dxInput(dxID), json.RawMessage(`{}`)); err != nil {
				t.Errorf("verify after the call: %v", err)
			}
		})
		t.Run(tc.name+" unchanged", func(t *testing.T) {
			r := dxNew(t)
			def, kind := r.t.deviceExpire(), core.EffectModify
			before := fmt.Sprintf(`{"expires":%q}`, dxExpiry)
			if tc.remove {
				def, kind = r.t.deviceDelete(), core.EffectDelete
				before = fmt.Sprintf(`{"id":%q,"name":%q}`, dxID, dxName)
			}
			ctx, env := context.Background(), r.h.env("chg-v")
			seq, err := env.Effects.Intend(ctx, cloudEffect(kind, "tailnet.device:"+dxID, json.RawMessage(before)), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := env.Effects.Made(ctx, seq, "tailnet.device:"+dxID, core.StateDoc{State: json.RawMessage(`null`)}); err != nil {
				t.Fatal(err)
			}
			if err := def.Handler.Verify(ctx, env, dxInput(dxID), json.RawMessage(`{}`)); err == nil {
				t.Error("verify passed although the provider still reports the recorded before-state")
			}
		})
	}
}
