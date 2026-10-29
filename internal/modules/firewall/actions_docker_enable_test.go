package firewall

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	denAfter4 = "/etc/ufw/after.rules"
	denAfter6 = "/etc/ufw/after6.rules"
	denRow1   = "01J00000000000000000000R01"
	denTag1   = "01J00000000000000000000T01"
)

var denULIDRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// denRouteTuple is the tuple line UFW writes in user.rules for a route rule
// out on docker0 to 172.17.0.2 port 80, tagged id with label; an empty from
// is any source.
func denRouteTuple(action, from, id, label string) string {
	if from == "" {
		from = "0.0.0.0/0"
	}
	return fmt.Sprintf("### tuple ### route:%s tcp 80 172.17.0.2 any %s out_docker0 comment=%s",
		action, from, hex.EncodeToString([]byte(model.Tag(id, label))))
}

// denConfig is a configuration whose user.rules holds the given tuple
// lines.
func denConfig(t *testing.T, v4 ...string) fwstate.Config {
	t.Helper()
	cfg := fwstate.Config{IPv6: true, DefaultInput: "DROP"}
	for _, line := range v4 {
		tp, err := model.ParseTuple(line, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Rules4 = append(cfg.Rules4, tp)
	}
	return cfg
}

// denBlock is a block as an after file holds it between its markers.
var denBlock = []byte("*filter\n:DOCKER-USER - [0:0]\nCOMMIT\n")

// denAfterFile is an after file's own lines, with the Docker block when
// block is true.
func denAfterFile(t *testing.T, block bool) []byte {
	t.Helper()
	own := []byte("*filter\n# after\nCOMMIT\n")
	if !block {
		return own
	}
	out, err := dockerfw.WithBlock(own, denBlock)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// denHasBlock reports whether an after file holds the marked block.
func denHasBlock(t *testing.T, data []byte) bool {
	t.Helper()
	_, ok, err := dockerfw.BlockOf(data)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// denFileConfig is a configuration with DEFAULT_INPUT_POLICY DROP, the
// given user.rules tuples and both after files, each with a block when its
// flag is set.
func denFileConfig(t *testing.T, block4, block6 bool, v4 ...string) fwstate.Config {
	t.Helper()
	cfg := denConfig(t, v4...)
	cfg.Files = map[string]core.RestoreEntry{
		denAfter4: {Path: denAfter4, Present: true, Mode: 0o640, Data: denAfterFile(t, block4)},
		denAfter6: {Path: denAfter6, Present: true, Mode: 0o640, Data: denAfterFile(t, block6)},
	}
	return cfg
}

// denFacts is one running container, web, publishing 8080 to its port 80 on
// every IPv4 address, on the default bridge.
func denFacts() dockerfw.Facts {
	return dockerfw.Facts{
		Containers: []dockerfw.Container{{
			ID: "id-web", Name: "web",
			Ports:     []dockerfw.Published{{HostAddr: netip.MustParseAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"}},
			Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: netip.MustParseAddr("172.17.0.2")}},
		}},
		Networks: []dockerfw.Network{{ID: "id-bridge", Name: "bridge", Driver: "bridge", Bridge: "docker0",
			Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}}},
	}
}

// denWebAllow is the allowance of web's published port 8080.
func denWebAllow() dockerfw.ContainerRule {
	return dockerfw.ContainerRule{ID: denRow1, Kind: dockerfw.Allow,
		Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}}
}

// denState is a supported Docker 29.9.1 with the integration off, the
// default settings, the facts of denFacts and the allowance of web:8080 as
// its only stored row; edit changes it before the block is realized.
func denState(edit func(*dockerState)) dockerState {
	st := dockerState{
		available: true,
		det: dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft",
			RawProtection: true},
		facts: denFacts(),
		cfg:   dockerConfig{Settings: dockerfw.DefaultSettings()},
		rows:  []dockerfw.ContainerRule{denWebAllow()},
	}
	if edit != nil {
		edit(&st)
	}
	st.realized = dockerfw.Realize(st.rows, st.facts, st.cfg.Settings)
	return st
}

// denLoaded is a loaded state whose nat tables hold the given save text of
// each family ("" for no table).
func denLoaded(t *testing.T, nat4, nat6 string) fwstate.Loaded {
	t.Helper()
	parse := func(text string) *netfilter.Ruleset {
		if text == "" {
			return fhParse(t, fhSave)
		}
		return fhParse(t, fhSave+text)
	}
	return fwstate.Loaded{Variant: "nft", V4: parse(nat4), V6: parse(nat6)}
}

const (
	denNatDocker = "*nat\n:PREROUTING ACCEPT [0:0]\n:DOCKER - [0:0]\n-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER\n" +
		"-A DOCKER ! -i docker0 -p tcp -m tcp --dport 8080 -j DNAT --to-destination 172.17.0.2:80\nCOMMIT\n"
	denForward4 = "-A PREROUTING -d 192.0.2.10/32 -i eth0 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:8080"
	denForward6 = "-A PREROUTING -d 2001:db8::10/128 -p tcp -m tcp --dport 443 -j DNAT --to-destination [fd00::5]:8443"
)

// denNat is a nat table with Docker's chain and the given extra rule lines
// of PREROUTING.
func denNat(rules ...string) string {
	return "*nat\n:PREROUTING ACCEPT [0:0]\n:DOCKER - [0:0]\n-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER\n" +
		strings.Join(append(rules, ""), "\n") + "COMMIT\n"
}

// denLabels lists the label of each ufw operation's rule tag, sorted.
func denLabels(t *testing.T, ops []ufwOp) []string {
	t.Helper()
	var out []string
	for _, op := range ops {
		n := len(op.Args)
		if n < 2 || op.Args[n-2] != "comment" {
			t.Fatalf("operation %q does not end with a comment", op.Args)
		}
		id, label, ok := model.ParseTag(op.Args[n-1])
		if !ok || !denULIDRe.MatchString(id) {
			t.Fatalf("operation %q carries the tag %q, want darkpanel:<ULID> and a label", op.Args, op.Args[n-1])
		}
		out = append(out, label)
	}
	slices.Sort(out)
	return out
}

// denTokens reports whether args holds the tokens in order.
func denTokens(args []string, tokens ...string) bool {
	i := 0
	for _, a := range args {
		if i < len(tokens) && a == tokens[i] {
			i++
		}
	}
	return i == len(tokens)
}

func denEmpty(c change) bool {
	return c.flag == nil && c.sources == nil && c.enable == nil && c.restore == nil && !c.repair && !c.cleanup && !c.reload &&
		len(c.ops) == 0 && len(c.files) == 0 && len(c.rowsAdd) == 0 && len(c.rowsDel) == 0
}

func denSameFiles(a, b map[string][]byte) bool { return maps.EqualFunc(a, b, bytes.Equal) }

// TestDenEnableChange: enabling stores the flag, writes the block into both
// after files with the stored rows realized in the same change, reloads UFW,
// and refuses every condition spec 2.6 and the integration invariant name.
func TestDenEnableChange(t *testing.T) {
	env := newFakeHost(t).env()
	exemption := dockerfw.Exemption{InInterface: "eth0", OrigDestination: netip.MustParseAddr("192.0.2.10"), OrigPort: 80,
		Destination: netip.MustParseAddr("10.9.0.5"), Proto: "tcp", Port: 8080}
	inDocker := dockerfw.Exemption{OrigDestination: netip.MustParseAddr("192.0.2.11"), OrigPort: 80,
		Destination: netip.MustParseAddr("172.17.0.9"), Proto: "tcp", Port: 80}
	withExemptions := func(ex ...dockerfw.Exemption) func(*dockerState) {
		return func(s *dockerState) { s.cfg.Settings.ForwardExemptions = ex }
	}
	tests := []struct {
		name string
		edit func(*dockerState)
		cur  fwstate.Loaded
		cfg  func(*fwstate.Config)
		// refused says the change is refused with firewall.unsupported_integration;
		// detail is what its detail must hold.
		refused bool
		detail  string
	}{
		{name: "a supported host", cur: denLoaded(t, denNatDocker, "")},
		{name: "a host with no nat table", cur: denLoaded(t, "", "")},
		{name: "a forward Docker made is not a foreign one", cur: denLoaded(t, denNat(), "")},
		{name: "a foreign forward its exemption covers", edit: withExemptions(exemption), cur: denLoaded(t, denNat(denForward4), "")},
		{name: "Docker cannot be reached", edit: func(s *dockerState) {
			s.available, s.reason, s.det, s.facts = false, "the Docker daemon is not reachable", dockerfw.Detection{}, dockerfw.Facts{}
		}, cur: denLoaded(t, "", ""), refused: true, detail: "the Docker daemon is not reachable"},
		{name: "an old Docker", edit: func(s *dockerState) { s.det.Version = "27.5.1" }, cur: denLoaded(t, "", ""),
			refused: true, detail: "Docker 27.5.1 is older than 28.0"},
		{name: "raw protection missing", edit: func(s *dockerState) { s.det.RawProtection = false }, cur: denLoaded(t, "", ""),
			refused: true, detail: "raw-table protection"},
		{name: "an open incoming policy", cur: denLoaded(t, "", ""), cfg: func(c *fwstate.Config) { c.DefaultInput = "ACCEPT" },
			refused: true},
		{name: "an exemption into a Docker network", edit: withExemptions(inDocker), cur: denLoaded(t, "", ""),
			refused: true, detail: "exemption 0: destination 172.17.0.9 lies in Docker network bridge (172.17.0.0/16)"},
		{name: "a foreign IPv4 forward without an exemption", cur: denLoaded(t, denNat(denForward4), ""),
			refused: true, detail: denForward4},
		{name: "a foreign IPv6 forward without an exemption", cur: denLoaded(t, "", denNat(denForward6)),
			refused: true, detail: denForward6},
		{name: "an exemption that covers another forward", edit: withExemptions(exemption),
			cur: denLoaded(t, denNat(denForward4), denNat(denForward6)), refused: true, detail: denForward6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := denState(tt.edit)
			cfg := denFileConfig(t, false, false, fhRule3000v4)
			if tt.cfg != nil {
				tt.cfg(&cfg)
			}
			c, err := dockerEnableChange(env, cfg, tt.cur, st)
			if tt.refused {
				if e := errcode.From(err); err == nil || e.Code != errcode.FirewallUnsupportedIntegration || !strings.Contains(e.Detail, tt.detail) {
					t.Fatalf("dockerEnableChange = %v, want firewall.unsupported_integration naming %q", err, tt.detail)
				}
				if !denEmpty(c) {
					t.Errorf("a refusal came with the change %+v", c)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.flag == nil || !*c.flag || !c.reload || c.cleanup || c.sources != nil || c.enable != nil ||
				len(c.rowsAdd) != 0 || len(c.rowsDel) != 0 {
				t.Errorf("change %+v, want only the flag on, the block files, the rule operations and a reload", c)
			}
			want, err := blockFiles(cfg, st, true)
			if err != nil {
				t.Fatal(err)
			}
			if !denSameFiles(c.files, want) || len(c.files) != 2 {
				t.Errorf("files %v, want what blockFiles writes for both after files", slices.Sorted(maps.Keys(c.files)))
			}
			for _, p := range []string{denAfter4, denAfter6} {
				if !denHasBlock(t, c.files[p]) {
					t.Errorf("%s has no block", p)
				}
			}
			if len(st.realized.Rules) != 1 || len(c.ops) != 1 {
				t.Fatalf("%d realized rules and %d operations, want one of each", len(st.realized.Rules), len(c.ops))
			}
			op := c.ops[0]
			if op.Kind != core.EffectCreate || !denTokens(op.Args, "route", "allow", "docker0", "172.17.0.2", "80") {
				t.Errorf("operation %+v, want the route allow to 172.17.0.2 port 80 out on docker0", op)
			}
			if got, want := denLabels(t, c.ops), []string{dockerfw.LabelPrefix + denRow1}; !slices.Equal(got, want) {
				t.Errorf("operation labels %q, want %q", got, want)
			}
		})
	}
}

// TestDenEnableForeignForwards: the refusal for uncovered foreign DNAT rules
// names every uncovered rule of both families with the exemption proposed
// for it as JSON, or says that none can name it, and leaves out the rules
// an exemption covers.
func TestDenEnableForeignForwards(t *testing.T) {
	env := newFakeHost(t).env()
	const (
		noAddress = "-A PREROUTING -p tcp -m tcp --dport 2222 -j DNAT --to-destination 10.9.0.7:22"
		covered   = "-A PREROUTING -d 192.0.2.11/32 -p udp -m udp --dport 53 -j DNAT --to-destination 10.9.0.8:5353"
	)
	cur := denLoaded(t, denNat(denForward4, noAddress, covered), denNat(denForward6))
	coveredEx := dockerfw.Exemption{OrigDestination: netip.MustParseAddr("192.0.2.11"), OrigPort: 53,
		Destination: netip.MustParseAddr("10.9.0.8"), Proto: "udp", Port: 5353}
	st := denState(func(s *dockerState) { s.cfg.Settings.ForwardExemptions = []dockerfw.Exemption{coveredEx} })
	_, err := dockerEnableChange(env, denFileConfig(t, false, false), cur, st)
	e := errcode.From(err)
	if err == nil || e.Code != errcode.FirewallUnsupportedIntegration {
		t.Fatalf("dockerEnableChange = %v, want firewall.unsupported_integration", err)
	}
	propose := func(line string, fam fwstate.Family) string {
		t.Helper()
		table := cur.V4.Table("nat")
		if fam == fwstate.V6 {
			table = cur.V6.Table("nat")
		}
		for _, r := range dockerfw.ForeignDNAT(table) {
			if r.Text != line {
				continue
			}
			ex, ok := dockerfw.Propose(r, fam)
			if !ok {
				t.Fatalf("no exemption can name %s", line)
			}
			b, err := json.Marshal(ex)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		t.Fatalf("%s is not a foreign DNAT rule of the fixture", line)
		return ""
	}
	for _, want := range []string{
		denForward4, propose(denForward4, fwstate.V4),
		denForward6, propose(denForward6, fwstate.V6),
		noAddress, "no exemption can name it",
		"forwardExemptions",
	} {
		if !strings.Contains(e.Detail, want) {
			t.Errorf("detail lacks %q:\n%s", want, e.Detail)
		}
	}
	if strings.Contains(e.Detail, covered) {
		t.Errorf("detail names the rule an exemption covers:\n%s", e.Detail)
	}
}

// TestDenEnableAlreadyOn: enabling an enabled integration is a no-op even
// when Docker has become unavailable.
func TestDenEnableAlreadyOn(t *testing.T) {
	env := newFakeHost(t).env()
	st := denState(func(s *dockerState) {
		s.cfg.Enabled, s.available, s.reason, s.facts = true, false, "the Docker daemon is not reachable", dockerfw.Facts{}
	})
	c, err := dockerEnableChange(env, denFileConfig(t, true, true), denLoaded(t, "", ""), st)
	if err != nil || !denEmpty(c) {
		t.Errorf("dockerEnableChange = %+v, %v; want no change and no error", c, err)
	}
}

// TestDenEnableManualRoutes: enabling lists each manual route rule to a
// container network as a plan note (spec 2.6 "Manual route rules"); a manual
// route rule to another address is not listed.
func TestDenEnableManualRoutes(t *testing.T) {
	env := newFakeHost(t).env()
	toWeb := "### tuple ### route:allow tcp 80 172.17.0.5 any 0.0.0.0/0 out_docker0"
	elsewhere := "### tuple ### route:allow tcp 80 192.0.2.9 any 0.0.0.0/0 out_eth1"
	cfg := denFileConfig(t, false, false, toWeb, elsewhere)
	c, err := dockerEnableChange(env, cfg, denLoaded(t, "", ""), denState(nil))
	if err != nil {
		t.Fatal(err)
	}
	var want []core.DiffLine
	for _, r := range model.Merge(cfg.Rules4, cfg.Rules6) {
		if r.To == "172.17.0.5" {
			want = append(want, core.DiffLine{Op: " ", Text: "manual route rule " + ruleText(r) + ": no effect without a Darkpanel binding"})
		}
	}
	if len(want) != 1 {
		t.Fatalf("fixture: %d manual route rules to 172.17.0.5, want 1", len(want))
	}
	if !reflect.DeepEqual(c.notes, want) {
		t.Errorf("notes = %+v, want %+v", c.notes, want)
	}
}

// TestDenDisableChange: disabling stores the flag off, writes both after
// files without the block, deletes every tagged container rule, keeps the
// stored rows and the settings, and asks for the owned-chain cleanup; it
// works without Docker and is a no-op when the integration is off.
func TestDenDisableChange(t *testing.T) {
	env := newFakeHost(t).env()
	tagged := denRouteTuple("allow", "", denTag1, dockerfw.LabelPrefix+denRow1)
	cfg := denFileConfig(t, true, true, fhRule3000v4, tagged)
	current := model.Merge(cfg.Rules4, cfg.Rules6)
	on := func(s *dockerState) { s.cfg.Enabled = true }
	gone := func(s *dockerState) {
		on(s)
		s.available, s.reason, s.det, s.facts = false, "the Docker daemon is not reachable", dockerfw.Detection{}, dockerfw.Facts{}
	}
	for name, edit := range map[string]func(*dockerState){"Docker running": on, "Docker unavailable": gone} {
		t.Run(name, func(t *testing.T) {
			st := denState(edit)
			c, err := dockerDisableChange(env, cfg, st)
			if err != nil {
				t.Fatal(err)
			}
			if c.flag == nil || *c.flag || !c.cleanup || c.reload || c.sources != nil || c.enable != nil ||
				len(c.rowsAdd) != 0 || len(c.rowsDel) != 0 {
				t.Errorf("change %+v, want only the flag off, the files, the rule deletes and the cleanup", c)
			}
			want, err := blockFiles(cfg, st, false)
			if err != nil {
				t.Fatal(err)
			}
			if !denSameFiles(c.files, want) || len(c.files) != 2 {
				t.Errorf("files %v, want what blockFiles writes without the block", slices.Sorted(maps.Keys(c.files)))
			}
			for _, p := range []string{denAfter4, denAfter6} {
				if denHasBlock(t, c.files[p]) {
					t.Errorf("%s still has the block", p)
				}
			}
			wantOps := []ufwOp{{Kind: core.EffectDelete, Resource: ruleResource(denTag1), Args: model.DeleteArgs(current[1])}}
			if !reflect.DeepEqual(c.ops, wantOps) {
				t.Errorf("operations %+v, want the delete of the tagged rule only, not the manual one: %+v", c.ops, wantOps)
			}
		})
	}
	t.Run("already off", func(t *testing.T) {
		c, err := dockerDisableChange(env, cfg, denState(nil))
		if err != nil || !denEmpty(c) {
			t.Errorf("dockerDisableChange = %+v, %v; want no change and no error", c, err)
		}
	})
}

// denHost is a fake host with a Docker daemon whose UFW reloads and holds
// the Docker block and a tagged container rule when asked.
func denHost(t *testing.T) (*fakeHost, fw, *fakeDocker) {
	t.Helper()
	h := newFakeHost(t)
	d := newFakeDocker()
	h.ufw["reload"] = func(*fhState) int { return 0 }
	f, _ := newFW(h)
	f.d = fdDeps(h, d)
	return h, f, d
}

// denSeed stores the Docker configuration of the fake host's database.
func denSeed(t *testing.T, h *fakeHost, enabled bool) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return writeDockerConfig(ctx, tx, dockerConfig{Enabled: enabled, Settings: dockerfw.DefaultSettings()})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// denDockerLines are the Docker lines of a plan's diff.
func denDockerLines(out core.PlanOutcome) []core.DiffLine {
	var lines []core.DiffLine
	for _, l := range out.Diff {
		for _, p := range []string{"docker integration", "after.rules:", "after6.rules:", "Docker chains", "container rule"} {
			if strings.HasPrefix(l.Text, p) {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// denSupported makes the fake host a supported one: Docker's chains and raw
// protection in the nft variant UFW uses, DOCKER-USER holding only -j
// RETURN, and the legacy variant unreadable.
func denSupported(h *fakeHost) {
	const filter = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n:DOCKER-FORWARD - [0:0]\n" +
		":ufw-user-input - [0:0]\n-A INPUT -j ufw-user-input\n-A FORWARD -j DOCKER-USER\n-A DOCKER-USER -j RETURN\n" +
		"-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\nCOMMIT\n"
	const raw = "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
	h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(h.t, filter), V6: fhParse(h.t, filter)}
	h.onReadVariant = func(f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
		switch {
		case f != fwstate.V4 || variant != "nft":
			return nil, nil
		case slices.Equal(tables, []string{"raw"}):
			return fhParse(h.t, raw), nil
		}
		return fhParse(h.t, filter), nil
	}
}

// TestDenEnablePlan: the plan of firewall.docker.enable on a supported host
// shows the flag and the block written into both after files; on an
// unsupported or unreachable Docker it is refused; when the integration is
// on already it shows nothing.
func TestDenEnablePlan(t *testing.T) {
	ctx := context.Background()
	raw := json.RawMessage(`{}`)

	t.Run("supported", func(t *testing.T) {
		h, f, _ := denHost(t)
		denSupported(h)
		out, err := f.dockerEnable().Handler.Plan(ctx, h.env(), raw)
		if err != nil {
			t.Fatal(err)
		}
		want := []core.DiffLine{
			{Op: "-", Text: "docker integration off"},
			{Op: "+", Text: "docker integration on"},
			{Op: "+", Text: "after.rules: Docker block written"},
			{Op: "+", Text: "after6.rules: Docker block written"},
		}
		if got := denDockerLines(out); !reflect.DeepEqual(got, want) {
			t.Errorf("Docker lines\n%+v\nwant\n%+v", got, want)
		}
		if len(out.Guards) != 1 || out.Guards[0].ID != guard.GuardID || !out.Guards[0].OK {
			t.Errorf("guards %+v, want the lockout guard passing", out.Guards)
		}
	})
	t.Run("an old Docker", func(t *testing.T) {
		h, f, d := denHost(t)
		denSupported(h)
		d.info.ServerVersion = "27.5.1"
		_, err := f.dockerEnable().Handler.Plan(ctx, h.env(), raw)
		if e := errcode.From(err); e.Code != errcode.FirewallUnsupportedIntegration || !strings.Contains(e.Detail, "Docker 27.5.1 is older than 28.0") {
			t.Errorf("plan = %v, want firewall.unsupported_integration naming the version", err)
		}
	})
	t.Run("Docker is not running", func(t *testing.T) {
		h, f, d := denHost(t)
		denSupported(h)
		d.err = fmt.Errorf("dial unix /var/run/docker.sock: %w", dockerapi.ErrUnavailable)
		_, err := f.dockerEnable().Handler.Plan(ctx, h.env(), raw)
		if e := errcode.From(err); e.Code != errcode.FirewallUnsupportedIntegration || !strings.Contains(e.Detail, "not reachable") {
			t.Errorf("plan = %v, want firewall.unsupported_integration saying Docker is not reachable", err)
		}
	})
	t.Run("already enabled", func(t *testing.T) {
		h, f, d := denHost(t)
		denSupported(h)
		denSeed(t, h, true)
		d.err = fmt.Errorf("dial unix /var/run/docker.sock: %w", dockerapi.ErrUnavailable)
		out, err := f.dockerEnable().Handler.Plan(ctx, h.env(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := denDockerLines(out); len(got) != 0 {
			t.Errorf("Docker lines %+v, want none", got)
		}
	})
}

// TestDenDisablePlan: the plan of firewall.docker.disable shows the flag off,
// the block removed from the file that has it, and the chains removed, with
// Docker unreachable too; on a host where the integration is off it shows
// nothing.
func TestDenDisablePlan(t *testing.T) {
	ctx := context.Background()
	raw := json.RawMessage(`{}`)
	h, f, d := denHost(t)
	denSupported(h)
	denSeed(t, h, true)
	tagged := denRouteTuple("allow", "", denTag1, dockerfw.LabelPrefix+denRow1)
	s := h.state()
	s.Rules4 = append(s.Rules4, tagged)
	h.setState(s)
	h.writeFile(denAfter4, denAfterFile(t, true))
	rule := model.Merge(h.config().Rules4, nil)[2]
	h.ufw[strings.Join(model.DeleteArgs(rule), " ")] = func(s *fhState) int {
		s.Rules4 = slices.DeleteFunc(s.Rules4, func(l string) bool { return l == tagged })
		return 0
	}
	d.err = fmt.Errorf("docker: status %d", 500)

	out, err := f.dockerDisable().Handler.Plan(ctx, h.env(), raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: "-", Text: "docker integration on"},
		{Op: "+", Text: "docker integration off"},
		{Op: "-", Text: "after.rules: Docker block removed"},
		{Op: "-", Text: "Docker chains removed"},
	}
	if got := denDockerLines(out); !reflect.DeepEqual(got, want) {
		t.Errorf("Docker lines\n%+v\nwant\n%+v", got, want)
	}

	denSeed(t, h, false)
	out, err = f.dockerDisable().Handler.Plan(ctx, h.env(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := denDockerLines(out); len(got) != 0 {
		t.Errorf("Docker lines %+v with the integration off, want none", got)
	}
}

// TestDenVerify: verify of enable and disable compares the stored flag and
// the after files with what the action set, before the comparison with the
// prediction.
func TestDenVerify(t *testing.T) {
	ctx := context.Background()
	h, f, _ := denHost(t)
	verify := func(def core.ActionDef) error {
		return def.Handler.Verify(ctx, h.env(), json.RawMessage(`{}`), json.RawMessage(`{}`))
	}
	mismatch := func(name string, err error) {
		t.Helper()
		if e := errcode.From(err); e.Code != errcode.FirewallPredictionMismatch || !strings.Contains(e.Detail, "after the change") {
			t.Errorf("%s verify = %v, want the action's own prediction mismatch", name, err)
		}
	}
	h.writeFile(denAfter4, denAfterFile(t, false))
	h.writeFile(denAfter6, denAfterFile(t, false))
	if err := verify(f.dockerDisable()); err != nil {
		t.Errorf("disable verify with the flag off and no block = %v, want nil", err)
	}
	mismatch("enable with the flag off", verify(f.dockerEnable()))

	denSeed(t, h, true)
	mismatch("enable without the block", verify(f.dockerEnable()))
	mismatch("disable with the flag on", verify(f.dockerDisable()))
	h.writeFile(denAfter4, denAfterFile(t, true))
	mismatch("enable with the block in one file only", verify(f.dockerEnable()))
	h.writeFile(denAfter6, denAfterFile(t, true))
	if err := verify(f.dockerEnable()); err != nil {
		t.Errorf("enable verify with the flag and both blocks = %v, want nil", err)
	}

	denSeed(t, h, false)
	mismatch("disable with the blocks still in the files", verify(f.dockerDisable()))
}

// TestDenActionDefinitions: both actions are the revertible, agent-side,
// firewall-locked mutations of every firewall change, with the lockout
// guard and their empty guarded input.
func TestDenActionDefinitions(t *testing.T) {
	_, f, _ := denHost(t)
	for name, def := range map[string]core.ActionDef{
		"firewall.docker.enable": f.dockerEnable(), "firewall.docker.disable": f.dockerDisable(),
	} {
		s := def.Spec
		if s.Name != name || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
			!slices.Equal(s.LockKeys, []string{"firewall"}) || s.RepairedBy != "firewall.state.repair" ||
			len(s.Guards) != 1 || s.Guards[0].ID != guard.GuardID || !s.Guards[0].Overridable {
			t.Errorf("%s spec %+v", name, s)
		}
		if err := schema.NewValidator().ValidateInput(def, json.RawMessage(`{}`)); err != nil {
			t.Errorf("%s rejects an empty input: %v", name, err)
		}
		if err := schema.NewValidator().ValidateInput(def, json.RawMessage(`{"manual":true}`)); err == nil {
			t.Errorf("%s accepts a manual flag it has no use for", name)
		}
	}
}
