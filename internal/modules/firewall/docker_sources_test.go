package firewall

import (
	"encoding/json"
	"errors"
	"fmt"
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
)

const (
	dsoAfter4 = "/etc/ufw/after.rules"
	dsoAfter6 = "/etc/ufw/after6.rules"
	dsoAllow1 = "01J00000000000000000000001"
	dsoDeny2  = "01J00000000000000000000002"
)

func dsoAddr(s string) netip.Addr     { return netip.MustParseAddr(s) }
func dsoPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// dsoFacts is one running container, web, that publishes host port 8080 to
// its port 80 on the default bridge.
func dsoFacts() dockerfw.Facts {
	return dockerfw.Facts{
		Containers: []dockerfw.Container{{
			ID: "id-web", Name: "web",
			Ports:     []dockerfw.Published{{HostAddr: dsoAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"}},
			Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: dsoAddr("172.17.0.2")}},
		}},
		Networks: []dockerfw.Network{{
			ID: "id-bridge", Name: "bridge", Driver: "bridge", Bridge: "docker0",
			Subnets: []netip.Prefix{dsoPrefix("172.17.0.0/16")},
		}},
	}
}

// dsoRows is an allowance of web's binding 8080/tcp and, when deny is set,
// a deny of 203.0.113.0/24 on it.
func dsoRows(deny bool) []dockerfw.ContainerRule {
	b := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	rows := []dockerfw.ContainerRule{{ID: dsoAllow1, Binding: b, Kind: dockerfw.Allow}}
	if deny {
		rows = append(rows, dockerfw.ContainerRule{ID: dsoDeny2, Binding: b, Kind: dockerfw.Deny, From: dsoPrefix("203.0.113.0/24")})
	}
	return rows
}

// dsoState is the Docker state of a supported daemon whose integration flag
// is enabled, holding rows and settings s, realized against dsoFacts.
func dsoState(enabled bool, rows []dockerfw.ContainerRule, s dockerfw.Settings) dockerState {
	facts := dsoFacts()
	return dockerState{
		available: true,
		det:       dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft", RawProtection: true},
		facts:     facts,
		cfg:       dockerConfig{Enabled: enabled, Settings: s},
		rows:      rows,
		realized:  dockerfw.Realize(rows, facts, s),
	}
}

// dsoIDs mints distinct rule ids.
func dsoIDs() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("01J%023d", n)
	}
}

// dsoRefused fails the test unless err is an *errcode.Error with code.
func dsoRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return e
}

// dsoEmpty reports whether c asks for nothing.
func dsoEmpty(c change) bool {
	return c.sources == nil && c.flag == nil && c.files == nil && !c.reload && !c.cleanup && !c.repair && !c.discardForeign &&
		len(c.ops) == 0 && len(c.rowsAdd) == 0 && len(c.rowsDel) == 0
}

// dsoHost is a fake host whose Docker daemon cannot be reached and whose
// ufw reloads, with the firewall over it.
func dsoHost(t *testing.T) (*fakeHost, fw) {
	t.Helper()
	h := newFakeHost(t)
	d := newFakeDocker()
	d.err = fmt.Errorf("dockerapi: %w", dockerapi.ErrUnavailable)
	h.ufw["reload"] = func(*fhState) int { return 0 }
	f, _ := newFW(h)
	f.d = fdDeps(h, d)
	return h, f
}

func dsoSources(ranges ...string) *[]netip.Prefix {
	out := make([]netip.Prefix, len(ranges))
	for i, r := range ranges {
		out[i] = dsoPrefix(r)
	}
	return &out
}

func dsoExemptions(e ...dockerfw.Exemption) *[]dockerfw.Exemption { return &e }

// dsoForward is a foreign port forward: connections to 192.0.2.10:8443 that
// the host's own rule sends to dest:443.
func dsoForward(dest string) dockerfw.Exemption {
	return dockerfw.Exemption{OrigDestination: dsoAddr("192.0.2.10"), OrigPort: 8443, Destination: dsoAddr(dest), Proto: "tcp", Port: 443}
}

// TestDsoSourcesChange: the new lists are validated against the settings'
// rules, the Docker networks and the stored container rules; with the
// integration off they are only stored, and with it on the block is
// rewritten and UFW reloaded.
func TestDsoSourcesChange(t *testing.T) {
	cfg := newFakeHost(t).config()
	settings := dockerfw.DefaultSettings()
	off := dsoState(false, nil, settings)
	on := dsoState(true, nil, settings)
	denied := dsoState(false, dsoRows(true), settings)
	gone := on
	gone.available, gone.reason = false, "the Docker daemon is not running"
	nftables := on
	nftables.det.Driver = "nftables"
	open := cfg
	open.DefaultInput = "ACCEPT"
	extra := append(slices.Clone(settings.PrivateSources), dsoPrefix("203.0.113.0/24"))
	var tooMany []string
	for i := range 33 {
		tooMany = append(tooMany, fmt.Sprintf("10.%d.0.0/16", i))
	}

	refusals := []struct {
		name string
		cfg  fwstate.Config
		in   DockerSourcesInput
		st   dockerState
		code errcode.Code
		path string
		want string
	}{
		{"no list", cfg, DockerSourcesInput{}, off, errcode.InvalidInput, "/privateSources", ""},
		{"a range shorter than /8", cfg, DockerSourcesInput{PrivateSources: dsoSources("0.0.0.0/0")}, off, errcode.InvalidInput, "/privateSources/0", ""},
		{"a range that is not masked", cfg, DockerSourcesInput{PrivateSources: dsoSources("10.1.2.3/8")}, off, errcode.InvalidInput, "/privateSources/0", ""},
		{"more than 32 ranges", cfg, DockerSourcesInput{PrivateSources: dsoSources(tooMany...)}, off, errcode.InvalidInput, "", ""},
		{"an exemption with an unknown protocol", cfg,
			DockerSourcesInput{ForwardExemptions: dsoExemptions(dockerfw.Exemption{OrigDestination: dsoAddr("192.0.2.10"), Destination: dsoAddr("10.9.0.5"), Proto: "icmp"})},
			off, errcode.InvalidInput, "/forwardExemptions/0/proto", ""},
		{"an exemption into a Docker network", cfg, DockerSourcesInput{ForwardExemptions: dsoExemptions(dsoForward("172.17.0.2"))},
			off, errcode.FirewallUnsupportedIntegration, "", "bridge"},
		{"a deny that a new range would shadow", cfg, DockerSourcesInput{PrivateSources: &extra},
			denied, errcode.InvalidInput, "/privateSources", dsoDeny2},
		{"integration on with an open incoming policy", open, DockerSourcesInput{PrivateSources: &extra},
			on, errcode.FirewallUnsupportedIntegration, "", ""},
		{"integration on without a daemon", cfg, DockerSourcesInput{PrivateSources: &extra},
			gone, errcode.FirewallUnsupportedIntegration, "", "not running"},
		{"integration on with an unsupported driver", cfg, DockerSourcesInput{PrivateSources: &extra},
			nftables, errcode.FirewallUnsupportedIntegration, "", ""},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			_, err := dockerSourcesChange(tc.cfg, tc.in, tc.st, dsoIDs())
			e := dsoRefused(t, err, tc.code)
			if !strings.Contains(e.Detail, tc.want) {
				t.Errorf("detail %q, want it to contain %q", e.Detail, tc.want)
			}
			if tc.path != "" && (len(e.Fields) == 0 || e.Fields[0].Path != tc.path) {
				t.Errorf("fields %+v, want the first at %s", e.Fields, tc.path)
			}
		})
	}

	t.Run("the stored lists change nothing", func(t *testing.T) {
		same := settings.PrivateSources
		c, err := dockerSourcesChange(cfg, DockerSourcesInput{PrivateSources: &same, ForwardExemptions: dsoExemptions()}, on, dsoIDs())
		if err != nil || !dsoEmpty(c) {
			t.Errorf("change %+v, err %v; want an empty change", c, err)
		}
	})
	t.Run("integration off: stored, nothing written", func(t *testing.T) {
		c, err := dockerSourcesChange(cfg, DockerSourcesInput{PrivateSources: &extra}, off, dsoIDs())
		if err != nil {
			t.Fatal(err)
		}
		if c.sources == nil || !slices.Equal(c.sources.PrivateSources, extra) || len(c.sources.ForwardExemptions) != 0 {
			t.Errorf("sources %+v, want the new ranges and no exemptions", c.sources)
		}
		if c.files != nil || c.reload || len(c.ops) != 0 || c.flag != nil || c.repair {
			t.Errorf("change %+v, want only the stored settings", c)
		}
	})
	t.Run("integration off: a missing daemon and an open policy do not matter", func(t *testing.T) {
		st := off
		st.available = false
		if _, err := dockerSourcesChange(open, DockerSourcesInput{PrivateSources: &extra}, st, dsoIDs()); err != nil {
			t.Errorf("refused: %v", err)
		}
	})
	t.Run("one list keeps the other", func(t *testing.T) {
		st := dsoState(false, nil, dockerfw.Settings{PrivateSources: extra})
		c, err := dockerSourcesChange(cfg, DockerSourcesInput{ForwardExemptions: dsoExemptions(dsoForward("10.9.0.5"))}, st, dsoIDs())
		if err != nil {
			t.Fatal(err)
		}
		if c.sources == nil || !slices.Equal(c.sources.PrivateSources, extra) || len(c.sources.ForwardExemptions) != 1 {
			t.Errorf("sources %+v, want the stored ranges and the one new exemption", c.sources)
		}
	})
	t.Run("integration on: the block is rewritten and UFW reloaded", func(t *testing.T) {
		c, err := dockerSourcesChange(cfg, DockerSourcesInput{
			PrivateSources: &extra, ForwardExemptions: dsoExemptions(dsoForward("10.9.0.5")),
		}, on, dsoIDs())
		if err != nil {
			t.Fatal(err)
		}
		if c.sources == nil || !slices.Equal(c.sources.PrivateSources, extra) || !c.reload || c.flag != nil || c.repair {
			t.Errorf("change %+v, want the new settings and a reload", c)
		}
		after4, after6 := string(c.files[dsoAfter4]), string(c.files[dsoAfter6])
		for _, want := range []string{
			"-A DOCKER-USER -s 203.0.113.0/24 -j RETURN",
			"-A DOCKER-USER -m conntrack --ctstate DNAT --ctorigdst 192.0.2.10 --ctorigdstport 8443 -p tcp -d 10.9.0.5 --dport 443 -j RETURN",
		} {
			if !strings.Contains(after4, want) {
				t.Errorf("after.rules lacks %q", want)
			}
		}
		if strings.Contains(after6, "203.0.113.0/24") || strings.Contains(after6, "10.9.0.5") {
			t.Errorf("after6.rules holds IPv4 entries:\n%s", after6)
		}
		if _, ok, err := dockerfw.BlockOf(c.files[dsoAfter6]); err != nil || !ok {
			t.Errorf("after6.rules holds no marked block (ok %v, err %v)", ok, err)
		}
	})
	t.Run("integration on: the stored rows are realized with it", func(t *testing.T) {
		st := dsoState(true, dsoRows(false), settings)
		c, err := dockerSourcesChange(cfg, DockerSourcesInput{PrivateSources: &extra}, st, dsoIDs())
		if err != nil {
			t.Fatal(err)
		}
		if len(c.ops) != 1 || c.ops[0].Kind != core.EffectCreate {
			t.Errorf("ops %+v, want the one route allow for web's address", c.ops)
		}
	})
}

// TestDsoSourcesSetApplied: with the integration off the action runs
// through the engine's steps, stores the lists as one journaled setting
// effect and runs no ufw command; the same input again changes nothing.
func TestDsoSourcesSetApplied(t *testing.T) {
	h, f := dsoHost(t)
	def := f.dockerSourcesSet()
	out, ran := acRun(t, h, def, `{"privateSources":["10.0.0.0/8","203.0.113.0/24"]}`)
	if len(ran) != 0 {
		t.Errorf("ufw ran %q, want nothing while the integration is off", ran)
	}
	if !slices.ContainsFunc(out.Diff, func(l core.DiffLine) bool { return strings.HasPrefix(l.Text, "private sources:") }) {
		t.Errorf("plan lines %q, want one that starts with \"private sources:\"", acDiff(out))
	}
	got, err := readDockerConfig(t.Context(), h.db)
	if err != nil {
		t.Fatal(err)
	}
	if want := []netip.Prefix{dsoPrefix("10.0.0.0/8"), dsoPrefix("203.0.113.0/24")}; !slices.Equal(got.Settings.PrivateSources, want) ||
		len(got.Settings.ForwardExemptions) != 0 || got.Enabled {
		t.Errorf("stored config %+v, want the two ranges, no exemptions and the flag off", got)
	}
	var made []string
	for _, e := range h.journal.effects {
		if e.State == core.EffectMade {
			made = append(made, e.Resource)
		}
	}
	if want := []string{"firewall.setting:sources"}; !slices.Equal(made, want) {
		t.Errorf("effects made %q, want %q", made, want)
	}

	again, _ := acRun(t, h, def, `{"privateSources":["10.0.0.0/8","203.0.113.0/24"]}`)
	if len(again.Diff) != 0 || len(h.journal.effects) != len(made) {
		t.Errorf("the same lists again: plan %q, %d effects; want no plan line and no new effect", acDiff(again), len(h.journal.effects))
	}
}

// TestDsoSourcesSetPlanRefused: an input that names neither list is refused
// when planned.
func TestDsoSourcesSetPlanRefused(t *testing.T) {
	h, f := dsoHost(t)
	_, err := f.dockerSourcesSet().Handler.Plan(t.Context(), h.env(), []byte(`{}`))
	e := dsoRefused(t, err, errcode.InvalidInput)
	if len(e.Fields) == 0 || e.Fields[0].Path != "/privateSources" {
		t.Errorf("fields %+v, want the first at /privateSources", e.Fields)
	}
}

// TestDsoActionSpec: the action is a revertible agent action under the
// firewall lock, repaired by firewall.state.repair, and its input schema
// takes the documented fields only.
func TestDsoActionSpec(t *testing.T) {
	def := fw{}.dockerSourcesSet()
	s := def.Spec
	if s.Name != "firewall.docker.set-sources" || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"firewall"}) || s.RepairedBy != "firewall.state.repair" {
		t.Errorf("spec %+v, want a version 1 revertible agent action under the firewall lock repaired by firewall.state.repair", s)
	}
	v := schema.NewValidator()
	for _, raw := range []string{
		`{"privateSources":["10.0.0.0/8","fc00::/7"]}`, `{"privateSources":[]}`,
		`{"forwardExemptions":[{"origDestination":"192.0.2.10","destination":"10.9.0.5","proto":"tcp","port":80}]}`,
	} {
		if err := v.ValidateInput(def, json.RawMessage(raw)); err != nil {
			t.Errorf("input %s refused: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"privateSources":"10.0.0.0/8"}`, `{"privateSources":["not-a-prefix"]}`,
		`{"forwardExemptions":[{"origDestination":"192.0.2.10"}]}`,
	} {
		if err := v.ValidateInput(def, json.RawMessage(raw)); err == nil {
			t.Errorf("input %s accepted", raw)
		}
	}
}
