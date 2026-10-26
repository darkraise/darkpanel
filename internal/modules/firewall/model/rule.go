// Package model is the firewall's rule model (spec 2.2): the rules UFW can
// represent, their validation, the tags that mark Darkpanel's rules, the
// tuples UFW stores rules as and the argument vectors that write them.
package model

import (
	"cmp"
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Action is what a rule does with a matching packet.
type Action string

// The actions.
const (
	ActionAllow  Action = "allow"
	ActionDeny   Action = "deny"
	ActionLimit  Action = "limit"
	ActionReject Action = "reject"
)

// Direction is the traffic a rule governs: incoming, outgoing or routed.
type Direction string

// The directions.
const (
	DirIn    Direction = "in"
	DirOut   Direction = "out"
	DirRoute Direction = "route"
)

// Proto is a rule's protocol.
type Proto string

// The protocols UFW supports. The port-less ones take no ports, and ipv6
// and igmp exist only in IPv4.
const (
	ProtoTCP  Proto = "tcp"
	ProtoUDP  Proto = "udp"
	ProtoAny  Proto = "any"
	ProtoAH   Proto = "ah"
	ProtoESP  Proto = "esp"
	ProtoGRE  Proto = "gre"
	ProtoIPv6 Proto = "ipv6"
	ProtoIGMP Proto = "igmp"
)

// Protos returns every protocol in the order of the JSON Schema enum; the
// model, the schema and the tuple parser share this one enumeration.
func Protos() []Proto {
	return []Proto{ProtoTCP, ProtoUDP, ProtoAny, ProtoAH, ProtoESP, ProtoGRE, ProtoIPv6, ProtoIGMP}
}

// Family is the IP family a rule applies to.
type Family string

// The families.
const (
	FamilyV4   Family = "v4"
	FamilyV6   Family = "v6"
	FamilyBoth Family = "both"
)

// Log is a rule's logging.
type Log string

// The log settings. The empty value means LogOff.
const (
	LogOff Log = "off"
	LogOn  Log = "log"
	LogAll Log = "log-all"
)

// Origin says who made a rule: Darkpanel (it carries the tag) or a person.
type Origin string

// The origins. The empty value means OriginDarkpanel.
const (
	OriginDarkpanel Origin = "darkpanel"
	OriginManual    Origin = "manual"
)

// Any is the From or To value that matches every address of the family.
const Any = "any"

// The schema patterns, which the struct tags repeat (a struct tag cannot
// name a constant). They are the only protection of the argument vectors,
// since ufw has no "--": no value may start with "-".
const (
	idPattern      = `^([0-9A-HJKMNP-TV-Z]{26}|manual-[0-9a-f]{16})$`
	addrPattern    = `^(any|[0-9A-Fa-f:./]{2,43})$`
	portPattern    = `^[1-9][0-9]{0,4}(:[1-9][0-9]{0,4})?(,[1-9][0-9]{0,4}(:[1-9][0-9]{0,4})?)*$`
	ifacePattern   = `^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0,14}$`
	commentPattern = `^[A-Za-z0-9 :._/-]{0,64}$`
)

var (
	ulidRe     = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	manualIDRe = regexp.MustCompile(`^manual-[0-9a-f]{16}$`)
	portRe     = regexp.MustCompile(portPattern)
	ifaceRe    = regexp.MustCompile(ifacePattern)
	commentRe  = regexp.MustCompile(commentPattern)
)

// maxPortSlots is iptables' multiport limit: a port takes one slot, a
// range two.
const maxPortSlots = 15

// Rule is one UFW rule (spec 2.2). Raw is read-only: it holds the tuple of
// a manual rule the model cannot express, and Validate refuses it in input.
type Rule struct {
	ID        string    `json:"id,omitempty" jsonschema:"pattern=^([0-9A-HJKMNP-TV-Z]{26}|manual-[0-9a-f]{16})$" jsonschema_description:"Rule id: the ULID in a Darkpanel rule's tag, or manual- and 16 hex digits derived from a manual rule's tuple."`
	Action    Action    `json:"action" jsonschema:"enum=allow,enum=deny,enum=limit,enum=reject" jsonschema_description:"What the rule does; limit only for incoming rules."`
	Direction Direction `json:"direction" jsonschema:"enum=in,enum=out,enum=route" jsonschema_description:"Incoming, outgoing or routed (forwarded) traffic."`
	Proto     Proto     `json:"proto" jsonschema:"enum=tcp,enum=udp,enum=any,enum=ah,enum=esp,enum=gre,enum=ipv6,enum=igmp" jsonschema_description:"Protocol; ah, esp, gre, ipv6 and igmp take no ports, ipv6 and igmp only with family v4."`
	From      string    `json:"from" jsonschema:"pattern=^(any|[0-9A-Fa-f:./]{2\\,43})$" jsonschema_description:"Source: any, an address or a CIDR in canonical form."`
	FromPort  string    `json:"fromPort,omitempty" jsonschema:"pattern=^[1-9][0-9]{0\\,4}(:[1-9][0-9]{0\\,4})?(\\,[1-9][0-9]{0\\,4}(:[1-9][0-9]{0\\,4})?)*$" jsonschema_description:"Source port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp."`
	To        string    `json:"to" jsonschema:"pattern=^(any|[0-9A-Fa-f:./]{2\\,43})$" jsonschema_description:"Destination: any, an address or a CIDR in canonical form."`
	ToPort    string    `json:"toPort,omitempty" jsonschema:"pattern=^[1-9][0-9]{0\\,4}(:[1-9][0-9]{0\\,4})?(\\,[1-9][0-9]{0\\,4}(:[1-9][0-9]{0\\,4})?)*$" jsonschema_description:"Destination port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp."`
	InIface   string    `json:"inIface,omitempty" jsonschema:"pattern=^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0\\,14}$" jsonschema_description:"Interface the packet arrives on; only for in and route rules."`
	OutIface  string    `json:"outIface,omitempty" jsonschema:"pattern=^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0\\,14}$" jsonschema_description:"Interface the packet leaves on; only for out and route rules."`
	Family    Family    `json:"family" jsonschema:"enum=v4,enum=v6,enum=both" jsonschema_description:"IP family; both only when from and to are any."`
	Log       Log       `json:"log,omitempty" jsonschema:"enum=off,enum=log,enum=log-all" jsonschema_description:"Logging; off when omitted."`
	Comment   string    `json:"comment,omitempty" jsonschema:"pattern=^[A-Za-z0-9 :._/-]{0\\,64}$" jsonschema_description:"Label after the Darkpanel tag, or a manual rule's comment."`
	Origin    Origin    `json:"origin,omitempty" jsonschema:"enum=darkpanel,enum=manual" jsonschema_description:"Who made the rule; darkpanel when omitted."`
	Raw       string    `json:"raw,omitempty" jsonschema:"readOnly=true" jsonschema_description:"Read-only: the tuple of a manual rule the model cannot express; never accepted in input."`
}

// Validate checks r against the grammar of spec 2.2. A refusal is an
// *errcode.Error with code request.invalid_input and one field error per
// failing field, its path a JSON pointer relative to the rule ("/toPort");
// a caller that embeds rules prefixes the paths. Addresses and port lists
// must be canonical: Normalize first.
func (r Rule) Validate() error {
	if f := r.problems(true); len(f) > 0 {
		return &errcode.Error{Code: errcode.InvalidInput, Detail: "the rule is outside the grammar UFW can represent", Fields: f}
	}
	return nil
}

// Normalize returns r with the defaults filled in (Log off, Origin
// darkpanel), addresses in canonical form (the network address, no host
// prefix length, a zero-length prefix as any) and port lists in UFW's
// order. Values it cannot parse are left for Validate to refuse.
func (r Rule) Normalize() Rule {
	if r.Log == "" {
		r.Log = LogOff
	}
	if r.Origin == "" {
		r.Origin = OriginDarkpanel
	}
	r.From = canonicalAddr(r.From)
	r.To = canonicalAddr(r.To)
	r.FromPort = sortPorts(r.FromPort)
	r.ToPort = sortPorts(r.ToPort)
	return r
}

// problems lists r's field errors in field order. With input false it
// skips the checks that apply only to what a client sends (the comment's
// pattern and Raw), which is how a rule read from UFW's files is judged
// representable. A check that depends on another field runs only when
// that field is valid, so one mistake yields one error.
func (r Rule) problems(input bool) []errcode.FieldError {
	var out []errcode.FieldError
	add := func(path, msg string) { out = append(out, errcode.FieldError{Path: path, Message: msg}) }

	origin := r.Origin
	if origin == "" {
		origin = OriginDarkpanel
	}
	switch {
	case r.ID == "":
	case origin == OriginDarkpanel && !ulidRe.MatchString(r.ID):
		add("/id", "a Darkpanel rule's id must be a ULID")
	case origin == OriginManual && !manualIDRe.MatchString(r.ID):
		add("/id", "a manual rule's id must be manual- and 16 lower-case hex digits")
	}

	dirOK := slices.Contains([]Direction{DirIn, DirOut, DirRoute}, r.Direction)
	switch {
	case !slices.Contains([]Action{ActionAllow, ActionDeny, ActionLimit, ActionReject}, r.Action):
		add("/action", "must be allow, deny, limit or reject")
	case r.Action == ActionLimit && dirOK && r.Direction != DirIn:
		add("/action", "limit applies only to incoming rules")
	}
	if !dirOK {
		add("/direction", "must be in, out or route")
	}

	famOK := slices.Contains([]Family{FamilyV4, FamilyV6, FamilyBoth}, r.Family)
	protoOK := slices.Contains(Protos(), r.Proto)
	switch {
	case !protoOK:
		add("/proto", "must be one of tcp, udp, any, ah, esp, gre, ipv6, igmp")
	case (r.Proto == ProtoIPv6 || r.Proto == ProtoIGMP) && famOK && r.Family != FamilyV4:
		add("/proto", "ipv6 and igmp exist only with family v4")
	}

	fromFam, fromOK := checkAddr(r.From, r.Family, famOK, "/from", add)
	if msg := portProblem(r.FromPort, r.Proto, protoOK); msg != "" {
		add("/fromPort", msg)
	}
	toFam, toOK := checkAddr(r.To, r.Family, famOK, "/to", add)
	if msg := portProblem(r.ToPort, r.Proto, protoOK); msg != "" {
		add("/toPort", msg)
	}

	checkIface(r.InIface, "/inIface", dirOK && r.Direction == DirOut, "an in-interface applies only to in and route rules", add)
	checkIface(r.OutIface, "/outIface", dirOK && r.Direction == DirIn, "an out-interface applies only to out and route rules", add)

	switch {
	case !famOK:
		add("/family", "must be v4, v6 or both")
	case r.Family == FamilyBoth && fromOK && toOK && (fromFam != "" || toFam != ""):
		add("/family", "both applies only when from and to are any")
	}
	if r.Log != "" && !slices.Contains([]Log{LogOff, LogOn, LogAll}, r.Log) {
		add("/log", "must be off, log or log-all")
	}
	if input && (!commentRe.MatchString(r.Comment) || strings.TrimSpace(r.Comment) != r.Comment) {
		add("/comment", "at most 64 letters, digits, spaces and :._/- without a leading or trailing space")
	}
	if r.Origin != "" && r.Origin != OriginDarkpanel && r.Origin != OriginManual {
		add("/origin", "must be darkpanel or manual")
	}
	if input && r.Raw != "" {
		add("/raw", "is read-only")
	}
	return out
}

// checkAddr checks one of From and To and returns its family ("" for any)
// and whether it parsed.
func checkAddr(s string, fam Family, famOK bool, path string, add func(path, msg string)) (Family, bool) {
	if s == Any {
		return "", true
	}
	p, err := parseAddr(s)
	if err != nil {
		add(path, "must be any, an address or a CIDR")
		return "", false
	}
	if canonicalAddr(s) != s {
		add(path, "must be canonical: the network address, no host prefix length, any instead of a zero-length prefix")
		return "", false
	}
	af := FamilyV6
	if p.Addr().Is4() {
		af = FamilyV4
	}
	if famOK && fam != FamilyBoth && fam != af {
		add(path, "its family differs from the rule's family")
	}
	return af, true
}

// parseAddr parses an address or a CIDR without a zone.
func parseAddr(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		return netip.ParsePrefix(s)
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	if a.Zone() != "" {
		return netip.Prefix{}, errZone
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

var errZone = errors.New("model: address has a zone")

// canonicalAddr renders s as UFW stores it once normalized: the network
// address, a full-length prefix as a plain address, a zero-length prefix
// as any. An unparsable s is returned unchanged.
func canonicalAddr(s string) string {
	if s == Any {
		return s
	}
	p, err := parseAddr(s)
	if err != nil {
		return s
	}
	switch p.Bits() {
	case 0:
		return Any
	case p.Addr().BitLen():
		return p.Addr().String()
	}
	return p.Masked().String()
}

// portProblem checks a FromPort or ToPort value against the grammar and the
// protocol, returning "" when it is valid.
func portProblem(s string, proto Proto, protoOK bool) string {
	if s == "" {
		return ""
	}
	if !portRe.MatchString(s) {
		return "must be a port, a range a:b or a comma list of them"
	}
	slots := 0
	for _, e := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(e, ":")
		a, _ := strconv.Atoi(lo)
		if a > 65535 {
			return "ports are within 1-65535"
		}
		slots++
		if isRange {
			b, _ := strconv.Atoi(hi)
			if b > 65535 {
				return "ports are within 1-65535"
			}
			if a >= b {
				return "a range a:b needs a below b"
			}
			slots++
		}
	}
	if slots > maxPortSlots {
		return "a port list uses at most 15 multiport slots (a range takes two)"
	}
	if sortPorts(s) != s {
		return "a port list must be in ascending order"
	}
	if !protoOK {
		return ""
	}
	multi := strings.ContainsAny(s, ",:")
	switch {
	case proto == ProtoTCP || proto == ProtoUDP:
	case proto == ProtoAny && !multi:
	case proto == ProtoAny:
		return "a range or list needs proto tcp or udp"
	default:
		return "this protocol takes no ports"
	}
	return ""
}

// sortPorts orders a comma list as UFW's human_sort does: by the first
// port, a single port before a range starting at it, then by the range's
// end. A list it cannot parse is returned unchanged.
func sortPorts(s string) string {
	if !strings.Contains(s, ",") {
		return s
	}
	type entry struct {
		text    string
		lo, hi  int
		isRange bool
	}
	var es []entry
	for _, e := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(e, ":")
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 != nil || (isRange && err2 != nil) {
			return s
		}
		es = append(es, entry{e, a, b, isRange})
	}
	slices.SortStableFunc(es, func(x, y entry) int {
		if c := cmp.Compare(x.lo, y.lo); c != 0 {
			return c
		}
		if x.isRange != y.isRange {
			if x.isRange {
				return 1
			}
			return -1
		}
		return cmp.Compare(x.hi, y.hi)
	})
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.text
	}
	return strings.Join(parts, ",")
}

// checkIface checks an interface name and whether the direction allows it.
func checkIface(s, path string, wrongDir bool, dirMsg string, add func(path, msg string)) {
	switch {
	case s == "":
	case !ifaceRe.MatchString(s):
		add(path, "must be an interface name: 1-15 letters, digits and _.+- starting with a letter or digit")
	case wrongDir:
		add(path, dirMsg)
	}
}

// TagPrefix starts the comment of every rule Darkpanel writes.
const TagPrefix = "darkpanel:"

// Tag is the comment Darkpanel writes on a rule: the prefix, the rule's
// ULID and, when label is not empty, a space and the label.
func Tag(id, label string) string {
	if label == "" {
		return TagPrefix + id
	}
	return TagPrefix + id + " " + label
}

// ParseTag splits a rule's comment into the id and label Tag wrote. ok is
// false for a comment without the prefix and a ULID, which marks a manual
// rule; the label is returned as written.
func ParseTag(comment string) (id, label string, ok bool) {
	rest, found := strings.CutPrefix(comment, TagPrefix)
	if !found || len(rest) < 26 || !ulidRe.MatchString(rest[:26]) {
		return "", "", false
	}
	id, rest = rest[:26], rest[26:]
	if rest == "" {
		return id, "", true
	}
	label, found = strings.CutPrefix(rest, " ")
	if !found {
		return "", "", false
	}
	return id, label, true
}
