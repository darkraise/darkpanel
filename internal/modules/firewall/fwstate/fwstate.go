// Package fwstate holds the firewall's shared state types (spec 2.1-2.5):
// UFW's configured state, the loaded netfilter state, transitions, packets
// and verdicts. The configured-state reader, the loaded-state reader, the
// predictor, the evaluator, the guard and snapshots all exchange these.
package fwstate

import (
	"net/netip"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// Family is an IP family.
type Family string

// The families.
const (
	V4 Family = "v4"
	V6 Family = "v6"
)

// StateFiles are UFW's state files a snapshot holds (spec 2.5), in this order.
var StateFiles = []string{
	"/etc/ufw/user.rules", "/etc/ufw/user6.rules",
	"/etc/ufw/before.rules", "/etc/ufw/before6.rules",
	"/etc/ufw/after.rules", "/etc/ufw/after6.rules",
	"/etc/ufw/ufw.conf", "/etc/default/ufw",
}

// MaxFileSize is the largest state file a snapshot records (spec 2.5).
const MaxFileSize = 4 << 20

// Config is UFW's configured state (spec 2.1).
type Config struct {
	Enabled        bool   // ufw.conf ENABLED=yes
	LogLevel       string // ufw.conf LOGLEVEL: off, low, medium, high or full
	IPv6           bool   // /etc/default/ufw IPV6=yes
	ManageBuiltins bool   // MANAGE_BUILTINS=yes
	DefaultInput   string // DEFAULT_INPUT_POLICY: ACCEPT, DROP or REJECT
	DefaultOutput  string
	DefaultForward string
	SysctlFile     string                       // IPT_SYSCTL, absolute
	Rules4, Rules6 []model.Tuple                // the ### tuple ### lines of user.rules and user6.rules, in file order
	Files          map[string]core.RestoreEntry // StateFiles plus /etc/ufw/sysctl.conf, by absolute path; Present false when absent
	Sysctl         []SysctlSetting              // SysctlFile's settings, in file order
	Unsupported    []string                     // why the configuration cannot be predicted (2.1); empty when supported
}

// SysctlSetting is one key = value line of UFW's sysctl file, the key in
// /proc/sys form ("net/ipv4/ip_forward").
type SysctlSetting struct{ Key, Value string }

// Loaded is the loaded state (spec 2.1): what the kernel holds now, or what
// a prediction says it will hold.
type Loaded struct {
	Variant     string             // "nft" or "legacy": the iptables variant UFW uses
	V4, V6      *netfilter.Ruleset // that variant's five tables; V6 nil when ip6tables is absent
	Sysctl      map[string]string  // supported keys in /proc/sys form to value, global and per present interface
	Unsupported []string           // foreign netfilter hooks found (2.1)
}

// Transition is what an action makes UFW do (spec 2.3).
type Transition string

// The transitions.
const (
	TransNone    Transition = "none"
	TransReload  Transition = "reload" // a rule change with reload, or a reload after a file change
	TransEnable  Transition = "enable"
	TransDisable Transition = "disable"
)

// Packet is one packet tuple the evaluator walks (spec 2.3).
type Packet struct {
	Family   Family
	Path     string // "input", "forward" or "output": the hook chain after routing
	InIface  string // input and forward
	OutIface string // forward and output
	Src, Dst netip.Addr
	SrcPort  uint16
	DstPort  uint16
	Proto    string // "tcp", "udp", "icmp", ...
	CtState  string // conntrack state: "NEW", "ESTABLISHED", ...
	Mark     uint32
}

// Verdict is what the evaluator decided.
type Verdict string

// The verdicts.
const (
	Accept  Verdict = "accept"
	Drop    Verdict = "drop"
	Reject  Verdict = "reject"
	Unknown Verdict = "unknown"
)

// Result is a verdict with the reason and the rules that decided it.
type Result struct {
	Verdict Verdict
	Reason  string   // why, for unknown and for a drop or reject: the deciding rule
	Trace   []string // "<table>/<chain>#<rule index>" in walk order
	// Decided names the rule or policy that ended the walk: a rule as
	// "<table>/<chain>#<rule index>", where the chain is the one that holds
	// the rule, or a built-in chain's policy as "<table>/<chain> policy
	// ACCEPT" or "<table>/<chain> policy DROP". It is empty for an unknown
	// verdict caused by a whole hook or by an invalid input.
	Decided string
}

// Router says where a packet goes after nat PREROUTING rewrote its
// destination: the new path and out-interface.
type Router func(p Packet) (path, outIface string, err error)

// ChainDiff is one chain whose rules differ between two loaded states.
type ChainDiff struct {
	Family       Family
	Table, Chain string
	PolicyA      string // "" when equal or not a built-in chain
	PolicyB      string
	OnlyA, OnlyB []string // netfilter rule keys present on one side only, in order
	OrderDiffers bool     // same keys, different order
}
