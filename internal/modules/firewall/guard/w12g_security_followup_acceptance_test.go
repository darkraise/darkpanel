package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestA21_TheFirewallGuardAcceptanceTestHasNoQaSpanAndAColonRangeFailsItsCheck(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files: %v", err)
	}
	self := "w12g_security_followup_acceptance_test.go"
	for _, f := range files {
		if f == self {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "qaSpan") {
			t.Errorf("%s still holds qaSpan", f)
		}
	}
	for _, msg := range []string{
		"cut exposure exp1 from :-: to [fe80::1]:443",
		"cut exposure exp1 from ::-: to [fe80::1]:443",
		"cut exposure exp1 from :-:: to [fe80::1]:443",
	} {
		if w12eNamesIPv6Range(msg) {
			t.Errorf("%q passes the IPv6 range check", msg)
		}
	}
	if !w12eNamesIPv6Range("cut exposure exp1 hidden client from fe80::-fe80::ffff to [fe80::1]:443 on eth0") {
		t.Error("a real IPv6 range fails the check")
	}
}
