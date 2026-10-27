package predict

import (
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// directions are UFW's chain suffixes, in the order ufw-init-functions
// writes them.
var directions = []string{"input", "output", "forward"}

// primaryChains are the chains ufw_start creates once and ufw_stop keeps,
// so their jumps in INPUT, OUTPUT and FORWARD keep their positions.
var primaryChains = []string{
	"before-logging-input", "before-logging-output", "before-logging-forward",
	"before-input", "before-output", "before-forward",
	"after-input", "after-output", "after-forward",
	"after-logging-input", "after-logging-output", "after-logging-forward",
	"reject-input", "reject-output", "reject-forward",
	"track-input", "track-output", "track-forward",
}

// restoreText is the iptables-restore input of one family (user6.rules,
// before6.rules and after6.rules for v6) in the order ufw_start loads it
// on a host where none of UFW's chains exists: the primary chains and
// their jumps from the built-in chains, the reject and track rules of the
// default policies, the logging and skip-to-policy chains, before.rules,
// after.rules, the user chains, user.rules, the jumps into the user chains
// and the built-in policies (REJECT loads as DROP). The files are the
// bytes cfg.Files holds, which ufw_start reads from disk: for a user.rules
// UFW has written they equal Files' rendering, but the package's template,
// which a first "ufw enable" loads before rewriting it, does not. A
// missing or empty before, after or user file, which makes ufw_start fail,
// is an error.
func restoreText(cfg fwstate.Config, v6 bool) (string, error) {
	if err := checkRenderable(cfg); err != nil {
		return "", err
	}
	typ := ""
	if v6 {
		typ = "6"
	}
	u := chainPrefix(v6)
	policy := map[string]string{"input": cfg.DefaultInput, "output": cfg.DefaultOutput, "forward": cfg.DefaultForward}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("*filter")
	for _, c := range primaryChains {
		w(":%s-%s - [0:0]", u, c)
	}
	for _, d := range directions {
		for _, c := range []string{"before-logging", "before", "after", "after-logging", "reject", "track"} {
			w("-A %s -j %s-%s-%s", strings.ToUpper(d), u, c, d)
		}
	}
	w("COMMIT")
	for _, d := range directions {
		if policy[d] == "REJECT" {
			w("*filter\n-A %s-reject-%s -j REJECT\nCOMMIT", u, d)
		}
	}
	for _, d := range directions {
		if policy[d] == "ACCEPT" {
			w("*filter\n-A %[1]s-track-%[2]s -p tcp -m conntrack --ctstate NEW -j ACCEPT\n"+
				"-A %[1]s-track-%[2]s -p udp -m conntrack --ctstate NEW -j ACCEPT\nCOMMIT", u, d)
		}
	}
	w("*filter\n:%[1]s-logging-deny - [0:0]\n:%[1]s-logging-allow - [0:0]\nCOMMIT", u)
	w("*filter")
	for _, d := range directions {
		w(":%s-skip-to-policy-%s - [0:0]", u, d)
	}
	for _, d := range directions {
		w("-A %s-skip-to-policy-%s -j %s", u, d, policy[d])
	}
	w("COMMIT")
	file := func(name string) error {
		path := "/etc/ufw/" + name + typ + ".rules"
		e := cfg.Files[path]
		if !e.Present || len(e.Data) == 0 {
			return fmt.Errorf("predict: %s is missing or empty, so UFW cannot start", path)
		}
		b.Write(e.Data)
		if e.Data[len(e.Data)-1] != '\n' {
			b.WriteByte('\n')
		}
		return nil
	}
	for _, name := range []string{"before", "after"} {
		if err := file(name); err != nil {
			return "", err
		}
	}
	w("*filter")
	for _, c := range []string{"user-input", "user-output", "user-forward", "user-logging-input", "user-logging-output",
		"user-logging-forward", "user-limit", "user-limit-accept"} {
		w(":%s-%s - [0:0]", u, c)
	}
	w("COMMIT")
	if err := file("user"); err != nil {
		return "", err
	}
	w("*filter\n-A %[1]s-before-input -j %[1]s-user-input\n-A %[1]s-before-output -j %[1]s-user-output\n"+
		"-A %[1]s-before-forward -j %[1]s-user-forward\nCOMMIT", u)
	builtin := func(d string) string {
		if policy[d] == "REJECT" {
			return "DROP"
		}
		return policy[d]
	}
	w("*filter\n:INPUT %s [0:0]\n:FORWARD %s [0:0]\n:OUTPUT %s [0:0]\nCOMMIT", builtin("input"), builtin("forward"), builtin("output"))
	return b.String(), nil
}

// translate is what UFW loads for one family on a host where none of its
// chains exists: restoreText parsed as iptables-restore would load it. The
// filter table holds UFW's chains; another table appears only when
// before.rules or after.rules write to it (a nat block for masquerading,
// for example).
func translate(cfg fwstate.Config, v6 bool) (*netfilter.Ruleset, error) {
	text, err := restoreText(cfg, v6)
	if err != nil {
		return nil, err
	}
	rs, err := netfilter.ParseRestore(strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("predict: UFW's rules do not load: %w", err)
	}
	return rs, nil
}
