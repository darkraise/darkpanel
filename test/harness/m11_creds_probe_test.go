//go:build harness

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m11CredsDeadline stays under the 15-minute -timeout of the vm-m11c step,
// so the lab's cleanups run before go test kills the binary.
const m11CredsDeadline = 12 * time.Minute

const (
	m11ProbeScript = "/var/lib/darkpanel-probe/s.sh"
	m11ProbeUnit   = "darkpanel-probe.service"
)

// m11ProbeUnitFile is the unit shape the jobs module writes: the script is
// root-only on disk and systemd hands the job a copy through LoadCredential=.
const m11ProbeUnitFile = `[Service]
Type=simple
User=probea
LoadCredential=script:` + m11ProbeScript + `
StandardInput=data
StandardInputData=aGkK
ExecStart=/bin/sh ${CREDENTIALS_DIRECTORY}/script
`

// TestM11CredsProbe checks on a fresh VM of -os that LoadCredential= hands a
// root-only script to an unprivileged service, that ${CREDENTIALS_DIRECTORY}
// expands in ExecStart=, that standard input still reaches the script, and
// that another user of the same primary group can read neither the delivered
// copy nor the file on disk.
func TestM11CredsProbe(t *testing.T) {
	if *osFlag == "" || !*m11Flag {
		t.Skip("no -os or -m11 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m11CredsDeadline)
	t.Cleanup(cancel)
	lab := NewLab(t)
	_, img, _ := harnessImages(ctx, t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	h := lab.Boot(ctx, t, []vm.Config{{
		Name: lab.VMName("creds"), Image: img, MemoryMiB: 2048, CPUs: 2,
		MgmtMAC: lab.MAC(NICMgmt, 11), InetMAC: lab.MAC(NICInet, 11), InetAddrs: lab.InetAddrs(11),
		McastPort: lab.McastPort, Key: key,
		Provision: scriptHeader,
	}})[0]
	root := func(cmd string) string {
		t.Helper()
		s, err := h.Run(ctx, "sudo bash -c "+m3Quote(cmd))
		if err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, s)
		}
		return s
	}

	t.Logf("systemd: %s", strings.SplitN(root("systemctl --version"), "\n", 2)[0])
	root("groupadd probeg && useradd -M -g probeg -s /bin/sh probea && useradd -M -g probeg -s /bin/sh probeb")
	root("mkdir -p /var/lib/darkpanel-probe && printf %s " +
		m3Quote(`id -un; read x; echo "after:$x"; echo "creds:$CREDENTIALS_DIRECTORY"; sleep 30`+"\n") +
		" > " + m11ProbeScript + " && chown 0:0 " + m11ProbeScript + " && chmod 0700 " + m11ProbeScript)
	root("printf %s " + m3Quote(m11ProbeUnitFile) + " > /etc/systemd/system/" + m11ProbeUnit +
		" && systemctl daemon-reload && systemctl start " + m11ProbeUnit)

	var lines []string
	if err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		out, err := h.Run(ctx, "sudo journalctl -u "+m11ProbeUnit+" -o cat --no-pager")
		if err != nil {
			return err
		}
		lines = strings.Split(strings.TrimSpace(out), "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "creds:") {
				return nil
			}
		}
		return fmt.Errorf("no creds: line yet:\n%s", out)
	}); err != nil {
		t.Fatalf("the probe unit printed no credentials directory: %v\n%s", err,
			root("systemctl status --no-pager "+m11ProbeUnit+" || true"))
	}
	creds := ""
	want := []string{"probea", "after:hi", "creds:"}
	next := 0
	for _, l := range lines {
		if next < len(want) && strings.HasPrefix(l, want[next]) {
			if want[next] == "creds:" {
				creds = strings.TrimPrefix(l, "creds:")
			}
			next++
		}
	}
	if next < len(want) || creds == "" {
		t.Fatalf("journal of %s: want lines starting %q in order, got:\n%s", m11ProbeUnit, want, strings.Join(lines, "\n"))
	}
	t.Logf("credentials directory: %s", creds)

	if s := root("systemctl is-active " + m11ProbeUnit + " || true"); strings.TrimSpace(s) != "active" {
		t.Fatalf("%s is %q, want active while the denials are checked", m11ProbeUnit, strings.TrimSpace(s))
	}
	for _, path := range []string{creds + "/script", m11ProbeScript} {
		out := root("runuser -u probeb -- cat " + m3Quote(path) + " 2>&1; echo rc=$?")
		if strings.HasSuffix(strings.TrimSpace(out), "rc=0") || !strings.Contains(out, "Permission denied") {
			t.Fatalf("probeb reading %s: want Permission denied, got:\n%s", path, out)
		}
	}
}
