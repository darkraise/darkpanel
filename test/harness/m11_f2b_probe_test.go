//go:build harness

package harness

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/test/harness/vm"
)

var (
	m11Flag       = flag.Bool("m11", false, "run the M11 scenarios; needs -os, and is a go test run of its own")
	m11RecordFlag = flag.String("m11-record", "", "directory TestM11F2BProbe writes its recordings to; empty uses a test temporary directory")
)

const m11ProbeDeadline = 30 * time.Minute

// m11ProbeJail is the jail.d file the fail2ban module writes: nftables ban
// actions and the journal backend.
const m11ProbeJail = `[DEFAULT]
banaction = nftables-multiport
banaction_allports = nftables-allports
backend = systemd

[sshd]
enabled = true
`

const m11ProbeJailPath = "/etc/fail2ban/jail.d/zz-darkpanel.local"

// m11ShutdownTargets are the targets a unit that powers the host off or
// reboots it depends on.
var m11ShutdownTargets = []string{"sleep.target", "shutdown.target", "umount.target", "final.target"}

// TestM11F2BProbe records the nftables table fail2ban's nftables actions
// create on a fresh VM of -os, after install, after an sshd ban and after a
// recidive ban, and the units whose start can power the host off: the
// firewall's fail2ban recognition and the services module's protected units
// are written against these recordings.
func TestM11F2BProbe(t *testing.T) {
	if *osFlag == "" || !*m11Flag {
		t.Skip("no -os or -m11 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m11ProbeDeadline)
	t.Cleanup(cancel)
	out := *m11RecordFlag
	if out == "" {
		out = t.TempDir()
	}
	for _, d := range []string{"f2b", "actions"} {
		if err := os.MkdirAll(filepath.Join(out, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lab := NewLab(t)
	o, img, _ := harnessImages(ctx, t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	h := lab.Boot(ctx, t, []vm.Config{{
		Name: lab.VMName("f2b"), Image: img, MemoryMiB: 2048, CPUs: 2,
		MgmtMAC: lab.MAC(NICMgmt, 11), InetMAC: lab.MAC(NICInet, 11), InetAddrs: lab.InetAddrs(11),
		McastPort: lab.McastPort, Key: key,
		Provision: scriptHeader + "apt_get update\napt_get install --no-install-recommends nftables\n",
	}})[0]
	root := func(cmd string) string {
		t.Helper()
		s, err := h.Run(ctx, "sudo bash -c "+m3Quote(cmd))
		if err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, s)
		}
		return s
	}
	jailUp := func(jail string) {
		t.Helper()
		if err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
			_, err := h.Run(ctx, "sudo fail2ban-client status "+jail)
			return err
		}); err != nil {
			t.Fatalf("jail %s did not start: %v\n%s", jail, err, root("journalctl -u fail2ban --no-pager | tail -n 40"))
		}
	}
	record := func(c string) {
		t.Helper()
		table := root(`if nft list table inet f2b-table >/run/f2b.out 2>/run/f2b.err; then cat /run/f2b.out
elif grep -q 'No such file or directory' /run/f2b.err; then echo absent
else cat /run/f2b.err >&2; exit 1; fi`)
		name := filepath.Join(out, "f2b", o.ID+"-"+c+".nft")
		if err := os.WriteFile(name, []byte(table), 0o644); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(table) == "absent" {
			if c != "install" {
				t.Fatalf("case %s: no inet f2b-table after a ban", c)
			}
			return
		}
		chains := nftBaseChains(table)
		if len(chains) != 1 || chains[0].name != "f2b-chain" ||
			!strings.Contains(chains[0].spec, "hook input") || !strings.Contains(chains[0].spec, "policy accept") {
			t.Fatalf("case %s: want one base chain f2b-chain with hook input and policy accept, got %+v\n%s", c, chains, table)
		}
		t.Logf("%s: %s", c, chains[0].spec)
	}

	root("mkdir -p /etc/fail2ban/jail.d && printf %s " + m3Quote(m11ProbeJail) + " > " + m11ProbeJailPath)
	root("apt-get -o DPkg::Lock::Timeout=600 -y install --no-install-recommends fail2ban python3-systemd && systemctl enable --now fail2ban")
	jailUp("sshd")
	record("install")
	root("fail2ban-client set sshd banip 192.0.2.10")
	record("sshd")
	root("printf '\\n[recidive]\\nenabled = true\\n' >> " + m11ProbeJailPath + " && fail2ban-client reload")
	jailUp("recidive")
	root("fail2ban-client set recidive banip 192.0.2.11")
	record("recidive")

	// A glob given to systemctl show matches only units already in memory, so
	// the static units that matter most would be missing.
	units := root("systemctl show --property=" + strings.Join(m11UnitProps, ",") +
		" $(systemctl list-unit-files --type=service --no-legend | awk '{print $1}' | grep -v '@\\.service$')")
	listing := shutdownUnits(units)
	if err := os.WriteFile(filepath.Join(out, "actions", o.ID+".txt"), []byte(listing), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("units that can power the host off:\n%s", listing)
}

type nftBaseChain struct{ name, spec string }

// nftBaseChains returns the chains of an `nft list table` text that carry a
// `type … hook …` line, with that line as spec.
func nftBaseChains(table string) []nftBaseChain {
	var chains []nftBaseChain
	chain := ""
	for _, l := range strings.Split(table, "\n") {
		l = strings.TrimSpace(l)
		if name, ok := strings.CutPrefix(l, "chain "); ok {
			chain = strings.TrimSpace(strings.TrimSuffix(name, "{"))
			continue
		}
		if chain != "" && strings.HasPrefix(l, "type ") && strings.Contains(l, " hook ") {
			chains = append(chains, nftBaseChain{chain, l})
		}
		if l == "}" {
			chain = ""
		}
	}
	return chains
}

// m11UnitProps are the properties a unit listing line records, in order.
var m11UnitProps = []string{"Id", "Names", "Requires", "Wants", "SuccessAction", "FailureAction", "JobTimeoutAction", "StartLimitAction"}

// shutdownUnits turns `systemctl show` output for many units into sorted
// lines `<unit> <rule> <property>=<value>...`, one per unit, with the values
// of m11UnitProps. The rule is the first that matches: dependency when
// Requires or Wants names a shutdown target, success-action when
// SuccessAction is set, other-action when FailureAction, JobTimeoutAction or
// StartLimitAction is. A value may hold spaces; each property starts at the
// next known name.
func shutdownUnits(show string) string {
	seen := map[string]bool{}
	var lines []string
	for _, block := range strings.Split(show, "\n\n") {
		p := map[string]string{}
		for _, l := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				p[k] = v
			}
		}
		id := p["Id"]
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		set := func(k string) bool { return p[k] != "" && p[k] != "none" }
		deps := strings.Fields(p["Requires"] + " " + p["Wants"])
		rule := ""
		switch {
		case slices.ContainsFunc(m11ShutdownTargets, func(t string) bool { return slices.Contains(deps, t) }):
			rule = "dependency"
		case set("SuccessAction"):
			rule = "success-action"
		case set("FailureAction") || set("JobTimeoutAction") || set("StartLimitAction"):
			rule = "other-action"
		default:
			continue
		}
		fields := []string{id, rule}
		for _, k := range m11UnitProps {
			fields = append(fields, k+"="+p[k])
		}
		lines = append(lines, strings.Join(fields, " "))
	}
	slices.Sort(lines)
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("%s\n", strings.Join(lines, "\n"))
}
