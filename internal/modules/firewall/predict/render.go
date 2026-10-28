// Package predict simulates UFW 0.36 (spec 2.3, 2.5): how one ufw command
// changes the configured state and the files UFW writes, the netfilter
// state UFW loads from a configuration, and the loaded state a transition
// leaves behind.
package predict

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The files Files renders.
const (
	userRules   = "/etc/ufw/user.rules"
	userRules6  = "/etc/ufw/user6.rules"
	ufwConf     = "/etc/ufw/ufw.conf"
	defaultsUFW = "/etc/default/ufw"
)

// logLevels ranks ufw.conf's LOGLEVEL values as UFW's backend does.
var logLevels = map[string]int{"off": 0, "low": 100, "medium": 200, "high": 300, "full": 400}

// Files returns the bytes UFW writes for cfg the next time it writes each
// file: user.rules and user6.rules rendered from the tuples, the log level
// and the default policies as backend_iptables.py's _write_rules does, and
// ufw.conf and /etc/default/ufw as cfg.Files holds them with each key whose
// value UFW would read differently from cfg rewritten as UFW's set_default
// writes it. With IPv6 off UFW keeps no IPv6 rules in memory, so
// user6.rules renders without tuples, logging or rate-limit chains. A log
// level or default policy UFW refuses is an error.
func Files(cfg fwstate.Config) (map[string][]byte, error) {
	if err := checkRenderable(cfg); err != nil {
		return nil, err
	}
	out := map[string][]byte{
		userRules:  renderUser(cfg, false),
		userRules6: renderUser(cfg, true),
	}
	defs := ufwDefaults(cfg.Files[defaultsUFW].Data, cfg.Files[ufwConf].Data)
	data := cfg.Files[ufwConf].Data
	if (defs["enabled"] == "yes") != cfg.Enabled {
		data = setDefault(data, "ENABLED", yesNo(cfg.Enabled))
	}
	if defs["loglevel"] != cfg.LogLevel {
		data = setDefault(data, "LOGLEVEL", cfg.LogLevel)
	}
	out[ufwConf] = data
	data = cfg.Files[defaultsUFW].Data
	for _, p := range policyKeys(cfg) {
		if defs[strings.ToLower(p.key)] != strings.ToLower(p.value) {
			data = setDefault(data, p.key, `"`+p.value+`"`)
		}
	}
	out[defaultsUFW] = data
	return out, nil
}

// checkRenderable refuses what UFW itself refuses before it writes: a log
// level _get_logging_rules rejects and a default policy _get_defaults
// rejects.
func checkRenderable(cfg fwstate.Config) error {
	if _, ok := logLevels[cfg.LogLevel]; !ok {
		return fmt.Errorf("predict: log level %q is not one UFW accepts", cfg.LogLevel)
	}
	for _, p := range policyKeys(cfg) {
		if !slices.Contains([]string{"ACCEPT", "DROP", "REJECT"}, p.value) {
			return fmt.Errorf("predict: %s %q is not ACCEPT, DROP or REJECT", p.key, p.value)
		}
	}
	return nil
}

type policyKey struct{ key, value string }

func policyKeys(cfg fwstate.Config) []policyKey {
	return []policyKey{
		{"DEFAULT_INPUT_POLICY", cfg.DefaultInput},
		{"DEFAULT_OUTPUT_POLICY", cfg.DefaultOutput},
		{"DEFAULT_FORWARD_POLICY", cfg.DefaultForward},
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var ufwDefaultRe = regexp.MustCompile(`^\w+="?\w+"?`)

// ufwDefaults reads KEY=value lines as UFW's _get_defaults does: the files
// in the order given (/etc/default/ufw, then ufw.conf) into one map, keys
// and values lower-cased, quotes stripped, the last assignment winning.
func ufwDefaults(files ...[]byte) map[string]string {
	out := map[string]string{}
	for _, data := range files {
		for _, line := range strings.Split(string(data), "\n") {
			if !ufwDefaultRe.MatchString(line) {
				continue
			}
			f := strings.Split(strings.TrimSpace(line), "=")
			out[strings.ToLower(f[0])] = strings.Trim(strings.ToLower(f[1]), `"'`)
		}
	}
	return out
}

// setDefault rewrites every line that starts with "opt=" as "opt=value", or
// appends that line when none does, as UFW's set_default does.
func setDefault(data []byte, opt, value string) []byte {
	var b strings.Builder
	found := false
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if strings.HasPrefix(line, opt+"=") {
			b.WriteString(opt + "=" + value + "\n")
			found = true
			continue
		}
		b.WriteString(line)
	}
	if !found {
		b.WriteString(opt + "=" + value + "\n")
	}
	return []byte(b.String())
}

// chainPrefix is UFW's chain prefix for a family.
func chainPrefix(v6 bool) string {
	if v6 {
		return "ufw6"
	}
	return "ufw"
}

// renderUser renders user.rules (v6 false) or user6.rules as _write_rules
// writes it. Rate limiting is assumed available in IPv4 and, with IPv6 on,
// in IPv6, as on every supported image; with IPv6 off UFW skips the IPv6
// capability check and writes neither the IPv6 limit chains nor any IPv6
// tuple or logging rule.
func renderUser(cfg fwstate.Config, v6 bool) []byte {
	p := chainPrefix(v6)
	limit := !v6 || cfg.IPv6
	rules := cfg.Rules4
	if v6 {
		rules = nil
		if cfg.IPv6 {
			rules = cfg.Rules6
		}
	}
	var b strings.Builder
	b.WriteString("*filter\n")
	for _, c := range []string{"user-input", "user-output", "user-forward",
		"before-logging-input", "before-logging-output", "before-logging-forward",
		"user-logging-input", "user-logging-output", "user-logging-forward",
		"after-logging-input", "after-logging-output", "after-logging-forward",
		"logging-deny", "logging-allow"} {
		b.WriteString(":" + p + "-" + c + " - [0:0]\n")
	}
	if limit {
		b.WriteString(":" + p + "-user-limit - [0:0]\n")
		b.WriteString(":" + p + "-user-limit-accept - [0:0]\n")
	}
	b.WriteString("### RULES ###\n")
	for _, t := range rules {
		b.WriteString("\n" + tupleLine(t) + "\n")
		suffix := "input"
		if t.Route {
			suffix = "forward"
		} else if t.Direction == model.DirOut {
			suffix = "output"
		}
		for _, s := range rulesFromFormatted("-A "+p+"-user-"+suffix+" "+formatRule(t)+"\n", p, suffix) {
			b.WriteString(s)
		}
	}
	b.WriteString("\n### END RULES ###\n")
	b.WriteString("\n### LOGGING ###\n")
	if !v6 || cfg.IPv6 {
		for _, r := range loggingRules(cfg, p) {
			if r[0] == "-D" {
				continue
			}
			line := strings.Join(r, " ")
			line = strings.ReplaceAll(line, "[", `"[`)
			line = strings.ReplaceAll(line, "] ", `] "`)
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("### END LOGGING ###\n")
	if limit {
		b.WriteString("\n### RATE LIMITING ###\n")
		if cfg.LogLevel != "off" {
			b.WriteString("-A " + p + "-user-limit -m limit --limit 3/minute -j LOG --log-prefix \"[UFW LIMIT BLOCK] \"\n")
		}
		b.WriteString("-A " + p + "-user-limit -j REJECT\n")
		b.WriteString("-A " + p + "-user-limit-accept -j ACCEPT\n")
		b.WriteString("### END RATE LIMITING ###\n")
	}
	b.WriteString("COMMIT\n")
	return []byte(b.String())
}

// tupleLine is the "### tuple ###" line _write_rules writes for t.
func tupleLine(t model.Tuple) string {
	action := string(t.Action)
	if t.Route {
		action = "route:" + action
	}
	if t.Log != model.LogOff && t.Log != "" {
		action += "_" + string(t.Log)
	}
	var ifaces string
	switch {
	case t.InIface == "" && t.OutIface == "":
		ifaces = string(t.Direction)
	case t.InIface != "" && t.OutIface != "":
		ifaces = "in_" + t.InIface + "!out_" + t.OutIface
	case t.InIface != "":
		ifaces = string(t.Direction) + "_" + t.InIface
	default:
		ifaces = string(t.Direction) + "_" + t.OutIface
	}
	f := []string{model.TuplePrefix, action, string(t.Proto), t.DPort, t.Dst, t.SPort, t.Src}
	if t.DApp != "" || t.SApp != "" {
		f = append(f, appField(t.DApp), appField(t.SApp))
	}
	f = append(f, ifaces)
	line := strings.Join(f, " ")
	if t.Comment != "" {
		line += " comment=" + hex.EncodeToString([]byte(t.Comment))
	}
	return line
}

func appField(name string) string {
	if name == "" {
		return "-"
	}
	return strings.ReplaceAll(name, " ", "%20")
}

// multiPort reports whether UFW's set_port marks a rule with these ports
// multiport: a list or a range in either port.
func multiPort(t model.Tuple) bool {
	for _, p := range []string{t.DPort, t.SPort} {
		if p != model.Any && strings.ContainsAny(p, ",:") {
			return true
		}
	}
	return false
}

// formatRule is UFWRule.format_rule: the iptables options of t after
// "-A <chain>", before _get_rules_from_formatted adjusts them.
func formatRule(t model.Tuple) string {
	var b strings.Builder
	if t.InIface != "" {
		b.WriteString(" -i " + t.InIface)
	}
	if t.OutIface != "" {
		b.WriteString(" -o " + t.OutIface)
	}
	multi := multiPort(t)
	if t.Proto == model.ProtoAny {
		b.WriteString(" -p all")
	} else {
		b.WriteString(" -p " + string(t.Proto))
		if multi {
			b.WriteString(" -m multiport")
			switch {
			case t.DPort != model.Any && t.SPort != model.Any:
				b.WriteString(" --dports " + t.DPort + " -m multiport --sports " + t.SPort)
			case t.DPort != model.Any:
				b.WriteString(" --dports " + t.DPort)
			case t.SPort != model.Any:
				b.WriteString(" --sports " + t.SPort)
			}
		}
	}
	if t.Dst != "0.0.0.0/0" && t.Dst != "::/0" {
		b.WriteString(" -d " + t.Dst)
	}
	if !multi && t.DPort != model.Any {
		b.WriteString(" --dport " + t.DPort)
	}
	if t.Src != "0.0.0.0/0" && t.Src != "::/0" {
		b.WriteString(" -s " + t.Src)
	}
	if !multi && t.SPort != model.Any {
		b.WriteString(" --sport " + t.SPort)
	}
	lstr := ""
	if t.Log != model.LogOff && t.Log != "" {
		lstr = "_" + string(t.Log)
	}
	switch t.Action {
	case model.ActionAllow:
		b.WriteString(" -j ACCEPT" + lstr)
	case model.ActionReject:
		b.WriteString(" -j REJECT" + lstr)
		if t.Proto == model.ProtoTCP {
			b.WriteString(" --reject-with tcp-reset")
		}
	case model.ActionLimit:
		b.WriteString(" -j LIMIT" + lstr)
	default:
		b.WriteString(" -j DROP" + lstr)
	}
	if t.DApp != "" || t.SApp != "" {
		var apps []string
		if t.DApp != "" {
			apps = append(apps, "dapp_"+strings.ReplaceAll(t.DApp, " ", "%20"))
		}
		if t.SApp != "" {
			apps = append(apps, "sapp_"+strings.ReplaceAll(t.SApp, " ", "%20"))
		}
		b.WriteString(" -m comment --comment '" + strings.Join(apps, ",") + "'")
	}
	return strings.TrimSpace(b.String())
}

var (
	fmtProtoRe  = regexp.MustCompile(`-p all `)
	fmtPortRe   = regexp.MustCompile(`port `)
	fmtRejectRe = regexp.MustCompile(`-j (REJECT(_log(-all)?)?)`)
	fmtLogRe    = regexp.MustCompile(`(.*)-j ([A-Z]+)_log(-all)?(.*)`)
	fmtLogAllRe = regexp.MustCompile(`-j [A-Z]+_log-all`)
	fmtChainRe  = regexp.MustCompile(`(-A|-D) ([a-zA-Z0-9\-]+)`)
	fmtLimitRe  = regexp.MustCompile(` -j LIMIT`)
)

// rulesFromFormatted is _get_rules_from_formatted: it splits a "proto any"
// rule with a port into tcp and udp rules, expands a logging rule into its
// logging chain's LOG and RETURN rules and the jump to that chain, and
// expands limit into the recent rules. The text manipulation follows UFW's
// regular expressions so unusual names come out as UFW writes them.
func rulesFromFormatted(frule, prefix, suffix string) []string {
	var snippets []string
	switch {
	case !fmtProtoRe.MatchString(frule):
		snippets = append(snippets, frule)
	case fmtPortRe.MatchString(frule):
		if fmtRejectRe.MatchString(frule) {
			snippets = append(snippets, fmtProtoRe.ReplaceAllString(
				fmtRejectRe.ReplaceAllString(frule, "-j ${1} --reject-with tcp-reset"), "-p tcp "))
		} else {
			snippets = append(snippets, fmtProtoRe.ReplaceAllString(frule, "-p tcp "))
		}
		snippets = append(snippets, fmtProtoRe.ReplaceAllString(frule, "-p udp "))
	default:
		snippets = append(snippets, fmtProtoRe.ReplaceAllString(frule, ""))
	}

	logChain := prefix + "-user-logging-" + suffix
	limitArgs := "-m limit --limit 3/min --limit-burst 10"
	for i := 0; i < len(snippets); i++ {
		s := snippets[i]
		if !fmtLogRe.MatchString(s) {
			continue
		}
		policy := "BLOCK"
		switch strings.ToLower(strings.TrimSpace(fmtLogRe.ReplaceAllString(s, "${2}"))) {
		case "accept":
			policy = "ALLOW"
		case "limit":
			policy = "LIMIT"
		}
		lstr := limitArgs + ` -j LOG --log-prefix "[UFW ` + policy + `] "`
		if !fmtLogAllRe.MatchString(s) {
			lstr = "-m conntrack --ctstate NEW " + lstr
		}
		inChain := func(t string) string { return fmtChainRe.ReplaceAllString(t, "${1} "+logChain) }
		snippets[i] = fmtLogRe.ReplaceAllString(s, "${1}-j ${2}${4}")
		snippets = slices.Insert(snippets, i,
			inChain(fmtLogRe.ReplaceAllString(s, "${1}"+lstr)),
			inChain(fmtLogRe.ReplaceAllString(s, "${1}-j RETURN")),
			fmtLogRe.ReplaceAllString(s, "${1}-j "+logChain))
	}

	for i := 0; i < len(snippets); i++ {
		s := snippets[i]
		if !fmtLimitRe.MatchString(s) {
			continue
		}
		set := fmtLimitRe.ReplaceAllString(s, " -m conntrack --ctstate NEW -m recent --set")
		update := fmtLimitRe.ReplaceAllString(s, " -m conntrack --ctstate NEW -m recent --update --seconds 30 --hitcount 6 -j "+prefix+"-user-limit")
		snippets[i] = fmtLimitRe.ReplaceAllString(s, " -j "+prefix+"-user-limit-accept")
		snippets = slices.Insert(snippets, i, set, update)
	}
	return snippets
}

// loggingRules is _get_logging_rules for one family's chains (prefix "ufw"
// or "ufw6"), in UFW's order: each rule as the argument list UFW passes to
// iptables, the chain name second.
func loggingRules(cfg fwstate.Config, p string) [][]string {
	level := logLevels[cfg.LogLevel]
	userChains := []string{p + "-user-logging-input", p + "-user-logging-output", p + "-user-logging-forward"}
	var out [][]string
	if level == logLevels["off"] {
		for _, c := range userChains {
			out = append(out, []string{"-I", c, "-j", "RETURN"})
		}
		return out
	}
	for _, c := range userChains {
		out = append(out, []string{"-D", c, "-j", "RETURN"})
	}
	limitArgs := []string{"-m", "limit", "--limit", "3/min", "--limit-burst", "10"}
	policy := map[string]string{"input": cfg.DefaultInput, "output": cfg.DefaultOutput, "forward": cfg.DefaultForward}
	if level >= logLevels["low"] {
		var largs []string
		if level < logLevels["high"] {
			largs = limitArgs
		}
		for _, t := range []string{"input", "output", "forward"} {
			c := p + "-after-logging-" + t
			switch {
			case policy[t] == "REJECT" || policy[t] == "DROP":
				out = append(out, append([]string{"-A", c, "-j", "LOG", "--log-prefix", "[UFW BLOCK] "}, largs...))
			case level >= logLevels["medium"]:
				out = append(out, append([]string{"-A", c, "-j", "LOG", "--log-prefix", "[UFW ALLOW] "}, largs...))
			}
		}
		for _, c := range []string{p + "-logging-deny", p + "-logging-allow"} {
			prefix := "[UFW ALLOW] "
			if strings.HasSuffix(c, "deny") {
				prefix = "[UFW BLOCK] "
				if level < logLevels["medium"] {
					out = append(out, append([]string{"-I", c, "-m", "conntrack", "--ctstate", "INVALID", "-j", "RETURN"}, largs...))
				} else {
					out = append(out, append([]string{"-A", c, "-m", "conntrack", "--ctstate", "INVALID", "-j", "LOG", "--log-prefix", "[UFW AUDIT INVALID] "}, largs...))
				}
			}
			out = append(out, append([]string{"-A", c, "-j", "LOG", "--log-prefix", prefix}, largs...))
		}
	}
	if level >= logLevels["medium"] {
		var largs []string
		if level < logLevels["full"] {
			largs = limitArgs
		}
		if level < logLevels["high"] {
			largs = append([]string{"-m", "conntrack", "--ctstate", "NEW"}, limitArgs...)
		}
		for _, t := range []string{"input", "output", "forward"} {
			out = append(out, append([]string{"-I", p + "-before-logging-" + t, "-j", "LOG", "--log-prefix", "[UFW AUDIT] "}, largs...))
		}
	}
	return out
}
