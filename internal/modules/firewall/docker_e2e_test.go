package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const deeBinding = `{"selector":"web","hostPort":8080,"proto":"tcp"}`

// deePlanApply plans and applies action name with input raw; an error of the
// plan or of the apply is returned, not fatal.
func deePlanApply(t *testing.T, eng *lifecycle.Engine, name, raw string) (core.PlanView, core.ChangeView, error) {
	t.Helper()
	ctx := context.Background()
	pv, err := eng.Plan(ctx, eeUser, core.ActionRef{Name: name, Version: 1}, json.RawMessage(raw), core.PlanOptions{})
	if err != nil {
		return core.PlanView{}, core.ChangeView{}, err
	}
	cv, err := eng.Apply(ctx, eeUser, pv.PlanID, core.ApplyOptions{})
	return pv, cv, err
}

// deeConfirm applies action name and confirms the pending change.
func deeConfirm(t *testing.T, eng *lifecycle.Engine, name, raw string) {
	t.Helper()
	_, cv, err := deePlanApply(t, eng, name, raw)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("%s %s = %+v, %v; want pending", name, raw, cv, err)
	}
	if cv, err = eng.Confirm(context.Background(), eeUser, cv.ID); err != nil || cv.State != core.StateConfirmed {
		t.Fatalf("confirm %s = %+v, %v", name, cv, err)
	}
}

// deeHasLine reports whether the plan's diff has a line with exactly this text.
func deeHasLine(pv core.PlanView, text string) bool {
	return slices.ContainsFunc(pv.Diff, func(l core.DiffLine) bool { return l.Text == text })
}

// deeRead reads the resource name of the host's module into a T.
func deeRead[T any](t *testing.T, h *deeHost, name string) T {
	t.Helper()
	var out T
	for _, r := range (module{f: h.fw()}).Manifest().Resources {
		if r.Name != name {
			continue
		}
		raw, err := r.Read(context.Background(), h.env())
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return out
	}
	t.Fatalf("the module has no resource %s", name)
	return out
}

// deeInternet is the internet class's verdict of the exposure row.
func deeInternet(t *testing.T, h *deeHost, proto, addr string, port uint16) string {
	t.Helper()
	for _, row := range deeRead[ExposureView](t, h, "firewall.exposure").Rows {
		if row.Proto != proto || row.Addr != addr || row.Port != port {
			continue
		}
		for _, c := range row.Classes {
			if c.Class == "internet" {
				return c.Verdict
			}
		}
		t.Fatalf("row %s %s:%d has no internet class: %+v", proto, addr, port, row.Classes)
	}
	t.Fatalf("no exposure row for %s %s:%d", proto, addr, port)
	return ""
}

// deeOwnedChains lists the darkpanel-* and dp-b-* chains of the filter table
// of either family.
func deeOwnedChains(h *deeHost) []string {
	var out []string
	for _, rs := range []*netfilter.Ruleset{h.loaded.V4, h.loaded.V6} {
		if rs == nil || rs.Table("filter") == nil {
			continue
		}
		for _, c := range rs.Table("filter").Chains {
			if strings.HasPrefix(c.Name, "darkpanel-") || strings.HasPrefix(c.Name, "dp-b-") {
				out = append(out, c.Name)
			}
		}
	}
	return out
}

// deeTexts are the rule lines of the filter chain of the family.
func deeTexts(h *deeHost, f fwstate.Family, chain string) []string {
	rs := h.loaded.V4
	if f == fwstate.V6 {
		rs = h.loaded.V6
	}
	var out []string
	if c := chainOf(rs, "filter", chain); c != nil {
		for _, r := range c.Rules {
			out = append(out, r.Text)
		}
	}
	return out
}

// deeContainerRules are the UFW rules that carry a container rule's label.
func deeContainerRules(cfg fwstate.Config) []model.Rule {
	var out []model.Rule
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		if _, ok := dockerfw.RuleOf(r.Comment); ok {
			out = append(out, r)
		}
	}
	return out
}

// deeKernelDiffs compares the kernel's chains in two loaded states as a
// restore verifies them, except that DOCKER-USER, in either family, may hold
// one -j RETURN in the later state that the earlier one lacks: Docker 28 and
// later create the chain empty (and make none for IPv6 without IPv6 rules),
// while the block and the cleanup declare it ending in -j RETURN, and an
// empty chain returns as well.
func deeKernelDiffs(before, after fwstate.Loaded) []fwstate.ChainDiff {
	var out []fwstate.ChainDiff
	for _, d := range loaded.Compare(before, after, restoreKeep) {
		if d.Table == "filter" && d.Chain == "DOCKER-USER" && !slices.Contains(d.OnlyA, "-j RETURN") {
			if i := slices.Index(d.OnlyB, "-j RETURN"); i >= 0 {
				d.OnlyB = slices.Delete(slices.Clone(d.OnlyB), i, i+1)
			}
			if len(d.OnlyA) == 0 && len(d.OnlyB) == 0 {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// deeHasOption reports whether rule r has the option name, with value, of the -m module mod.
func deeHasOption(r netfilter.Rule, mod, name, value string) bool {
	for _, m := range r.Matches {
		for _, o := range m.Options {
			if m.Module == mod && o.Name == name && slices.Contains(o.Values, value) {
				return true
			}
		}
	}
	return false
}

// deeBindingLine finds, in the IPv4 darkpanel-docker-bindings chain, the line
// that sends web's published port 8080 to the container address addr, and
// returns its target chain, or "".
func deeBindingLine(h *deeHost, addr netip.Addr) string {
	c := chainOf(h.loaded.V4, "filter", "darkpanel-docker-bindings")
	if c == nil {
		return ""
	}
	for _, r := range c.Rules {
		if r.Dest.Prefix == netip.PrefixFrom(addr, 32) && r.Proto.Name == "tcp" && deeHasOption(r, "tcp", "dport", "80") &&
			deeHasOption(r, "conntrack", "ctorigdstport", "8080") {
			return r.Target.Name
		}
	}
	return ""
}

// TestDeeEnableApplyRevert: through the engine, on the host of Task 1's
// capture, firewall.docker.enable plans the block and applies it as
// journaled effects with UFW reloaded; verify finds the kernel as predicted;
// the published port that was open through Docker's own chains is no longer
// reachable; and the revert restores the files, the kernel's chains and the
// flag.
func TestDeeEnableApplyRevert(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	before, beforeLoaded := h.config(), h.loaded

	st := deeRead[DockerStatus](t, h, "firewall.docker")
	if !st.Available || !st.Supported || st.Enabled || st.Health != "off" || st.Version != "29.9.1" ||
		st.DockerVariant != "nft" || st.UFWVariant != "nft" || !st.RawProtection {
		t.Fatalf("docker before the change %+v, want a supported Docker 29.9.1 on nft with raw protection and the integration off", st)
	}
	if got := deeInternet(t, h, "tcp", "0.0.0.0", 8080); got != "open" {
		t.Errorf("web's published port is %s from the internet before the integration, want open through Docker's chains", got)
	}

	pv, cv, err := deePlanApply(t, eng, "firewall.docker.enable", `{}`)
	if err != nil || cv.State != core.StatePending || cv.Error != nil {
		t.Fatalf("enable = %+v, %v; want pending after verify", cv, err)
	}
	for _, line := range []string{"docker integration on", "after.rules: Docker block written", "after6.rules: Docker block written"} {
		if !deeHasLine(pv, line) {
			t.Errorf("plan diff %+v lacks %q", pv.Diff, line)
		}
	}
	cfg := h.config()
	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		if _, ok, err := dockerfw.BlockOf(cfg.Files[p].Data); err != nil || !ok {
			t.Errorf("%s holds the Docker block: %v, %v; want it written", p, ok, err)
		}
	}
	for name, rs := range map[string]*netfilter.Ruleset{"v4": h.loaded.V4, "v6": h.loaded.V6} {
		user := chainOf(rs, "filter", "DOCKER-USER")
		if user == nil || !slices.ContainsFunc(user.Rules, func(r netfilter.Rule) bool { return r.Target.Name == "darkpanel-docker-bindings" }) ||
			chainOf(rs, "filter", "darkpanel-docker-deny") == nil {
			t.Errorf("%s: DOCKER-USER %+v and darkpanel-docker-deny do not hold the loaded block", name, user)
		}
	}
	if st = deeRead[DockerStatus](t, h, "firewall.docker"); !st.Enabled || st.Health != "ok" {
		t.Errorf("docker after the change %+v, want enabled and ok", st)
	}
	if got := deeInternet(t, h, "tcp", "0.0.0.0", 8080); got != "filtered" {
		t.Errorf("web's published port is %s from the internet with the integration on and no binding allowed, want filtered", got)
	}

	if cv, err = eng.Revert(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	after := h.config()
	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules", "/etc/ufw/user.rules", "/etc/ufw/user6.rules"} {
		if !bytes.Equal(after.Files[p].Data, before.Files[p].Data) {
			t.Errorf("%s differs from before the change", p)
		}
	}
	if diffs := deeKernelDiffs(beforeLoaded, h.loaded); len(diffs) != 0 {
		t.Errorf("the kernel's chains differ from before the change: %+v", diffs)
	}
	if owned := deeOwnedChains(h); len(owned) != 0 {
		t.Errorf("the revert left the chains %v loaded", owned)
	}
	if c, err := readDockerConfig(ctx, h.db); err != nil || c.Enabled {
		t.Errorf("flag after the revert = %+v, %v; want off", c, err)
	}
}

// TestDeeContainerAllowAndRemove: with the integration on, allowing web's
// published port 8080 stores one policy row and realizes it as a tagged UFW
// route allow to web's address, an allow in for IPv6, where docker-proxy
// serves the port, a binding line and a binding chain, which opens the port
// for both families; removing the row closes it again, and reverting the
// removal brings the rule and the opening back.
func TestDeeContainerAllowAndRemove(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	h.deePublishV6(t)
	eng := deeEngine(t, h)
	web := h.deeAddr(t, "web")
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	for addr, want := range map[string]string{"0.0.0.0": "filtered", "::": "filtered"} {
		if got := deeInternet(t, h, "tcp", addr, 8080); got != want {
			t.Errorf("port 8080 on %s is %s from the internet before the binding is allowed, want %s", addr, got, want)
		}
	}

	pv, cv, err := deePlanApply(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	if err != nil || cv.State != core.StatePending || cv.Error != nil {
		t.Fatalf("allow = %+v, %v; want pending after verify", cv, err)
	}
	if !deeHasLine(pv, "container rule + web all:8080/tcp allow any") {
		t.Errorf("plan diff %+v lacks the container rule line", pv.Diff)
	}
	rows, err := listContainerRules(ctx, h.db)
	if err != nil || len(rows) != 1 || rows[0].Kind != dockerfw.Allow ||
		rows[0].Binding != (dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}) {
		t.Fatalf("stored rules %+v, %v; want web:8080/tcp allowed", rows, err)
	}
	var route4, in6 int
	for _, r := range deeContainerRules(h.config()) {
		if r.Comment != "ctr "+rows[0].ID || r.Action != model.ActionAllow || r.Proto != "tcp" {
			t.Errorf("tagged rule %+v, want an allow for tcp labelled ctr %s", r, rows[0].ID)
		}
		switch {
		case r.Direction == model.DirRoute && string(r.Family) == "v4" && r.OutIface == "docker0" && r.To == web.String() && r.ToPort == "80":
			route4++
		case r.Direction == model.DirIn && string(r.Family) == "v6" && r.To == "any" && r.ToPort == "8080":
			in6++
		default:
			t.Errorf("tagged rule %+v is neither the route allow to %s:80 nor the IPv6 allow in on 8080", r, web)
		}
	}
	if route4 != 1 || in6 != 1 {
		t.Errorf("%d route allows and %d IPv6 allow-in rules, want one each", route4, in6)
	}
	chain := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}.Chain()
	if got := deeBindingLine(h, web); got != chain {
		t.Errorf("binding line for %s:80 targets %q, want %s", web, got, chain)
	}
	if c := chainOf(h.loaded.V4, "filter", chain); c == nil || !slices.ContainsFunc(c.Rules, func(r netfilter.Rule) bool { return r.Target.Name == "ufw-user-forward" }) {
		t.Errorf("binding chain %s = %+v, want a jump to ufw-user-forward", chain, c)
	}
	for addr, want := range map[string]string{"0.0.0.0": "open", "::": "open"} {
		if got := deeInternet(t, h, "tcp", addr, 8080); got != want {
			t.Errorf("port 8080 on %s is %s from the internet after the allow, want %s", addr, got, want)
		}
	}
	if _, err := eng.Confirm(ctx, eeUser, cv.ID); err != nil {
		t.Fatal(err)
	}

	allowed, allowedLoaded := h.config(), h.loaded
	_, cv, err = deePlanApply(t, eng, "firewall.container.remove", `{"id":"`+rows[0].ID+`"}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("remove = %+v, %v; want pending", cv, err)
	}
	if rows, err = listContainerRules(ctx, h.db); err != nil || len(rows) != 0 || len(deeContainerRules(h.config())) != 0 {
		t.Errorf("after the removal: rows %+v (%v) and tagged rules %+v, want none", rows, err, deeContainerRules(h.config()))
	}
	for _, addr := range []string{"0.0.0.0", "::"} {
		if got := deeInternet(t, h, "tcp", addr, 8080); got != "filtered" {
			t.Errorf("port 8080 on %s is %s from the internet after the removal, want filtered", addr, got)
		}
	}

	if cv, err = eng.Revert(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if rows, err = listContainerRules(ctx, h.db); err != nil || len(rows) != 1 {
		t.Errorf("rows after the revert %+v (%v), want the allow back", rows, err)
	}
	if got := h.config(); !bytes.Equal(got.Files["/etc/ufw/user.rules"].Data, allowed.Files["/etc/ufw/user.rules"].Data) ||
		!bytes.Equal(got.Files["/etc/ufw/after.rules"].Data, allowed.Files["/etc/ufw/after.rules"].Data) {
		t.Error("UFW's files differ from before the removal")
	}
	if diffs := deeKernelDiffs(allowedLoaded, h.loaded); len(diffs) != 0 {
		t.Errorf("the kernel's chains differ from before the removal: %+v", diffs)
	}
	if got := deeInternet(t, h, "tcp", "0.0.0.0", 8080); got != "open" {
		t.Errorf("port 8080 is %s from the internet after the revert, want open", got)
	}
}

// TestDeeDisableCleansTheChains: disabling the integration removes the block,
// the tagged container rules and every loaded Darkpanel chain, leaves
// DOCKER-USER with Docker's -j RETURN and keeps the stored policy; the revert
// brings all of it back.
func TestDeeDisableCleansTheChains(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	deeConfirm(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	beforeLoaded, beforeTagged := h.loaded, len(deeContainerRules(h.config()))
	if beforeTagged == 0 || len(deeOwnedChains(h)) == 0 {
		t.Fatalf("setup: %d tagged rules and chains %v, want both present", beforeTagged, deeOwnedChains(h))
	}

	pv, cv, err := deePlanApply(t, eng, "firewall.docker.disable", `{}`)
	if err != nil || cv.State != core.StatePending || cv.Error != nil {
		t.Fatalf("disable = %+v, %v; want pending after verify", cv, err)
	}
	for _, line := range []string{"docker integration off", "after.rules: Docker block removed", "after6.rules: Docker block removed", "Docker chains removed"} {
		if !deeHasLine(pv, line) {
			t.Errorf("plan diff %+v lacks %q", pv.Diff, line)
		}
	}
	cfg := h.config()
	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		if _, ok, err := dockerfw.BlockOf(cfg.Files[p].Data); err != nil || ok {
			t.Errorf("%s holds the Docker block: %v, %v; want it removed", p, ok, err)
		}
	}
	if owned := deeOwnedChains(h); len(owned) != 0 {
		t.Errorf("chains %v are still loaded", owned)
	}
	for _, f := range []fwstate.Family{fwstate.V4, fwstate.V6} {
		if got := deeTexts(h, f, "DOCKER-USER"); !slices.Equal(got, []string{"-A DOCKER-USER -j RETURN"}) {
			t.Errorf("%s DOCKER-USER = %q, want only -j RETURN", f, got)
		}
	}
	if n := len(deeContainerRules(cfg)); n != 0 {
		t.Errorf("%d tagged container rules remain in UFW", n)
	}
	if rows, err := listContainerRules(ctx, h.db); err != nil || len(rows) != 1 {
		t.Errorf("stored rules %+v, %v; want the allow kept for a later enable", rows, err)
	}
	if c, err := readDockerConfig(ctx, h.db); err != nil || c.Enabled {
		t.Errorf("flag = %+v, %v; want off", c, err)
	}
	if st := deeRead[DockerStatus](t, h, "firewall.docker"); st.Enabled || st.Health != "off" {
		t.Errorf("docker after the change %+v, want disabled and off", st)
	}

	if cv, err = eng.Revert(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted", cv, err)
	}
	if diffs := deeKernelDiffs(beforeLoaded, h.loaded); len(diffs) != 0 {
		t.Errorf("the kernel's chains differ from before the disable: %+v", diffs)
	}
	if n := len(deeContainerRules(h.config())); n != beforeTagged {
		t.Errorf("%d tagged container rules after the revert, want %d", n, beforeTagged)
	}
	if c, err := readDockerConfig(ctx, h.db); err != nil || !c.Enabled {
		t.Errorf("flag after the revert = %+v, %v; want on", c, err)
	}
}

const deeForeign = "-A DOCKER-USER -s 198.51.100.0/24 -j DROP"

// TestDeeForeignRuleSurvivesRevert: a rule of another program appended to
// DOCKER-USER during the confirmation window does not stop the revert, which
// removes the block and every Darkpanel chain and leaves the rule loaded.
func TestDeeForeignRuleSurvivesRevert(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	before, beforeLoaded := h.config(), h.loaded
	_, cv, err := deePlanApply(t, eng, "firewall.docker.enable", `{}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("enable = %+v, %v; want pending", cv, err)
	}
	h.addForeign(t, deeForeign)

	if cv, err = eng.Revert(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert = %+v, %v; want reverted although DOCKER-USER holds a foreign rule", cv, err)
	}
	if got, want := deeTexts(h, fwstate.V4, "DOCKER-USER"), []string{"-A DOCKER-USER -j RETURN", deeForeign}; !slices.Equal(got, want) {
		t.Errorf("DOCKER-USER = %q, want Docker's RETURN and the foreign rule %q", got, want)
	}
	if owned := deeOwnedChains(h); len(owned) != 0 {
		t.Errorf("the revert left the chains %v loaded", owned)
	}
	if got := h.config(); !bytes.Equal(got.Files["/etc/ufw/after.rules"].Data, before.Files["/etc/ufw/after.rules"].Data) {
		t.Error("after.rules differs from before the change")
	}
	for _, d := range deeKernelDiffs(beforeLoaded, h.loaded) {
		if d.Family != fwstate.V4 || d.Table != "filter" || d.Chain != "DOCKER-USER" || len(d.OnlyB) != 1 || len(d.OnlyA) != 0 {
			t.Errorf("chain difference %+v, want only the foreign rule loaded in the IPv4 DOCKER-USER", d)
		}
	}
}

// TestDeeForeignRuleRefusesMutations: a foreign rule in DOCKER-USER while the
// integration is on refuses every mutating firewall action with
// firewall.docker_user_in_use and changes nothing, until
// firewall.docker.repair discards it with the operator's confirmation.
func TestDeeForeignRuleRefusesMutations(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	h.addForeign(t, deeForeign)
	ran, level := len(h.ran), h.config().LogLevel

	_, _, err := deePlanApply(t, eng, "firewall.logging.set", `{"level":"high"}`)
	if e := errcode.From(err); e.Code != errcode.FirewallDockerUserInUse || !strings.Contains(e.Detail, "198.51.100.0/24") {
		t.Errorf("logging.set = %v, want %s naming the rule", err, errcode.FirewallDockerUserInUse)
	}
	if len(h.ran) != ran || h.config().LogLevel != level {
		t.Errorf("ufw ran %q and the log level is %s after the refusal, want nothing changed", h.ran[ran:], h.config().LogLevel)
	}
	if st := deeRead[DockerStatus](t, h, "firewall.docker"); st.Health != "broken" {
		t.Errorf("health %s with a foreign rule, want broken", st.Health)
	}

	pv, cv, err := deePlanApply(t, eng, "firewall.docker.repair", `{"discardForeign":true,"manual":true}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("repair = %+v, %v; want pending", cv, err)
	}
	if !slices.ContainsFunc(pv.Diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "198.51.100.0/24") }) {
		t.Errorf("plan diff %+v does not list the rule it discards", pv.Diff)
	}
	if slices.ContainsFunc(deeTexts(h, fwstate.V4, "DOCKER-USER"), func(s string) bool { return strings.Contains(s, "198.51.100.0/24") }) {
		t.Error("the foreign rule is still loaded after the repair discarded it")
	}
	if st := deeRead[DockerStatus](t, h, "firewall.docker"); st.Health != "ok" || len(st.ForeignRules) != 0 {
		t.Errorf("docker after the repair %+v, want ok and no foreign rule", st)
	}
	if _, err := eng.Confirm(ctx, eeUser, cv.ID); err != nil {
		t.Fatal(err)
	}
	if _, cv, err = deePlanApply(t, eng, "firewall.logging.set", `{"level":"high"}`); err != nil || cv.State != core.StatePending {
		t.Errorf("logging.set after the repair = %+v, %v; want pending", cv, err)
	}
}

// TestDeeReconcileFollowsARecreatedContainer: after Docker recreates web with
// a new address, one pass of firewall.reconcile, as the system actor,
// re-realizes the stored allowance for the new address, closing nothing the
// policy allows and opening nothing it does not; a pass over a host already
// in line applies nothing.
func TestDeeReconcileFollowsARecreatedContainer(t *testing.T) {
	ctx := context.Background()
	h := deeNewHost(t)
	eng := deeEngine(t, h)
	deeConfirm(t, eng, "firewall.docker.enable", `{}`)
	deeConfirm(t, eng, "firewall.container.allow", `{"binding":`+deeBinding+`}`)
	old := h.deeAddr(t, "web")
	a := old.As4()
	next := netip.AddrFrom4([4]byte{a[0], a[1], a[2], 9})
	h.recreate(t, "web", next)

	system := core.Principal{Actor: "system:firewall.reconcile", Scope: core.ScopeAdmin}
	var applied []string
	env := h.env()
	env.Principal = system
	tc := core.TaskContext{Env: env, Apply: func(ctx context.Context, action string, input any) (core.ChangeView, error) {
		raw, err := json.Marshal(input)
		if err != nil {
			return core.ChangeView{}, err
		}
		applied = append(applied, action)
		return eng.ApplySystem(ctx, system, core.ActionRef{Name: action, Version: 1}, raw)
	}}
	tasks := module{f: h.fw()}.BackgroundTasks()
	if len(tasks) != 1 {
		t.Fatalf("%d background tasks, want the reconcile task", len(tasks))
	}
	if err := tasks[0].Run(ctx, tc); err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("the pass applied nothing although web's address changed")
	}

	changes, err := eng.Changes(ctx, core.ChangeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var reconciled int
	for _, c := range changes {
		if c.Actor == "system:firewall.reconcile" {
			reconciled++
			if c.State != core.StateConfirmed {
				t.Errorf("change %s (%s) is %s, want confirmed as soon as it verified", c.ID, c.Action, c.State)
			}
		}
	}
	if reconciled != len(applied) {
		t.Errorf("%d changes by the reconcile actor for %d applies %v", reconciled, len(applied), applied)
	}
	var routes []string
	for _, r := range deeContainerRules(h.config()) {
		if r.Direction == model.DirRoute {
			routes = append(routes, r.To)
		}
	}
	if !slices.Equal(routes, []string{next.String()}) {
		t.Errorf("route rules to %v, want only the new address %s (old %s)", routes, next, old)
	}
	if got := deeBindingLine(h, next); got == "" || deeBindingLine(h, old) != "" {
		t.Errorf("binding line for the new address targets %q and for the old %q, want the binding chain and none", got, deeBindingLine(h, old))
	}
	if rows, err := listContainerRules(ctx, h.db); err != nil || len(rows) != 1 {
		t.Errorf("stored rules %+v, %v; want the policy unchanged", rows, err)
	}
	if got := deeInternet(t, h, "tcp", "0.0.0.0", 8080); got != "open" {
		t.Errorf("port 8080 is %s from the internet after the reconcile, want open", got)
	}

	if err := tasks[0].Run(ctx, tc); err != nil {
		t.Fatal(err)
	}
	if len(applied) != reconciled {
		t.Errorf("a second pass applied %v, want nothing more", applied[reconciled:])
	}
}
