//go:build harness

package harness

import (
	"reflect"
	"slices"
	"testing"
)

const w11a02Save = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER-USER - [0:0]
:ufw-user-input - [0:0]
:ufw-user-logging-input - [0:0]
-A INPUT -j ufw-before-logging-input
-A DOCKER-USER -j RETURN
-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`

func TestW11a02UnitUFWChainsKeepsOnlyUFWChainsAndPolicies(t *testing.T) {
	got := w11a02UFWChains(w11a02Save)
	want := map[string]string{
		"INPUT policy":           "DROP",
		"FORWARD policy":         "DROP",
		"OUTPUT policy":          "ACCEPT",
		"ufw-user-input":         "-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT",
		"ufw-user-logging-input": "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("w11a02UFWChains = %#v, want %#v", got, want)
	}
}

func TestW11a02UnitPolicyLeaksNamesEveryAcceptLine(t *testing.T) {
	log := "v4 -P INPUT DROP\nv6 -P INPUT DROP\nv4 -P INPUT ACCEPT\nv6 -P INPUT DROP\nv6 -P INPUT ACCEPT\n"
	got := w11a02PolicyLeaks(log)
	want := []string{"v4", "v6"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("w11a02PolicyLeaks = %q, want %q", got, want)
	}
	if got := w11a02PolicyLeaks("v4 -P INPUT DROP\nv6 -P INPUT DROP\n"); len(got) != 0 {
		t.Errorf("w11a02PolicyLeaks on a clean log = %q, want none", got)
	}
}

func TestW11a02UnitPolicyUnsampled(t *testing.T) {
	if got, want := w11a02PolicyUnsampled("v4 -P INPUT DROP\nv6 \n", "v4", "v6"), []string{"v6"}; !slices.Equal(got, want) {
		t.Errorf("w11a02PolicyUnsampled = %q, want %q", got, want)
	}
	if got := w11a02PolicyUnsampled("v4 -P INPUT DROP\nv6 -P INPUT DROP\n", "v4", "v6"); len(got) != 0 {
		t.Errorf("w11a02PolicyUnsampled on a full log = %q, want none", got)
	}
}
