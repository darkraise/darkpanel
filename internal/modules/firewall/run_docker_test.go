package firewall

import (
	"context"
	"database/sql"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

const (
	druPlain4 = "*filter\n# after\nCOMMIT\n"
	druPlain6 = "*filter\n# after6\nCOMMIT\n"
	druBlock  = "# BEGIN DARKPANEL docker\n*filter\n:DOCKER-USER - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
		"-A DOCKER-USER -j RETURN\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n# END DARKPANEL docker\n"
	// druLoaded is a loaded filter table that holds the block's chains.
	druLoaded = "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n" +
		":DOCKER-USER - [0:0]\n:darkpanel-docker-deny - [0:0]\n" +
		"-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n" +
		"-A DOCKER-USER -j RETURN\n-A darkpanel-docker-deny -j DROP\nCOMMIT\n"
	druForeignRule = "-A DOCKER-USER -s 192.0.2.0/24 -j DROP\n"
)

func druPtr[T any](v T) *T { return &v }

var (
	druOld = dockerfw.ContainerRule{ID: "01J00000000000000000000A01", Kind: dockerfw.Allow,
		Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}}
	druNew = dockerfw.ContainerRule{ID: "01J00000000000000000000A02", Kind: dockerfw.Deny, From: netip.MustParsePrefix("203.0.113.0/24"),
		Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}}
	druSources = dockerfw.Settings{PrivateSources: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
)

// druHost is a fake host whose after files hold the Docker block and whose
// loaded filter table holds the block's chains; a restore program removes
// them. Its database is the module's, with the Docker tables.
func druHost(t *testing.T) (*fakeHost, fw, core.Env, model.Rule) {
	t.Helper()
	h, f, _ := ruHost(t)
	h.db = dsDB(t)
	h.writeFile("/etc/ufw/after.rules", []byte(druPlain4+druBlock))
	h.writeFile("/etc/ufw/after6.rules", []byte(druPlain6+druBlock))
	h.loaded.V4, h.loaded.V6 = fhParse(t, druLoaded), fhParse(t, druLoaded)
	h.onRun = func(h *fakeHost, cmd core.Cmd) core.CmdResult {
		if strings.HasSuffix(cmd.Path, "-restore") {
			h.loaded.V4, h.loaded.V6 = fhParse(t, fhSave), fhParse(t, fhSave)
		}
		return core.CmdResult{}
	}
	env := h.env()
	env.ChangeID = ruChange
	var rule model.Rule
	for _, r := range model.Merge(h.config().Rules4, h.config().Rules6) {
		if r.Origin == model.OriginDarkpanel {
			rule = r
		}
	}
	return h, f, env, rule
}

// druResources lists the resources the journal holds, in order, one entry
// for each run of equal resources.
func druResources(h *fakeHost) []string {
	var out []string
	for _, e := range h.journal.effects {
		if len(out) == 0 || out[len(out)-1] != e.Resource {
			out = append(out, e.Resource)
		}
	}
	return out
}

func druIndex(calls []string, call string) int {
	return slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, call) })
}

func druLastIndex(calls []string, call string) int {
	for i := len(calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(calls[i], call) {
			return i
		}
	}
	return -1
}

// TestDruRunOrder: a change that carries the Docker fields runs them in
// the order of spec 2.6: the logical rows and settings, the block files, the
// ufw commands, the cleanup of the loaded chains, then the transition.
func TestDruRunOrder(t *testing.T) {
	ctx := context.Background()
	h, f, env, rule := druHost(t)
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := writeDockerConfig(ctx, tx, dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()}); err != nil {
			return err
		}
		return insertContainerRule(ctx, tx, druOld, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	c := change{
		ops:     []ufwOp{deleteRuleOp(rule)},
		flag:    druPtr(false),
		sources: &druSources,
		rowsDel: []string{druOld.ID},
		rowsAdd: []dockerfw.ContainerRule{druNew},
		files:   map[string][]byte{"/etc/ufw/after.rules": []byte(druPlain4), "/etc/ufw/after6.rules": []byte(druPlain6)},
		cleanup: true,
		reload:  true,
	}
	p := prepared{cfg0: h.config(), cur: h.loaded, c: c, tg: target{cmds: [][]string{reloadArgs}}}
	h.calls = nil
	if err := f.run(ctx, env, p); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"firewall.setting:docker", "firewall.setting:sources", "firewall.container:" + druOld.ID, "firewall.container:" + druNew.ID,
		"firewall.file:/etc/ufw/after.rules", "firewall.file:/etc/ufw/after6.rules",
		"firewall.rule:" + rule.ID, "firewall.chains:docker", "firewall.state:enabled",
	}
	if got := druResources(h); !reflect.DeepEqual(got, want) {
		t.Errorf("effects\n%q\nwant\n%q", got, want)
	}
	for _, e := range h.journal.effects {
		if e.State == core.EffectIntended {
			t.Errorf("effect %d (%s) is still intended", e.Seq, e.Resource)
		}
	}
	cfg, err := readDockerConfig(ctx, h.db)
	if err != nil || cfg.Enabled || !reflect.DeepEqual(cfg.Settings.PrivateSources, druSources.PrivateSources) {
		t.Errorf("stored config %+v (%v), want the flag off and the new sources", cfg, err)
	}
	if rows, err := listContainerRules(ctx, h.db); err != nil || !reflect.DeepEqual(rows, []dockerfw.ContainerRule{druNew}) {
		t.Errorf("stored rules %+v (%v), want only the new one", rows, err)
	}
	if left, err := pendingCleanups(ctx, h.db); err != nil || len(left) != 0 {
		t.Errorf("pending cleanups %v (%v), want none", left, err)
	}
	if got := string(h.config().Files["/etc/ufw/after.rules"].Data); got != druPlain4 {
		t.Errorf("after.rules is %q, want the file without the block", got)
	}
	file, del := druIndex(h.calls, "restore /etc/ufw/after.rules"), druIndex(h.calls, "run /usr/sbin/ufw delete")
	clean, reload := druIndex(h.calls, "run /usr/sbin/iptables-nft-restore -n"), druLastIndex(h.calls, "run /usr/sbin/ufw reload")
	if file < 0 || del <= file || clean <= del || reload <= clean {
		t.Errorf("calls %q: want the block file, the ufw rule delete, the chain cleanup and the reload in that order", h.calls)
	}
}

// TestDruRunFiles: a block file already as wanted is not
// rewritten; a missing row, and a file that is not a block file, stop the
// change before it writes.
func TestDruRunFiles(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		c       change
		wantErr string
		want    []string
	}{
		{
			name: "one file is as wanted",
			c: change{files: map[string][]byte{
				"/etc/ufw/after.rules": []byte(druPlain4 + druBlock), "/etc/ufw/after6.rules": []byte(druPlain6)}},
			want: []string{"firewall.file:/etc/ufw/after6.rules"},
		},
		{
			name:    "a row that does not exist",
			c:       change{rowsDel: []string{"01J00000000000000000000FFF"}, flag: druPtr(true)},
			wantErr: "01J00000000000000000000FFF",
			want:    []string{"firewall.setting:docker"},
		},
		{
			name:    "a file that is not a block file",
			c:       change{files: map[string][]byte{"/etc/ufw/user.rules": []byte("*filter\nCOMMIT\n")}},
			wantErr: "/etc/ufw/user.rules",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, f, env, _ := druHost(t)
			h.calls = nil
			err := f.run(ctx, env, prepared{cfg0: h.config(), cur: h.loaded, c: c.c})
			if c.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("run = %v, want an error naming %s", err, c.wantErr)
			}
			if got := druResources(h); !reflect.DeepEqual(got, c.want) {
				t.Errorf("effects %q, want %q", got, c.want)
			}
			if c.name == "a file that is not a block file" && len(h.calls) != 0 {
				t.Errorf("calls %q, want none", h.calls)
			}
		})
	}
}

// TestDruCleanupRefused: a foreign rule in DOCKER-USER stops the
// change at the cleanup, before the chains are loaded and before UFW
// reloads.
func TestDruCleanupRefused(t *testing.T) {
	ctx := context.Background()
	h, f, env, _ := druHost(t)
	h.loaded.V4 = fhParse(t, strings.Replace(druLoaded, "-A DOCKER-USER -j RETURN\n", "-A DOCKER-USER -j RETURN\n"+druForeignRule, 1))
	h.calls = nil
	c := change{
		files:   map[string][]byte{"/etc/ufw/after.rules": []byte(druPlain4), "/etc/ufw/after6.rules": []byte(druPlain6)},
		cleanup: true, reload: true,
	}
	err := f.run(ctx, env, prepared{cfg0: h.config(), cur: h.loaded, c: c, tg: target{cmds: [][]string{reloadArgs}}})
	if got := errcode.From(err).Code; got != errcode.FirewallDockerUserInUse || !strings.Contains(err.Error(), "192.0.2.0/24") {
		t.Fatalf("run = %v, want firewall.docker_user_in_use naming the foreign rule", err)
	}
	for _, call := range h.calls {
		if strings.Contains(call, "-restore") || strings.HasSuffix(call, "ufw reload") {
			t.Errorf("call %q ran after the refusal", call)
		}
	}
	if slices.Contains(druResources(h), "firewall.state:enabled") {
		t.Errorf("effects %q hold the reload", druResources(h))
	}
}
