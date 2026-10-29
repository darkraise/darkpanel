package firewall

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	dreAfter4 = "/etc/ufw/after.rules"
	dreAfter6 = "/etc/ufw/after6.rules"
	dreAllow1 = "01J00000000000000000000001"
)

func dreAddr(s string) netip.Addr     { return netip.MustParseAddr(s) }
func drePrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// dreFacts is one running container, web, that publishes host port 8080 to
// its port 80 on the default bridge.
func dreFacts() dockerfw.Facts {
	return dockerfw.Facts{
		Containers: []dockerfw.Container{{
			ID: "id-web", Name: "web",
			Ports:     []dockerfw.Published{{HostAddr: dreAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"}},
			Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: dreAddr("172.17.0.2")}},
		}},
		Networks: []dockerfw.Network{{
			ID: "id-bridge", Name: "bridge", Driver: "bridge", Bridge: "docker0",
			Subnets: []netip.Prefix{drePrefix("172.17.0.0/16")},
		}},
	}
}

// dreRows is an allowance of web's binding 8080/tcp.
func dreRows() []dockerfw.ContainerRule {
	b := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	return []dockerfw.ContainerRule{{ID: dreAllow1, Binding: b, Kind: dockerfw.Allow}}
}

// dreState is the Docker state of a supported daemon whose integration flag
// is enabled, holding rows and settings s, realized against dreFacts.
func dreState(enabled bool, rows []dockerfw.ContainerRule, s dockerfw.Settings) dockerState {
	facts := dreFacts()
	return dockerState{
		available: true,
		det:       dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft", RawProtection: true},
		facts:     facts,
		cfg:       dockerConfig{Enabled: enabled, Settings: s},
		rows:      rows,
		realized:  dockerfw.Realize(rows, facts, s),
	}
}

// dreIDs mints distinct rule ids.
func dreIDs() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("01J%023d", n)
	}
}

// dreRefused fails the test unless err is an *errcode.Error with code.
func dreRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return e
}

// dreHost is a fake host whose Docker daemon cannot be reached and whose
// ufw reloads, with the firewall over it.
func dreHost(t *testing.T) (*fakeHost, fw) {
	t.Helper()
	h := newFakeHost(t)
	d := newFakeDocker()
	d.err = fmt.Errorf("dockerapi: %w", dockerapi.ErrUnavailable)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	f, _ := newFW(h)
	f.d = fdDeps(h, d)
	return h, f
}

// TestDreRepairChange: the repair rewrites the block into both files,
// realizes the stored rows and reloads, as a repair of UFW's loaded state;
// only discardForeign together with manual lets it discard foreign rules.
func TestDreRepairChange(t *testing.T) {
	cfg := newFakeHost(t).config()
	settings := dockerfw.DefaultSettings()
	supported := dreState(true, nil, settings)
	withRows := dreState(true, dreRows(), settings)
	off := dreState(false, nil, settings)
	gone := supported
	gone.available, gone.reason = false, "the Docker daemon is not running"
	nftables := supported
	nftables.det.Driver = "nftables"

	refusals := []struct {
		name string
		in   DockerRepairInput
		st   dockerState
		code errcode.Code
		want string
	}{
		{"discard without manual", DockerRepairInput{DiscardForeign: true}, supported, errcode.FirewallManualRequired, "manual: true"},
		{"integration off", DockerRepairInput{}, off, errcode.InvalidInput, "off"},
		{"daemon unavailable", DockerRepairInput{}, gone, errcode.FirewallUnsupportedIntegration, "not running"},
		{"unsupported driver", DockerRepairInput{}, nftables, errcode.FirewallUnsupportedIntegration, ""},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			_, err := dockerRepairChange(cfg, tc.in, tc.st, dreIDs())
			if e := dreRefused(t, err, tc.code); !strings.Contains(e.Detail, tc.want) {
				t.Errorf("detail %q, want it to contain %q", e.Detail, tc.want)
			}
		})
	}

	t.Run("rewrites the block and reloads", func(t *testing.T) {
		c, err := dockerRepairChange(cfg, DockerRepairInput{}, supported, dreIDs())
		if err != nil {
			t.Fatal(err)
		}
		if !c.repair || !c.reload || c.discardForeign || c.flag != nil || c.sources != nil || len(c.ops) != 0 {
			t.Errorf("change %+v, want a repair that reloads and changes no flag, setting, rule or foreign rule", c)
		}
		for _, p := range []string{dreAfter4, dreAfter6} {
			if _, ok, err := dockerfw.BlockOf(c.files[p]); err != nil || !ok {
				t.Errorf("%s holds no marked block (ok %v, err %v)", p, ok, err)
			}
		}
	})
	t.Run("realizes the stored rows", func(t *testing.T) {
		c, err := dockerRepairChange(cfg, DockerRepairInput{}, withRows, dreIDs())
		if err != nil {
			t.Fatal(err)
		}
		if len(c.ops) != 1 || c.ops[0].Kind != core.EffectCreate {
			t.Errorf("ops %+v, want the one route allow for web's address", c.ops)
		}
		if want := ":" + dreRows()[0].Binding.Chain() + " - [0:0]"; !strings.Contains(string(c.files[dreAfter4]), want) {
			t.Errorf("after.rules lacks the binding chain declaration %q", want)
		}
	})
	t.Run("manual alone discards nothing", func(t *testing.T) {
		c, err := dockerRepairChange(cfg, DockerRepairInput{Manual: true}, supported, dreIDs())
		if err != nil || c.discardForeign {
			t.Errorf("discardForeign %v, err %v; want false, nil", c.discardForeign, err)
		}
	})
	t.Run("discard with manual", func(t *testing.T) {
		c, err := dockerRepairChange(cfg, DockerRepairInput{DiscardForeign: true, Manual: true}, supported, dreIDs())
		if err != nil || !c.discardForeign || !c.repair {
			t.Errorf("discardForeign %v repair %v, err %v; want true, true, nil", c.discardForeign, c.repair, err)
		}
	})
	t.Run("an open incoming policy is not refused", func(t *testing.T) {
		open := cfg
		open.DefaultInput = "ACCEPT"
		if _, err := dockerRepairChange(open, DockerRepairInput{}, supported, dreIDs()); err != nil {
			t.Errorf("repair refused an ACCEPT incoming policy, which it cannot change: %v", err)
		}
	})
}

// TestDreRepairDiscards: a repair that discards foreign rules lists each one
// in the plan, the rules DOCKER-USER holds beyond the block's own, per
// family and in chain order; any other repair lists none.
func TestDreRepairDiscards(t *testing.T) {
	cfg := newFakeHost(t).config()
	block := dockerfw.Render(dockerfw.BlockInput{Settings: dockerfw.DefaultSettings()}, fwstate.V4)
	spliced, err := dockerfw.WithBlock(cfg.Files[dreAfter4].Data, block)
	if err != nil {
		t.Fatal(err)
	}
	withBlock := cfg
	withBlock.Files = maps.Clone(cfg.Files)
	e := withBlock.Files[dreAfter4]
	e.Data = spliced
	withBlock.Files[dreAfter4] = e

	loaded := func(text string) fwstate.Loaded {
		rs, err := netfilter.ParseRestore(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return fwstate.Loaded{Variant: "nft", V4: rs}
	}
	blockPlus := func(foreign ...string) fwstate.Loaded {
		extra := ""
		for _, l := range foreign {
			extra += l + "\n"
		}
		return loaded(strings.Replace(string(block), "COMMIT\n", extra+"COMMIT\n", 1))
	}
	const drop1, drop2 = "-A DOCKER-USER -s 203.0.113.0/24 -j DROP", "-A DOCKER-USER -i eth9 -j ACCEPT"
	line := func(text string) core.DiffLine {
		return core.DiffLine{Op: "-", Text: "foreign DOCKER-USER rule (v4): " + text}
	}
	both := DockerRepairInput{DiscardForeign: true, Manual: true}

	cases := []struct {
		name string
		in   DockerRepairInput
		cfg  fwstate.Config
		cur  fwstate.Loaded
		want []core.DiffLine
	}{
		{"one rule beside the block", both, withBlock, blockPlus(drop1), []core.DiffLine{line(drop1)}},
		{"two rules in chain order", both, withBlock, blockPlus(drop1, drop2), []core.DiffLine{line(drop1), line(drop2)}},
		{"only the block", both, withBlock, blockPlus(), nil},
		{"no block in the file: every rule but Docker's RETURN", both, cfg,
			loaded("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\n" + drop1 + "\nCOMMIT\n"),
			[]core.DiffLine{line(drop1)}},
		{"a repair without discardForeign lists none", DockerRepairInput{Manual: true}, withBlock, blockPlus(drop1), nil},
		{"discardForeign without manual lists none", DockerRepairInput{DiscardForeign: true}, withBlock, blockPlus(drop1), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repairDiscards(tc.in, tc.cfg, tc.cur)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("lines %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDreRepairOffRefusedByPlan: planning the repair while the integration is
// off is refused before anything is computed.
func TestDreRepairOffRefusedByPlan(t *testing.T) {
	h, f := dreHost(t)
	_, err := f.dockerRepair().Handler.Plan(t.Context(), h.env(), json.RawMessage(`{}`))
	_ = dreRefused(t, err, errcode.InvalidInput)
}

// TestDreActionSpec: the action is a revertible agent action under the
// firewall lock, repaired by firewall.state.repair, and its input schema
// takes the documented fields only.
func TestDreActionSpec(t *testing.T) {
	def := fw{}.dockerRepair()
	s := def.Spec
	if s.Name != "firewall.docker.repair" || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"firewall"}) || s.RepairedBy != "firewall.state.repair" {
		t.Errorf("spec %+v, want a version 1 revertible agent action under the firewall lock repaired by firewall.state.repair", s)
	}
	v := schema.NewValidator()
	for _, raw := range []string{`{}`, `{"discardForeign":true,"manual":true}`} {
		if err := v.ValidateInput(def, json.RawMessage(raw)); err != nil {
			t.Errorf("input %s refused: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"discardForeign":"yes"}`, `{"discard":true}`} {
		if err := v.ValidateInput(def, json.RawMessage(raw)); err == nil {
			t.Errorf("input %s accepted", raw)
		}
	}
}

// TestDreVerifyBrokenMarkers: a repair whose after file holds broken block
// markers fails its verify with the marker error, not with a missing block.
func TestDreVerifyBrokenMarkers(t *testing.T) {
	h, f := dreHost(t)
	denSeed(t, h, true)
	h.writeFile(denAfter4, append(denAfterFile(t, true), []byte("# BEGIN DARKPANEL docker\n")...))
	h.writeFile(denAfter6, denAfterFile(t, true))
	err := f.dockerRepair().Handler.Verify(t.Context(), h.env(), json.RawMessage(`{}`), json.RawMessage(`{}`))
	if err == nil || strings.Contains(err.Error(), "holds no Docker block") || !strings.Contains(err.Error(), denAfter4+":") {
		t.Errorf("verify = %v, want the marker error of %s", err, denAfter4)
	}
}
