//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// scenarioBreakGlass is scenario 11: with the controller stopped the agent's
// local CLI still reads through the agent socket, and with the agent
// stopped too, update rollback --local runs the rollback in-process and
// returns the agent host from the good build to N.
func scenarioBreakGlass(ctx context.Context, t *testing.T, f *fleet) {
	requireSettled(ctx, t, f, versionGood)
	if _, err := f.controller.Run(ctx, "sudo systemctl stop "+update.ControllerUnit); err != nil {
		t.Fatal(err)
	}
	out, err := f.agent.Run(ctx, "sudo darkpanel hostinfo summary get --local --json")
	if err != nil {
		t.Fatal(err)
	}
	var sum struct {
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil || sum.Hostname != "agent" {
		t.Errorf("hostinfo summary get --local = %+v, %v; want the agent's summary\n%s", sum, err, out)
	}

	if _, err := f.agent.Run(ctx, "sudo systemctl stop "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	// The irreversible rollback asks for no confirmation phrase here: without
	// an agent there is no plan to name one, so --yes is all it takes.
	const notice = "/var/tmp/rollback-notice"
	out, err = f.agent.Run(ctx, "sudo darkpanel update rollback --local --yes --json 2>"+notice)
	stderr, _ := f.agent.Run(ctx, "cat "+notice)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	want := "darkpanel: the agent socket " + paths.Production().AgentSocket + " does not answer; running core.update.rollback in this process"
	if !strings.Contains(stderr, want) {
		t.Errorf("update rollback --local printed %q to standard error, want %q", stderr, want)
	}
	var tr core.UpdateTransaction
	if err := json.Unmarshal([]byte(out), &tr); err != nil {
		t.Fatalf("update rollback --local --json: %v\n%s", err, out)
	}
	if tr.Kind != core.UpdateKindRollback || tr.FromVersion != versionGood || tr.ToVersion != versionN || tr.State != core.UpdateRolledBack {
		t.Errorf("the rollback is %+v; want a %s from %s to %s, %s", tr, core.UpdateKindRollback, versionGood, versionN, core.UpdateRolledBack)
	}
	requireBinaryVersion(ctx, t, f.agent, paths.Production().Binary(), versionN)

	// The rollback restarts the agent itself; starting both units again is a
	// no-op for a running one.
	if _, err := f.agent.Run(ctx, "sudo systemctl start "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	if _, err := f.controller.Run(ctx, "sudo systemctl start "+update.ControllerUnit); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, 3*time.Minute, func(hosts []core.Host) error {
		if err := hostsAt(versionGood, f.localID)(hosts); err != nil {
			return err
		}
		return hostsAt(versionN, f.agentID)(hosts)
	})
}

// scenarioUninstall is scenario 12: uninstall leaves nothing of the agent's
// ownership manifest and none of its units; on the controller, uninstall
// --keep-data and a reinstall of N find the kept databases, keys and users.
func scenarioUninstall(ctx context.Context, t *testing.T, f *fleet) {
	// uninstall deletes agent.db, the manifest with it, last.
	entries := readManifest(ctx, t, f.agent)
	agentUnit := install.Production().UnitPath(update.AgentUnit)
	if !slices.ContainsFunc(entries, func(e ownedEntry) bool { return e.Path == agentUnit }) {
		t.Fatalf("the agent's ownership manifest does not list %s: %+v", agentUnit, entries)
	}
	if out, err := f.agent.Run(ctx, "sudo darkpanel uninstall --yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := f.agent.RunInput(ctx, "sudo sh -s", strings.NewReader(remainingScript(entries)))
	if err != nil {
		t.Fatal(err)
	}
	if left := strings.TrimSpace(out); left != "" {
		t.Errorf("uninstall left what the manifest lists:\n%s", left)
	}
	requireUnitsGone(ctx, t, f.agent, update.AgentUnit, update.RollbackUnit, install.RecoverUnit)

	if out, err := f.controller.Run(ctx, "sudo darkpanel uninstall --keep-data --yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	requireUnitsGone(ctx, t, f.controller, update.ControllerUnit, update.AgentUnit, update.RollbackUnit, install.RecoverUnit)
	p := paths.Production()
	for _, kept := range []string{p.ControllerDB(), p.AgentDB(), p.ControllerKey, p.AgentKey, p.AgentTLSCurrent()} {
		if _, err := f.controller.Run(ctx, "sudo test -e "+kept); err != nil {
			t.Errorf("uninstall --keep-data removed %s", kept)
		}
	}
	if _, err := f.controller.Run(ctx, "getent passwd "+install.ServiceUser); err != nil {
		t.Errorf("uninstall --keep-data removed the user %s: %v", install.ServiceUser, err)
	}
	installRole(ctx, t, f.controller, versionN, "--role controller --agent-listen "+agentAddress)
	// A new session needs the kept admin, its TOTP secret and the kept
	// controller key, which seals them.
	login(ctx, t, f)
	waitHosts(ctx, t, f, 2*time.Minute, func(hosts []core.Host) error {
		if h, ok := localHost(hosts); !ok || h.ID != f.localID {
			return fmt.Errorf("the local host is %+v, want the kept host %s", h, f.localID)
		}
		if _, ok := findHost(hosts, f.agentID); !ok {
			return fmt.Errorf("the kept agent host %s is not listed", f.agentID)
		}
		return hostsAt(versionN, f.localID)(hosts)
	})
}

// requireUnitsGone checks that systemd on v no longer knows units.
func requireUnitsGone(ctx context.Context, t *testing.T, v *vm.VM, units ...string) {
	t.Helper()
	for _, u := range units {
		out, err := v.Run(ctx, "systemctl show -p LoadState --value "+u)
		if err != nil {
			t.Fatal(err)
		}
		if state := strings.TrimSpace(out); state != "not-found" {
			t.Errorf("%s is %s after uninstall, want not-found", u, state)
		}
	}
}

// ownedEntry is one row of the ownership manifest (core_owned_files).
type ownedEntry struct {
	Kind, Module, Block, Path string
}

// readManifest reads v's ownership manifest from its agent database.
func readManifest(ctx context.Context, t *testing.T, v *vm.VM) []ownedEntry {
	t.Helper()
	out, err := v.Run(ctx, "sudo sqlite3 -separator '|' "+paths.Production().AgentDB()+
		` "SELECT kind, module, block_id, path FROM core_owned_files ORDER BY path, module, block_id"`)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// parseManifest reads sqlite3's kind|module|block_id|path lines.
func parseManifest(out string) ([]ownedEntry, error) {
	var entries []ownedEntry
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 4)
		if len(f) != 4 || f[0] == "" || f[3] == "" {
			return nil, fmt.Errorf("harness: manifest line %q: want kind|module|block_id|path", line)
		}
		entries = append(entries, ownedEntry{Kind: f[0], Module: f[1], Block: f[2], Path: f[3]})
	}
	return entries, nil
}

// remainingScript is a shell script, run as root, that prints one line for
// each manifest entry still present: a user that exists, a marked block
// still in its file, or any other path that exists, a dangling link
// included.
func remainingScript(entries []ownedEntry) string {
	var b strings.Builder
	b.WriteString(`while IFS='|' read -r kind module block path; do
	case $kind in
	user) getent passwd "$path" >/dev/null && echo "user $path" ;;
	block) grep -qsF "BEGIN DARKPANEL $module/$block" "$path" && echo "block $module/$block in $path" ;;
	*) { [ -e "$path" ] || [ -L "$path" ]; } && echo "$kind $path" ;;
	esac
done <<'MANIFEST'
`)
	for _, e := range entries {
		b.WriteString(e.Kind + "|" + e.Module + "|" + e.Block + "|" + e.Path + "\n")
	}
	b.WriteString("MANIFEST\ntrue\n")
	return b.String()
}

func TestParseManifest(t *testing.T) {
	out := "unit|core||/etc/systemd/system/darkpanel-agent.service\nblock|firewall|rules|/etc/ufw/before.rules\nuser|core||darkpanel\n"
	got, err := parseManifest(out)
	want := []ownedEntry{
		{Kind: "unit", Module: "core", Path: "/etc/systemd/system/darkpanel-agent.service"},
		{Kind: "block", Module: "firewall", Block: "rules", Path: "/etc/ufw/before.rules"},
		{Kind: "user", Module: "core", Path: "darkpanel"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("parseManifest = %+v, %v; want %+v", got, err, want)
	}
	for _, bad := range []string{"unit|core|/etc/x\n", "dir|core||\n", "|core||/etc/x\n"} {
		if got, err := parseManifest(bad); err == nil {
			t.Errorf("parseManifest(%q) = %+v, want an error", bad, got)
		}
	}
}

func TestRemainingScript(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	kept := write("kept.conf", "x\n")
	blocks := write("hosts", "127.0.0.1 localhost\n# BEGIN DARKPANEL demo/names\n10.0.0.2 b\n# END DARKPANEL demo/names\n")
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	entries := []ownedEntry{
		{Kind: "file", Module: "core", Path: kept},
		{Kind: "file", Module: "core", Path: filepath.Join(dir, "gone.conf")},
		{Kind: "file", Module: "core", Path: link},
		{Kind: "dir", Module: "core", Path: dir},
		{Kind: "unit", Module: "core", Path: filepath.Join(dir, "gone.service")},
		{Kind: "block", Module: "demo", Block: "names", Path: blocks},
		{Kind: "block", Module: "demo", Block: "other", Path: blocks},
		{Kind: "block", Module: "demo", Block: "names", Path: filepath.Join(dir, "gone-hosts")},
		{Kind: "user", Module: "core", Path: "root"},
		{Kind: "user", Module: "core", Path: "dp-no-such-user"},
	}
	out, err := exec.Command("sh", "-c", remainingScript(entries)).CombinedOutput() //nolint:gosec // G204: the test's own script
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{"file " + kept, "file " + link, "dir " + dir, "block demo/names in " + blocks, "user root"}
	if got := strings.Split(strings.TrimSpace(string(out)), "\n"); !slices.Equal(got, want) {
		t.Errorf("remaining = %q, want %q", got, want)
	}
	if out, err := exec.Command("sh", "-c", remainingScript(nil)).CombinedOutput(); err != nil || len(out) != 0 { //nolint:gosec // G204: the test's own script
		t.Errorf("remainingScript of no entries printed %q, %v; want nothing", out, err)
	}
}
