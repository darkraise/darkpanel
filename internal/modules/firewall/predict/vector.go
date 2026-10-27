package predict

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// The kinds of ufw command Edit simulates.
const (
	vecRule    = "rule"
	vecDefault = "default"
	vecLogging = "logging"
	vecEnable  = "enable"
	vecDisable = "disable"
	vecReload  = "reload"
)

// vector is one parsed ufw argument vector.
type vector struct {
	kind string
	// rule: the rule before UFW fixes its addresses for a family; Src and
	// Dst are "0.0.0.0/0" for any, Family is unset and Raw empty.
	rule     model.Tuple
	iptype   model.Family // rule: the families UFW writes it to
	remove   bool         // rule: delete
	position int          // rule: insert position, 0 to append
	policy   string       // default: allow, deny or reject
	chain    string       // default: INPUT, OUTPUT or FORWARD
	level    string       // logging: off, on, low, medium, high or full
}

var (
	vecPortRe   = regexp.MustCompile(`^\d([0-9,:]*\d+)*$`)
	vecRangeRe  = regexp.MustCompile(`^\d+:\d+$`)
	vecNumberRe = regexp.MustCompile(`^\d+$`)
	vecIfaceRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0,14}$`)
)

// parseVector reads one ufw argument vector as UFW 0.36's parser does, for
// the forms Darkpanel issues: the full rule form model.AddArgs, DeleteArgs
// and InsertArgs write (and the same form with "from" or "to" left out),
// "default <allow|deny|reject> <incoming|outgoing|routed>", "logging
// <off|on|low|medium|high|full>", "--force enable", "disable" and
// "reload". Anything else is refused, including what ufw would accept
// but Darkpanel never writes: the short form, application rules, service
// names, dotted netmasks, a comment that is not UTF-8 (UFW drops the
// invalid bytes), interface names outside the rule model's pattern
// (UFW accepts some it cannot read back, such as "%0"), "prepend" and
// deleting by number.
func parseVector(args []string) (vector, error) {
	refuse := func(why string) (vector, error) {
		return vector{}, fmt.Errorf("predict: ufw %s: %s", strings.Join(args, " "), why)
	}
	switch {
	case len(args) == 0:
		return refuse("no arguments")
	case slices.Equal(args, []string{"--force", "enable"}):
		return vector{kind: vecEnable}, nil
	case slices.Equal(args, []string{"disable"}):
		return vector{kind: vecDisable}, nil
	case slices.Equal(args, []string{"reload"}):
		return vector{kind: vecReload}, nil
	case args[0] == "default":
		chain := map[string]string{"incoming": "INPUT", "outgoing": "OUTPUT", "routed": "FORWARD"}
		if len(args) != 3 || !slices.Contains([]string{"allow", "deny", "reject"}, args[1]) || chain[args[2]] == "" {
			return refuse("want default <allow|deny|reject> <incoming|outgoing|routed>")
		}
		return vector{kind: vecDefault, policy: args[1], chain: chain[args[2]]}, nil
	case args[0] == "logging":
		if len(args) != 2 || !slices.Contains([]string{"off", "on", "low", "medium", "high", "full"}, args[1]) {
			return refuse("want logging <off|on|low|medium|high|full>")
		}
		return vector{kind: vecLogging, level: args[1]}, nil
	}
	v, err := parseRuleVector(args)
	if err != nil {
		return refuse(err.Error())
	}
	return v, nil
}

// parseRuleVector reads "[route] [delete | insert <n>] <action> <spec>".
func parseRuleVector(args []string) (vector, error) {
	v := vector{kind: vecRule, rule: model.Tuple{
		Proto: model.ProtoAny, DPort: model.Any, SPort: model.Any,
		Dst: "0.0.0.0/0", Src: "0.0.0.0/0", Direction: model.DirIn, Log: model.LogOff,
	}}
	a := args
	if a[0] == "route" {
		v.rule.Route, a = true, a[1:]
	}
	switch {
	case len(a) > 0 && a[0] == "delete":
		v.remove, a = true, a[1:]
	case len(a) > 0 && a[0] == "insert":
		if len(a) < 4 {
			return v, fmt.Errorf("insert needs a position and a rule")
		}
		n, err := strconv.Atoi(a[1])
		if err != nil || n < 1 {
			return v, fmt.Errorf("insert position %q is not a positive number", a[1])
		}
		v.position, a = n, a[2:]
	}
	if len(a) == 0 || !slices.Contains([]model.Action{model.ActionAllow, model.ActionDeny, model.ActionReject, model.ActionLimit}, model.Action(a[0])) {
		return v, fmt.Errorf("want allow, deny, reject or limit")
	}
	v.rule.Action, a = model.Action(a[0]), a[1:]

	hasIface := false
	setIface := func(dir, name string) error {
		if !vecIfaceRe.MatchString(name) {
			return fmt.Errorf("bad interface name %q", name)
		}
		if dir == "in" {
			if v.rule.InIface != "" {
				return fmt.Errorf("two in interfaces")
			}
			v.rule.InIface = name
		} else {
			if v.rule.OutIface != "" {
				return fmt.Errorf("two out interfaces")
			}
			v.rule.OutIface = name
		}
		hasIface = true
		return nil
	}
	if len(a) > 0 && (a[0] == "in" || a[0] == "out") {
		v.rule.Direction = model.Direction(a[0])
		switch {
		case len(a) > 2 && a[1] == "on":
			if err := setIface(a[0], a[2]); err != nil {
				return v, err
			}
			a = a[3:]
		case v.rule.Route:
			return v, fmt.Errorf("a route rule needs an interface with in or out")
		default:
			a = a[1:]
		}
		if v.rule.Route && len(a) > 2 && (a[0] == "in" || a[0] == "out") && a[1] == "on" {
			if err := setIface(a[0], a[2]); err != nil {
				return v, err
			}
			a = a[3:]
		}
	}
	if len(a) > 0 && (a[0] == "log" || a[0] == "log-all") {
		v.rule.Log, a = model.Log(a[0]), a[1:]
	}
	if len(a)%2 != 0 {
		return v, fmt.Errorf("wrong number of arguments")
	}
	fromType, toType := "any", "any"
	seen := map[string]bool{}
	loc := ""
	for i := 0; i < len(a); i += 2 {
		key, val := a[i], a[i+1]
		if key != "port" && seen[key] {
			return v, fmt.Errorf("%s given twice", key)
		}
		seen[key] = true
		switch key {
		case "proto":
			p := model.Proto(val)
			if !slices.Contains(model.Protos(), p) {
				return v, fmt.Errorf("unsupported protocol %q", val)
			}
			v.rule.Proto = p
		case "from", "to":
			addr, typ, err := vecAddr(val)
			if err != nil {
				return v, err
			}
			if key == "from" {
				v.rule.Src, fromType, loc = addr, typ, "src"
			} else {
				v.rule.Dst, toType, loc = addr, typ, "dst"
			}
		case "port":
			if loc == "" {
				return v, fmt.Errorf("port needs from or to before it")
			}
			if seen["port "+loc] {
				return v, fmt.Errorf("two ports for one address")
			}
			seen["port "+loc] = true
			if err := vecPort(val); err != nil {
				return v, err
			}
			if loc == "src" {
				v.rule.SPort = val
			} else {
				v.rule.DPort = val
			}
		case "comment":
			if strings.Contains(val, "'") || !utf8.ValidString(val) {
				return v, fmt.Errorf("a comment may not contain ' and must be UTF-8")
			}
			v.rule.Comment = val
		default:
			return v, fmt.Errorf("unexpected %q", key)
		}
	}
	if !seen["from"] && !seen["to"] && !hasIface {
		return v, fmt.Errorf("need a from, to or interface clause")
	}
	switch {
	case fromType == "any" && toType == "any":
		v.iptype = model.FamilyBoth
	case fromType != "any" && toType != "any" && fromType != toType:
		return v, fmt.Errorf("mixed IP versions for from and to")
	case fromType != "any":
		v.iptype = model.Family(fromType)
	default:
		v.iptype = model.Family(toType)
	}
	ipv4Only := v.rule.Proto == model.ProtoIPv6 || v.rule.Proto == model.ProtoIGMP
	if ipv4Only && v.iptype == model.FamilyBoth {
		v.iptype = model.FamilyV4
	}
	if ipv4Only && v.iptype == model.FamilyV6 {
		return v, fmt.Errorf("protocol %s has no IPv6 form", v.rule.Proto)
	}
	portless := !slices.Contains([]model.Proto{model.ProtoTCP, model.ProtoUDP, model.ProtoAny}, v.rule.Proto)
	if portless && (v.rule.DPort != model.Any || v.rule.SPort != model.Any) {
		return v, fmt.Errorf("protocol %s takes no port", v.rule.Proto)
	}
	if multiPort(v.rule) && v.rule.Proto != model.ProtoTCP && v.rule.Proto != model.ProtoUDP {
		return v, fmt.Errorf("multiple ports need tcp or udp")
	}
	return v, nil
}

// vecAddr checks an address argument: any, or an address or CIDR without a
// zone. It returns the address as UFW stores it before normalizing (lower
// case, any as 0.0.0.0/0) and its type: any, v4 or v6. A literal
// 0.0.0.0/0 is v4 and ::/0 is v6, as in UFW.
func vecAddr(s string) (string, string, error) {
	s = strings.ToLower(s)
	if s == model.Any {
		return "0.0.0.0/0", "any", nil
	}
	addr, _, hasMask := strings.Cut(s, "/")
	a, err := netip.ParseAddr(addr)
	if err == nil && hasMask {
		_, err = netip.ParsePrefix(s)
	}
	if err != nil || a.Zone() != "" {
		return "", "", fmt.Errorf("bad address %q", s)
	}
	if a.Is4() {
		return s, string(model.FamilyV4), nil
	}
	return s, string(model.FamilyV6), nil
}

// vecPort checks a port argument as UFW's parser and set_port do once
// service names are excluded.
func vecPort(s string) error {
	bad := fmt.Errorf("bad port %q", s)
	if !vecPortRe.MatchString(s) || strings.Count(s, ",")+strings.Count(s, ":") > 14 {
		return bad
	}
	for _, p := range strings.Split(s, ",") {
		switch {
		case vecRangeRe.MatchString(p):
			lo, hi, _ := strings.Cut(p, ":")
			a, _ := strconv.Atoi(lo)
			b, _ := strconv.Atoi(hi)
			if a < 1 || b > 65535 || a >= b {
				return bad
			}
		case vecNumberRe.MatchString(p):
			if n, _ := strconv.Atoi(p); n < 1 || n > 65535 {
				return bad
			}
		default:
			return bad
		}
	}
	return nil
}

// forFamily is the rule as UFW writes it into one family's file: set_v6
// turns any into the family's form, normalize gives the addresses their
// standard form and sorts the ports, and Raw is the tuple line.
func forFamily(t model.Tuple, fam model.Family) (model.Tuple, error) {
	v6 := fam == model.FamilyV6
	t.Family = fam
	for _, a := range []*string{&t.Src, &t.Dst} {
		switch {
		case v6 && (*a == model.Any || *a == "0.0.0.0/0"):
			*a = "::/0"
		case !v6 && (*a == model.Any || *a == "::/0"):
			*a = "0.0.0.0/0"
		}
	}
	var err error
	if t, err = normalizeTuple(t); err != nil {
		return t, err
	}
	t.Raw = tupleLine(t)
	return t, nil
}

// normalizeTuple is UFWRule.normalize for a rule of t.Family: addresses as
// util.normalize_address writes them, port lists in human_sort order.
func normalizeTuple(t model.Tuple) (model.Tuple, error) {
	v6 := t.Family == model.FamilyV6
	for _, a := range []*string{&t.Src, &t.Dst} {
		n, err := normalizeAddr(*a, v6)
		if err != nil {
			return t, err
		}
		*a = n
	}
	t.DPort, t.SPort = sortPorts(t.DPort), sortPorts(t.SPort)
	return t, nil
}

// normalizeAddr is util.normalize_address: a host prefix is dropped, the
// address takes its standard text form, and an IPv4 network is masked
// (IPv6 keeps its host bits). A dotted IPv4 netmask becomes a prefix
// length when it is contiguous.
func normalizeAddr(s string, v6 bool) (string, error) {
	addr, mask, hasMask := strings.Cut(s, "/")
	if hasMask && ((v6 && mask == "128") || (!v6 && (mask == "32" || mask == "255.255.255.255"))) {
		hasMask = false
	}
	a, err := netip.ParseAddr(addr)
	if err != nil || a.Is4() == v6 {
		return "", fmt.Errorf("predict: address %q is not valid in this family", s)
	}
	if !hasMask {
		return a.String(), nil
	}
	if !v6 && strings.Contains(mask, ".") {
		m, err := netip.ParseAddr(mask)
		if err != nil || !m.Is4() {
			return "", fmt.Errorf("predict: netmask %q is not valid", mask)
		}
		mb := m.As4()
		mv := binary.BigEndian.Uint32(mb[:])
		ones := bits.LeadingZeros32(^mv)
		if ones == 32 || mv == ^uint32(0)<<(32-ones) {
			mask = strconv.Itoa(ones)
		} else {
			ab := a.As4()
			binary.BigEndian.PutUint32(ab[:], binary.BigEndian.Uint32(ab[:])&mv)
			return netip.AddrFrom4(ab).String() + "/" + mask, nil
		}
	}
	p, err := netip.ParsePrefix(a.String() + "/" + mask)
	if err != nil {
		return "", fmt.Errorf("predict: address %q is not valid", s)
	}
	if v6 {
		return p.String(), nil
	}
	return p.Masked().String(), nil
}

// sortPorts sorts a port list as util.human_sort does: by the first port
// of each entry, a single port before a range starting at it, then by the
// range's end.
func sortPorts(s string) string {
	if s == model.Any || !strings.Contains(s, ",") {
		return s
	}
	ports := strings.Split(s, ",")
	slices.SortStableFunc(ports, func(x, y string) int {
		xa, xb, xr := strings.Cut(x, ":")
		ya, yb, yr := strings.Cut(y, ":")
		if c := atoiCmp(xa, ya); c != 0 {
			return c
		}
		switch {
		case xr != yr && !xr:
			return -1
		case xr != yr:
			return 1
		}
		return atoiCmp(xb, yb)
	})
	return strings.Join(ports, ",")
}

func atoiCmp(a, b string) int {
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)
	return x - y
}
