// Package link holds the rules of the tailscale.link guard (spec 4.1).
package link

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	// GuardID is the id of the overridable guard that protects flows from tailscale changes.
	GuardID = "tailscale.link"
	// Interface is the network interface tailscaled creates.
	Interface = "tailscale0"
)

var (
	tailscaleV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

const (
	okMessage = "no protected flow depends on what this change removes"
	sshInUse  = "Tailscale SSH is on and a session may use it"
	sshNoPath = "Tailscale SSH is on and no request flow is known to arrive another way"
)

// Flow is one protected flow (spec 2.3) as the tailscale.link guard classifies it.
type Flow struct {
	Label    string     // "ssh", "panel", "agent link", "agent peer", "controller session", "controller link"
	Inbound  bool       // the flow arrives at this host; false: this host opens it
	Local    netip.Addr // this host's address of the flow
	Remote   netip.Addr // the other end
	OutIface string     // the interface the kernel routes Remote through from Local ("" when unknown)
	Default  bool       // the route to Remote is a default route (0.0.0.0/0 or ::/0)
	Request  bool       // this flow carries the request being planned
	Unknown  string     // non-empty: the flow could not be classified; the guard fails with this reason
}

// Prefs are the node's preferences the rules read.
type Prefs struct {
	WantRunning       bool
	AcceptRoutes      bool
	ShieldsUp         bool
	RunSSH            bool
	ExitNode          string // "" when none
	AdvertiseExitNode bool
	AdvertiseRoutes   []netip.Prefix // without 0.0.0.0/0 and ::/0
}

// Change is what an action would do to the node.
type Change struct {
	Kind  string // "prefs" | "logout" | "relogin"
	After Prefs  // Kind "prefs": the node's preferences after the change (current ones with the masked fields applied)
}

// Check evaluates spec 4.1's guard for change c on a node whose current preferences are cur:
//   - depends on Tailscale: OutIface == Interface or Local is a Tailscale address (100.64.0.0/10, fd7a:115c:a1e0::/48);
//   - "logout", "relogin", or After.WantRunning false: fails when any flow depends on Tailscale or cur.RunSSH;
//   - After.ShieldsUp true (cur false): fails when an Inbound flow depends on Tailscale or cur.RunSSH;
//   - After.RunSSH false (cur true): fails unless some Request flow does not depend on Tailscale;
//   - After.AcceptRoutes false (cur true): fails when a flow's OutIface is Interface, Default is false and Remote is not a Tailscale address;
//   - After.ExitNode differs from cur.ExitNode (cur non-empty): fails when a flow's OutIface is Interface and Default is true;
//   - a prefix in cur.AdvertiseRoutes missing from After: fails when an Inbound flow with OutIface Interface has Local inside it;
//   - cur.AdvertiseExitNode true, After false: fails when an Inbound flow with OutIface Interface has a Local that is neither a Tailscale address nor inside any of cur.AdvertiseRoutes;
//   - a flow with Unknown set fails whatever the change;
//   - a Kind other than "prefs", "logout" or "relogin" fails, so a caller's typo cannot skip the rules.
//
// The result is core.Guard{ID: GuardID, Overridable: true, OK, Message}; Message names every failing flow by
// Label, Remote and the rule (one sentence each, joined with "; "), or "no protected flow depends on what this change removes".
func Check(cur Prefs, c Change, flows []Flow) core.Guard {
	known := make([]Flow, 0, len(flows))
	for _, f := range flows {
		if f.Unknown == "" {
			known = append(known, f)
		}
	}

	var sentences []string
	switch c.Kind {
	case "logout", "relogin":
		sentences = stopRule(cur, known)
	case "prefs":
		after := c.After
		if !after.WantRunning {
			sentences = append(sentences, stopRule(cur, known)...)
		}
		sentences = append(sentences, shieldsUpRule(cur, after, known)...)
		sentences = append(sentences, runSSHOffRule(cur, after, known)...)
		sentences = append(sentences, acceptRoutesOffRule(cur, after, known)...)
		sentences = append(sentences, exitNodeRule(cur, after, known)...)
		sentences = append(sentences, advertisedRoutesRule(cur, after, known)...)
		sentences = append(sentences, advertisedExitNodeRule(cur, after, known)...)
	default:
		sentences = append(sentences, fmt.Sprintf("the change kind %q is not prefs, logout or relogin, so no rule could check it", c.Kind))
	}
	sentences = append(sentences, unknownRule(flows)...)

	sentences = dedupe(sentences)
	g := core.Guard{ID: GuardID, Overridable: true, OK: len(sentences) == 0, Message: okMessage}
	if !g.OK {
		g.Message = strings.Join(sentences, "; ")
	}
	return g
}

// IsTailscaleAddr reports whether a is inside 100.64.0.0/10 or fd7a:115c:a1e0::/48 (after Unmap).
func IsTailscaleAddr(a netip.Addr) bool {
	a = a.Unmap()
	return tailscaleV4.Contains(a) || tailscaleV6.Contains(a)
}

func dependsOnTailscale(f Flow) bool {
	return f.OutIface == Interface || IsTailscaleAddr(f.Local)
}

func sentence(f Flow, phrase string) string {
	remote := "an unknown address"
	if f.Remote.IsValid() {
		remote = f.Remote.String()
	}
	return fmt.Sprintf("%s flow with %s %s", f.Label, remote, phrase)
}

func stopRule(cur Prefs, flows []Flow) []string {
	var s []string
	for _, f := range flows {
		if dependsOnTailscale(f) {
			s = append(s, sentence(f, "depends on Tailscale"))
		}
	}
	if cur.RunSSH {
		s = append(s, sshInUse)
	}
	return s
}

func shieldsUpRule(cur, after Prefs, flows []Flow) []string {
	if !after.ShieldsUp || cur.ShieldsUp {
		return nil
	}
	var s []string
	for _, f := range flows {
		if f.Inbound && dependsOnTailscale(f) {
			s = append(s, sentence(f, "arrives over Tailscale and shields-up would block it"))
		}
	}
	if cur.RunSSH {
		s = append(s, sshInUse)
	}
	return s
}

func runSSHOffRule(cur, after Prefs, flows []Flow) []string {
	if !cur.RunSSH || after.RunSSH {
		return nil
	}
	for _, f := range flows {
		if f.Request && !dependsOnTailscale(f) {
			return nil
		}
	}
	return []string{sshNoPath}
}

func acceptRoutesOffRule(cur, after Prefs, flows []Flow) []string {
	if !cur.AcceptRoutes || after.AcceptRoutes {
		return nil
	}
	var s []string
	for _, f := range flows {
		if f.OutIface == Interface && !f.Default && !IsTailscaleAddr(f.Remote) {
			s = append(s, sentence(f, "uses a route accepted from the tailnet"))
		}
	}
	return s
}

func exitNodeRule(cur, after Prefs, flows []Flow) []string {
	if cur.ExitNode == "" || after.ExitNode == cur.ExitNode {
		return nil
	}
	var s []string
	for _, f := range flows {
		if f.OutIface == Interface && f.Default {
			s = append(s, sentence(f, "leaves through the exit node"))
		}
	}
	return s
}

func advertisedRoutesRule(cur, after Prefs, flows []Flow) []string {
	kept := make([]netip.Prefix, 0, len(after.AdvertiseRoutes))
	for _, p := range after.AdvertiseRoutes {
		kept = append(kept, p.Masked())
	}
	var s []string
	for _, p := range cur.AdvertiseRoutes {
		if slices.Contains(kept, p.Masked()) {
			continue
		}
		for _, f := range flows {
			if f.Inbound && f.OutIface == Interface && p.Contains(f.Local) {
				s = append(s, sentence(f, "arrives through the advertised route "+p.Masked().String()))
			}
		}
	}
	return s
}

func advertisedExitNodeRule(cur, after Prefs, flows []Flow) []string {
	if !cur.AdvertiseExitNode || after.AdvertiseExitNode {
		return nil
	}
	var s []string
	for _, f := range flows {
		if f.Inbound && f.OutIface == Interface && !IsTailscaleAddr(f.Local) && !insideAny(cur.AdvertiseRoutes, f.Local) {
			s = append(s, sentence(f, "arrives through the advertised exit node"))
		}
	}
	return s
}

func unknownRule(flows []Flow) []string {
	var s []string
	for _, f := range flows {
		if f.Unknown != "" {
			s = append(s, sentence(f, "cannot be classified: "+f.Unknown))
		}
	}
	return s
}

func insideAny(prefixes []netip.Prefix, a netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func dedupe(sentences []string) []string {
	seen := make(map[string]bool, len(sentences))
	out := sentences[:0]
	for _, s := range sentences {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
