//go:build harness

package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/controller/backup"
	ctlfleet "github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m7rFlag selects TestM7Restore.
var m7rFlag = flag.Bool("m7r", false, "run TestM7Restore; needs -os and -m2, and is a go test run of its own")

// m7rDeadline bounds TestM7Restore. The workflow step that runs it must keep
// its -timeout at least m7rDeadline plus 10 minutes, for the cleanups that stop
// the VMs.
const m7rDeadline = 40 * time.Minute

// The restored controller's addresses continue the lab's numbering: the
// controller is host 10, the agent 11 and the restored controller 12.
const (
	m7rRestoredIPv4 = "198.18.0.12"
	m7rRestoredIPv6 = "2001:db8:d0::12"
	m7rRestoredPriv = "10.77.0.12"
)

// m7rRestoredAddress is the restored controller's agent listen address, and
// the address its enrollment tokens name.
var m7rRestoredAddress = net.JoinHostPort(m7rRestoredPriv, strconv.Itoa(listen.AgentPort))

// The seeded in-flight composite (W7-R3: a controller-side record) and the locks it and an update hold.
const (
	m7rCompositeID      = "01JW7R00000000000000000C0M"
	m7rCompositePlan    = "01JW7R00000000000000000P1N"
	m7rCompositeAction  = "derp.server.deploy"
	m7rLockKey          = "derp.server:m7r"
	m7rReservationKey   = "core.update:m7r"
	m7rReservationOwner = "01JW7R00000000000000000RSV"
)

const (
	// m7rTokenSkew is the clock difference allowed between the runner and a VM
	// when an enrollment token's expiry is judged.
	m7rTokenSkew = 5 * time.Minute
	// m7rDiagnosticTimeout bounds each command of logDiagnostics.
	m7rDiagnosticTimeout = 15 * time.Second

	m7rBackupFile = "/var/tmp/m7r.dpbk"
	m7rPassFile   = "/root/m7r-pass" //nolint:gosec // G101: a file path, not a credential
	m7rWrongFile  = "/root/m7r-wrong"
)

var (
	m7rArgvRE  = regexp.MustCompile(`argv\[\]=[^;]*\scontroller restore --recover(\s|$)`)
	m7rCleanRE = regexp.MustCompile(`\bcode=exited ; status=0\b`)
)

// m7rSeedStatements are the INSERT statements, without trailing semicolons, that seed the composite.
func m7rSeedStatements(agentID string, now time.Time) []string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	const layout = "2006-01-02T15:04:05.000000000Z"
	at := q(now.UTC().Format(layout))
	deadline := q(now.UTC().Add(24 * time.Hour).Format(layout))
	return []string{
		"INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json, confirmation, created_at, expires_at) VALUES (" +
			strings.Join([]string{q(m7rCompositePlan), q(""), q(m7rCompositeAction), "1", q("user:m7r"), q("m7r"), q("m7r"), q("[]"), q("[]"), q(""), at, deadline}, ", ") + ")",
		"INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json, recovery_owner, created_at, updated_at) VALUES (" +
			strings.Join([]string{q(m7rCompositeID), q(m7rCompositePlan), q(""), q(m7rCompositeAction), "1", q("composite"), q(""), q("user:m7r"), q("applying"), q("{}"), q("composite"), at, at}, ", ") + ")",
		"INSERT INTO core_composites (change_id, direction, cursor, steps_deadline) VALUES (" +
			strings.Join([]string{q(m7rCompositeID), q("forward"), "0", deadline}, ", ") + ")",
		"INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action, after_json, input_template, state, updated_at) VALUES (" +
			strings.Join([]string{q(m7rCompositeID), q("tailscaled"), "0", q(agentID), q("tailscale.package.pin/v1"), q("[]"), q("{}"), q("pending"), at}, ", ") + ")",
		"INSERT INTO core_locks (key, owner) VALUES (" + q(m7rLockKey) + ", " + q(m7rCompositeID) + ")",
		"INSERT INTO core_locks (key, owner) VALUES (" + q(m7rReservationKey) + ", " + q(m7rReservationOwner) + ")",
	}
}

// m7rDecode decodes the output of a CLI run with --json: exactly one JSON
// document whose members T knows.
func m7rDecode[T any](out string) (T, error) {
	var zero, v T
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return zero, fmt.Errorf("harness: decode the CLI's JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return zero, errors.New("harness: the CLI printed more than one JSON document")
	}
	return v, nil
}

// m7rCheckBackup checks that data is a backup file under passphrase that
// holds an SQLite database and a controller key file, and that it does not
// hold the passphrase itself.
func m7rCheckBackup(data, passphrase []byte) error {
	if bytes.Contains(data, passphrase) {
		return errors.New("harness: the backup file holds the passphrase in the clear")
	}
	var names []string
	var defect error
	sink := func(name string, size int64, body io.Reader) error {
		names = append(names, name)
		head := make([]byte, 17)
		n, err := io.ReadFull(body, head)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		head = head[:n]
		if _, err := io.Copy(io.Discard, body); err != nil {
			return err
		}
		switch name {
		case backup.MemberDB:
			if !bytes.HasPrefix(head, []byte("SQLite format 3\x00")) {
				defect = errors.New("harness: the database member is not an SQLite database")
			}
		case backup.MemberKey:
			if !bytes.HasPrefix(head, []byte(secret.KeyPrefix)) || size > backup.MaxKeyMember {
				defect = errors.New("harness: the key member is not a controller key file")
			}
		}
		return defect
	}
	// backup.Read clears the passphrase slice it is given.
	_, err := backup.Read(bytes.NewReader(data), slices.Clone(passphrase), sink)
	if defect != nil {
		return defect
	}
	if err != nil {
		return fmt.Errorf("harness: read the backup: %w", err)
	}
	if want := []string{backup.MemberDB, backup.MemberKey}; !slices.Equal(names, want) {
		return fmt.Errorf("harness: the backup holds the members %v, want %v", names, want)
	}
	return nil
}

// m7rCheckQuarantine checks that st is an active quarantine holding exactly
// the record want.
func m7rCheckQuarantine(st backup.Status, want backup.Record) error {
	if !st.Active {
		return errors.New("harness: the quarantine is not active")
	}
	if st.Tx == "" {
		return errors.New("harness: the quarantine names no restore transaction")
	}
	if st.RestoredAt == nil {
		return errors.New("harness: the quarantine has no restoredAt")
	}
	if len(st.Records) != 1 {
		return fmt.Errorf("harness: the quarantine lists %d records, want the one record %s", len(st.Records), want.ID)
	}
	r := st.Records[0]
	switch {
	case r.ID != want.ID:
		return fmt.Errorf("harness: the quarantined record is %s, want %s", r.ID, want.ID)
	case r.Kind != want.Kind:
		return fmt.Errorf("harness: the record's kind is %q, want %s", r.Kind, want.Kind)
	case r.Action != want.Action:
		return fmt.Errorf("harness: the record's action is %q, want %s", r.Action, want.Action)
	case r.HostID != want.HostID:
		return fmt.Errorf("harness: the record's host is %q, want %q", r.HostID, want.HostID)
	case r.QuarantinedFrom != want.QuarantinedFrom:
		return fmt.Errorf("harness: the record's quarantinedFrom is %q, want %s", r.QuarantinedFrom, want.QuarantinedFrom)
	case r.Locks == nil:
		return errors.New("harness: the record's locks are null, want a list")
	case !slices.Equal(r.Locks, want.Locks):
		return fmt.Errorf("harness: the record's locks are %v, want %v", r.Locks, want.Locks)
	}
	return nil
}

// m7rCheckToken checks that res is a token valid for about ctlfleet.TokenTTL
// from now, as the VM's clock tells it.
func m7rCheckToken(res backup.EnrollTokenResult, now time.Time) error {
	switch {
	case res.Token == "":
		return errors.New("harness: the enrollment token is empty")
	case !res.ExpiresAt.After(now):
		return errors.New("harness: the enrollment token has already expired")
	case res.ExpiresAt.After(now.Add(ctlfleet.TokenTTL + m7rTokenSkew)):
		return fmt.Errorf("harness: the enrollment token is valid for more than %s", ctlfleet.TokenTTL)
	case res.ExpiresAt.Before(now.Add(ctlfleet.TokenTTL - m7rTokenSkew)):
		return fmt.Errorf("harness: the enrollment token is valid for less than %s", ctlfleet.TokenTTL)
	}
	return nil
}

// m7rCheckNames checks the names of a directory listing (ls -A) after a
// restore: no staged file and no journal is left, base is there, and so is
// base's set-aside copy.
func m7rCheckNames(listing, base string) error {
	names := strings.Fields(listing)
	for _, n := range names {
		if strings.Contains(n, ".restore-") {
			return fmt.Errorf("harness: the restore left the staged file %s", n)
		}
		if strings.HasPrefix(n, "restore-") && strings.HasSuffix(n, ".journal") {
			return fmt.Errorf("harness: the restore left the journal %s", n)
		}
	}
	if !slices.Contains(names, base) {
		return fmt.Errorf("harness: %s is missing after the restore", base)
	}
	if !slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, base+".pre-") }) {
		return fmt.Errorf("harness: no %s.pre-<tx> set-aside copy of the original", base)
	}
	return nil
}

// m7rExecStartPre judges the output of systemctl show -p ExecStartPre of the
// controller unit: the line runs controller restore --recover and has run to
// a clean exit.
func m7rExecStartPre(show string) error {
	s := strings.TrimSpace(show)
	if !strings.HasPrefix(s, "ExecStartPre={") {
		return errors.New("harness: systemctl show printed no ExecStartPre")
	}
	if !m7rArgvRE.MatchString(s) {
		return errors.New("harness: ExecStartPre runs a command other than controller restore --recover")
	}
	if !m7rCleanRE.MatchString(s) {
		return errors.New("harness: ExecStartPre has not run to a clean exit")
	}
	return nil
}

// m7rBinaryCommands are the commands that install.sh runs around copying the
// binary: prepare makes the binary directory and link puts the binary on PATH.
func m7rBinaryCommands() (prepare, link string) {
	p := paths.Production()
	return "sudo install -d -m 0755 " + p.BinaryDir, "sudo ln -sfn " + p.Binary() + " " + p.BinaryLink
}

// m7rControllerInstall installs the controller role listening for agents on
// addr and naming addr in its enrollment tokens.
func m7rControllerInstall(addr string) string {
	return "sudo darkpanel system install --role controller --agent-listen " + addr + " --agent-advertise " + addr
}

// m7rReEnrollCommands enroll an enrolled agent again with a token on standard
// input, then restart it. system install --role agent cannot do this: it keeps
// saved credentials that a controller still accepts and ignores the token. A
// running agent takes the new credentials up only when its live session ends.
func m7rReEnrollCommands() (enroll, restart string) {
	return "sudo darkpanel agent enroll", "sudo systemctl restart " + update.AgentUnit
}

// m7rRestoreCommand restores m7rBackupFile with the passphrase in passFile.
func m7rRestoreCommand(passFile string) string {
	return "sudo darkpanel controller restore --in " + m7rBackupFile + " --passphrase-file " + passFile
}

// m7rConfigs returns the source controller, the agent and the restored
// controller on the lab's own links, built as m5Configs builds a target.
func m7rConfigs(lab *Lab, o OS, targetImg string, key ssh.Signer) []vm.Config {
	target := func(role string, host int, inet []string, priv string) vm.Config {
		return vm.Config{
			Name: lab.VMName(role), Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: lab.MAC(NICMgmt, host), InetMAC: lab.MAC(NICInet, host), PrivMAC: lab.MAC(NICPriv, host),
			InetAddrs: inet, PrivAddrs: []string{priv + "/24"},
			McastPort: lab.McastPort, PrivMcastPort: lab.PrivMcastPort,
			Provision: TargetScript(o), Key: key,
		}
	}
	return []vm.Config{
		target("controller", 10, []string{targetIPv4 + "/15", targetIPv6 + "/64"}, controllerPriv),
		target("agent", 11, []string{agentIPv4 + "/15", agentIPv6 + "/64"}, agentPriv),
		target("restored", 12, []string{m7rRestoredIPv4 + "/15", m7rRestoredIPv6 + "/64"}, m7rRestoredPriv),
	}
}

// m7rRun is what the steps of TestM7Restore share.
type m7rRun struct {
	ctx        context.Context
	f          *fleet // the source controller and the agent; the lab has no scanner
	restored   *vm.VM
	g          *fleet // the restored controller's API session, set by refusedInQuarantine
	backup     []byte
	passphrase string
	tx         string // the restore's transaction, set by quarantine
}

// TestM7Restore is the host half of the controller backup and restore (spec
// 10): a controller backs itself up while a composite and an update hold
// locks, the backup is restored onto a fresh controller, which starts in
// restore quarantine with the composite quarantined and nothing unwound, and
// the agent is enrolled again with a token the quarantine issues before the
// record is resolved and the quarantine released. The workflow selects it
// with -run, -os, -m2 and -m7r.
func TestM7Restore(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m7rFlag {
		t.Skip("no -os, -m2 or -m7r given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m7rDeadline)
	t.Cleanup(cancel)
	lab := NewLab(t)
	o, targetImg, _ := harnessImages(ctx, t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	vms := lab.Boot(ctx, t, m7rConfigs(lab, o, targetImg, key))
	r := &m7rRun{
		ctx:        ctx,
		f:          &fleet{os: o, controller: vms[0], agent: vms[1]},
		restored:   vms[2],
		passphrase: rand.Text(),
	}
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install-source", true, r.installSource},
		{"seed", true, r.seed},
		{"backup", true, r.takeBackup},
		{"install-restored", true, r.installRestored},
		{"refused-while-running", false, r.refusedWhileRunning},
		{"stop-services", true, r.stopServices},
		{"wrong-passphrase", false, r.wrongPassphrase},
		{"restore", true, r.restore},
		{"start", true, r.start},
		{"quarantine", true, r.quarantine},
		{"refused-in-quarantine", false, r.refusedInQuarantine},
		{"re-enroll", true, r.reEnroll},
		{"release-refused", false, r.releaseRefused},
		{"resolve", true, r.resolve},
		{"release", true, r.release},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			r.logDiagnostics(t)
			if s.gate {
				return
			}
		}
	}
}

// installBinary installs the binary of -bin on v as install.sh does.
func (r *m7rRun) installBinary(t *testing.T, v *vm.VM) {
	t.Helper()
	prepare, link := m7rBinaryCommands()
	m3Run(r.ctx, t, v, prepare)
	if err := v.CopyFile(r.ctx, *binFlag, paths.Production().Binary(), 0o755); err != nil {
		t.Fatal(err)
	}
	m3Run(r.ctx, t, v, link)
}

// waitActive waits until every unit of units is active on v, quoting their
// journals when they are not.
func (r *m7rRun) waitActive(t *testing.T, v *vm.VM, units ...string) {
	t.Helper()
	err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
		for _, u := range units {
			if state := unitState(ctx, v, u); state != "active" {
				return fmt.Errorf("%s is %q", u, state)
			}
		}
		return nil
	})
	if err != nil {
		cmd := "sudo journalctl --no-pager -n 80"
		for _, u := range units {
			cmd += " -u " + u
		}
		journal, _ := v.Run(r.ctx, cmd)
		t.Fatalf("%v\njournal:\n%s", err, journal)
	}
}

// installController installs the binary and the controller role on v with
// the agent address addr and waits for both of its services.
func (r *m7rRun) installController(t *testing.T, v *vm.VM, addr string) {
	t.Helper()
	r.installBinary(t, v)
	if out, err := v.Run(r.ctx, m7rControllerInstall(addr)); err != nil {
		journal, _ := v.Run(r.ctx, "sudo journalctl -u "+update.ControllerUnit+" -u "+update.AgentUnit+" --no-pager -n 80")
		t.Fatalf("%v\n%s\njournal:\n%s", err, out, journal)
	}
	r.waitActive(t, v, update.ControllerUnit, update.AgentUnit)
}

// enrollAgent enrolls the agent VM with token, which goes through standard
// input and never into a command line.
func (r *m7rRun) enrollAgent(t *testing.T, token string) {
	t.Helper()
	if _, err := r.f.agent.RunInput(r.ctx, "sudo darkpanel system install --role agent", strings.NewReader(token+"\n")); err != nil {
		journal, _ := r.f.agent.Run(r.ctx, "sudo journalctl -u "+update.AgentUnit+" --no-pager -n 80")
		t.Fatalf("%v\njournal:\n%s", err, journal)
	}
}

// m7rOnline accepts a host list in which the host id is enrolled and online.
func m7rOnline(id string) func([]core.Host) error {
	return func(hosts []core.Host) error {
		h, ok := findHost(hosts, id)
		if !ok {
			return fmt.Errorf("host %s is not listed", id)
		}
		if h.State != core.HostEnrolled || !h.Online {
			return fmt.Errorf("host %s (%s) is %s and online %v, want enrolled and online", h.Name, id, h.State, h.Online)
		}
		return nil
	}
}

// installSource installs the source controller, creates and logs in the
// admin, and enrolls the agent.
func (r *m7rRun) installSource(t *testing.T) {
	f := r.f
	r.installBinary(t, f.agent)
	r.installController(t, f.controller, agentAddress)
	createAdmin(r.ctx, t, f)
	login(r.ctx, t, f)
	hosts := waitHosts(r.ctx, t, f, 2*time.Minute, func(hosts []core.Host) error {
		h, ok := localHost(hosts)
		if !ok {
			return errors.New("no local host listed")
		}
		return m7rOnline(h.ID)(hosts)
	})
	local, _ := localHost(hosts)
	f.localID = local.ID

	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(r.ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent", ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	f.agentID = grant.Host.ID
	r.enrollAgent(t, grant.Token)
	waitHosts(r.ctx, t, f, 2*time.Minute, m7rOnline(f.agentID))
}

// query runs the read-only SQL q on v's controller database and returns its
// output without surrounding space.
func (r *m7rRun) query(t *testing.T, v *vm.VM, q string) string {
	t.Helper()
	return strings.TrimSpace(m3Run(r.ctx, t, v, "sudo sqlite3 -separator '|' "+paths.Production().ControllerDB()+" "+m3Quote(q)))
}

// expect requires that q prints want on v's controller database.
func (r *m7rRun) expect(t *testing.T, v *vm.VM, q, want string) {
	t.Helper()
	if got := r.query(t, v, q); got != want {
		t.Errorf("%s printed %q, want %q", q, got, want)
	}
}

// checkSeed requires that the seeded composite is in flight and that it and
// the update hold their locks on v.
func (r *m7rRun) checkSeed(t *testing.T, v *vm.VM) {
	t.Helper()
	r.expect(t, v, "SELECT state, recovery_owner FROM core_changes WHERE id = '"+m7rCompositeID+"'", "applying|composite")
	r.expect(t, v, "SELECT count(*) FROM core_locks WHERE owner IN ('"+m7rCompositeID+"', '"+m7rReservationOwner+"')", "2")
}

// seed writes the composite and the update reservation into the running
// source controller's database. They stand for work in flight when the
// backup is taken; nothing else writes these rows.
func (r *m7rRun) seed(t *testing.T) {
	v := r.f.controller
	now := time.Unix(vmNow(r.ctx, t, v), 0)
	script := ".timeout 10000\nBEGIN IMMEDIATE;\n" + strings.Join(m7rSeedStatements(r.f.agentID, now), ";\n") + ";\nCOMMIT;\n"
	if out, err := v.RunInput(r.ctx, "sudo sqlite3 -bail "+paths.Production().ControllerDB(), strings.NewReader(script)); err != nil {
		t.Fatalf("the seed: %v\n%s", err, out)
	}
	r.checkSeed(t, v)
}

// takeBackup backs the source controller up, checks the file, and checks
// that a second backup refuses to overwrite it and that the seed was in the
// database before and after the copy.
func (r *m7rRun) takeBackup(t *testing.T) {
	v := r.f.controller
	if _, err := v.RunInput(r.ctx, "sudo sh -c 'umask 077 && cat > "+m7rPassFile+"'", strings.NewReader(r.passphrase)); err != nil {
		t.Fatal(err)
	}
	cmd := "sudo darkpanel controller backup --out " + m7rBackupFile + " --passphrase-file " + m7rPassFile
	if out := m3Run(r.ctx, t, v, cmd); !strings.Contains(out, "Backup written to "+m7rBackupFile) {
		t.Fatalf("%s printed %q, want it to name the written file", cmd, out)
	}
	data := m3Run(r.ctx, t, v, "sudo cat "+m7rBackupFile)
	size, err := strconv.Atoi(strings.TrimSpace(m3Run(r.ctx, t, v, "sudo stat -c %s "+m7rBackupFile)))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != size {
		t.Fatalf("read %d bytes of %s, whose size is %d", len(data), m7rBackupFile, size)
	}
	if err := m7rCheckBackup([]byte(data), []byte(r.passphrase)); err != nil {
		t.Fatal(err)
	}
	r.backup = []byte(data)

	if _, err := v.Run(r.ctx, cmd); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("a second backup to %s = %v, want it refused", m7rBackupFile, err)
	}
	for _, n := range strings.Fields(m3Run(r.ctx, t, v, "ls -A /var/tmp")) {
		if strings.Contains(n, ".m7r.dpbk.") {
			t.Errorf("/var/tmp holds the partial file %s", n)
		}
	}
	r.checkSeed(t, v)
}

// installRestored installs the restored controller and copies the backup,
// its passphrase file and a file with another passphrase to it.
func (r *m7rRun) installRestored(t *testing.T) {
	v := r.restored
	r.installController(t, v, m7rRestoredAddress)
	dir := t.TempDir()
	for _, c := range []struct {
		dst  string
		data []byte
	}{
		{m7rBackupFile, r.backup},
		{m7rPassFile, []byte(r.passphrase)},
		{m7rWrongFile, []byte(rand.Text())},
	} {
		local := filepath.Join(dir, filepath.Base(c.dst))
		if err := os.WriteFile(local, c.data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := v.CopyFile(r.ctx, local, c.dst, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// untouched requires that the restored controller's directory holds no
// set-aside copy, no staged file and no journal.
func (r *m7rRun) untouched(t *testing.T) {
	t.Helper()
	for _, n := range strings.Fields(m3Run(r.ctx, t, r.restored, "sudo ls -A "+paths.Production().ControllerDir)) {
		if strings.Contains(n, ".pre-") || strings.Contains(n, ".restore-") || (strings.HasPrefix(n, "restore-") && strings.HasSuffix(n, ".journal")) {
			t.Errorf("the controller directory holds %s after a refused restore", n)
		}
	}
}

// refusedWhileRunning: a restore is refused while the services run, and
// touches nothing.
func (r *m7rRun) refusedWhileRunning(t *testing.T) {
	const want = "stop darkpanel-controller.service and darkpanel-agent.service before a restore"
	if _, err := r.restored.Run(r.ctx, m7rRestoreCommand(m7rPassFile)); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a restore while the services run = %v, want an error containing %q", err, want)
	}
	r.untouched(t)
}

// stopServices stops both services of the restored controller.
func (r *m7rRun) stopServices(t *testing.T) {
	v := r.restored
	units := []string{update.ControllerUnit, update.AgentUnit}
	m3Run(r.ctx, t, v, "sudo systemctl stop "+strings.Join(units, " "))
	err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		for _, u := range units {
			if state := unitState(ctx, v, u); state != "inactive" {
				return fmt.Errorf("%s is %q", u, state)
			}
		}
		return nil
	})
	if err != nil {
		r.logUnits(t, "restored controller", v, units...)
		t.Fatal(err)
	}
}

// m7rUnitDiagnostics are the commands whose output explains why each unit of
// units is not in the state a step waits for.
func m7rUnitDiagnostics(units ...string) []string {
	var cmds []string
	for _, u := range units {
		cmds = append(cmds, "sudo systemctl status "+u+" --no-pager", "sudo journalctl -u "+u+" --no-pager -n 40")
	}
	return cmds
}

// logUnits logs the m7rUnitDiagnostics of units on v, the VM name names.
// systemctl status exits non-zero for a unit that is not active, so a
// command's output is logged with its error and never fails the test.
func (r *m7rRun) logUnits(t *testing.T, name string, v *vm.VM, units ...string) {
	t.Helper()
	for _, cmd := range m7rUnitDiagnostics(units...) {
		ctx, cancel := context.WithTimeout(r.ctx, m7rDiagnosticTimeout)
		out, err := v.Run(ctx, cmd)
		cancel()
		if err != nil {
			t.Logf("diagnostic %s\n$ %s\n%s\nerror: %v", name, cmd, out, err)
			continue
		}
		t.Logf("diagnostic %s\n$ %s\n%s", name, cmd, out)
	}
}

// wrongPassphrase: a restore with another passphrase fails and touches nothing.
func (r *m7rRun) wrongPassphrase(t *testing.T) {
	const want = "wrong passphrase or damaged backup file"
	if _, err := r.restored.Run(r.ctx, m7rRestoreCommand(m7rWrongFile)); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a restore with the wrong passphrase = %v, want an error containing %q", err, want)
	}
	r.untouched(t)
}

// restore restores the backup and checks what it left: the installed
// database and key with their owner and mode, the set-aside originals, no
// staged file or journal, and the key of the source controller.
func (r *m7rRun) restore(t *testing.T) {
	v, p := r.restored, paths.Production()
	out := m3Run(r.ctx, t, v, m7rRestoreCommand(m7rPassFile))
	for _, want := range []string{"Restored the backup taken", "restore quarantine"} {
		if !strings.Contains(out, want) {
			t.Errorf("the restore printed %q, want it to contain %q", out, want)
		}
	}
	for dir, base := range map[string]string{
		p.ControllerDir:               filepath.Base(p.ControllerDB()),
		filepath.Dir(p.ControllerKey): filepath.Base(p.ControllerKey),
	} {
		if err := m7rCheckNames(m3Run(r.ctx, t, v, "sudo ls -A "+dir), base); err != nil {
			t.Error(err)
		}
	}
	for _, file := range []string{p.ControllerDB(), p.ControllerKey} {
		if got, want := strings.TrimSpace(m3Run(r.ctx, t, v, "sudo stat -c '%U %a' "+file)), install.ServiceUser+" 600"; got != want {
			t.Errorf("%s is %q (owner and mode), want %q", file, got, want)
		}
	}
	sum := func(v *vm.VM) string {
		f := strings.Fields(m3Run(r.ctx, t, v, "sudo sha256sum "+p.ControllerKey))
		if len(f) == 0 {
			t.Fatalf("sha256sum of %s printed nothing", p.ControllerKey)
		}
		return f[0]
	}
	if sum(v) != sum(r.f.controller) {
		t.Errorf("the restored %s differs from the source controller's: the key did not come from the backup", p.ControllerKey)
	}
}

// start starts the restored controller, whose unit runs the restore
// recovery before the controller.
func (r *m7rRun) start(t *testing.T) {
	v := r.restored
	m3Run(r.ctx, t, v, "sudo systemctl start "+update.ControllerUnit+" "+update.AgentUnit)
	r.waitActive(t, v, update.ControllerUnit)
	if err := m7rExecStartPre(m3Run(r.ctx, t, v, "sudo systemctl show -p ExecStartPre "+update.ControllerUnit)); err != nil {
		t.Error(err)
	}
}

// status reads the restored controller's quarantine through the CLI.
func (r *m7rRun) status(ctx context.Context) (backup.Status, error) {
	out, err := r.restored.Run(ctx, "sudo darkpanel controller quarantine list --json")
	if err != nil {
		return backup.Status{}, err
	}
	return m7rDecode[backup.Status](out)
}

// notUnwound requires that nothing of the composite was unwound.
func (r *m7rRun) notUnwound(t *testing.T) {
	t.Helper()
	v := r.restored
	r.expect(t, v, "SELECT direction, cursor FROM core_composites WHERE change_id = '"+m7rCompositeID+"'", "forward|0")
	r.expect(t, v, "SELECT state FROM core_composite_steps WHERE change_id = '"+m7rCompositeID+"'", "pending")
	r.expect(t, v, "SELECT count(*) FROM core_changes WHERE parent_change_id = '"+m7rCompositeID+"'", "0")
}

// quarantine: the restored controller is in quarantine with the composite
// quarantined, its lock kept, the update's lock gone and nothing unwound.
func (r *m7rRun) quarantine(t *testing.T) {
	var st backup.Status
	err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
		var err error
		st, err = r.status(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m7rCheckQuarantine(st, backup.Record{
		ID: m7rCompositeID, Kind: "composite", Action: m7rCompositeAction, HostID: "",
		QuarantinedFrom: core.StateApplying, Locks: []string{m7rLockKey},
	}); err != nil {
		t.Fatal(err)
	}
	r.tx = st.Tx
	v := r.restored
	r.expect(t, v, "SELECT state, quarantined_from FROM core_changes WHERE id = '"+m7rCompositeID+"'", "quarantined|applying")
	r.expect(t, v, "SELECT count(*) FROM core_locks WHERE owner = '"+m7rCompositeID+"'", "1")
	r.expect(t, v, "SELECT count(*) FROM core_locks WHERE key = '"+m7rReservationKey+"'", "0")
	r.notUnwound(t)
}

// refusedInQuarantine logs the admin of the backup in on the restored
// controller, whose changes the quarantine refuses.
func (r *m7rRun) refusedInQuarantine(t *testing.T) {
	f := r.f
	r.g = &fleet{controller: r.restored, password: f.password, totpSecret: f.totpSecret, lastCode: f.lastCode}
	login(r.ctx, t, r.g)
	_, err := r.g.api.Apply(r.ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "blocked", ControllerAddress: m7rRestoredAddress}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != string(errcode.Quarantined) {
		t.Errorf("core.host.create in quarantine = %v, want %s", err, errcode.Quarantined)
	}
}

// reEnroll enrolls the agent with the restored controller through a token
// the quarantine issues.
func (r *m7rRun) reEnroll(t *testing.T) {
	if r.g == nil {
		t.Fatal("no API session on the restored controller")
	}
	v := r.restored
	out := m3Run(r.ctx, t, v, "sudo darkpanel controller quarantine enroll-token "+r.f.agentID+" --json")
	res, err := m7rDecode[backup.EnrollTokenResult](out)
	if err != nil {
		t.Fatal(err)
	}
	if err := m7rCheckToken(res, time.Unix(vmNow(r.ctx, t, v), 0)); err != nil {
		t.Fatal(err)
	}
	a := r.f.agent
	enroll, restart := m7rReEnrollCommands()
	if out, err := a.RunInput(r.ctx, enroll, strings.NewReader(res.Token+"\n")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m3Run(r.ctx, t, a, restart)
	r.waitActive(t, a, update.AgentUnit)
	waitHosts(r.ctx, t, r.g, 3*time.Minute, m7rOnline(r.f.agentID))
}

// releaseRefused: the quarantine is not released while a record is
// unresolved, and minutes after the start nothing was unwound.
func (r *m7rRun) releaseRefused(t *testing.T) {
	const want = "records are still quarantined"
	if _, err := r.restored.Run(r.ctx, "sudo darkpanel controller quarantine release"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a release with an unresolved record = %v, want an error containing %q", err, want)
	}
	st, err := r.status(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Active {
		t.Error("the quarantine is no longer active after a refused release")
	}
	r.notUnwound(t)
}

// resolve resolves the composite as never applied: its one step names the
// agent and has no child plan (W7-R4).
func (r *m7rRun) resolve(t *testing.T) {
	v := r.restored
	out := m3Run(r.ctx, t, v, "sudo darkpanel controller quarantine resolve "+m7rCompositeID+" never --json")
	rec, err := m7rDecode[backup.Record](out)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != m7rCompositeID || rec.Kind != "composite" || rec.QuarantinedFrom != core.StateApplying {
		t.Errorf("resolve answered %+v, want the record %s of kind composite quarantined from %s", rec, m7rCompositeID, core.StateApplying)
	}
	r.expect(t, v, "SELECT state, error_message FROM core_changes WHERE id = '"+m7rCompositeID+"'", "abandoned|restore: never applied")
	r.expect(t, v, "SELECT count(*) FROM core_locks WHERE owner = '"+m7rCompositeID+"'", "0")
}

// release releases the quarantine, after which the controller takes changes
// again.
func (r *m7rRun) release(t *testing.T) {
	out := m3Run(r.ctx, t, r.restored, "sudo darkpanel controller quarantine release --json")
	res, err := m7rDecode[backup.ReleaseResult](out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.HeldLocks) != 0 {
		t.Errorf("release answered the held locks %+v, want none", res.HeldLocks)
	}
	st, err := r.status(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Active {
		t.Errorf("the quarantine of restore %s is still active after the release", st.Tx)
	}
	err = eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		_, err := r.g.api.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "after-release", ControllerAddress: m7rRestoredAddress}, nil)
		return err
	})
	if err != nil {
		t.Errorf("core.host.create after the release: %v", err)
	}
}

// logDiagnostics logs the end of the controller journal of the source and
// the restored controller. A command that fails is logged with its error and
// never fails the test.
func (r *m7rRun) logDiagnostics(t *testing.T) {
	const cmd = "sudo journalctl -u " + update.ControllerUnit + " --no-pager -n 60"
	for _, d := range []struct {
		name string
		v    *vm.VM
	}{{"source controller", r.f.controller}, {"restored controller", r.restored}} {
		ctx, cancel := context.WithTimeout(r.ctx, m7rDiagnosticTimeout)
		out, err := d.v.Run(ctx, cmd)
		cancel()
		if err != nil {
			t.Logf("diagnostic %s\n$ %s\n%s\nerror: %v", d.name, cmd, out, err)
			continue
		}
		t.Logf("diagnostic %s\n$ %s\n%s", d.name, cmd, out)
	}
}
