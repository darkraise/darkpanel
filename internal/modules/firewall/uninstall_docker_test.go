package firewall

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	dunBlock = "# BEGIN DARKPANEL docker\n*filter\n:DOCKER-USER - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
		"-A DOCKER-USER -j RETURN\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n# END DARKPANEL docker\n"
	dunOwned = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n" +
		":DOCKER-USER - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
		"-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n" +
		"-A DOCKER-USER -j RETURN\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n"
	dunForeignOnly = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n" +
		"-A DOCKER-USER -s 192.0.2.0/24 -j DROP\n-A DOCKER-USER -j RETURN\nCOMMIT\n"
	dunForeign = "-A DOCKER-USER -s 192.0.2.0/24 -j DROP\n"
	dunCleanup = "-X darkpanel-docker-deny"
)

// dunHost is unSetup's host with iptables installed, the Docker block in
// both after files and the block's chains loaded; a restore program removes
// them. h.stdin holds every input a program received, in order: the
// cleanup's chain load is followed by the DOCKER-USER reload.
func dunHost(t *testing.T) (*fakeHost, module, core.Env, *bytes.Buffer) {
	t.Helper()
	h, m, env, log := unSetup(t)
	h.writeFile("/usr/sbin/iptables", []byte("#!/bin/sh\n"))
	h.writeFile("/etc/ufw/after.rules", []byte("*filter\n# after\nCOMMIT\n"+dunBlock))
	h.writeFile("/etc/ufw/after6.rules", []byte("*filter\n# after6\nCOMMIT\n"+dunBlock))
	h.loaded.V4, h.loaded.V6 = fhParse(t, dunOwned), fhParse(t, dunOwned)
	inputs := map[string][]byte{}
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		inputs[cmd.Path] = append(inputs[cmd.Path], cmd.Stdin...)
		h.stdin[cmd.Path] = inputs[cmd.Path]
		if strings.HasSuffix(cmd.Path, "-restore") {
			h.loaded.V4, h.loaded.V6 = fhParse(t, fhSave), fhParse(t, fhSave)
		}
		return core.CmdResult{}
	}
	return h, m, env, log
}

// dunCleaned fails the test unless the cleanup ran: the block is out of
// both files, the chains were flushed and deleted, and none is loaded.
func dunCleaned(t *testing.T, h *fakeHost) {
	t.Helper()
	cfg := h.config()
	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		if strings.Contains(string(cfg.Files[p].Data), "DARKPANEL docker") {
			t.Errorf("%s still holds the block", p)
		}
	}
	if in := string(h.stdin["/usr/sbin/iptables-nft-restore"]); !strings.Contains(in, dunCleanup) {
		t.Errorf("iptables-nft-restore inputs %q, want the owned chains deleted (%s)", in, dunCleanup)
	}
	for _, tables := range [][]netfilter.Table{h.loaded.V4.Tables, h.loaded.V6.Tables} {
		for _, tb := range tables {
			if tb.Chain("darkpanel-docker-deny") != nil {
				t.Errorf("table %s still holds the chain darkpanel-docker-deny", tb.Name)
			}
		}
	}
}

// TestDunRunsCleanup: the owned-chain cleanup runs for every
// value of rules, after the rules work, and on a host without UFW.
func TestDunRunsCleanup(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name       string
		rules      string
		noUFW      bool
		wantDelete bool
	}{
		{name: "keep", rules: "keep"},
		{name: "remove", rules: "remove", wantDelete: true},
		{name: "baseline without a baseline snapshot", rules: "baseline"},
		{name: "remove without UFW", rules: "remove", noUFW: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, m, env, _ := dunHost(t)
			if c.noUFW {
				s := h.state()
				s.Enabled = false
				h.setState(s)
				pfRemove(t, h.root, ufwPath)
			}
			if err := m.Uninstall(ctx, env, false, map[string]string{"rules": c.rules}); err != nil {
				t.Fatal(err)
			}
			deleted := slices.ContainsFunc(unRan(h, 0), func(v string) bool { return strings.HasPrefix(v, "delete ") })
			if deleted != c.wantDelete {
				t.Errorf("ufw ran %q, want a rule delete: %v", unRan(h, 0), c.wantDelete)
			}
			if c.noUFW && len(unRan(h, 0)) > 0 {
				t.Errorf("ufw ran %q on a host without UFW", unRan(h, 0))
			}
			if c.wantDelete {
				del := slices.IndexFunc(h.calls, func(v string) bool { return strings.HasPrefix(v, "run /usr/sbin/ufw delete ") })
				clean := slices.IndexFunc(h.calls, func(v string) bool { return strings.HasPrefix(v, "run /usr/sbin/iptables-nft-restore -n") })
				if del < 0 || clean < del {
					t.Errorf("calls %q, want the rules deleted before the cleanup loads", h.calls)
				}
			}
			dunCleaned(t, h)
		})
	}
}

// TestDunNeedsDarkpanelState: foreign DOCKER-USER rules
// alone are no reason to run the cleanup, which would refuse them; the block
// in a file or a loaded owned chain is.
func TestDunNeedsDarkpanelState(t *testing.T) {
	ctx := context.Background()
	h, m, env, _ := dunHost(t)
	own4, own6 := h.config().Files["/etc/ufw/after.rules"], h.config().Files["/etc/ufw/after6.rules"]
	h.writeFile("/etc/ufw/after.rules", []byte("*filter\n# after\nCOMMIT\n"))
	h.writeFile("/etc/ufw/after6.rules", []byte("*filter\n# after6\nCOMMIT\n"))
	h.loaded.V4, h.loaded.V6 = fhParse(t, dunForeignOnly), fhParse(t, fhSave)
	n := len(h.calls)
	if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "keep"}); err != nil || len(h.calls) != n {
		t.Errorf("uninstall = %v and ran %q, want nothing", err, h.calls[n:])
	}

	h.loaded.V4, h.loaded.V6 = fhParse(t, dunOwned), fhParse(t, dunOwned)
	h.writeFile("/etc/ufw/after.rules", own4.Data)
	h.writeFile("/etc/ufw/after6.rules", own6.Data)
	if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "keep"}); err != nil {
		t.Fatal(err)
	}
	dunCleaned(t, h)
}

// TestDunCleanupRefused: a foreign rule in DOCKER-USER refuses
// the cleanup, which fails the uninstall after the rules were deleted.
func TestDunCleanupRefused(t *testing.T) {
	ctx := context.Background()
	h, m, env, _ := dunHost(t)
	h.loaded.V4 = fhParse(t, strings.Replace(dunOwned, "-A DOCKER-USER -j RETURN\n", "-A DOCKER-USER -j RETURN\n"+dunForeign, 1))
	err := m.Uninstall(ctx, env, false, map[string]string{"rules": "remove"})
	if errcode.From(err).Code != errcode.FirewallDockerUserInUse || !strings.Contains(err.Error(), "192.0.2.0/24") {
		t.Fatalf("uninstall = %v, want firewall.docker_user_in_use naming the foreign rule", err)
	}
	if !slices.Contains(unRan(h, 0), "delete allow in proto tcp from any to any port 22") {
		t.Errorf("ufw ran %q, want the Darkpanel rule deleted before the refusal", unRan(h, 0))
	}
}

// TestDunFinishesInterrupted: an intent that an earlier
// change left is repeated, and cleared, by the next uninstall.
func TestDunFinishesInterrupted(t *testing.T) {
	ctx := context.Background()
	h, m, env, _ := dunHost(t)
	if err := beginCleanup(ctx, h.db, "01J00000000000000000000C01", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "keep"}); err != nil {
		t.Fatal(err)
	}
	if left, err := pendingCleanups(ctx, h.db); err != nil || !reflect.DeepEqual(left, []string(nil)) {
		t.Errorf("pending cleanups %v (%v), want none", left, err)
	}
	dunCleaned(t, h)
}
