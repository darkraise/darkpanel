package eval

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

const srcPortFilter = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -p tcp -m tcp --sport 1:1023 -j LOG --log-prefix "[LOW] "
-A INPUT -p tcp -m tcp --dport 22 --sport 1:1023 -j DROP
-A INPUT -p tcp -m multiport --dports 2222 --sports 40000 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 2223 ! --sport 53 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 2224 --sport 53 -m tcp --dport 2225 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`

func TestEvaluateUnknownSourcePort(t *testing.T) {
	rs := evalRuleset(t, srcPortFilter)
	tests := []struct {
		name    string
		srcPort uint16
		dstPort uint16
		verdict fwstate.Verdict
		trace   []string
		reason  string
	}{
		{
			name:    "a known source port is matched",
			srcPort: 40000, dstPort: 22,
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#6"},
		},
		{
			name:    "an unknown source port makes a deciding rule unknown",
			srcPort: 0, dstPort: 22,
			verdict: fwstate.Unknown,
			reason:  "filter/INPUT#2: the source port of a connection that does not exist yet is unknown",
		},
		{
			name:    "an unknown source port in a multiport list is unknown",
			srcPort: 0, dstPort: 2222,
			verdict: fwstate.Unknown,
			reason:  "filter/INPUT#3: the source port",
		},
		{
			name:    "a negated source port is unknown too",
			srcPort: 0, dstPort: 2223,
			verdict: fwstate.Unknown,
			reason:  "filter/INPUT#4: the source port",
		},
		{
			name:    "a false match elsewhere in the rule still decides",
			srcPort: 0, dstPort: 2224,
			verdict: fwstate.Drop,
			trace:   []string{},
			reason:  "filter/INPUT policy DROP",
		},
		{
			name:    "a known source port keeps its old meaning",
			srcPort: 53, dstPort: 2223,
			verdict: fwstate.Drop,
			trace:   []string{"filter/INPUT#1"},
			reason:  "filter/INPUT policy DROP",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := evalPacket()
			p.SrcPort, p.DstPort = tt.srcPort, tt.dstPort
			evalCheck(t, Evaluate(rs, p, nil), tt.verdict, tt.trace, tt.reason)
		})
	}
}
