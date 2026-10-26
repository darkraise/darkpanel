package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TuplePrefix starts every rule line UFW writes in user.rules and
// user6.rules.
const TuplePrefix = "### tuple ###"

// Tuple is one "### tuple ###" line of user.rules or user6.rules, parsed as
// UFW 0.36 reads it (backend_iptables.py _read_rules). Addresses keep UFW's
// form: "0.0.0.0/0" or "::/0" for any, as the file's family requires.
type Tuple struct {
	Action    Action
	Log       Log // LogOff when the action has no _log or _log-all suffix
	Route     bool
	Proto     Proto
	DPort     string // "any" or a port list
	Dst       string
	SPort     string
	Src       string
	DApp      string // application profile names, "" for none
	SApp      string
	Direction Direction // DirIn or DirOut
	InIface   string
	OutIface  string
	Comment   string // decoded from the comment=<hex> field
	Family    Family // FamilyV4 for user.rules, FamilyV6 for user6.rules
	Raw       string // the line as read, trailing white space removed
}

var (
	tupleIfaceRe = regexp.MustCompile(`^[a-zA-Z0-9_.+,=%@-]{1,15}$`)
	ifaceInRe    = regexp.MustCompile(`in_\w+`)
	ifaceOutRe   = regexp.MustCompile(`out_\w+`)
	tuplePortRe  = regexp.MustCompile(`^[0-9]+(:[0-9]+)?$`)
)

// ParseTuple parses one tuple line; v6 says it comes from user6.rules. It
// refuses what UFW would skip as malformed, and also an action, address or
// port UFW would silently reinterpret, so a hand-edited file is reported
// rather than guessed at.
func ParseTuple(line string, v6 bool) (Tuple, error) {
	raw := strings.TrimRight(line, " \t\r\n")
	rest, ok := strings.CutPrefix(raw, TuplePrefix)
	if !ok {
		return Tuple{}, errors.New("model: not a tuple line")
	}
	t := Tuple{Raw: raw, Family: FamilyV4, Log: LogOff, Direction: DirIn}
	if v6 {
		t.Family = FamilyV6
	}
	if strings.Count(rest, " comment=") > 1 {
		return Tuple{}, errors.New("model: tuple has more than one comment")
	}
	body, hexComment, found := strings.Cut(rest, " comment=")
	if found {
		b, err := hex.DecodeString(strings.TrimSpace(hexComment))
		if err != nil || !utf8.Valid(b) {
			return Tuple{}, errors.New("model: tuple comment is not hex-encoded UTF-8")
		}
		t.Comment = string(b)
	}
	f := strings.Fields(body)
	if len(f) < 6 || len(f) > 9 {
		return Tuple{}, fmt.Errorf("model: tuple has %d fields, want 6 to 9", len(f))
	}
	if len(f) == 7 || len(f) == 9 {
		if err := t.parseIfaces(f[len(f)-1]); err != nil {
			return Tuple{}, err
		}
	}
	if err := t.parseAction(f[0]); err != nil {
		return Tuple{}, err
	}
	t.Proto = Proto(f[1])
	if !slices.Contains(Protos(), t.Proto) {
		return Tuple{}, fmt.Errorf("model: tuple protocol %q is not supported", f[1])
	}
	var err error
	if t.DPort, err = tuplePort(f[2]); err != nil {
		return Tuple{}, err
	}
	if t.Dst, err = tupleAddr(f[3], v6); err != nil {
		return Tuple{}, err
	}
	if t.SPort, err = tuplePort(f[4]); err != nil {
		return Tuple{}, err
	}
	if t.Src, err = tupleAddr(f[5], v6); err != nil {
		return Tuple{}, err
	}
	if len(f) >= 8 {
		t.DApp, t.SApp = tupleApp(f[6]), tupleApp(f[7])
	}
	return t, nil
}

// parseIfaces reads the last field of a 7- or 9-field tuple: "in", "out",
// "in_<if>", "out_<if>" or "in_<if>!out_<if>".
func (t *Tuple) parseIfaces(s string) error {
	dir, _, _ := strings.Cut(s, "_")
	if strings.Contains(s, "_") {
		switch {
		case strings.Contains(s, "!") && ifaceInRe.MatchString(s) && ifaceOutRe.MatchString(s):
			if strings.Count(s, "!") != 1 {
				return fmt.Errorf("model: tuple interfaces %q are malformed", s)
			}
			in, out, _ := strings.Cut(s, "!")
			_, t.InIface, _ = strings.Cut(in, "_")
			_, t.OutIface, _ = strings.Cut(out, "_")
		case strings.HasPrefix(s, "in_"):
			t.InIface = s[len("in_"):]
		case strings.HasPrefix(s, "out_"):
			t.OutIface = s[len("out_"):]
		default:
			return fmt.Errorf("model: tuple interfaces %q are malformed", s)
		}
	}
	for _, name := range []string{t.InIface, t.OutIface} {
		if name != "" && (!tupleIfaceRe.MatchString(name) || name == "." || name == "..") {
			return fmt.Errorf("model: tuple interface %q is not valid", name)
		}
	}
	switch Direction(dir) {
	case DirIn, DirOut:
		t.Direction = Direction(dir)
		return nil
	}
	return fmt.Errorf("model: tuple direction %q is not in or out", dir)
}

// parseAction reads "[route:]<action>[_log|_log-all]".
func (t *Tuple) parseAction(s string) error {
	if rest, ok := strings.CutPrefix(s, "route:"); ok {
		t.Route, s = true, rest
	}
	act, logType, hasLog := strings.Cut(s, "_")
	t.Action = Action(act)
	if !slices.Contains([]Action{ActionAllow, ActionDeny, ActionLimit, ActionReject}, t.Action) {
		return fmt.Errorf("model: tuple action %q is not supported", act)
	}
	if hasLog {
		t.Log = Log(logType)
		if t.Log != LogOn && t.Log != LogAll {
			return fmt.Errorf("model: tuple log type %q is not supported", logType)
		}
	}
	return nil
}

// tuplePort checks a port field as UFW's set_port does once service names
// are resolved, which UFW does before it writes a tuple.
func tuplePort(s string) (string, error) {
	if s == Any {
		return s, nil
	}
	bad := fmt.Errorf("model: tuple port %q is not valid", s)
	if strings.Count(s, ",")+strings.Count(s, ":") > maxPortSlots-1 {
		return "", bad
	}
	for _, e := range strings.Split(s, ",") {
		if !tuplePortRe.MatchString(e) {
			return "", bad
		}
		lo, hi, isRange := strings.Cut(e, ":")
		a, _ := strconv.Atoi(lo)
		if a < 1 || a > 65535 {
			return "", bad
		}
		if isRange {
			if b, _ := strconv.Atoi(hi); b > 65535 || a >= b {
				return "", bad
			}
		}
	}
	return s, nil
}

// tupleAddr checks an address field and writes any in the file's family,
// as UFW's _fix_anywhere does. An IPv4 netmask in dotted form, which UFW
// keeps when it is not a prefix, is accepted.
func tupleAddr(s string, v6 bool) (string, error) {
	s = strings.ToLower(s)
	if v6 && (s == Any || s == "0.0.0.0/0") {
		return "::/0", nil
	}
	if !v6 && (s == Any || s == "::/0") {
		return "0.0.0.0/0", nil
	}
	bad := fmt.Errorf("model: tuple address %q is not valid in this file", s)
	addr, mask, hasMask := strings.Cut(s, "/")
	a, err := netip.ParseAddr(addr)
	if err != nil || a.Zone() != "" || a.Is4() == v6 {
		return "", bad
	}
	if !hasMask {
		return s, nil
	}
	if !v6 && strings.Contains(mask, ".") {
		if m, err := netip.ParseAddr(mask); err != nil || !m.Is4() {
			return "", bad
		}
		return s, nil
	}
	if _, err := netip.ParsePrefix(s); err != nil {
		return "", bad
	}
	return s, nil
}

func tupleApp(s string) string {
	if s == "-" {
		return ""
	}
	return strings.ReplaceAll(s, "%20", " ")
}

// manualID derives a manual rule's id from its family and tuple line, so it
// is stable across reads while the line is unchanged.
func manualID(fam Family, raw string) string {
	sum := sha256.Sum256([]byte(string(fam) + raw))
	return "manual-" + hex.EncodeToString(sum[:])[:16]
}

// Rule converts t into the model. ID, Origin and Comment come from the tag,
// or for a manual rule the derived id and the comment as written. ok is
// false when the model cannot express t (an application rule, a route rule
// with a bare out direction, or any value outside the grammar of 2.2
// except the comment); the Rule then carries only ID, Action, Direction,
// Log, Family, Comment, Origin and Raw.
func (t Tuple) Rule() (Rule, bool) {
	r := Rule{Action: t.Action, Direction: t.Direction, Log: t.Log, Family: t.Family}
	if t.Route {
		r.Direction = DirRoute
	}
	if id, label, ok := ParseTag(t.Comment); ok {
		r.ID, r.Comment, r.Origin = id, label, OriginDarkpanel
	} else {
		r.ID, r.Comment, r.Origin = manualID(t.Family, t.Raw), t.Comment, OriginManual
	}
	full := r
	full.Proto, full.InIface, full.OutIface = t.Proto, t.InIface, t.OutIface
	full.From, full.To = ruleAddr(t.Src), ruleAddr(t.Dst)
	full.FromPort, full.ToPort = rulePort(t.SPort), rulePort(t.DPort)
	bareOut := t.Route && t.InIface == "" && t.OutIface == "" && t.Direction == DirOut
	if t.DApp != "" || t.SApp != "" || bareOut || len(full.problems(false)) > 0 {
		r.Raw = t.Raw
		return r, false
	}
	return full, true
}

func ruleAddr(s string) string {
	if s == "0.0.0.0/0" || s == "::/0" {
		return Any
	}
	return s
}

func rulePort(s string) string {
	if s == Any {
		return ""
	}
	return s
}

// Merge turns the tuples of user.rules (v4) and user6.rules (v6) into rules.
// A v4 tuple whose addresses are both any and an otherwise identical v6
// tuple, which is how UFW writes one rule for both families, become one
// rule with Family both, at the v4 tuple's position; each v6 tuple pairs at
// most once. The result lists the v4 file's rules in order, then the
// unpaired v6 rules in order. A merged manual rule's id is derived from
// "both" and the v4 line, and a merged unrepresentable rule's Raw is the v4
// line. Ids repeat only when the files carry the same tag twice.
func Merge(v4, v6 []Tuple) []Rule {
	out := make([]Rule, 0, len(v4)+len(v6))
	paired := make([]bool, len(v6))
	for _, a := range v4 {
		r, _ := a.Rule()
		for j, b := range v6 {
			if paired[j] || !mergeable(a, b) {
				continue
			}
			paired[j] = true
			r.Family = FamilyBoth
			if r.Origin == OriginManual {
				r.ID = manualID(FamilyBoth, a.Raw)
			}
			break
		}
		out = append(out, r)
	}
	for j, b := range v6 {
		if !paired[j] {
			r, _ := b.Rule()
			out = append(out, r)
		}
	}
	return out
}

// mergeable reports whether a (from user.rules) and b (from user6.rules)
// are the two halves UFW writes for one rule of both families.
func mergeable(a, b Tuple) bool {
	if a.Family != FamilyV4 || b.Family != FamilyV6 || a.Proto == ProtoIPv6 || a.Proto == ProtoIGMP {
		return false
	}
	if a.Src != "0.0.0.0/0" || a.Dst != "0.0.0.0/0" || b.Src != "::/0" || b.Dst != "::/0" {
		return false
	}
	return a.Action == b.Action && a.Log == b.Log && a.Route == b.Route && a.Proto == b.Proto &&
		a.DPort == b.DPort && a.SPort == b.SPort && a.DApp == b.DApp && a.SApp == b.SApp &&
		a.Direction == b.Direction && a.InIface == b.InIface && a.OutIface == b.OutIface && a.Comment == b.Comment
}
