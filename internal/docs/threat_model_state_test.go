package docs_test

import (
	"strings"
	"testing"
)

// v1r25Part returns the bullets of one part of an area, one to a line.
func v1r25Part(d w10tmDoc, key string) string {
	return strings.Join(w10tmBullets(d.parts[key]), "\n")
}

// TestV1r25KnownResidualRisksAreWhatStaysOpen: the section ends with the four
// risks the owner accepted and the rebinding of a forward host, and with no
// question described as undecided.
func TestV1r25KnownResidualRisksAreWhatStaysOpen(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	lines := d.lines["Known residual risks"]
	bullets := w10tmBullets(lines)
	want := []struct {
		prefix string
		terms  []string
	}{
		{"- `Origin`:", []string{"accepted"}},
		{"- `port 81`:", []string{"port 3000", "accepted"}},
		{"- `openpgp`:", []string{"GO-2026-5932", "accepted"}},
		{"- `enrollment budget`:", []string{"accepted"}},
		{"- `forward host`:", []string{"re-pointed"}},
	}
	if len(bullets) != len(want) {
		t.Fatalf("Known residual risks has %d bullets, want %d:\n%s", len(bullets), len(want), strings.Join(bullets, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(bullets[i], w.prefix) {
			t.Errorf("bullet %d is %.60q, want it to start with %q", i+1, bullets[i], w.prefix)
		}
		for _, name := range dcMissing(bullets[i], w.terms) {
			t.Errorf("bullet %d does not mention %q", i+1, name)
		}
	}
	var intro []string
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") {
			break
		}
		intro = append(intro, line)
	}
	text := strings.Join(intro, " ")
	if !strings.Contains(text, "accepted") || strings.Contains(text, "None is described as accepted") {
		t.Errorf("the introduction %q must say that the owner accepted the risks", text)
	}
	all := strings.Join(bullets, "\n")
	for _, gone := range []string{"RFC 1918", "digest", "undecided"} {
		if strings.Contains(all, gone) {
			t.Errorf("Known residual risks still mentions %q, a question the fixes closed", gone)
		}
	}
	if page := dcRead(t, w10tmPage); strings.Contains(page, "open question") {
		t.Error("the page still calls a risk an open question; each is accepted or fixed")
	}
}

// TestV1r25PageStatesWhatTheCodeNowDoes: each bullet a fix or a corrected
// claim changed holds its statement, and the claim the review found
// inaccurate is gone.
func TestV1r25PageStatesWhatTheCodeNowDoes(t *testing.T) {
	d := w10tmParse(dcRead(t, w10tmPage))
	for _, tc := range []struct {
		key  string
		must []string
		not  []string
	}{
		{"Authentication/Mitigations", []string{
			"a lock on the pair of user name and source", "the /64 of its IPv6 address", "the client address the listener parsed",
		}, nil},
		{"Authentication/Residual risk", []string{"its own attempts against a user name"}, nil},
		{"Agent protocol/Residual risk", []string{"global enrollment budget"}, nil},
		{"Command execution/Mitigations", []string{"`withComment`", "`darkpanel:`"},
			[]string{"the input schema patterns refuse a value that starts with `-`"}},
		{"Compose execution/Mitigations", []string{"exit status", "Compose's stderr"}, nil},
		{"Secret handling and delivery/Mitigations", []string{
			"a JSON input file named with `--input`", "opens the copy without following a link", "a keyed fingerprint",
			"label values are secret inputs", "every element of a container's `Cmd`",
		}, []string{"only from a prompt or standard input"}},
		{"Firewall/Mitigations", []string{
			"public leg of a panel exposure", "fails closed", "each cell of the public unicast space", "first blocked cell",
			"a cell the change itself blocks", "already drop or reject",
		}, nil},
		{"Exposure/Mitigations", []string{
			"any port of NPM's own container names", "at plan and again at apply", "`plan.state_changed`",
			"allowlisted error code", "first administrator",
		}, nil},
		{"Exposure/Residual risk", []string{"forward host that is a DNS name"}, nil},
		{"DERP host/Threats", []string{"compromised `darkpanel-derp` account"}, nil},
		{"DERP host/Mitigations", []string{
			"`verifyClients: false`", "supported opt-out", "repository file", "a directory the service user already owns",
			"no symlink beneath the service directory", "1 MiB", "`operate`", "CGNAT", "ULA", "`no_stun_answer`",
		}, []string{"created and kept at mode 0700"}},
		{"DERP host/Residual risk", []string{"debug log"}, nil},
	} {
		got := v1r25Part(d, tc.key)
		for _, name := range dcMissing(got, tc.must) {
			t.Errorf("%s does not state %q", tc.key, name)
		}
		for _, name := range tc.not {
			if strings.Contains(got, name) {
				t.Errorf("%s still says %q", tc.key, name)
			}
		}
	}
}
