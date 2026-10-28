package firewall

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	dtgAfter4 = "/etc/ufw/after.rules"
	dtgAfter6 = "/etc/ufw/after6.rules"
)

// dtgDocker is a running UFW beside Docker's own DOCKER-USER, which holds
// only its default -j RETURN.
const dtgDocker = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n" +
	":DOCKER-FORWARD - [0:0]\n:DOCKER-USER - [0:0]\n:ufw-before-logging-input - [0:0]\n:ufw-user-input - [0:0]\n" +
	"-A INPUT -j ufw-user-input\n-A FORWARD -j DOCKER-USER\n-A DOCKER-USER -j RETURN\n" +
	"-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\nCOMMIT\n"

// dtgLoadedBlock is the same host with Darkpanel's block loaded, a chain of
// another program, and a nat table that holds Docker's chain.
const dtgLoadedBlock = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n" +
	":DOCKER-FORWARD - [0:0]\n:DOCKER-USER - [0:0]\n:darkpanel-docker-bindings - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
	":dp-b-0123456789ab - [0:0]\n:foreign-chain - [0:0]\n:ufw-before-logging-input - [0:0]\n:ufw-user-input - [0:0]\n" +
	"-A INPUT -j ufw-user-input\n-A FORWARD -j DOCKER-USER\n" +
	"-A DOCKER-USER -i docker0 -j RETURN\n-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings\n-A DOCKER-USER -j RETURN\n" +
	"-A darkpanel-docker-bindings -j darkpanel-docker-deny\n-A dp-b-0123456789ab -s 203.0.113.0/24 -j darkpanel-docker-deny\n" +
	"-A darkpanel-docker-deny -j DROP\n-A foreign-chain -j RETURN\n" +
	"-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\nCOMMIT\n" +
	"*nat\n:PREROUTING ACCEPT [0:0]\n:DOCKER - [0:0]\n-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER\nCOMMIT\n"

// dtgBlock is a minimal block of spec 2.6 as an after file holds it.
const dtgBlock = "# BEGIN DARKPANEL docker\n*filter\n:ufw-user-forward - [0:0]\n:DOCKER-USER - [0:0]\n" +
	":darkpanel-docker-bindings - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
	"-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN\n-A DOCKER-USER -i docker0 -j RETURN\n" +
	"-A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings\n-A DOCKER-USER -j RETURN\n" +
	"-A darkpanel-docker-bindings -j darkpanel-docker-deny\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n# END DARKPANEL docker\n"

// dtgAfterWith is after.rules as the fake host holds it, with the block.
func dtgAfterWith() []byte { return []byte("*filter\n# after\nCOMMIT\n" + dtgBlock) }

// dtgChain returns the named filter chain of rs, or nil.
func dtgChain(rs *netfilter.Ruleset, name string) *netfilter.Chain {
	return chainOf(rs, "filter", name)
}

// dtgTexts are the -A lines of a chain.
func dtgTexts(c *netfilter.Chain) []string {
	var out []string
	for _, r := range c.Rules {
		out = append(out, r.Text)
	}
	return out
}

// TestDtgFilesOverlay: the files a change writes replace the configured
// after files in the configuration the predictor reads and in the target's
// configurations, keeping each file's mode and owner; the caller's
// configuration is left alone.
func TestDtgFilesOverlay(t *testing.T) {
	h := newTGHost(t, tgRunning, true)
	var seen map[string][]byte
	h.f.d.predict = func(cfg fwstate.Config, cur fwstate.Loaded, _ fwstate.Transition) (fwstate.Loaded, error) {
		seen = map[string][]byte{}
		for p, e := range cfg.Files {
			seen[p] = e.Data
		}
		return cur, nil
	}
	cfg0 := h.config()
	before4, before6, beforeRules := cfg0.Files[dtgAfter4], cfg0.Files[dtgAfter6], cfg0.Files["/etc/ufw/before.rules"]
	files := map[string][]byte{dtgAfter4: dtgAfterWith(), dtgAfter6: []byte("*filter\n# after6 with block\nCOMMIT\n")}

	tg, err := h.f.predictTarget(cfg0, h.cur(), change{files: files, reload: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dtgAfter4, dtgAfter6} {
		if string(seen[p]) != string(files[p]) {
			t.Errorf("predictor read %s as %q, want the written file %q", p, seen[p], files[p])
		}
		for name, cfg := range map[string]fwstate.Config{"cfgN": tg.cfgN, "final": tg.final} {
			got := cfg.Files[p]
			if !got.Present || string(got.Data) != string(files[p]) || got.Mode != cfg0.Files[p].Mode || got.UID != cfg0.Files[p].UID {
				t.Errorf("%s holds %s as %+v, want the written data with mode and owner of %+v", name, p, got, cfg0.Files[p])
			}
		}
	}
	if string(seen["/etc/ufw/before.rules"]) != string(beforeRules.Data) {
		t.Errorf("the predictor read before.rules as %q, want it unchanged", seen["/etc/ufw/before.rules"])
	}
	if !reflect.DeepEqual(cfg0.Files[dtgAfter4], before4) || !reflect.DeepEqual(cfg0.Files[dtgAfter6], before6) {
		t.Error("predictTarget changed the caller's configuration")
	}
}

// TestDtgFilesRefused: only the two after files can be written, and a file
// UFW cannot start without is a plain error.
func TestDtgFilesRefused(t *testing.T) {
	for _, path := range []string{"/etc/ufw/before.rules", "/etc/ufw/user.rules", "/etc/passwd", "etc/ufw/after.rules"} {
		h := newTGHost(t, tgRunning, true)
		_, err := h.f.predictTarget(h.config(), h.cur(), change{files: map[string][]byte{path: []byte("x\n")}, reload: true})
		var coded *errcode.Error
		if err == nil || errors.As(err, &coded) || !strings.HasPrefix(err.Error(), "firewall:") || !strings.Contains(err.Error(), path) {
			t.Errorf("file %q: %v, want a plain firewall error naming the path", path, err)
		}
	}
	h := newTGHost(t, tgRunning, true)
	if err := os.Remove(filepath.Join(h.root, dtgAfter6)); err != nil {
		t.Fatal(err)
	}
	_, err := h.f.predictTarget(h.config(), h.cur(), change{files: map[string][]byte{dtgAfter6: []byte("x\n")}, reload: true})
	if err == nil || !strings.Contains(err.Error(), dtgAfter6+" is missing") {
		t.Errorf("a missing after6.rules: %v, want an error saying it is missing", err)
	}
}

// TestDtgReload: reload makes an action on an active UFW a reload, which no
// other field does; with UFW stopped or being stopped it adds nothing.
func TestDtgReload(t *testing.T) {
	files := map[string][]byte{dtgAfter4: dtgAfterWith()}
	cases := []struct {
		name    string
		save    string
		enabled bool
		change  change
		tr      fwstate.Transition
		cmds    []string
	}{
		{"reload on an active UFW", tgRunning, true, change{files: files, reload: true}, fwstate.TransReload, []string{"reload"}},
		{"reload alone on an active UFW", tgRunning, true, change{reload: true}, fwstate.TransReload, []string{"reload"}},
		{"reload with enable on an active UFW", tgRunning, true, change{enable: &yes, reload: true}, fwstate.TransReload, []string{"reload"}},
		{"files without reload", tgRunning, true, change{files: files}, fwstate.TransNone, nil},
		{"the flag and the rows are not predicted", tgRunning, true, change{flag: &yes, rowsDel: []string{"01J00000000000000000000009"}}, fwstate.TransNone, nil},
		{"reload on a stopped UFW", tgStopped, false, change{files: files, reload: true}, fwstate.TransNone, nil},
		{"reload with enable starts UFW once", tgStopped, false, change{enable: &yes, files: files, reload: true}, fwstate.TransEnable, []string{"--force enable"}},
		{"reload with disable stops UFW", tgRunning, true, change{enable: &no, files: files, reload: true}, fwstate.TransDisable, []string{"disable"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newTGHost(t, c.save, c.enabled)
			tg, err := h.f.predictTarget(h.config(), h.cur(), c.change)
			if err != nil {
				t.Fatal(err)
			}
			if tg.tr != c.tr || !reflect.DeepEqual(tgCmds(tg.cmds), c.cmds) {
				t.Errorf("transition %s with %q, want %s with %q", tg.tr, tgCmds(tg.cmds), c.tr, c.cmds)
			}
		})
	}
}

// TestDtgPredictsTheBlock: with the real predictor, a block written to
// after.rules replaces what DOCKER-USER holds after a reload, and an
// unchanged after.rules leaves Docker's chain alone.
func TestDtgPredictsTheBlock(t *testing.T) {
	h := newTGHost(t, dtgDocker, true)
	h.f.d.predict = predict.Predict
	tg, err := h.f.predictTarget(h.config(), h.cur(), change{files: map[string][]byte{dtgAfter4: dtgAfterWith(), dtgAfter6: dtgAfterWith()}, reload: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, rs := range map[string]*netfilter.Ruleset{"v4": tg.pred.V4, "v6": tg.pred.V6} {
		du := dtgChain(rs, "DOCKER-USER")
		if du == nil || len(du.Rules) != 4 || du.Rules[1].Text != "-A DOCKER-USER -i docker0 -j RETURN" {
			t.Errorf("%s DOCKER-USER after the reload = %v, want the block's four rules", name, du)
		}
		if dtgChain(rs, "darkpanel-docker-deny") == nil {
			t.Errorf("%s predicted state lacks darkpanel-docker-deny", name)
		}
	}
	tg, err = h.f.predictTarget(h.config(), h.cur(), change{reload: true})
	if err != nil {
		t.Fatal(err)
	}
	if du := dtgChain(tg.pred.V4, "DOCKER-USER"); du == nil || !reflect.DeepEqual(dtgTexts(du), []string{"-A DOCKER-USER -j RETURN"}) {
		t.Errorf("DOCKER-USER after a reload with no block = %v, want Docker's own rule", du)
	}
}

// TestDtgCleanup: the cleanup runs before the transition, so the prediction
// starts from a loaded state without Darkpanel's chains, and DOCKER-USER
// holds only -j RETURN in both families; every other chain and table is
// kept, and the caller's loaded state is not modified.
func TestDtgCleanup(t *testing.T) {
	assertClean := func(t *testing.T, name string, rs *netfilter.Ruleset, inputRules int) {
		t.Helper()
		du := dtgChain(rs, "DOCKER-USER")
		if du == nil || !reflect.DeepEqual(dtgTexts(du), []string{"-A DOCKER-USER -j RETURN"}) {
			t.Errorf("%s DOCKER-USER = %v, want only -j RETURN", name, du)
		}
		for _, c := range rs.Table("filter").Chains {
			if strings.HasPrefix(c.Name, "darkpanel-") || strings.HasPrefix(c.Name, "dp-b-") {
				t.Errorf("%s still holds chain %s", name, c.Name)
			}
		}
		for _, keep := range []string{"INPUT", "FORWARD", "DOCKER-FORWARD", "foreign-chain", "ufw-user-input"} {
			if dtgChain(rs, keep) == nil {
				t.Errorf("%s lost chain %s", name, keep)
			}
		}
		if got := dtgChain(rs, "INPUT"); got.Policy != "DROP" || len(got.Rules) < inputRules {
			t.Errorf("%s INPUT policy %q with %d rules, want DROP with at least %d", name, got.Policy, len(got.Rules), inputRules)
		}
		if nat := rs.Table("nat"); nat == nil || nat.Chain("DOCKER") == nil {
			t.Errorf("%s lost the nat table's DOCKER chain", name)
		}
	}

	t.Run("no transition", func(t *testing.T) {
		h := newTGHost(t, tgStopped, false)
		h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, dtgLoadedBlock), V6: fhParse(t, dtgLoadedBlock)}
		cur := h.cur()
		tg, err := h.f.predictTarget(h.config(), cur, change{cleanup: true})
		if err != nil {
			t.Fatal(err)
		}
		if tg.tr != fwstate.TransNone {
			t.Errorf("transition %s, want none", tg.tr)
		}
		assertClean(t, "v4", tg.pred.V4, 1)
		assertClean(t, "v6", tg.pred.V6, 1)
		for name, rs := range map[string]*netfilter.Ruleset{"v4": cur.V4, "v6": cur.V6} {
			if dtgChain(rs, "dp-b-0123456789ab") == nil || len(dtgChain(rs, "DOCKER-USER").Rules) != 3 {
				t.Errorf("the caller's %s loaded state was modified", name)
			}
		}
	})

	t.Run("the predictor starts from the cleaned state", func(t *testing.T) {
		h := newTGHost(t, tgRunning, true)
		h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, dtgLoadedBlock), V6: fhParse(t, dtgLoadedBlock)}
		var from fwstate.Loaded
		h.f.d.predict = func(_ fwstate.Config, cur fwstate.Loaded, _ fwstate.Transition) (fwstate.Loaded, error) {
			from = cur
			return cur, nil
		}
		tg, err := h.f.predictTarget(h.config(), h.cur(), change{cleanup: true, reload: true})
		if err != nil {
			t.Fatal(err)
		}
		if tg.tr != fwstate.TransReload {
			t.Errorf("transition %s, want reload", tg.tr)
		}
		assertClean(t, "predictor input v4", from.V4, 1)
		assertClean(t, "predictor input v6", from.V6, 1)
		assertClean(t, "prediction v4", tg.pred.V4, 1)
	})

	t.Run("with the real predictor and the block gone from the files", func(t *testing.T) {
		h := newTGHost(t, tgRunning, true)
		h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, dtgLoadedBlock), V6: fhParse(t, dtgLoadedBlock)}
		h.f.d.predict = predict.Predict
		plain := map[string][]byte{dtgAfter4: []byte("*filter\n# after\nCOMMIT\n"), dtgAfter6: []byte("*filter\n# after6\nCOMMIT\n")}
		tg, err := h.f.predictTarget(h.config(), h.cur(), change{files: plain, reload: true, cleanup: true})
		if err != nil {
			t.Fatal(err)
		}
		assertClean(t, "v4", tg.pred.V4, 1)
		assertClean(t, "v6", tg.pred.V6, 1)
		tg, err = h.f.predictTarget(h.config(), h.cur(), change{files: plain, reload: true})
		if err != nil {
			t.Fatal(err)
		}
		if dtgChain(tg.pred.V4, "darkpanel-docker-deny") == nil {
			t.Error("without cleanup a reload keeps the loaded darkpanel chains, as UFW does")
		}
	})

	t.Run("a host without DOCKER-USER or IPv6", func(t *testing.T) {
		h := newTGHost(t, tgStopped, false)
		h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, tgStopped), V6: nil}
		tg, err := h.f.predictTarget(h.config(), h.cur(), change{cleanup: true})
		if err != nil {
			t.Fatal(err)
		}
		if du := dtgChain(tg.pred.V4, "DOCKER-USER"); du == nil || !reflect.DeepEqual(dtgTexts(du), []string{"-A DOCKER-USER -j RETURN"}) {
			t.Errorf("DOCKER-USER = %v, want it declared with -j RETURN, as the cleanup input declares it", du)
		}
		if tg.pred.V6 != nil {
			t.Error("a family the host does not have gained a ruleset")
		}
	})
}
