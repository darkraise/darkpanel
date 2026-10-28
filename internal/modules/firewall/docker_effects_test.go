package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

const (
	deTestID1 = "01J00000000000000000000011"
	deTestID2 = "01J00000000000000000000012"
	deTestID3 = "01J00000000000000000000013"

	deTestAllowJSON = `{"id":"` + deTestID1 + `","binding":{"selector":"web","hostPort":8080,"proto":"tcp"},"kind":"allow"}`
	deTestDenyJSON  = `{"id":"` + deTestID2 + `","binding":{"selector":"web","hostAddr":"192.0.2.10","hostPort":8080,"proto":"tcp"},` +
		`"kind":"deny","from":"203.0.113.0/24"}`
	deTestDefaultSources = `{"privateSources":["10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","100.64.0.0/10","fc00::/7","fe80::/10"],` +
		`"forwardExemptions":[]}`
)

// The filter tables a host holds without and with Darkpanel's Docker chains.
const (
	deTestSavePlain = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n" +
		"-A DOCKER-USER -j RETURN\nCOMMIT\n"
	deTestSaveDeny = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n" +
		":darkpanel-docker-deny - [0:0]\n-A DOCKER-USER -j RETURN\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n"
	deTestSaveBinding = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n" +
		":dp-b-0123456789ab - [0:0]\n-A DOCKER-USER -j RETURN\n-A dp-b-0123456789ab -j DROP\nCOMMIT\n"
)

func deTestAllow() dockerfw.ContainerRule {
	return dockerfw.ContainerRule{ID: deTestID1, Kind: dockerfw.Allow,
		Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}}
}

func deTestDeny() dockerfw.ContainerRule {
	return dockerfw.ContainerRule{ID: deTestID2, Kind: dockerfw.Deny, From: netip.MustParsePrefix("203.0.113.0/24"),
		Binding: dockerfw.Binding{Selector: "web", HostAddr: netip.MustParseAddr("192.0.2.10"), HostPort: 8080, Proto: "tcp"}}
}

// deTestHold sets the chains the fake host has loaded: the IPv4 and IPv6
// save texts, "" for a family without ip6tables.
func deTestHold(t *testing.T, h *fakeHost, v4, v6 string) {
	t.Helper()
	h.loaded = fwstate.Loaded{Variant: "nft"}
	if v4 != "" {
		h.loaded.V4 = fhParse(t, v4)
	}
	if v6 != "" {
		h.loaded.V6 = fhParse(t, v6)
	}
}

func deTestRun(t *testing.T, h *fakeHost, op dockerOp) core.Effect {
	t.Helper()
	h.journal.effects = nil
	if err := runDockerEffect(context.Background(), h.env(), h.deps(), op); err != nil {
		t.Fatal(err)
	}
	e := efTestOnly(t, h)
	if e.State != core.EffectMade || !e.Local || !e.InverseLocal || e.Before.Version != nil || e.After.Version != nil || e.Post.Version != nil {
		t.Errorf("effect = %+v, want made, local, inverse-local, without a version", e)
	}
	return e
}

func deTestStates(t *testing.T, e core.Effect, before, after string) {
	t.Helper()
	if !jsonEqual(e.Before.State, json.RawMessage(before)) || !jsonEqual(e.After.State, json.RawMessage(after)) ||
		!jsonEqual(e.Post.State, json.RawMessage(after)) {
		t.Errorf("%s: before %s after %s post %s, want %s then %s", e.Resource, e.Before.State, e.After.State, e.Post.State, before, after)
	}
}

func deTestRules(t *testing.T, h *fakeHost) []dockerfw.ContainerRule {
	t.Helper()
	rows, err := listContainerRules(context.Background(), h.db)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestDeRunDockerEffectJournalsEachOp(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)

	e := deTestRun(t, h, flagOp(true))
	if e.Kind != core.EffectModify || e.Resource != "firewall.setting:docker" {
		t.Errorf("flag effect = %+v", e)
	}
	deTestStates(t, e, `false`, `true`)

	sources := dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	e = deTestRun(t, h, sourcesOp(sources))
	if e.Kind != core.EffectModify || e.Resource != "firewall.setting:sources" {
		t.Errorf("sources effect = %+v", e)
	}
	deTestStates(t, e, deTestDefaultSources, `{"privateSources":["10.0.0.0/8"],"forwardExemptions":[]}`)
	cfg, err := readDockerConfig(ctx, h.db)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || !slices.Equal(cfg.Settings.PrivateSources, sources.PrivateSources) {
		t.Errorf("config after the sources = %+v, want the flag kept and the new sources", cfg)
	}
	deTestRun(t, h, flagOp(false))
	if cfg, err = readDockerConfig(ctx, h.db); err != nil || cfg.Enabled || !slices.Equal(cfg.Settings.PrivateSources, sources.PrivateSources) {
		t.Errorf("config after the flag = %+v, %v, want the sources kept", cfg, err)
	}

	allow, deny := deTestAllow(), deTestDeny()
	e = deTestRun(t, h, rowCreateOp(allow))
	if e.Kind != core.EffectCreate || e.Resource != "firewall.container:"+deTestID1 ||
		e.Recovery == nil || *e.Recovery != (core.RecoveryID{Kind: "marker", Value: deTestID1}) {
		t.Errorf("row creation effect = %+v", e)
	}
	deTestStates(t, e, `null`, deTestAllowJSON)
	deTestRun(t, h, rowCreateOp(deny))
	if got := deTestRules(t, h); !slices.Equal(got, []dockerfw.ContainerRule{allow, deny}) {
		t.Errorf("rows = %+v, want the allow and the deny", got)
	}
	e = deTestRun(t, h, rowDeleteOp(allow))
	if e.Kind != core.EffectDelete || e.Resource != "firewall.container:"+deTestID1 || e.Recovery != nil {
		t.Errorf("row deletion effect = %+v", e)
	}
	deTestStates(t, e, deTestAllowJSON, `null`)
	if got := deTestRules(t, h); !slices.Equal(got, []dockerfw.ContainerRule{deny}) {
		t.Errorf("rows after the deletion = %+v, want only the deny", got)
	}

	deTestHold(t, h, deTestSaveDeny, deTestSavePlain)
	cleaned := 0
	e = deTestRun(t, h, chainsOp("absent", func(context.Context, core.Env) error {
		cleaned++
		deTestHold(t, h, deTestSavePlain, deTestSavePlain)
		return nil
	}))
	if e.Kind != core.EffectModify || e.Resource != "firewall.chains:docker" || cleaned != 1 {
		t.Errorf("chains effect = %+v after %d cleanups", e, cleaned)
	}
	deTestStates(t, e, `"loaded"`, `"absent"`)
}

func TestDeRunDockerEffectOutcomes(t *testing.T) {
	boom := errors.New("boom")
	// chains runs a chains op that leaves the host as set by leave, which
	// may also fail.
	chains := func(h *fakeHost, ran *bool, leave string, fail error) dockerOp {
		return chainsOp("absent", func(context.Context, core.Env) error {
			*ran = true
			deTestHold(h.t, h, leave, leave)
			return fail
		})
	}
	for _, c := range []struct {
		name      string
		op        func(h *fakeHost, ran *bool) dockerOp
		failOn    string
		wantErr   bool
		wantState core.EffectState
		wantNone  bool
	}{
		{name: "fails, nothing changed", wantErr: true, wantState: core.EffectSkipped,
			op: func(h *fakeHost, ran *bool) dockerOp { return chains(h, ran, deTestSaveDeny, boom) }},
		{name: "succeeds, nothing changed", wantState: core.EffectSkipped,
			op: func(h *fakeHost, ran *bool) dockerOp { return chains(h, ran, deTestSaveDeny, nil) }},
		{name: "fails after the change", wantErr: true, wantState: core.EffectIntended,
			op: func(h *fakeHost, ran *bool) dockerOp { return chains(h, ran, deTestSavePlain, boom) }},
		{name: "ends in neither state", wantErr: true, wantState: core.EffectIntended,
			op: func(_ *fakeHost, ran *bool) dockerOp {
				op := sourcesOp(dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
				write := sourcesOp(dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}}).apply
				op.apply = func(ctx context.Context, env core.Env) error { *ran = true; return write(ctx, env) }
				return op
			}},
		{name: "journal refuses the intent", failOn: "intend", wantErr: true, wantNone: true,
			op: func(h *fakeHost, ran *bool) dockerOp { return chains(h, ran, deTestSavePlain, nil) }},
		{name: "journal refuses made", failOn: "made", wantErr: true, wantState: core.EffectIntended,
			op: func(h *fakeHost, ran *bool) dockerOp { return chains(h, ran, deTestSavePlain, nil) }},
		{name: "op without apply", wantErr: true, wantNone: true,
			op: func(*fakeHost, *bool) dockerOp {
				return dockerOp{Kind: core.EffectModify, Resource: "firewall.chains:docker", After: "absent"}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newFakeHost(t)
			deTestHold(t, h, deTestSaveDeny, deTestSaveDeny)
			h.journal.failOn = c.failOn
			ran := false
			err := runDockerEffect(context.Background(), h.env(), h.deps(), c.op(h, &ran))
			if (err != nil) != c.wantErr {
				t.Errorf("runDockerEffect = %v, want error %v", err, c.wantErr)
			}
			if c.wantNone {
				if len(h.journal.effects) != 0 || ran {
					t.Errorf("%d effects journaled and apply run %v, want neither", len(h.journal.effects), ran)
				}
				return
			}
			if e := efTestOnly(t, h); e.State != c.wantState {
				t.Errorf("effect state = %s, want %s", e.State, c.wantState)
			}
		})
	}
}

func TestDeRunDockerEffectCreationIsPendingUntilMade(t *testing.T) {
	h := newFakeHost(t)
	h.journal.failOn = "made"
	if err := runDockerEffect(context.Background(), h.env(), h.deps(), rowCreateOp(deTestAllow())); err == nil {
		t.Fatal("a creation the journal could not record succeeded")
	}
	e := efTestOnly(t, h)
	if e.State != core.EffectIntended || e.Resource != "firewall.container:?"+deTestID1 ||
		e.Recovery == nil || *e.Recovery != (core.RecoveryID{Kind: "marker", Value: deTestID1}) {
		t.Errorf("effect = %+v, want intended under the pending resource with the id as marker", e)
	}
}

func TestDeRunDockerEffectRefusesRowsThatCannotBeJournaled(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	deTestRun(t, h, rowCreateOp(deTestAllow()))
	for name, c := range map[string]struct {
		op      dockerOp
		effects int
	}{
		"creating an id that exists":   {rowCreateOp(deTestAllow()), 0},
		"deleting an id that is gone":  {rowDeleteOp(deTestDeny()), 0},
		"a second row for one binding": {rowCreateOp(dockerfw.ContainerRule{ID: deTestID3, Kind: dockerfw.Allow, Binding: deTestAllow().Binding}), 1},
	} {
		t.Run(name, func(t *testing.T) {
			h.journal.effects = nil
			if err := runDockerEffect(ctx, h.env(), h.deps(), c.op); err == nil {
				t.Error("the op succeeded")
			}
			if len(h.journal.effects) != c.effects || (c.effects == 1 && h.journal.effects[0].State != core.EffectSkipped) {
				t.Errorf("effects = %+v, want %d, skipped", h.journal.effects, c.effects)
			}
			if got := deTestRules(t, h); !slices.Equal(got, []dockerfw.ContainerRule{deTestAllow()}) {
				t.Errorf("rows = %+v, want only the first allow", got)
			}
		})
	}
}

func TestDeObserveDockerResources(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	deTestHold(t, h, deTestSavePlain, "")
	for _, c := range []struct {
		kind     core.EffectKind
		resource string
		want     string
		matches  int
		found    string
	}{
		{core.EffectModify, "firewall.setting:docker", `false`, 1, ""},
		{core.EffectModify, "firewall.setting:sources", deTestDefaultSources, 1, ""},
		{core.EffectDelete, "firewall.container:" + deTestID1, `null`, 1, ""},
		{core.EffectCreate, "firewall.container:?" + deTestID1, `null`, 0, ""},
		{core.EffectModify, "firewall.chains:docker", `"absent"`, 1, ""},
	} {
		obs := obTestObserve(t, h, c.kind, c.resource)
		if !jsonEqual(obs.Current.State, json.RawMessage(c.want)) || obs.Current.Version != nil || obs.Matches != c.matches ||
			!obs.Exclusive || obs.Resource != c.found {
			t.Errorf("%s = %+v, want state %s, %d matches", c.resource, obs, c.want, c.matches)
		}
		doc, err := dockerDoc(ctx, h.env(), h.deps(), c.resource)
		if err != nil || !jsonEqual(doc, json.RawMessage(c.want)) {
			t.Errorf("dockerDoc(%s) = %s, %v, want %s", c.resource, doc, err, c.want)
		}
	}

	deTestRun(t, h, flagOp(true))
	deTestRun(t, h, rowCreateOp(deTestDeny()))
	for _, c := range []struct {
		kind     core.EffectKind
		resource string
		want     string
		matches  int
		found    string
	}{
		{core.EffectModify, "firewall.setting:docker", `true`, 1, ""},
		{core.EffectDelete, "firewall.container:" + deTestID2, deTestDenyJSON, 1, ""},
		{core.EffectCreate, "firewall.container:?" + deTestID2, deTestDenyJSON, 1, "firewall.container:" + deTestID2},
	} {
		obs := obTestObserve(t, h, c.kind, c.resource)
		if !jsonEqual(obs.Current.State, json.RawMessage(c.want)) || obs.Matches != c.matches || !obs.Exclusive || obs.Resource != c.found {
			t.Errorf("%s = %+v, want state %s, %d matches, resource %q", c.resource, obs, c.want, c.matches, c.found)
		}
	}

	for name, c := range map[string]struct{ v4, v6, want string }{
		"no chain":                     {deTestSavePlain, deTestSavePlain, `"absent"`},
		"IPv6 not present":             {deTestSavePlain, "", `"absent"`},
		"a darkpanel chain in IPv4":    {deTestSaveDeny, deTestSavePlain, `"loaded"`},
		"a binding chain in IPv6 only": {deTestSavePlain, deTestSaveBinding, `"loaded"`},
		"a darkpanel chain, no IPv6":   {deTestSaveDeny, "", `"loaded"`},
	} {
		t.Run(name, func(t *testing.T) {
			deTestHold(t, h, c.v4, c.v6)
			if obs := obTestObserve(t, h, core.EffectModify, "firewall.chains:docker"); !jsonEqual(obs.Current.State, json.RawMessage(c.want)) {
				t.Errorf("chains = %s, want %s", obs.Current.State, c.want)
			}
		})
	}
}

func TestDeObserveRefusesUnknownDockerResources(t *testing.T) {
	h := newFakeHost(t)
	deTestHold(t, h, deTestSavePlain, deTestSavePlain)
	for _, resource := range []string{
		"firewall.setting:other", "firewall.setting:", "firewall.chains:other", "firewall.container:", "firewall.container:?",
	} {
		if _, err := observe(context.Background(), h.env(), h.deps(), core.Effect{Kind: core.EffectModify, Resource: resource}); err == nil {
			t.Errorf("observe(%q) succeeded", resource)
		}
	}
	h.loadErr = errors.New("save failed")
	if _, err := observe(context.Background(), h.env(), h.deps(), core.Effect{Kind: core.EffectModify, Resource: "firewall.chains:docker"}); err == nil {
		t.Error("observe of the chains succeeded without a readable loaded state")
	}
}
