package firewall

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	if ok, detail, err := lockout(ctx, core.Env{}, st, nil); !ok || detail != answer.Message || err != nil || len(got) != 2 || got[1].Plan != nil {
		t.Errorf("lockout = %v, %q, %v, inputs %+v; want true, %q, nil and a second input with no plan", ok, detail, err, got, answer.Message)
	}

	answerErr = errors.New("firewall: read the socket table: netstat: no socket tables under the proc root")
	if ok, detail, err := lockout(ctx, core.Env{}, st, gc); ok || detail != "" || !errors.Is(err, answerErr) {
		t.Errorf("lockout = %v, %q, %v; want false, no detail and the check's error", ok, detail, err)
	}
}

// ayRunner is a core.Runner answered by a function.
type ayRunner func(ctx context.Context, cmd core.Cmd) (core.CmdResult, error)

func (r ayRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) { return r(ctx, cmd) }

// TestProductionReadLoaded: the production loaded-state reader is part C's:
// through env.Exec it asks iptables for its variant, then runs that
// variant's save tool for the first table, and a tool that fails is an
// error, not an empty firewall.
func TestProductionReadLoaded(t *testing.T) {
	failed := errors.New("aytool: the tool failed")
	var ran []string
	exec := ayRunner(func(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
		ran = append(ran, strings.Join(append([]string{cmd.Path}, cmd.Args...), " "))
		if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
			return core.CmdResult{Stdout: []byte("iptables v1.8.10 (nf_tables)\n")}, nil
		}
		return core.CmdResult{}, failed
	})
	_, err := productionFW().d.readLoaded(context.Background(), core.Env{Exec: exec})
	if !errors.Is(err, failed) {
		t.Errorf("readLoaded = %v, want the failing tool's error", err)
	}
	want := []string{"/usr/sbin/iptables --version", "/usr/sbin/iptables-nft-save -t filter"}
	if !reflect.DeepEqual(ran, want) {
		t.Errorf("ran %q, want %q", ran, want)
	}
}

// TestProductionLockout: the production lockout guard of a revert is
// guard.Check's: when the check cannot read the host (here it has neither
// a file writer nor a unit client) the guard answers the check's own error
// and not a verdict.
func TestProductionLockout(t *testing.T) {
	ctx := context.Background()
	st := fwstate.Loaded{Variant: "nft"}
	_, _, want := guard.Check(ctx, core.Env{}, st, guard.Input{Eval: eval.Evaluate})
	if want == nil {
		t.Fatal("guard.Check accepted an environment without host access")
	}
	ok, detail, err := productionFW().d.lockout(ctx, core.Env{}, st, nil)
	if ok || detail != "" || err == nil || err.Error() != want.Error() {
		t.Errorf("lockout = %v, %q, %v; want false, no detail and %q", ok, detail, err, want)
	}
}
