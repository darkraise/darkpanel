package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// ayMangle is a mangle table the evaluator cannot model: a rate match
// guarding a drop.
const ayMangle = "*mangle\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n-A PREROUTING -m recent --name scan --rcheck -j DROP\nCOMMIT\n"

// TestHostUnsupported: the reasons are the configured state's, the loaded
// state's and then the mangle and security rules the evaluator cannot
// model, each naming the tool, the variant and the rule.
func TestHostUnsupported(t *testing.T) {
	h := newFakeHost(t)
	st := h.loaded
	st.V4 = fhParse(t, fhSave+ayMangle)
	st.V6 = fhParse(t, fhSave+"*security\n:INPUT ACCEPT [0:0]\n-A INPUT -j SECMARK --selctx system_u:object_r:ssh_server_packet_t:s0\nCOMMIT\n")
	st.Unsupported = []string{"loaded reason"}
	got := hostUnsupported(fwstate.Config{Unsupported: []string{"configured reason"}}, st)
	want := []string{
		"configured reason", "loaded reason",
		"iptables-nft mangle/PREROUTING#1: match recent, which the evaluator cannot model",
		"ip6tables-nft security/INPUT#1: target SECMARK, which the evaluator cannot model",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reasons\n%q\nwant\n%q", got, want)
	}
	if got := hostUnsupported(fwstate.Config{}, h.loaded); got == nil || len(got) != 0 {
		t.Errorf("reasons of a supported host = %#v, want an empty list", got)
	}
}

// TestStatusUnmodelledMangle: a mangle rule the evaluator cannot model
// makes the status unsupported with the reason, and the host is not
// compared with its configuration.
func TestStatusUnmodelledMangle(t *testing.T) {
	h := newFakeHost(t)
	h.loaded.V4 = fhParse(t, fhSave+ayMangle)
	st := suStatus(t, h)
	want := []string{"iptables-nft mangle/PREROUTING#1: match recent, which the evaluator cannot model"}
	if st.Supported || !reflect.DeepEqual(st.Unsupported, want) || st.InSync || len(st.OutOfSync) != 0 {
		t.Errorf("status supported %v, reasons %q, in sync %v; want unsupported with %q", st.Supported, st.Unsupported, st.InSync, want)
	}
}

// TestPlanRefusesUnmodelledMangle: with such a rule loaded, or with a
// state the change would load, every mutating action is refused with
// firewall.unsupported_configuration, an override of the lockout guard
// included.
func TestPlanRefusesUnmodelledMangle(t *testing.T) {
	ctx := context.Background()
	const add = `{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"v4","comment":"web"}`
	for _, c := range []struct {
		name, raw string
		loaded    bool
		want      string
	}{
		{"loaded", add + `}`, true, "the host is an unsupported configuration: iptables-nft mangle/PREROUTING#1: match recent"},
		{"overridden", add + `,"overrides":["firewall.lockout"]}`, true, "the host is an unsupported configuration"},
		{"predicted", add + `}`, false, "the configuration the change leads to is unsupported: iptables-nft mangle/PREROUTING#1: match recent"},
	} {
		h, f, env := ruHost(t)
		if c.loaded {
			h.loaded.V4 = fhParse(t, fhSave+ayMangle)
		} else {
			pred := h.loaded
			pred.V4 = fhParse(t, fhSave+ayMangle)
			h.predicted = &pred
		}
		_, err := f.ruleAdd().Handler.Plan(ctx, env, json.RawMessage(c.raw))
		if e := errcode.From(err); e.Code != errcode.FirewallUnsupportedConfiguration || !strings.Contains(e.Detail, c.want) {
			t.Errorf("%s: plan = %v, want firewall.unsupported_configuration containing %q", c.name, err, c.want)
		}
	}
}
