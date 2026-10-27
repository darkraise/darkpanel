package firewall

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
)

func wrSameFunc(t *testing.T, name string, got, want any) {
	t.Helper()
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
		t.Errorf("%s is not the production function", name)
	}
}

// TestProductionFW: the module's production functions are the readers,
// predictor, comparison and guard of the firewall's packages, rooted at /.
func TestProductionFW(t *testing.T) {
	f := productionFW()
	if f.d.root != "/" {
		t.Errorf("root = %q, want /", f.d.root)
	}
	if f.d.readLoaded == nil || f.d.lockout == nil {
		t.Fatal("readLoaded or lockout is nil")
	}
	wrSameFunc(t, "readConfig", f.d.readConfig, conf.Read)
	wrSameFunc(t, "predict", f.d.predict, predict.Predict)
	wrSameFunc(t, "edit", f.d.edit, predict.Edit)
	wrSameFunc(t, "files", f.d.files, predict.Files)
	wrSameFunc(t, "compare", f.d.compare, loaded.Compare)
	wrSameFunc(t, "sysctl", f.d.sysctl, loaded.PredictSysctl)
	wrSameFunc(t, "ipv6Refusal", f.ipv6Refusal, predict.IPv6Refusal)
	wrSameFunc(t, "check", f.check, guard.Check)
}

// TestLockoutVia: the revert's guard evaluates the snapshot's guard
// context with the real evaluator and no apply-time refresh, and answers
// the guard's verdict and message; a check that fails is an error.
func TestLockoutVia(t *testing.T) {
	ctx := context.Background()
	gc := &core.GuardContext{Complete: true}
	st := fwstate.Loaded{Variant: "nft"}
	var got []guard.Input
	answer := core.Guard{ID: guard.GuardID, OK: false, Message: "the change would cut ssh from 203.0.113.7 to 192.0.2.1:22 on eth0: drop"}
	var answerErr error
	lockout := lockoutVia(func(_ context.Context, _ core.Env, s fwstate.Loaded, in guard.Input) (core.Guard, []guard.FlowResult, error) {
		if s.Variant != "nft" {
			t.Errorf("state %+v, want the state passed in", s)
		}
		got = append(got, in)
		return answer, nil, answerErr
	})

	ok, detail, err := lockout(ctx, core.Env{}, st, gc)
	if ok || detail != answer.Message || err != nil {
		t.Errorf("lockout = %v, %q, %v; want false, %q, nil", ok, detail, err, answer.Message)
	}
	if len(got) != 1 || got[0].Plan != gc || got[0].Refresh != nil || got[0].Retired != nil {
		t.Fatalf("inputs %+v, want one with Plan the snapshot's context and no refresh", got)
	}
	wrSameFunc(t, "Eval", got[0].Eval, eval.Evaluate)

	answer = core.Guard{ID: guard.GuardID, OK: true, Message: "every protected flow passes (3 checked)"}
	if ok, detail, err := lockout(ctx, core.Env{}, st, nil); !ok || detail != answer.Message || err != nil {
		t.Errorf("lockout = %v, %q, %v; want true, %q, nil", ok, detail, err, answer.Message)
	}

	answerErr = errors.New("firewall: read the socket table: netstat: no socket tables under the proc root")
	if ok, _, err := lockout(ctx, core.Env{}, st, gc); ok || !errors.Is(err, answerErr) {
		t.Errorf("lockout = %v, %v; want false and the check's error", ok, err)
	}
}
