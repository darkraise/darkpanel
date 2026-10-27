package firewall

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
)

// syLoaded parses one filter table, given as -A lines after the chain
// declarations, for both families.
func syLoaded(t *testing.T, decls string, rules ...string) fwstate.Loaded {
	t.Helper()
	text := "*filter\n" + decls + strings.Join(rules, "\n") + "\nCOMMIT\n"
	return fwstate.Loaded{Variant: "nft", V4: fhParse(t, text), V6: fhParse(t, text)}
}

const syDecls = ":INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n:fw-foreign - [0:0]\n"

// TestSyncDiffs: an enabled UFW is compared with a reload's prediction over
// UFW's and Darkpanel's chains and the filter built-ins; a disabled one with
// a stop's over UFW's chains only, so a built-in policy set while UFW is off
// does not count.
func TestSyncDiffs(t *testing.T) {
	cur := syLoaded(t, syDecls, "-A INPUT -j ufw-user-input", "-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT")
	pred := syLoaded(t, strings.Replace(syDecls, ":INPUT DROP", ":INPUT ACCEPT", 1),
		"-A INPUT -j ufw-user-input", "-A ufw-user-input -p tcp -m tcp --dport 80 -j ACCEPT",
		"-A fw-foreign -j DROP")
	var trs []fwstate.Transition
	f := fw{d: deps{
		predict: func(_ fwstate.Config, c fwstate.Loaded, tr fwstate.Transition) (fwstate.Loaded, error) {
			if c.V4 != cur.V4 {
				t.Error("predict is not given the loaded state")
			}
			trs = append(trs, tr)
			return pred, nil
		},
		compare: loaded.Compare,
	}}

	diffs, err := f.syncDiffs(fwstate.Config{Enabled: true}, cur)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range diffs {
		got = append(got, chainDiffText(d))
	}
	want := []string{
		"v4 filter/INPUT: policy ACCEPT predicted, DROP loaded",
		"v4 filter/ufw-user-input: 1 rule only predicted, 1 only loaded",
		"v6 filter/INPUT: policy ACCEPT predicted, DROP loaded",
		"v6 filter/ufw-user-input: 1 rule only predicted, 1 only loaded",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("enabled diffs\n%q\nwant\n%q", got, want)
	}

	diffs, err = f.syncDiffs(fwstate.Config{Enabled: false}, cur)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, d := range diffs {
		got = append(got, string(d.Family)+" "+d.Chain)
	}
	if want := []string{"v4 ufw-user-input", "v6 ufw-user-input"}; !reflect.DeepEqual(got, want) {
		t.Errorf("disabled diffs %q, want %q", got, want)
	}
	if want := []fwstate.Transition{fwstate.TransReload, fwstate.TransDisable}; !reflect.DeepEqual(trs, want) {
		t.Errorf("transitions %v, want %v", trs, want)
	}

	f.d.predict = func(fwstate.Config, fwstate.Loaded, fwstate.Transition) (fwstate.Loaded, error) {
		return fwstate.Loaded{}, errors.New("predict: translate: /etc/ufw/user.rules is empty")
	}
	if _, err := f.syncDiffs(fwstate.Config{Enabled: true}, cur); err == nil || !strings.Contains(err.Error(), "user.rules is empty") {
		t.Errorf("syncDiffs = %v, want the prediction's error", err)
	}
}

// TestConfined: a difference is confined to UFW's and Darkpanel's chains
// when it lies in such a chain, or in a filter built-in chain whose other
// rules are the same in order; a foreign chain, a foreign rule of a
// built-in chain, or a built-in chain of another table is not.
func TestConfined(t *testing.T) {
	base := []string{"-A INPUT -s 198.51.100.7/32 -j DROP", "-A INPUT -j ufw-user-input", "-A ufw-user-input -j ACCEPT"}
	cases := []struct {
		name  string
		cur   []string
		decls string
		ok    bool
	}{
		{"in sync", base, syDecls, true},
		{"ufw chain", []string{base[0], base[1]}, syDecls, true},
		{"jump moved", []string{base[1], base[0], base[2]}, syDecls, true},
		{"jump missing and policy", []string{base[0], base[2]}, strings.Replace(syDecls, ":INPUT DROP", ":INPUT ACCEPT", 1), true},
		{"foreign rule added", append([]string{"-A INPUT -p tcp -m tcp --dport 9000 -j ACCEPT"}, base...), syDecls, false},
		{"foreign rule removed", base[1:], syDecls, false},
		{"foreign matched jump", append([]string{"-A INPUT -s 203.0.113.9/32 -j ufw-user-input"}, base...), syDecls, false},
		{"foreign chain", append(append([]string(nil), base...), "-A fw-foreign -j DROP"), syDecls, false},
	}
	pred := syLoaded(t, syDecls, base...)
	for _, c := range cases {
		cur := syLoaded(t, c.decls, c.cur...)
		diffs := loaded.Compare(pred, cur, nil)
		if got := confined(diffs, pred, cur); got != c.ok {
			t.Errorf("%s: confined = %v, want %v (diffs %+v)", c.name, got, c.ok, diffs)
		}
	}
	natPred := fwstate.Loaded{V4: fhParse(t, "*nat\n:POSTROUTING ACCEPT [0:0]\nCOMMIT\n")}
	natCur := fwstate.Loaded{V4: fhParse(t, "*nat\n:POSTROUTING ACCEPT [0:0]\n-A POSTROUTING -j MASQUERADE\nCOMMIT\n")}
	if confined(loaded.Compare(natPred, natCur, nil), natPred, natCur) {
		t.Error("a nat built-in chain counts as UFW's")
	}
}

// TestChainDifferences: the JSON form keeps every field of a ChainDiff.
func TestChainDifferences(t *testing.T) {
	got := chainDifferences([]fwstate.ChainDiff{{Family: fwstate.V6, Table: "filter", Chain: "INPUT", PolicyA: "DROP", PolicyB: "ACCEPT",
		OnlyA: []string{"-j ufw6-before-input"}, OnlyB: []string{"-j ACCEPT"}, OrderDiffers: true}})
	want := []ChainDifference{{Family: "v6", Table: "filter", Chain: "INPUT", PolicyPredicted: "DROP", PolicyLoaded: "ACCEPT",
		OnlyPredicted: []string{"-j ufw6-before-input"}, OnlyLoaded: []string{"-j ACCEPT"}, OrderDiffers: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chainDifferences = %+v, want %+v", got, want)
	}
	if chainDifferences(nil) == nil {
		t.Error("chainDifferences(nil) is nil, want an empty list for JSON")
	}
	if s := chainDiffText(fwstate.ChainDiff{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyA: []string{"a", "b"}, OrderDiffers: true}); s != "v4 filter/ufw-user-input: 2 rules only predicted, order differs" {
		t.Errorf("chainDiffText = %q", s)
	}
}

// ayMasq is the rule a before.rules nat block adds on every start.
const ayMasq = "-A POSTROUTING -s 10.8.0.0/24 -o eth0 -j MASQUERADE"

// ayNat is a loaded state with a nat table whose POSTROUTING holds rules.
func ayNat(t *testing.T, policy string, rules ...string) fwstate.Loaded {
	t.Helper()
	text := "*nat\n:PREROUTING ACCEPT [0:0]\n:POSTROUTING " + policy + " [0:0]\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
	return fwstate.Loaded{Variant: "nft", V4: fhParse(t, text)}
}

// TestVerifyDiffs: after a start, verify compares UFW's and Darkpanel's
// chains and every table's built-in chains with the prediction of a reload.
// A rule a before.rules block appends to a built-in chain of a table other
// than filter is appended again by the reload the prediction models, so it
// counts only when the loaded chain does not hold it; a foreign chain, which
// no start writes, is never compared; a disabled UFW is compared as in
// syncDiffs.
func TestVerifyDiffs(t *testing.T) {
	var trs []fwstate.Transition
	withPred := func(pred fwstate.Loaded) fw {
		return fw{d: deps{
			predict: func(_ fwstate.Config, _ fwstate.Loaded, tr fwstate.Transition) (fwstate.Loaded, error) {
				trs = append(trs, tr)
				return pred, nil
			},
			compare: loaded.Compare,
		}}
	}
	texts := func(diffs []fwstate.ChainDiff) []string {
		var out []string
		for _, d := range diffs {
			out = append(out, chainDiffText(d))
		}
		return out
	}
	enabled := fwstate.Config{Enabled: true}

	cases := []struct {
		name      string
		cur, pred fwstate.Loaded
		want      []string
	}{
		{"the start's nat rule is loaded", ayNat(t, "ACCEPT", ayMasq), ayNat(t, "ACCEPT", ayMasq, ayMasq), nil},
		{"the start's nat rule is loaded among foreign ones", ayNat(t, "ACCEPT", ayMasq, "-A POSTROUTING -j LOG"),
			ayNat(t, "ACCEPT", ayMasq, "-A POSTROUTING -j LOG", ayMasq), nil},
		{"the kernel did not apply the nat rule", ayNat(t, "ACCEPT"), ayNat(t, "ACCEPT", ayMasq),
			[]string{"v4 nat/POSTROUTING: 1 rule only predicted"}},
		{"a nat built-in policy differs", ayNat(t, "DROP", ayMasq), ayNat(t, "ACCEPT", ayMasq, ayMasq),
			[]string{"v4 nat/POSTROUTING: policy ACCEPT predicted, DROP loaded, 1 rule only predicted"}},
		{"a rule only loaded", ayNat(t, "ACCEPT", ayMasq, "-A POSTROUTING -j LOG"), ayNat(t, "ACCEPT", ayMasq),
			[]string{"v4 nat/POSTROUTING: 1 rule only loaded"}},
		{"a foreign user chain is not compared",
			fwstate.Loaded{V4: fhParse(t, "*nat\n:POSTROUTING ACCEPT [0:0]\n:fw-foreign - [0:0]\nCOMMIT\n")},
			fwstate.Loaded{V4: fhParse(t, "*nat\n:POSTROUTING ACCEPT [0:0]\n:fw-foreign - [0:0]\n-A fw-foreign -j DROP\nCOMMIT\n")}, nil},
		{"a filter built-in is still compared exactly",
			syLoaded(t, syDecls, "-A INPUT -j ufw-user-input"),
			syLoaded(t, syDecls, "-A INPUT -j ufw-user-input", "-A INPUT -j ufw-user-input"),
			[]string{"v4 filter/INPUT: 1 rule only predicted", "v6 filter/INPUT: 1 rule only predicted"}},
	}
	for _, c := range cases {
		diffs, err := withPred(c.pred).verifyDiffs(enabled, c.cur)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := texts(diffs); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: diffs %q, want %q", c.name, got, c.want)
		}
	}
	if len(trs) != len(cases) || trs[0] != fwstate.TransReload {
		t.Errorf("transitions %v, want a reload predicted for each", trs)
	}

	cur := syLoaded(t, syDecls, "-A INPUT -j ufw-user-input", "-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT")
	pred := syLoaded(t, strings.Replace(syDecls, ":INPUT DROP", ":INPUT ACCEPT", 1),
		"-A INPUT -j ufw-user-input", "-A ufw-user-input -p tcp -m tcp --dport 80 -j ACCEPT")
	trs = nil
	diffs, err := withPred(pred).verifyDiffs(fwstate.Config{Enabled: false}, cur)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"v4 filter/ufw-user-input: 1 rule only predicted, 1 only loaded", "v6 filter/ufw-user-input: 1 rule only predicted, 1 only loaded"}; !reflect.DeepEqual(texts(diffs), want) {
		t.Errorf("disabled diffs %q, want %q", texts(diffs), want)
	}
	if want := []fwstate.Transition{fwstate.TransDisable}; !reflect.DeepEqual(trs, want) {
		t.Errorf("transitions %v, want a stop predicted", trs)
	}

	broken := fw{d: deps{predict: func(fwstate.Config, fwstate.Loaded, fwstate.Transition) (fwstate.Loaded, error) {
		return fwstate.Loaded{}, errors.New("predict: translate: /etc/ufw/user.rules is empty")
	}}}
	if _, err := broken.verifyDiffs(enabled, cur); err == nil || !strings.Contains(err.Error(), "user.rules is empty") {
		t.Errorf("verifyDiffs = %v, want the prediction's error", err)
	}
}
