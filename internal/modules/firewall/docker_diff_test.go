package firewall

import (
	"context"
	"database/sql"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

const (
	ddfAfterV4 = "/etc/ufw/after.rules"
	ddfAfterV6 = "/etc/ufw/after6.rules"
)

var ddfBlock = []byte("*filter\n:DOCKER-USER - [0:0]\nCOMMIT\n")

// ddfAfter is an after file's content: its own lines, and the Docker block
// when block is not nil.
func ddfAfter(t *testing.T, block []byte) []byte {
	t.Helper()
	own := []byte("*filter\n# after\nCOMMIT\n")
	if block == nil {
		return own
	}
	out, err := dockerfw.WithBlock(own, block)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func ddfConfig(v4, v6 []byte) fwstate.Config {
	return fwstate.Config{Files: map[string]core.RestoreEntry{
		ddfAfterV4: {Path: ddfAfterV4, Present: true, Mode: 0o640, Data: v4},
		ddfAfterV6: {Path: ddfAfterV6, Present: true, Mode: 0o640, Data: v6},
	}}
}

func ddfRule(id, selector, hostAddr string, port uint16, proto string, kind dockerfw.Kind, from string) dockerfw.ContainerRule {
	r := dockerfw.ContainerRule{ID: id, Kind: kind, Binding: dockerfw.Binding{Selector: selector, HostPort: port, Proto: proto}}
	if hostAddr != "" {
		r.Binding.HostAddr = netip.MustParseAddr(hostAddr)
	}
	if from != "" {
		r.From = netip.MustParsePrefix(from)
	}
	return r
}

func ddfSettings(sources []string, ex ...dockerfw.Exemption) dockerfw.Settings {
	s := dockerfw.Settings{ForwardExemptions: ex}
	for _, p := range sources {
		s.PrivateSources = append(s.PrivateSources, netip.MustParsePrefix(p))
	}
	return s
}

var (
	ddfEx1 = dockerfw.Exemption{InInterface: "eth0", OrigDestination: netip.MustParseAddr("203.0.113.5"), OrigPort: 8443,
		Destination: netip.MustParseAddr("10.9.0.2"), Proto: "tcp", Port: 443}
	ddfEx2 = dockerfw.Exemption{OrigDestination: netip.MustParseAddr("2001:db8::5"), Destination: netip.MustParseAddr("fd00::2"),
		Proto: "udp", Port: 80}
	ddfStored1 = ddfRule("01J00000000000000000000001", "web", "", 8080, "tcp", dockerfw.Allow, "")
	ddfStored2 = ddfRule("01J00000000000000000000002", "web", "", 8080, "tcp", dockerfw.Deny, "203.0.113.0/24")
)

// TestDdfDockerDiff: a change of the Docker state lists, in this order, the
// integration flag, the private sources and the forward exemptions, each as
// an old and a new line when it changes; each container rule added and then
// each removed; each after file's block written or removed, which is the
// block present with other bytes or the block gone; and the removal of the
// chains. What the change leaves as it is gives no line.
func TestDdfDockerDiff(t *testing.T) {
	defaults := dockerfw.DefaultSettings()
	before := dockerConfig{Settings: defaults}
	stored := []dockerfw.ContainerRule{ddfStored1, ddfStored2}
	on := true
	cfg0 := ddfConfig(ddfAfter(t, nil), ddfAfter(t, ddfBlock))
	c := change{
		flag:    &on,
		sources: &dockerfw.Settings{PrivateSources: ddfSettings([]string{"10.0.0.0/8", "192.168.0.0/16"}).PrivateSources, ForwardExemptions: []dockerfw.Exemption{ddfEx1, ddfEx2}},
		rowsDel: []string{ddfStored2.ID},
		rowsAdd: []dockerfw.ContainerRule{
			ddfRule("01J00000000000000000000003", "six", "2001:db8::1", 8443, "udp", dockerfw.Allow, "198.51.100.0/24"),
			ddfRule("01J00000000000000000000004", "shop/api", "192.0.2.1", 9090, "tcp", dockerfw.Deny, "203.0.113.7/32"),
		},
		files: map[string][]byte{
			ddfAfterV4: ddfAfter(t, ddfBlock),
			ddfAfterV6: ddfAfter(t, nil),
		},
		cleanup: true,
	}
	got, err := dockerDiff(before, stored, cfg0, c)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: "-", Text: "docker integration off"},
		{Op: "+", Text: "docker integration on"},
		{Op: "-", Text: "private sources: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, fc00::/7, fe80::/10"},
		{Op: "+", Text: "private sources: 10.0.0.0/8, 192.168.0.0/16"},
		{Op: "-", Text: "forward exemptions: none"},
		{Op: "+", Text: "forward exemptions: in eth0 203.0.113.5:8443 -> 10.9.0.2:443/tcp, 2001:db8::5 -> [fd00::2]:80/udp"},
		{Op: "+", Text: "container rule + six [2001:db8::1]:8443/udp allow 198.51.100.0/24"},
		{Op: "+", Text: "container rule + shop/api 192.0.2.1:9090/tcp deny 203.0.113.7/32"},
		{Op: "-", Text: "container rule - web all:8080/tcp deny 203.0.113.0/24"},
		{Op: "+", Text: "after.rules: Docker block written"},
		{Op: "-", Text: "after6.rules: Docker block removed"},
		{Op: "-", Text: "Docker chains removed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff\n%+v\nwant\n%+v", got, want)
	}
}

// TestDdfDockerDiffUnchanged: a change that sets what is already set, and
// one that touches no Docker state, give no line; a rewritten block shows as
// written; the exemptions alone, or the flag alone, give only their lines.
func TestDdfDockerDiffUnchanged(t *testing.T) {
	defaults := dockerfw.DefaultSettings()
	before := dockerConfig{Enabled: true, Settings: ddfSettings([]string{"10.0.0.0/8"}, ddfEx1)}
	cfg0 := ddfConfig(ddfAfter(t, ddfBlock), ddfAfter(t, nil))
	on, off := true, false
	same := ddfSettings([]string{"10.0.0.0/8"}, ddfEx1)
	other := []byte("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\nCOMMIT\n")
	exOnly := ddfSettings([]string{"10.0.0.0/8"})

	for _, tc := range []struct {
		name string
		c    change
		want []core.DiffLine
	}{
		{"no Docker change", change{enable: &on}, nil},
		{"the same flag, sources and block", change{flag: &on, sources: &same,
			files: map[string][]byte{ddfAfterV4: ddfAfter(t, ddfBlock), ddfAfterV6: ddfAfter(t, nil)}}, nil},
		{"the sources back to the defaults", change{sources: &defaults}, []core.DiffLine{
			{Op: "-", Text: "private sources: 10.0.0.0/8"},
			{Op: "+", Text: "private sources: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, fc00::/7, fe80::/10"},
			{Op: "-", Text: "forward exemptions: in eth0 203.0.113.5:8443 -> 10.9.0.2:443/tcp"},
			{Op: "+", Text: "forward exemptions: none"},
		}},
		{"the exemptions alone", change{sources: &exOnly}, []core.DiffLine{
			{Op: "-", Text: "forward exemptions: in eth0 203.0.113.5:8443 -> 10.9.0.2:443/tcp"},
			{Op: "+", Text: "forward exemptions: none"},
		}},
		{"the flag alone", change{flag: &off}, []core.DiffLine{
			{Op: "-", Text: "docker integration on"},
			{Op: "+", Text: "docker integration off"},
		}},
		{"a rewritten block", change{files: map[string][]byte{ddfAfterV4: ddfAfter(t, other)}}, []core.DiffLine{
			{Op: "+", Text: "after.rules: Docker block written"},
		}},
		{"no block before or after", change{files: map[string][]byte{ddfAfterV6: ddfAfter(t, nil)}}, nil},
		{"the removal of an absent block", change{files: map[string][]byte{ddfAfterV6: ddfAfter(t, nil)}, cleanup: true}, []core.DiffLine{
			{Op: "-", Text: "Docker chains removed"},
		}},
		{"notes after the other lines", change{flag: &off, notes: []core.DiffLine{{Op: " ", Text: "manual route rule x: no effect without a Darkpanel binding"}}}, []core.DiffLine{
			{Op: "-", Text: "docker integration on"},
			{Op: "+", Text: "docker integration off"},
			{Op: " ", Text: "manual route rule x: no effect without a Darkpanel binding"},
		}},
		{"notes alone", change{notes: []core.DiffLine{{Op: " ", Text: "note"}}}, []core.DiffLine{
			{Op: " ", Text: "note"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dockerDiff(before, nil, cfg0, tc.c)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("diff\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// TestDdfDockerDiffRefusals: a container rule to remove that is not stored,
// and an after file whose markers are malformed, fail the diff.
func TestDdfDockerDiffRefusals(t *testing.T) {
	before := dockerConfig{Settings: dockerfw.DefaultSettings()}
	cfg0 := ddfConfig(ddfAfter(t, nil), ddfAfter(t, nil))
	_, err := dockerDiff(before, []dockerfw.ContainerRule{ddfStored1}, cfg0, change{rowsDel: []string{"01J00000000000000000000009"}})
	if err == nil || !strings.HasPrefix(err.Error(), "firewall:") || !strings.Contains(err.Error(), "01J00000000000000000000009") {
		t.Errorf("diff removing an unknown rule = %v, want an error naming it", err)
	}
	broken := ddfConfig([]byte("*filter\n# BEGIN DARKPANEL docker\nCOMMIT\n"), ddfAfter(t, nil))
	_, err = dockerDiff(before, nil, broken, change{files: map[string][]byte{ddfAfterV4: ddfAfter(t, ddfBlock)}})
	if err == nil || !strings.HasPrefix(err.Error(), "firewall:") || !strings.Contains(err.Error(), ddfAfterV4) {
		t.Errorf("diff over an unterminated block = %v, want an error naming the file", err)
	}
}

// TestDdfPlanDockerLines: plan lists the Docker lines after UFW's own and
// before the out-of-sync lines, computed against the stored configuration
// and rows; a change that touches no Docker state reads no Docker store.
func TestDdfPlanDockerLines(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	f, _ := newFW(h)
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := writeDockerConfig(ctx, tx, dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()}); err != nil {
			return err
		}
		return insertContainerRule(ctx, tx, ddfStored1, h.clock.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	build := func(fwstate.Config, fwstate.Loaded) (change, error) {
		return change{
			flag:    &off,
			rowsDel: []string{ddfStored1.ID},
			files:   map[string][]byte{ddfAfterV4: ddfAfter(t, ddfBlock)},
			cleanup: true,
		}, nil
	}
	h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{"-p tcp -m tcp --dport 22 -j ACCEPT"}}}
	out, err := f.plan(ctx, h.env(), planReq{input: 1, build: build})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: "-", Text: "docker integration on"},
		{Op: "+", Text: "docker integration off"},
		{Op: "-", Text: "container rule - web all:8080/tcp allow any"},
		{Op: "+", Text: "after.rules: Docker block written"},
		{Op: "-", Text: "Docker chains removed"},
		{Op: " ", Text: "out of sync: v4 filter/ufw-user-input: 1 rule only loaded"},
	}
	if !reflect.DeepEqual(out.Diff, want) {
		t.Errorf("diff\n%+v\nwant\n%+v", out.Diff, want)
	}

	h = newFakeHost(t)
	f, _ = newFW(h)
	env := h.env()
	env.DB = nil
	if _, err := f.plan(ctx, env, planReq{input: 2, build: func(fwstate.Config, fwstate.Loaded) (change, error) { return change{}, nil }}); err != nil {
		t.Errorf("plan of a change without Docker state = %v, want it to read no Docker store", err)
	}
}
