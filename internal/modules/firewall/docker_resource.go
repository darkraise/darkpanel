package firewall

import (
	"context"
	"fmt"
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// DockerStatus is the output of resource firewall.docker (v1, spec 2.6): what
// the daemon and the host look like to the Docker integration, whether the
// integration is on and healthy, and the stored container rules with the
// bindings they apply to.
type DockerStatus struct {
	Available       bool                     `json:"available"`
	Reason          string                   `json:"reason,omitempty"`
	Version         string                   `json:"version,omitempty"`
	Driver          string                   `json:"driver,omitempty"`
	DockerVariant   string                   `json:"dockerVariant,omitempty"`
	UFWVariant      string                   `json:"ufwVariant,omitempty"`
	RawProtection   bool                     `json:"rawProtection"`
	Supported       bool                     `json:"supported"`
	Unsupported     []string                 `json:"unsupported,omitempty"`
	Enabled         bool                     `json:"enabled"`
	Health          string                   `json:"health"` // off | ok | degraded | broken | unsupported
	Issues          []dockerfw.Issue         `json:"issues,omitempty"`
	ForeignRules    []string                 `json:"foreignRules,omitempty"`
	ForeignForwards []dockerfw.Exemption     `json:"foreignForwards,omitempty"` // proposed exemptions for uncovered foreign DNAT rules
	ManualRoutes    []string                 `json:"manualRoutes,omitempty"`    // manual route rules to containers: no effect without a Darkpanel binding
	Networks        []DockerNetwork          `json:"networks"`
	Settings        dockerfw.Settings        `json:"settings"`
	Rules           []dockerfw.ContainerRule `json:"rules"`
	Bindings        []DockerBinding          `json:"bindings"`
}

// DockerNetwork is one Docker bridge network and whether the block protects
// it.
type DockerNetwork struct {
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	Bridge    string   `json:"bridge"`
	Subnets   []string `json:"subnets"`
	Protected bool     `json:"protected"`
	Reason    string   `json:"reason,omitempty"`
}

// DockerBinding is one published port that container rules govern: the
// containers it reaches now and the sources the rules allow and deny.
type DockerBinding struct {
	Binding       dockerfw.Binding `json:"binding"`
	ID            string           `json:"id"`
	Containers    []string         `json:"containers"`
	ContainerPort uint16           `json:"containerPort"`
	Allowed       []string         `json:"allowed"` // sources, "any" for every source
	Denied        []string         `json:"denied"`
	Proxied       []string         `json:"proxied,omitempty"`      // families served by docker-proxy
	PrivateReach  []string         `json:"privateReach,omitempty"` // privateSources ranges that reach it only through the exemption
}

// statusAfterFiles are the files that hold the block, by family.
var statusAfterFiles = []struct {
	family fwstate.Family
	path   string
}{{fwstate.V4, "/etc/ufw/after.rules"}, {fwstate.V6, "/etc/ufw/after6.rules"}}

// dockerResource is resource firewall.docker.
func (f fw) dockerResource() core.ResourceDef {
	return core.DefineResource(ID+".docker", f.dockerStatus)
}

// dockerStatus reads resource firewall.docker. A Docker daemon that cannot be
// reached is a status, not an error.
func (f fw) dockerStatus(ctx context.Context, env core.Env) (DockerStatus, error) {
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return DockerStatus{}, fmt.Errorf("firewall: docker status: %w", err)
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return DockerStatus{}, fmt.Errorf("firewall: docker status: %w", err)
	}
	st, err := f.dockerState(ctx, env, cur)
	if err != nil {
		return DockerStatus{}, fmt.Errorf("firewall: docker status: %w", err)
	}
	return dockerStatusOf(cfg, cur, st), nil
}

// dockerStatusOf builds the status from the configuration, the loaded state
// and the Docker state.
func dockerStatusOf(cfg fwstate.Config, cur fwstate.Loaded, st dockerState) DockerStatus {
	ds := DockerStatus{
		Available: st.available, Reason: st.reason, Enabled: st.cfg.Enabled,
		Settings: st.cfg.Settings, Rules: st.rows,
		Networks: []DockerNetwork{}, Bindings: []DockerBinding{},
		ForeignRules: statusForeignRules(cfg, cur),
	}
	if ds.Settings.PrivateSources == nil {
		ds.Settings.PrivateSources = []netip.Prefix{}
	}
	if ds.Settings.ForwardExemptions == nil {
		ds.Settings.ForwardExemptions = []dockerfw.Exemption{}
	}
	if ds.Rules == nil {
		ds.Rules = []dockerfw.ContainerRule{}
	}
	if !st.available {
		ds.Health = "unsupported"
		return ds
	}

	ds.Version, ds.Driver, ds.DockerVariant, ds.UFWVariant = st.det.Version, st.det.Driver, st.det.DockerVariant, st.det.UFWVariant
	ds.RawProtection = st.det.RawProtection
	if reasons := dockerfw.Unsupported(st.det); len(reasons) > 0 {
		ds.Unsupported = reasons
	}
	ds.Supported = len(ds.Unsupported) == 0
	ds.ForeignForwards = statusForeignForwards(cur, st.cfg.Settings)
	ds.ManualRoutes = statusManualRoutes(cfg, st.facts)
	ds.Networks = statusNetworks(st.facts, st.cfg.Settings)
	ds.Bindings = statusBindings(st)
	if ds.Supported {
		if issues := dockerfw.Health(statusHealthInput(cfg, cur, st)); len(issues) > 0 {
			ds.Issues = issues
		}
	}
	ds.Health = dockerHealthLevel(ds.Supported, ds.Enabled, ds.Issues)
	return ds
}

// dockerHealthLevel is the status's health: unsupported hosts and a flag that
// is off hide the issues, otherwise the worst issue decides.
func dockerHealthLevel(supported, enabled bool, issues []dockerfw.Issue) string {
	switch {
	case !supported:
		return "unsupported"
	case !enabled:
		return "off"
	case slices.ContainsFunc(issues, func(is dockerfw.Issue) bool { return is.Level == dockerfw.Broken }):
		return "broken"
	case len(issues) > 0:
		return "degraded"
	}
	return "ok"
}

// statusHealthInput is the input dockerfw.Health reads.
func statusHealthInput(cfg fwstate.Config, cur fwstate.Loaded, st dockerState) dockerfw.HealthInput {
	in := dockerfw.HealthInput{
		Want:     st.cfg.Enabled && cfg.Enabled,
		Files:    map[fwstate.Family][]byte{},
		Expected: map[fwstate.Family][]byte{},
		Loaded:   map[fwstate.Family]*netfilter.Ruleset{fwstate.V4: cur.V4, fwstate.V6: cur.V6},
		IPv6:     cfg.IPv6, DefaultInput: cfg.DefaultInput, Facts: st.facts, Settings: st.cfg.Settings,
	}
	for _, af := range statusAfterFiles {
		in.Files[af.family] = cfg.Files[af.path].Data
		in.Expected[af.family] = dockerfw.Render(st.realized.Block, af.family)
	}
	for _, i := range st.interfaces {
		in.Interfaces = append(in.Interfaces, i.Name)
	}
	return in
}

// statusRuleset is a loaded ruleset with its family.
type statusRuleset struct {
	family fwstate.Family
	rs     *netfilter.Ruleset
}

// statusLoadedFamilies lists the families whose loaded ruleset is present.
func statusLoadedFamilies(cur fwstate.Loaded) []statusRuleset {
	var out []statusRuleset
	for _, e := range []statusRuleset{{fwstate.V4, cur.V4}, {fwstate.V6, cur.V6}} {
		if e.rs != nil {
			out = append(out, e)
		}
	}
	return out
}

// statusForeignRules lists the loaded DOCKER-USER rules the block does not
// hold. An after file without a readable block counts as holding no known
// rule, so the resource stays readable and health reports the damage.
func statusForeignRules(cfg fwstate.Config, cur fwstate.Loaded) []string {
	var out []string
	for _, e := range statusLoadedFamilies(cur) {
		chain := chainOf(e.rs, "filter", dockerfw.ChainDockerUser)
		if chain == nil {
			continue
		}
		var known []netfilter.Rule
		for _, af := range statusAfterFiles {
			if af.family == e.family {
				known = statusKnownRules(cfg.Files[af.path].Data)
			}
		}
		for _, r := range dockerfw.Foreign(chain, known, e.family) {
			out = append(out, string(e.family)+" "+r.Text)
		}
	}
	return out
}

// statusKnownRules is the DOCKER-USER rules of the block in file; none when
// the file holds no block or one that cannot be read.
func statusKnownRules(file []byte) []netfilter.Rule {
	block, ok, err := dockerfw.BlockOf(file)
	if err != nil || !ok {
		return nil
	}
	rs, err := dockerfw.BlockRules(block)
	if err != nil {
		return nil
	}
	if c := chainOf(rs, "filter", dockerfw.ChainDockerUser); c != nil {
		return c.Rules
	}
	return nil
}

// statusForeignForwards proposes an exemption for each DNAT rule outside
// Docker's chains that the settings do not cover yet.
func statusForeignForwards(cur fwstate.Loaded, s dockerfw.Settings) []dockerfw.Exemption {
	var out []dockerfw.Exemption
	for _, e := range statusLoadedFamilies(cur) {
		for _, r := range dockerfw.ForeignDNAT(e.rs.Table("nat")) {
			if ex, ok := dockerfw.Propose(r, e.family); ok && !dockerfw.Covered(r, s.ForwardExemptions) {
				out = append(out, ex)
			}
		}
	}
	return out
}

// statusManualRoutes describes the manual route rules whose destination lies
// in a Docker network: UFW holds them, but Docker's published ports reach a
// container only through a binding.
func statusManualRoutes(cfg fwstate.Config, facts dockerfw.Facts) []string {
	var out []string
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		if r.Origin != model.OriginManual || r.Direction != model.DirRoute || r.Raw != "" || r.To == model.Any {
			continue
		}
		p, err := netip.ParsePrefix(r.To)
		if err != nil {
			a, aerr := netip.ParseAddr(r.To)
			if aerr != nil {
				continue
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		inNetwork := slices.ContainsFunc(facts.Networks, func(n dockerfw.Network) bool {
			return slices.ContainsFunc(n.Subnets, func(s netip.Prefix) bool { return s.Overlaps(p) })
		})
		if inNetwork {
			out = append(out, ruleText(r))
		}
	}
	return out
}

// statusNetworks lists the bridge networks and whether each is protected.
func statusNetworks(facts dockerfw.Facts, s dockerfw.Settings) []DockerNetwork {
	out := []DockerNetwork{}
	for _, n := range facts.Networks {
		if n.Driver != "bridge" {
			continue
		}
		dn := DockerNetwork{Name: n.Name, ID: n.ID, Bridge: n.Bridge, Subnets: []string{}}
		for _, p := range n.Subnets {
			dn.Subnets = append(dn.Subnets, p.String())
		}
		reason, unprotected := dockerfw.Unprotected(n, s)
		dn.Protected, dn.Reason = !unprotected, reason
		out = append(out, dn)
	}
	return out
}

// statusBindings lists each distinct binding of the stored rows, in the order
// the rows first name them.
func statusBindings(st dockerState) []DockerBinding {
	out := []DockerBinding{}
	index := map[string]int{}
	rowsOf := map[string][]dockerfw.ContainerRule{}
	for _, r := range st.rows {
		id := r.Binding.ID()
		if _, seen := index[id]; !seen {
			index[id] = len(out)
			out = append(out, DockerBinding{Binding: r.Binding, ID: id, Containers: []string{}, Allowed: []string{}, Denied: []string{}})
		}
		rowsOf[id] = append(rowsOf[id], r)
	}
	for i := range out {
		b := &out[i]
		rows := rowsOf[b.ID]
		for _, r := range rows {
			src := "any"
			if r.From.IsValid() {
				src = r.From.String()
			}
			switch r.Kind {
			case dockerfw.Allow:
				b.Allowed = append(b.Allowed, src)
			case dockerfw.Deny:
				b.Denied = append(b.Denied, src)
			}
		}
		statusContainers(b, st.facts)
		b.Proxied = statusProxied(rows, st.realized.Rules)
		b.PrivateReach = statusPrivateReach(*b, rows, st)
	}
	return out
}

// statusContainers fills the running containers the binding reaches and the
// first container port it publishes.
func statusContainers(b *DockerBinding, facts dockerfw.Facts) {
	for _, c := range facts.Containers {
		if !b.Binding.Selects(c.Name, c.Project, c.Service) {
			continue
		}
		for _, p := range c.Ports {
			if p.Proto != b.Binding.Proto || p.HostPort != b.Binding.HostPort {
				continue
			}
			if b.Binding.HostAddr.IsValid() && p.HostAddr != b.Binding.HostAddr && !p.HostAddr.IsUnspecified() {
				continue
			}
			b.Containers = append(b.Containers, c.Name)
			if b.ContainerPort == 0 {
				b.ContainerPort = p.ContainerPort
			}
			break
		}
	}
}

// statusProxied lists the families in which the rows' allow rules are
// realized as incoming rules, which docker-proxy serves.
func statusProxied(rows []dockerfw.ContainerRule, realized []model.Rule) []string {
	var out []string
	for _, row := range rows {
		if row.Kind != dockerfw.Allow {
			continue
		}
		for _, r := range realized {
			if r.Direction == model.DirIn && r.Comment == dockerfw.LabelPrefix+row.ID && !slices.Contains(out, string(r.Family)) {
				out = append(out, string(r.Family))
			}
		}
	}
	slices.Sort(out)
	return out
}

// statusPrivateReach lists the privateSources ranges that reach the binding
// through the exemption alone: the binding has a container address in the
// range's family and no allow row names a source that holds the whole range.
func statusPrivateReach(b DockerBinding, rows []dockerfw.ContainerRule, st dockerState) []string {
	var allows []netip.Prefix
	for _, r := range rows {
		if r.Kind != dockerfw.Allow {
			continue
		}
		if !r.From.IsValid() {
			return nil
		}
		allows = append(allows, r.From)
	}
	var v4, v6 bool
	for _, l := range st.realized.Block.Lines {
		if l.Chain != b.Binding.Chain() {
			continue
		}
		if l.ContainerAddr.Is4() {
			v4 = true
		} else {
			v6 = true
		}
	}
	var out []string
	for _, rng := range st.cfg.Settings.PrivateSources {
		if (rng.Addr().Is4() && !v4) || (!rng.Addr().Is4() && !v6) {
			continue
		}
		covered := slices.ContainsFunc(allows, func(from netip.Prefix) bool {
			return from.Bits() <= rng.Bits() && from.Contains(rng.Addr())
		})
		if !covered {
			out = append(out, rng.String())
		}
	}
	return out
}
