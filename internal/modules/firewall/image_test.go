package firewall

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// imTestSave4 is IPv4 save output with UFW's chains, a foreign chain
// jumped to from INPUT, Docker's DOCKER-USER and a Darkpanel chain in nat.
const imTestSave4 = `*nat
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:DOCKER - [0:0]
:darkpanel-nat - [0:0]
-A PREROUTING -j darkpanel-nat
-A darkpanel-nat -d 192.0.2.1/32 -j RETURN
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER-USER - [0:0]
:fw-foreign - [0:0]
:ufw-before-input - [0:0]
:ufw-user-input - [0:0]
-A INPUT -j fw-foreign
-A INPUT -j ufw-before-input
-A FORWARD -j DOCKER-USER
-A DOCKER-USER -j RETURN
-A fw-foreign -s 198.51.100.7/32 -j ACCEPT
-A ufw-before-input -i lo -j ACCEPT
-A ufw-before-input -j ufw-user-input
-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`

const imTestSave6 = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:ufw6-user-input - [0:0]
-A INPUT -j ufw6-user-input
-A ufw6-user-input -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`

func imTestParse(t *testing.T, text string) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func imTestLoaded(t *testing.T, v4, v6 string) fwstate.Loaded {
	t.Helper()
	st := fwstate.Loaded{Variant: "nft", V4: imTestParse(t, v4)}
	if v6 != "" {
		st.V6 = imTestParse(t, v6)
	}
	return st
}

func imTestNames(img []imageTable) []string {
	var out []string
	for _, t := range img {
		for _, c := range t.Chains {
			out = append(out, t.Name+"/"+c.Name)
		}
	}
	return out
}

func TestFirewallChain(t *testing.T) {
	for name, want := range map[string]bool{
		"ufw-user-input": true, "ufw6-before-input": true, "DOCKER-USER": true, "darkpanel-block": true,
		"dp-b-0a1b": true, "DOCKER": false, "ts-input": false, "INPUT": false, "ufwx": false, "": false,
	} {
		if got := firewallChain(name); got != want {
			t.Errorf("firewallChain(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTakeImage(t *testing.T) {
	img := takeImage(imTestLoaded(t, imTestSave4, imTestSave6))
	if got, want := imTestNames(img.V4), []string{
		"nat/darkpanel-nat", "filter/INPUT", "filter/FORWARD", "filter/OUTPUT", "filter/DOCKER-USER",
		"filter/ufw-before-input", "filter/ufw-user-input",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("v4 chains = %v, want %v", got, want)
	}
	if got, want := imTestNames(img.V6), []string{"filter/INPUT", "filter/FORWARD", "filter/OUTPUT", "filter/ufw6-user-input"}; !reflect.DeepEqual(got, want) {
		t.Errorf("v6 chains = %v, want %v", got, want)
	}
	input := img.V4[1].Chains[0]
	if !input.Builtin || input.Policy != "DROP" || len(input.Rules) != 2 {
		t.Fatalf("INPUT = %+v", input)
	}
	if input.Rules[0].Own || !input.Rules[1].Own {
		t.Errorf("INPUT own flags = %v %v, want false true", input.Rules[0].Own, input.Rules[1].Own)
	}
	if input.Rules[0].Text != "-A INPUT -j fw-foreign" || input.Rules[0].Key == "" {
		t.Errorf("INPUT rule 1 = %+v", input.Rules[0])
	}
	if empty := img.V4[1].Chains[2]; empty.Rules == nil {
		t.Error("an empty chain's rules are nil, which encodes as null")
	}
	matched := strings.Replace(imTestSave4, "-A INPUT -j ufw-before-input\n", "-A INPUT -j ufw-before-input\n-A INPUT -s 203.0.113.9/32 -j ufw-user-input\n", 1)
	if r := takeImage(imTestLoaded(t, matched, imTestSave6)).V4[1].Chains[0].Rules[2]; r.Own {
		t.Errorf("matched jump %q is own, want another program's", r.Text)
	}
	if got := takeImage(fwstate.Loaded{Variant: "nft", V4: imTestParse(t, imTestSave4)}); got.V6 != nil {
		t.Errorf("an image of a state without IPv6 has v6 chains %v", got.V6)
	}
}

func TestPlanImageRestoresEverything(t *testing.T) {
	img := takeImage(imTestLoaded(t, imTestSave4, imTestSave6))
	cur := strings.NewReplacer(":INPUT DROP", ":INPUT ACCEPT", "-A INPUT -j ufw-before-input\n", "",
		"--dport 22 -j ACCEPT", "--dport 2222 -j ACCEPT").Replace(imTestSave4)
	p := planImage(img, imTestLoaded(t, cur, imTestSave6))
	if len(p.Excluded) != 0 {
		t.Errorf("excluded %v, want none", p.Excluded)
	}
	want4 := `*nat
:darkpanel-nat - [0:0]
-A darkpanel-nat -d 192.0.2.1/32 -j RETURN
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER-USER - [0:0]
:ufw-before-input - [0:0]
:ufw-user-input - [0:0]
-F INPUT
-F FORWARD
-F OUTPUT
-A INPUT -j fw-foreign
-A INPUT -j ufw-before-input
-A FORWARD -j DOCKER-USER
-A DOCKER-USER -j RETURN
-A ufw-before-input -i lo -j ACCEPT
-A ufw-before-input -j ufw-user-input
-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`
	if string(p.Restore4) != want4 {
		t.Errorf("v4 restore input =\n%s\nwant\n%s", p.Restore4, want4)
	}
	if !strings.Contains(string(p.Restore6), ":ufw6-user-input - [0:0]\n-F INPUT\n") {
		t.Errorf("v6 restore input =\n%s", p.Restore6)
	}
}

func TestPlanImageExcludesChangedBuiltin(t *testing.T) {
	img := takeImage(imTestLoaded(t, imTestSave4, imTestSave6))
	for name, cur := range map[string]string{
		"foreign rule added":   strings.Replace(imTestSave4, "-A INPUT -j ufw-before-input\n", "-A INPUT -j ufw-before-input\n-A INPUT -s 192.0.2.9/32 -j DROP\n", 1),
		"foreign rule removed": strings.Replace(imTestSave4, "-A INPUT -j fw-foreign\n", "", 1),
		"foreign rule doubled": strings.Replace(imTestSave4, "-A INPUT -j fw-foreign\n-A INPUT -j ufw-before-input\n", "-A INPUT -j fw-foreign\n-A INPUT -j fw-foreign\n", 1),
		"foreign matched jump": strings.Replace(imTestSave4, "-A INPUT -j ufw-before-input\n", "-A INPUT -j ufw-before-input\n-A INPUT -s 203.0.113.9/32 -j ufw-user-input\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			p := planImage(img, imTestLoaded(t, cur, imTestSave6))
			if want := []chainRef{{fwstate.V4, "filter", "INPUT"}}; !reflect.DeepEqual(p.Excluded, want) {
				t.Errorf("excluded %v, want %v", p.Excluded, want)
			}
			text := string(p.Restore4)
			if strings.Contains(text, ":INPUT") || strings.Contains(text, "-F INPUT") || strings.Contains(text, "-A INPUT ") {
				t.Errorf("restore input touches INPUT:\n%s", text)
			}
			if !strings.Contains(text, "-F FORWARD\n") || !strings.Contains(text, ":ufw-user-input - [0:0]") {
				t.Errorf("restore input lost the other chains:\n%s", text)
			}
		})
	}
}

// TestPlanImageMissingJumpTarget: *-restore -n rejects a whole table when a
// rule jumps to a chain that neither the input nor the table declares, so
// a chain whose jump target another program deleted is excluded, and so is
// every chain that jumps to an excluded chain the table no longer has.
func TestPlanImageMissingJumpTarget(t *testing.T) {
	gone := strings.NewReplacer(":ufw-user-input - [0:0]\n", ":ufw-user-input - [0:0]\n:fw-gone - [0:0]\n",
		"-A ufw-user-input -p tcp", "-A ufw-user-input -j fw-gone\n-A ufw-user-input -p tcp").Replace(imTestSave4)
	for _, tc := range []struct {
		name, image, cur string
		want             []chainRef
	}{
		{"jump", gone, imTestSave4, []chainRef{{fwstate.V4, "filter", "ufw-user-input"}}},
		{"goto", strings.Replace(gone, "-j fw-gone", "-g fw-gone", 1), imTestSave4, []chainRef{{fwstate.V4, "filter", "ufw-user-input"}}},
		{"cascade", gone, strings.NewReplacer(":ufw-user-input - [0:0]\n", "", "-A ufw-before-input -j ufw-user-input\n", "",
			"-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n", "").Replace(imTestSave4),
			[]chainRef{{fwstate.V4, "filter", "ufw-before-input"}, {fwstate.V4, "filter", "ufw-user-input"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := takeImage(imTestLoaded(t, tc.image, imTestSave6))
			p := planImage(img, imTestLoaded(t, tc.cur, imTestSave6))
			if !reflect.DeepEqual(p.Excluded, tc.want) {
				t.Errorf("excluded %v, want %v", p.Excluded, tc.want)
			}
			text := string(p.Restore4)
			if strings.Contains(text, "fw-gone") {
				t.Errorf("restore input names the deleted chain:\n%s", text)
			}
			for _, ref := range tc.want {
				if strings.Contains(text, ":"+ref.Chain+" ") || strings.Contains(text, "-A "+ref.Chain+" ") {
					t.Errorf("restore input loads excluded %s:\n%s", ref.Chain, text)
				}
			}
			if !strings.Contains(text, "-F INPUT\n") {
				t.Errorf("restore input lost INPUT:\n%s", text)
			}
		})
	}
	if p := planImage(takeImage(imTestLoaded(t, gone, imTestSave6)), imTestLoaded(t, gone, imTestSave6)); len(p.Excluded) != 0 {
		t.Errorf("excluded %v while fw-gone still exists, want none", p.Excluded)
	}
}

// TestImageRefusesALineBreak: a damaged image row with a line break in a
// rule, a chain name or a policy is not loaded, so it cannot add lines of
// its own to the *-restore input.
func TestImageRefusesALineBreak(t *testing.T) {
	if err := takeImage(imTestLoaded(t, imTestSave4, imTestSave6)).check(); err != nil {
		t.Fatalf("an intact image: %v", err)
	}
	for name, damage := range map[string]func(*beforeImage){
		"rule":   func(img *beforeImage) { img.V4[1].Chains[0].Rules[0].Text += "\n-F DOCKER-USER" },
		"chain":  func(img *beforeImage) { img.V6[0].Chains[3].Name += "\nCOMMIT" },
		"policy": func(img *beforeImage) { img.V4[1].Chains[1].Policy = "DROP\r" },
	} {
		img := takeImage(imTestLoaded(t, imTestSave4, imTestSave6))
		damage(img)
		if err := img.check(); err == nil {
			t.Errorf("%s: a line break was accepted", name)
		}
		h := newFakeHost(t)
		if _, err := loadImage(context.Background(), h.env(), img, h.loaded); err == nil || len(h.calls) != 0 {
			t.Errorf("%s: loadImage = %v after %v, want a refusal before any run", name, err, h.calls)
		}
	}
}

func TestPlanImageMissingFamily(t *testing.T) {
	img := takeImage(imTestLoaded(t, imTestSave4, imTestSave6))
	p := planImage(img, imTestLoaded(t, imTestSave4, ""))
	if p.Restore6 != nil {
		t.Errorf("restore input for an absent family:\n%s", p.Restore6)
	}
	if len(p.Excluded) != 4 || p.Excluded[0] != (chainRef{fwstate.V6, "filter", "INPUT"}) {
		t.Errorf("excluded %v, want every v6 chain", p.Excluded)
	}
	if empty := planImage(&beforeImage{}, imTestLoaded(t, imTestSave4, "")); empty.Restore4 != nil || empty.Restore6 != nil {
		t.Error("an empty image produced restore input")
	}
}

func TestImageMismatch(t *testing.T) {
	img := takeImage(imTestLoaded(t, imTestSave4, imTestSave6))
	if got := imageMismatch(img, imTestLoaded(t, imTestSave4, imTestSave6), nil); len(got) != 0 {
		t.Errorf("mismatch against the imaged state: %v", got)
	}
	changed := strings.NewReplacer(":INPUT DROP", ":INPUT ACCEPT", "--dport 22 -j ACCEPT", "--dport 23 -j ACCEPT",
		":darkpanel-nat - [0:0]\n", "", "-A PREROUTING -j darkpanel-nat\n", "", "-A darkpanel-nat -d 192.0.2.1/32 -j RETURN\n", "").Replace(imTestSave4)
	want := []chainRef{{fwstate.V4, "nat", "darkpanel-nat"}, {fwstate.V4, "filter", "INPUT"}, {fwstate.V4, "filter", "ufw-user-input"}}
	if got := imageMismatch(img, imTestLoaded(t, changed, imTestSave6), nil); !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch = %v, want %v", got, want)
	}
	if got := imageMismatch(img, imTestLoaded(t, changed, imTestSave6), want[1:2]); len(got) != 2 {
		t.Errorf("mismatch ignoring INPUT = %v", got)
	}
	extra := strings.Replace(imTestSave6, "-A ufw6-user-input", "-A ufw6-user-input -p udp -m udp --dport 9 -j DROP\n-A ufw6-user-input", 1)
	if got := imageMismatch(img, imTestLoaded(t, imTestSave4, extra), nil); !reflect.DeepEqual(got, []chainRef{{fwstate.V6, "filter", "ufw6-user-input"}}) {
		t.Errorf("mismatch with an extra v6 rule = %v", got)
	}
}

func TestRestoreProgram(t *testing.T) {
	for _, c := range []struct {
		fam     fwstate.Family
		variant string
		want    string
	}{
		{fwstate.V4, "nft", "/usr/sbin/iptables-nft-restore"},
		{fwstate.V6, "nft", "/usr/sbin/ip6tables-nft-restore"},
		{fwstate.V4, "legacy", "/usr/sbin/iptables-legacy-restore"},
		{fwstate.V6, "legacy", "/usr/sbin/ip6tables-legacy-restore"},
	} {
		if got := restoreProgram(c.fam, c.variant); got != c.want {
			t.Errorf("restoreProgram(%s, %s) = %s, want %s", c.fam, c.variant, got, c.want)
		}
	}
}
