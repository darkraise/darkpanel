//go:build harness

package harness

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/fail2ban"
	"github.com/darkraise/darkpanel/internal/modules/jobs"
	m11svc "github.com/darkraise/darkpanel/internal/modules/services"
	"github.com/darkraise/darkpanel/internal/modules/tuning"
	"github.com/darkraise/darkpanel/internal/modules/users"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/release"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

const (
	// m11Deadline bounds TestM11. The workflow step's -timeout keeps 10
	// minutes more for the cleanups that stop the VMs.
	m11Deadline = 50 * time.Minute
	m11User     = "m11user"
	// m11SSHAddr is sshd on a VM's loopback, dialled from inside the VM.
	m11SSHAddr = "127.0.0.1:22"
	// m11CloudInit is the drop-in cloud images write; it sorts after
	// 00-darkpanel.conf, whose values must win.
	m11CloudInit = "/etc/ssh/sshd_config.d/50-cloud-init.conf"
	// m11RecidiveAddr is a documentation address the recidive jail bans.
	m11RecidiveAddr = "192.0.2.77"
	// m11Idle is how long an open terminal stays idle and still works.
	m11Idle         = 10 * time.Minute
	m11UploadPath   = "/var/tmp/m11-upload.bin"
	m11UploadSize   = 512 << 20
	m11TransferWait = 20 * time.Minute
	m11JobSlug      = "m11-minutely"
	m11JobOut       = "/var/tmp/m11-job.out"
	m11JobExit      = 3
	m11SwapFile     = "/darkpanel.swap"
)

// m11Modules are the modules M11 adds; the footprint is measured with all
// of them enabled.
var m11Modules = []string{"services", "users", "fail2ban", "console", "jobs", "tuning"}

// m11Run is what the steps of TestM11 share.
type m11Run struct {
	ctx context.Context
	f   *fleet
	// key and password sign m11User in; the users step sets them.
	key      ssh.Signer
	password string
}

// TestM11 is the M11 harness job: each host-administration module through the
// API on a real agent of -os, the terminal and file manager through the
// controller, directly and through a real NPM, the agent's footprint with
// every M11 module on, and the previous release's controller and agent
// against this build. The compat labs boot first, as the VM limit keeps one
// lab at a time. The workflow selects it with -run, -os, -m2 and -m11.
func TestM11(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m11Flag {
		t.Skip("no -os, -m2 or -m11 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m11Deadline)
	t.Cleanup(cancel)
	t.Run("compat", func(t *testing.T) { m11Compat(ctx, t) })
	f := m5dLab(ctx, t)
	r := &m11Run{ctx: ctx, f: f}
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"services", r.services},
		{"users", r.users},
		{"sshd", r.sshd},
		{"fail2ban", r.fail2ban},
		{"console", r.console},
		{"jobs", r.jobs},
		{"console-restart", r.consoleRestart},
		{"tuning", r.tuning},
		{"footprint", r.footprint},
	}
	for _, s := range steps {
		t.Run(s.name, s.run)
	}
}

// state runs cmd on v and returns its trimmed output whatever its exit
// status, for commands such as systemctl is-active that exit non-zero on
// the answer the step waits for.
func (r *m11Run) state(t *testing.T, v *vm.VM, cmd string) string {
	t.Helper()
	return strings.TrimSpace(m3Run(r.ctx, t, v, cmd+" || true"))
}

// enable enables module on the agent's host and waits until it is usable.
func (r *m11Run) enable(t *testing.T, module string) {
	t.Helper()
	f := r.f
	in := coreactions.ModuleInput{HostID: f.agentID, ModuleID: module}
	if _, err := f.api.Apply(r.ctx, "", "core.module.enable/v1", in, nil); err != nil {
		t.Fatal(err)
	}
	r.waitUsable(t, module)
}

// waitUsable waits until module is enabled and available on the agent's host.
func (r *m11Run) waitUsable(t *testing.T, module string) {
	t.Helper()
	err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
		hosts, err := r.f.api.Hosts(ctx)
		if err != nil {
			return err
		}
		return m11xUsable(hosts, r.f.agentID, module)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// requireUser fails a step that needs the account the users step creates.
func (r *m11Run) requireUser(t *testing.T) {
	t.Helper()
	if r.key == nil {
		t.Fatalf("the users step did not create %s", m11User)
	}
}

// services stops and starts cron.service through the API while following its
// journal, which shows systemd's stop line, and plans a start of
// systemd-storagetm.service, which powers the host into storage mode, where
// the OS ships it: the module refuses it as protected.
func (r *m11Run) services(t *testing.T) {
	f := r.f
	// Some cloud images ship no cron; it is installed for the step.
	err := eventually(r.ctx, 3*time.Minute, func(ctx context.Context) error {
		out, err := f.agent.Run(ctx, "dpkg -s cron >/dev/null 2>&1 || sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q cron")
		if err != nil {
			return fmt.Errorf("%w\n%s", err, out)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("install cron: %v", err)
	}
	if st := r.state(t, f.agent, "systemctl is-active cron.service"); st != "active" {
		if _, err := f.api.Apply(r.ctx, f.agentID, "services.unit.start/v1", m11svc.UnitInput{Unit: "cron.service"}, nil); err != nil {
			t.Fatalf("cron.service is %q; services.unit.start: %v", st, err)
		}
	}
	since := vmNow(r.ctx, t, f.agent) * 1_000_000
	fctx, cancel := context.WithTimeout(r.ctx, 3*time.Minute)
	defer cancel()
	type followed struct {
		e   m11svc.Entry
		err error
	}
	ready, got := make(chan struct{}), make(chan followed, 1)
	go func() {
		e, err := m11Follow(fctx, f.api, f.agentID, "cron.service", func() { close(ready) }, func(e m11svc.Entry) bool {
			return e.Time >= since && m11xStopLine(e)
		})
		got <- followed{e, err}
	}()
	select {
	case <-ready:
	case g := <-got:
		t.Fatalf("services.journal-follow of cron.service: %v", g.err)
	}
	if _, err := f.api.Apply(r.ctx, f.agentID, "services.unit.stop/v1", m11svc.UnitInput{Unit: "cron.service"}, nil); err != nil {
		t.Fatal(err)
	}
	if g := <-got; g.err != nil {
		t.Errorf("the journal showed no stop line of cron.service: %v", g.err)
	} else {
		t.Logf("journal: %s", g.e.Message)
	}
	if st := r.state(t, f.agent, "systemctl is-active cron.service"); st != "inactive" {
		t.Errorf("cron.service is %q after services.unit.stop, want inactive", st)
	}
	if _, err := f.api.Apply(r.ctx, f.agentID, "services.unit.start/v1", m11svc.UnitInput{Unit: "cron.service"}, nil); err != nil {
		t.Fatal(err)
	}
	if st := r.state(t, f.agent, "systemctl is-active cron.service"); st != "active" {
		t.Errorf("cron.service is %q after services.unit.start, want active", st)
	}

	const storagetm = "systemd-storagetm.service"
	if r.state(t, f.agent, "systemctl cat "+storagetm+" >/dev/null 2>&1 && echo present") != "present" {
		t.Logf("%s is not shipped on %s", storagetm, f.os.ID)
		return
	}
	_, err = m4Plan(r.ctx, f.api, f.agentID, "services.unit.start", m11svc.UnitInput{Unit: storagetm})
	if err := m11xRefused(err, string(errcode.ServicesProtected)); err != nil {
		t.Errorf("start of %s: %v", storagetm, err)
	}
}

// users creates m11User with a password and a key, after a step-up; the key
// signs in over SSH, a lock stops it and an unlock lets it in again, and the
// stored password hash is yescrypt.
func (r *m11Run) users(t *testing.T) {
	f := r.f
	signer, line, err := m11NewKey("m11")
	if err != nil {
		t.Fatal(err)
	}
	password := rand.Text()
	m11StepUp(r.ctx, t, f, f.api)
	in := users.CreateInput{Name: m11User, Password: password, Keys: []string{line}}
	if _, err := f.api.Apply(r.ctx, f.agentID, "users.user.create/v1", in, nil); err != nil {
		t.Fatal(err)
	}
	r.key, r.password = signer, password
	key := ssh.PublicKeys(signer)
	if err := r.signIn(key); err != nil {
		t.Fatalf("sign-in with %s's key: %v", m11User, err)
	}
	hash := r.state(t, f.agent, "sudo getent shadow "+m11User+" | cut -d: -f2")
	if !strings.HasPrefix(hash, "$y$") {
		t.Errorf("the stored hash of %s starts %.4q, want $y$ (yescrypt)", m11User, hash)
	}
	if _, err := f.api.Apply(r.ctx, f.agentID, "users.user.lock/v1", users.GuardedNameInput{Name: m11User}, nil); err != nil {
		t.Fatal(err)
	}
	if who, err := m11SSH(r.ctx, f.agent, m11SSHAddr, m11User, key); err == nil {
		t.Errorf("the locked %s signed in with its key as %q", m11User, who)
	}
	if _, err := f.api.Apply(r.ctx, f.agentID, "users.user.unlock/v1", users.NameInput{Name: m11User}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.signIn(key); err != nil {
		t.Errorf("sign-in with %s's key after the unlock: %v", m11User, err)
	}
}

// signIn signs m11User in on the agent's host with auth, retrying while
// sshd restarts after a reload.
func (r *m11Run) signIn(auth ssh.AuthMethod) error {
	return eventually(r.ctx, 30*time.Second, func(ctx context.Context) error {
		who, err := m11SSH(ctx, r.f.agent, m11SSHAddr, m11User, auth)
		if err == nil && who != m11User {
			return fmt.Errorf("harness: signed in as %q, want %s", who, m11User)
		}
		return err
	})
}

// sshdValue is the value sshd -T reports for key, in lower case.
func (r *m11Run) sshdValue(t *testing.T, key string) string {
	t.Helper()
	return r.state(t, r.f.agent, "sudo sshd -T | grep -i '^"+key+" ' | cut -d' ' -f2-")
}

// sshd writes a 50-cloud-init.conf that allows passwords, as a cloud image
// can, and checks a password signs in; users.sshd.apply then turns password
// sign-in off and is confirmed: sshd -T reports it off over that file, a
// password is refused and the key still signs in. An unconfirmed apply
// reverts at its deadline, and again across an agent restart.
func (r *m11Run) sshd(t *testing.T) {
	r.requireUser(t)
	f := r.f
	m3Run(r.ctx, t, f.agent, "printf 'PasswordAuthentication yes\\n' | sudo tee "+m11CloudInit+" >/dev/null && sudo systemctl try-reload-or-restart ssh.service")
	pw, key := ssh.Password(r.password), ssh.PublicKeys(r.key)
	if err := r.signIn(pw); err != nil {
		t.Fatalf("a password sign-in with %s allowing passwords: %v", m11CloudInit, err)
	}

	off := false
	ch, err := f.api.m3Apply(r.ctx, f.agentID, "users.sshd.apply", users.SshdInput{SshdSettings: users.SshdSettings{PasswordAuthentication: &off}}, 300)
	m3RequirePending(t, ch, err)
	if err := f.api.do(r.ctx, http.MethodPost, "/api/v1/changes/"+url.PathEscape(ch.ID)+"/confirm", nil, nil); err != nil {
		t.Fatalf("confirm %s: %v", ch.ID, err)
	}
	m3WaitChange(r.ctx, t, f.api, ch.ID, core.StateConfirmed, time.Minute)
	if v := r.sshdValue(t, "passwordauthentication"); v != "no" {
		t.Errorf("sshd -T reports passwordauthentication %q under %s, want no", v, m11CloudInit)
	}
	if who, err := m11SSH(r.ctx, f.agent, m11SSHAddr, m11User, pw); err == nil {
		t.Errorf("a password signed %s in as %q after PasswordAuthentication no", m11User, who)
	}
	if err := r.signIn(key); err != nil {
		t.Errorf("the key sign-in after PasswordAuthentication no: %v", err)
	}
	before := r.sshdValue(t, "maxauthtries")

	for _, c := range []struct {
		name    string
		tries   int
		timeout int
		restart bool
	}{
		{"deadline", 4, 30, false},
		{"agent-restart", 3, 90, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := users.SshdInput{SshdSettings: users.SshdSettings{PasswordAuthentication: &off, MaxAuthTries: &c.tries}}
			ch, err := f.api.m3Apply(r.ctx, f.agentID, "users.sshd.apply", in, c.timeout)
			m3RequirePending(t, ch, err)
			if v := r.sshdValue(t, "maxauthtries"); v != fmt.Sprint(c.tries) {
				t.Errorf("sshd -T reports maxauthtries %q while the change is pending, want %d", v, c.tries)
			}
			if c.restart {
				m3Run(r.ctx, t, f.agent, "sudo systemctl restart "+update.AgentUnit)
			}
			m3WaitChange(r.ctx, t, f.api, ch.ID, core.StateReverted, time.Duration(c.timeout)*time.Second+3*time.Minute)
			if v := r.sshdValue(t, "maxauthtries"); v != before {
				t.Errorf("sshd -T reports maxauthtries %q after the revert, want %q", v, before)
			}
			if v := r.sshdValue(t, "passwordauthentication"); v != "no" {
				t.Errorf("sshd -T reports passwordauthentication %q after the revert, want the confirmed no", v)
			}
		})
	}
}

// fail2ban installs fail2ban; the scanner then fails SSH sign-ins until its
// address is banned and port 22 is closed to it, while the firewall stays
// supported and counts the ban. With recidive on, a recidive ban keeps the
// firewall supported, and the unban opens port 22 to the scanner again.
func (r *m11Run) fail2ban(t *testing.T) {
	f := r.f
	m4ApplyAsync(r.ctx, t, f.api, f.agentID, "fail2ban.package.install", struct{}{})
	var st fail2ban.Status
	err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
		if err := f.api.Read(ctx, f.agentID, "fail2ban.status.get/v1", struct{}{}, &st); err != nil {
			return err
		}
		return m11xJailRunning(st, "sshd")
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Settings == nil {
		t.Fatal("fail2ban.status.get reports no settings after the install")
	}

	addr := net.JoinHostPort(agentIPv4, "22")
	err = eventually(r.ctx, 3*time.Minute, func(ctx context.Context) error {
		// Every connection signs in as an unknown user, which sshd logs as a
		// failure from the scanner's address.
		_, _ = m11SSH(ctx, f.scanner, addr, "m11-nobody", ssh.Password("m11-wrong"))
		var bans fail2ban.BanList
		if err := f.api.Read(ctx, f.agentID, "fail2ban.ban.list/v1", fail2ban.BanListInput{Jail: "sshd"}, &bans); err != nil {
			return err
		}
		return m11xHasBan(bans, "sshd", scannerIPv4)
	})
	if err != nil {
		t.Fatalf("the scanner's failed sign-ins: %v", err)
	}
	if err := r.reach(addr, false); err != nil {
		t.Error(err)
	}
	r.firewallBanned(t, 1)

	s := *st.Settings
	s.Recidive = true
	if _, err := f.api.Apply(r.ctx, f.agentID, "fail2ban.settings.apply/v1", fail2ban.SettingsInput{Settings: s}, nil); err != nil {
		t.Fatal(err)
	}
	recidive := fail2ban.BanInput{Jail: "recidive", Address: m11RecidiveAddr}
	if _, err := f.api.Apply(r.ctx, f.agentID, "fail2ban.ban.add/v1", recidive, nil); err != nil {
		t.Fatal(err)
	}
	r.firewallBanned(t, 2)
	if _, err := f.api.Apply(r.ctx, f.agentID, "fail2ban.ban.remove/v1", recidive, nil); err != nil {
		t.Error(err)
	}
	sshd := fail2ban.BanInput{Jail: "sshd", Address: scannerIPv4}
	if _, err := f.api.Apply(r.ctx, f.agentID, "fail2ban.ban.remove/v1", sshd, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.reach(addr, true); err != nil {
		t.Error(err)
	}
}

// reach waits until a connection from the scanner to addr opens, or fails
// when open is false.
func (r *m11Run) reach(addr string, open bool) error {
	return eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		c, err := r.f.scanner.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
		}
		switch {
		case open && err != nil:
			return fmt.Errorf("harness: the scanner cannot reach %s: %w", addr, err)
		case !open && err == nil:
			return fmt.Errorf("harness: the scanner still reaches %s", addr)
		}
		return nil
	})
}

// firewallBanned waits until the agent's firewall is supported and counts
// banned fail2ban addresses.
func (r *m11Run) firewallBanned(t *testing.T, banned int) {
	t.Helper()
	err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		var st m11Firewall
		if err := r.f.api.Resource(ctx, r.f.agentID, "firewall.status", &st); err != nil {
			return err
		}
		return m11xBanned(st, banned)
	})
	if err != nil {
		t.Error(err)
	}
}

// m11xJailRunning accepts a status of an installed, active fail2ban running jail.
func m11xJailRunning(st fail2ban.Status, jail string) error {
	if !st.Installed || st.Service != "active" {
		return fmt.Errorf("harness: fail2ban installed %t, service %q", st.Installed, st.Service)
	}
	for _, j := range st.Jails {
		if j.Name == jail {
			return nil
		}
	}
	return fmt.Errorf("harness: fail2ban runs no %s jail: %+v", jail, st.Jails)
}

// m11xHasBan accepts a ban list holding address in jail.
func m11xHasBan(l fail2ban.BanList, jail, address string) error {
	for _, b := range l.Bans {
		if b.Jail == jail && b.Address == address {
			return nil
		}
	}
	return fmt.Errorf("harness: no ban of %s in %s yet: %+v", address, jail, l.Bans)
}

// console enables the console on the agent's host and opens a terminal
// through the controller with a TOTP code, where echo m11 prints m11. Two
// terminals, one direct and one through an NPM-proxied listener, then stay
// idle for m11Idle and still answer, while a 512 MiB upload and download go
// through byte-identical. With the policy file denying the console the
// terminal is refused; the terminal and file entries reach the audit log.
func (r *m11Run) console(t *testing.T) {
	f := r.f
	r.enable(t, "console")
	if !t.Run("terminal", func(t *testing.T) {
		tm, err := m11OpenTerm(r.ctx, t, f, f.api, f.agentID)
		if err != nil {
			t.Fatal(err)
		}
		defer tm.close()
		if _, err := tm.expect(r.ctx, "echo m11", m11EchoLine); err != nil {
			t.Fatal(err)
		}
	}) {
		return
	}
	var x *API
	t.Run("npm", func(t *testing.T) { x = r.npm(t) })

	idle := map[string]*m11Term{}
	direct, err := m11OpenTerm(r.ctx, t, f, f.api, f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	idle["direct"] = direct
	if x != nil {
		proxied, err := m11OpenTerm(r.ctx, t, f, x, f.agentID)
		if err != nil {
			t.Errorf("the terminal through NPM: %v", err)
		} else {
			idle["through NPM"] = proxied
		}
	}
	opened := time.Now()
	t.Run("transfer", r.transfer)
	t.Run("idle", func(t *testing.T) {
		if d := time.Until(opened.Add(m11Idle)); d > 0 {
			t.Logf("the terminals stay idle %s more", d.Round(time.Second))
			select {
			case <-r.ctx.Done():
				t.Fatal(r.ctx.Err())
			case <-time.After(d):
			}
		}
		for name, tm := range idle {
			if err := tm.closed(); err != nil {
				t.Errorf("%s: the terminal closed while idle: %s", name, m11CloseReason(err))
				continue
			}
			if _, err := tm.expect(r.ctx, "echo m11", m11EchoLine); err != nil {
				t.Errorf("%s, after %s idle: %v", name, m11Idle, err)
			}
		}
	})
	for _, tm := range idle {
		tm.close()
	}
	t.Run("policy", r.policy)
	t.Run("audit", r.consoleAudit)
}

// npm deploys NPM on the controller's host and exposes the panel through
// it, as TestM6Panel does, and returns the admin's session through the
// exposure, or nil when a step failed.
func (r *m11Run) npm(t *testing.T) *API {
	f := r.f
	m3Run(r.ctx, t, f.controller, m6pAgentPortCommand())
	p := &m6pRun{ctx: r.ctx, f: f, n: &m6nRun{ctx: r.ctx, f: m6pController(f)}}
	for _, s := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{"prepare", p.n.prepare},
		{"deploy", p.n.deploy},
		{"harness-settings", p.harnessSettings},
		{"public", p.public},
		{"expose", p.expose},
		{"login", p.login},
	} {
		if !t.Run(s.name, s.run) {
			p.logDiagnostics(t)
			return nil
		}
	}
	return p.x
}

// transfer uploads m11UploadSize random bytes to the agent's host after a
// step-up, and downloads them again: the agent's hash, the file on the host
// and the download all match what was sent.
func (r *m11Run) transfer(t *testing.T) {
	f := r.f
	ctx, cancel := context.WithTimeout(r.ctx, m11TransferWait)
	defer cancel()
	m11StepUp(ctx, t, f, f.api)
	var seed [32]byte
	_, _ = rand.Read(seed[:])
	start := time.Now()
	w, sent, err := m11Upload(ctx, f.api, f.agentID, m11UploadPath, m11UploadSize, seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("uploaded %d bytes in %s", w.Size, time.Since(start).Round(time.Second))
	defer m3Run(r.ctx, t, f.agent, "sudo rm -f "+m11UploadPath)
	if w.Size != m11UploadSize || w.SHA256 != sent {
		t.Errorf("the agent wrote %d bytes with SHA-256 %s, want %d with %s", w.Size, w.SHA256, m11UploadSize, sent)
	}
	if on, _, _ := strings.Cut(r.state(t, f.agent, "sudo sha256sum "+m11UploadPath), " "); on != sent {
		t.Errorf("%s on the host has SHA-256 %q, want %s", m11UploadPath, on, sent)
	}
	start = time.Now()
	n, got, err := m11Download(ctx, f.api, f.agentID, m11UploadPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("downloaded %d bytes in %s", n, time.Since(start).Round(time.Second))
	if n != m11UploadSize || got != sent {
		t.Errorf("the download is %d bytes with SHA-256 %s, want %d with %s", n, got, m11UploadSize, sent)
	}
}

// policy denies console and jobs in the agent's policy file: the controller
// lists both unavailable with the policy's reason within 15 s and refuses
// the terminal with it. Allowing them lists both available within 15 s, and
// the agent never restarted.
func (r *m11Run) policy(t *testing.T) {
	f := r.f
	pid, err := fpMainPID(r.ctx, f.agent, update.AgentUnit)
	if err != nil {
		t.Fatal(err)
	}
	m3Run(r.ctx, t, f.agent, "sudo darkpanel agent policy deny console jobs")
	defer func() {
		m3Run(r.ctx, t, f.agent, "sudo darkpanel agent policy allow console jobs")
		r.waitUsable(t, "console")
	}()
	if err := r.policyListed(true); err != nil {
		t.Error(err)
	}
	tm, err := m11OpenTerm(r.ctx, t, f, f.api, f.agentID)
	if err == nil {
		tm.close()
	}
	if err := m11xRefusedWith(err, string(errcode.ModuleUnavailable), agentruntime.PolicyReason); err != nil {
		t.Errorf("a terminal with the console denied: %v", err)
	}
	m3Run(r.ctx, t, f.agent, "sudo darkpanel agent policy allow console jobs")
	if err := r.policyListed(false); err != nil {
		t.Error(err)
	}
	if got, err := fpMainPID(r.ctx, f.agent, update.AgentUnit); err != nil || got != pid {
		t.Errorf("the agent's main pid is %d (%v) after the reloads, want %d: the controller must learn of them without a restart", got, err, pid)
	}
}

// policyListed waits up to 15 s, the audit ingestion and the recheck it
// starts, until the controller lists console and jobs unavailable with the
// policy's reason when denied is set, or both available when it is not.
func (r *m11Run) policyListed(denied bool) error {
	return eventually(r.ctx, 15*time.Second, func(ctx context.Context) error {
		hosts, err := r.f.api.Hosts(ctx)
		if err != nil {
			return err
		}
		for _, id := range []string{"console", "jobs"} {
			m, err := m11xModule(hosts, r.f.agentID, id)
			if err != nil {
				return err
			}
			reason := ""
			if m.Reason != nil {
				reason = *m.Reason
			}
			if denied && (m.Available || reason != agentruntime.PolicyReason) {
				return fmt.Errorf("harness: %s is listed available %t (%s) after the deny, want unavailable: %s", id, m.Available, reason, agentruntime.PolicyReason)
			}
			if !denied && !m.Available {
				return fmt.Errorf("harness: %s is listed unavailable (%s) after the allow", id, reason)
			}
		}
		return nil
	})
}

// consoleAudit waits until the controller's audit log holds the agent's
// terminal and upload entries with their phases.
func (r *m11Run) consoleAudit(t *testing.T) {
	f := r.f
	want := []struct {
		action string
		phases []string
	}{
		{"console.terminal.open", []string{"open"}},
		{"console.terminal.close", []string{"close"}},
		{"console.file.upload", []string{"intent", "result"}},
	}
	for _, w := range want {
		err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
			var l coreactions.AuditList
			in := coreactions.AuditListInput{HostID: f.agentID, Action: w.action, Limit: 100}
			if err := f.api.Read(ctx, "", "core.audit.list/v1", in, &l); err != nil {
				return err
			}
			return m11xAudited(l.Entries, w.action, w.phases...)
		})
		if err != nil {
			t.Error(err)
		}
	}
}

// jobs enables jobs and adds, after a step-up, a job that runs every minute
// as m11User and exits m11JobExit: it runs as that user and the list shows
// its exit status. Turning jobs off stops its timer; the job is then
// deleted with jobs on again.
func (r *m11Run) jobs(t *testing.T) {
	r.requireUser(t)
	f := r.f
	r.enable(t, "jobs")
	m11StepUp(r.ctx, t, f, f.api)
	in := jobs.JobInput{Slug: m11JobSlug, Schedule: "minutely", User: m11User,
		Command: fmt.Sprintf("id -un > %s; exit %d", m11JobOut, m11JobExit), TimeoutSeconds: 60}
	if _, err := f.api.Apply(r.ctx, f.agentID, "jobs.job.create/v1", in, nil); err != nil {
		t.Fatal(err)
	}
	err := eventually(r.ctx, 3*time.Minute, func(ctx context.Context) error {
		var l jobs.JobList
		if err := f.api.Read(ctx, f.agentID, "jobs.job.list/v1", jobs.ListInput{}, &l); err != nil {
			return err
		}
		return m11xJobRan(l.Jobs, m11JobSlug, m11JobExit)
	})
	if err != nil {
		t.Fatal(err)
	}
	if who := r.state(t, f.agent, "cat "+m11JobOut); who != m11User {
		t.Errorf("the job ran as %q, want %s", who, m11User)
	}
	r.jobScripts(t)
	timer := "darkpanel-job-" + m11JobSlug + ".timer"
	off := coreactions.ModuleInput{HostID: f.agentID, ModuleID: "jobs"}
	if _, err := f.api.Apply(r.ctx, "", "core.module.disable/v1", off, nil); err != nil {
		t.Fatal(err)
	}
	err = eventually(r.ctx, time.Minute, func(context.Context) error {
		if st := r.state(t, f.agent, "systemctl is-active "+timer); st != "inactive" {
			return fmt.Errorf("harness: %s is %q with jobs off, want inactive", timer, st)
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	r.enable(t, "jobs")
	if _, err := f.api.Apply(r.ctx, f.agentID, "jobs.job.delete/v1", jobs.SlugInput{Slug: m11JobSlug}, nil); err != nil {
		t.Error(err)
	}
}

// m11Peer shares m11User's primary group; it must not read a job's script.
const m11Peer = "m11peer"

// jobScripts checks that a job's script is root-only and that systemd hands
// it to the job: the file and its directory are 0700 root:root, a member of
// the run-as user's group reads neither the file nor the copy the running
// job sees under $CREDENTIALS_DIRECTORY, and standard input is empty.
func (r *m11Run) jobScripts(t *testing.T) {
	f := r.f
	script := "/var/lib/darkpanel/jobs/" + m11JobSlug + ".sh"
	if got := r.state(t, f.agent, "sudo stat -c '%a %U:%G' "+script+" /var/lib/darkpanel/jobs"); got != "700 root:root\n700 root:root" {
		t.Errorf("the script and its directory are %q, want 700 root:root both", got)
	}
	m3Run(r.ctx, t, f.agent, "sudo useradd -M -N -g \"$(id -gn "+m11User+")\" -s /bin/sh "+m11Peer)
	defer func() { _, _ = f.agent.Run(r.ctx, "sudo userdel "+m11Peer) }()
	denied := func(path string) {
		t.Helper()
		out := r.state(t, f.agent, "sudo runuser -u "+m11Peer+" -- cat "+path+" 2>&1; echo rc=$?")
		if strings.HasSuffix(out, "rc=0") || !strings.Contains(out, "Permission denied") {
			t.Errorf("%s reading %s: %q, want Permission denied", m11Peer, path, out)
		}
	}
	denied(script)

	journal := func(slug string, want ...string) []string {
		t.Helper()
		unit := "darkpanel-job-" + slug + ".service"
		var lines []string
		err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
			out, err := f.agent.Run(ctx, "sudo journalctl -u "+unit+" -o cat --no-pager")
			if err != nil {
				return err
			}
			lines = strings.Split(strings.TrimSpace(out), "\n")
			for _, w := range want {
				if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, w) }) {
					return fmt.Errorf("harness: the journal of %s has no line starting %q yet:\n%s", unit, w, out)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return lines
	}
	run := func(slug, command string) {
		t.Helper()
		in := jobs.JobInput{Slug: slug, Schedule: "*-01-01 00:00:00", User: m11User, Command: command, TimeoutSeconds: 120}
		if _, err := f.api.Apply(r.ctx, f.agentID, "jobs.job.create/v1", in, nil); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = f.api.Apply(r.ctx, f.agentID, "jobs.job.delete/v1", jobs.SlugInput{Slug: slug}, nil) })
		if _, err := f.api.Apply(r.ctx, f.agentID, "jobs.job.run/v1", jobs.SlugInput{Slug: slug}, nil); err != nil {
			t.Fatal(err)
		}
	}

	run("m11-creds", `id -un; echo "creds:$CREDENTIALS_DIRECTORY"; sleep 30`)
	creds := ""
	for _, l := range journal("m11-creds", m11User, "creds:/") {
		if c, ok := strings.CutPrefix(l, "creds:"); ok {
			creds = c
		}
	}
	denied(creds + "/script")

	run("m11-stdin", "read x; echo \"x=$x\"\necho done")
	lines := journal("m11-stdin", "done")
	if !slices.Contains(lines, "x=") {
		t.Errorf("the stdin job printed %q, want x= then done", lines)
	}
}

// m11PIDLine matches the line that prints the background sleep's PID, not
// the echoed command, which holds $! instead.
var m11PIDLine = regexp.MustCompile(`M11PID=(\d+)`)

// consoleRestart starts a background sleep in a terminal; it lives in a
// darkpanel-console-*.scope and survives an agent restart.
func (r *m11Run) consoleRestart(t *testing.T) {
	f := r.f
	tm, err := m11OpenTerm(r.ctx, t, f, f.api, f.agentID)
	if err != nil {
		t.Fatal(err)
	}
	defer tm.close()
	m, err := tm.expect(r.ctx, "nohup sleep 600 >/dev/null 2>&1 & echo M11PID=$!", m11PIDLine)
	if err != nil {
		t.Fatal(err)
	}
	pid := m[1]
	defer func() { _, _ = f.agent.Run(r.ctx, "sudo kill "+pid) }()
	if err := m11xCgroupScope(r.state(t, f.agent, "cat /proc/"+pid+"/cgroup")); err != nil {
		t.Fatal(err)
	}
	m3Run(r.ctx, t, f.agent, "sudo systemctl restart "+update.AgentUnit)
	waitHosts(r.ctx, t, f, 2*time.Minute, hostsAt(versionN, f.agentID))
	if out := r.state(t, f.agent, "ps -o pid= -p "+pid); out != pid {
		t.Fatalf("the terminal's sleep %s is gone after the agent restart (ps: %q)", pid, out)
	}
	if err := m11xCgroupScope(r.state(t, f.agent, "cat /proc/"+pid+"/cgroup")); err != nil {
		t.Error(err)
	}
}

// tuning applies vm.swappiness, which takes effect live, and reverts it; it
// creates the swap file, which is on again after a reboot, and removes it.
func (r *m11Run) tuning(t *testing.T) {
	f := r.f
	before := r.state(t, f.agent, "cat /proc/sys/vm/swappiness")
	want := "33"
	if before == want {
		want = "34"
	}
	ch, err := f.api.m3Apply(r.ctx, f.agentID, "tuning.sysctl.apply", tuning.SysctlInput{Values: map[string]string{"vm.swappiness": want}}, 300)
	m3RequirePending(t, ch, err)
	if got := r.state(t, f.agent, "cat /proc/sys/vm/swappiness"); got != want {
		t.Errorf("vm.swappiness is %q after tuning.sysctl.apply, want %s", got, want)
	}
	if _, err := f.api.m3Revert(r.ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	m3WaitChange(r.ctx, t, f.api, ch.ID, core.StateReverted, 2*time.Minute)
	if got := r.state(t, f.agent, "cat /proc/sys/vm/swappiness"); got != before {
		t.Errorf("vm.swappiness is %q after the revert, want %s", got, before)
	}

	swapOn := func(t *testing.T) bool {
		t.Helper()
		return strings.Contains(r.state(t, f.agent, "cat /proc/swaps"), m11SwapFile)
	}
	if _, err := f.api.Apply(r.ctx, f.agentID, "tuning.swap.create/v1", tuning.SwapSizeInput{SizeMiB: 256}, nil); err != nil {
		t.Fatal(err)
	}
	if !swapOn(t) {
		t.Fatalf("%s is not on after tuning.swap.create", m11SwapFile)
	}
	if err := f.agent.Reboot(r.ctx); err != nil {
		t.Fatal(err)
	}
	waitHosts(r.ctx, t, f, 3*time.Minute, hostsAt(versionN, f.agentID))
	if !swapOn(t) {
		t.Errorf("%s is not on after a reboot", m11SwapFile)
	}
	if _, err := f.api.Apply(r.ctx, f.agentID, "tuning.swap.remove/v1", struct{}{}, nil); err != nil {
		t.Fatal(err)
	}
	if swapOn(t) {
		t.Errorf("%s is still on after tuning.swap.remove", m11SwapFile)
	}
	if st := r.state(t, f.agent, "test -e "+m11SwapFile+" && echo present"); st != "" {
		t.Errorf("%s is %s after tuning.swap.remove", m11SwapFile, st)
	}
}

// footprint samples the agent's resident memory with every M11 module
// enabled and idle, on TestFootprint's plan: every fpSampleEvery for
// fpWindow after fpWarmup. The largest sample is at most fpAgentMax.
func (r *m11Run) footprint(t *testing.T) {
	f := r.f
	hosts, err := f.api.Hosts(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range m11Modules {
		m, err := m11xModule(hosts, f.agentID, id)
		if err != nil {
			t.Fatal(err)
		}
		if !m.Enabled {
			t.Fatalf("module %s is not enabled on the agent's host", id)
		}
	}
	select {
	case <-r.ctx.Done():
		t.Fatal(r.ctx.Err())
	case <-time.After(fpWarmup):
	}
	pid, err := fpMainPID(r.ctx, f.agent, update.AgentUnit)
	if err != nil {
		t.Fatal(err)
	}
	want := fpSampleCount(fpWindow, fpSampleEvery)
	samples := make([]int64, 0, want)
	tick := time.NewTicker(fpSampleEvery)
	defer tick.Stop()
	for range want {
		rss, err := fpRSS(r.ctx, f.agent, pid)
		if err != nil {
			t.Fatal(err)
		}
		samples = append(samples, rss)
		select {
		case <-r.ctx.Done():
			t.Fatal(r.ctx.Err())
		case <-tick.C:
		}
	}
	if err := fpCheckBudget("agent", samples, fpAgentMax, want); err != nil {
		t.Error(err)
	}
	t.Logf("agent RSS: %d samples, largest %d bytes", len(samples), slices.Max(samples))
}

// m11Compat boots a lab per direction with the previous release, the newest
// entry of upgrade_sources.txt: an agent of it under this controller stays
// online and runs a firewall read, and its terminal is unserved; this agent
// under its controller stays online outside the support window, which
// refuses the read (amendment A37).
func m11Compat(ctx context.Context, t *testing.T) {
	sources, err := upgLoadSources(upgSourcesFile)
	if err != nil {
		t.Fatal(err)
	}
	prev := sources[len(sources)-1].Version
	t.Run("new-controller", func(t *testing.T) {
		f := m11CompatLab(ctx, t, prev, upgUnderTest, prev)
		var st m11Firewall
		if err := f.api.Resource(ctx, f.agentID, "firewall.status", &st); err != nil {
			t.Errorf("firewall.status of the %s agent: %v", prev, err)
		}
		tm, err := m11OpenTerm(ctx, t, f, f.api, f.agentID)
		if err == nil {
			tm.close()
		}
		if err := m11xRefusedWith(err, string(errcode.ModuleUnavailable), dispatch.ReasonChannelUnserved); err != nil {
			t.Errorf("a terminal on the %s agent: %v", prev, err)
		}
	})
	t.Run("old-controller", func(t *testing.T) {
		f := m11CompatLab(ctx, t, prev, prev, upgUnderTest)
		hosts, err := f.api.Hosts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if h, ok := findHost(hosts, f.agentID); !ok || !h.Online || h.Supported {
			t.Errorf("the %s agent under the %s controller: listed %t, online %t, supported %t; want online and unsupported",
				upgUnderTest, prev, ok, h.Online, h.Supported)
		}
		var st m11Firewall
		err = f.api.Resource(ctx, f.agentID, "firewall.status", &st)
		if err := m11xRefused(err, string(errcode.HostVersionUnsupported)); err != nil {
			t.Errorf("firewall.status of the %s agent under the %s controller: %v", upgUnderTest, prev, err)
		}
	})
}

// m11CompatLab boots a lab of its own, serves the previous release prev and
// the build under test, and installs the controller at cv and the agent at
// av, as upgInstall does at one version.
func m11CompatLab(ctx context.Context, t *testing.T, prev, cv, av string) *fleet {
	t.Helper()
	lab := NewLab(t)
	o, targetImg, scannerImg := harnessImages(ctx, t)
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	vms := lab.Boot(ctx, t, m5Configs(lab, o, targetImg, scannerImg, key))
	f := &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
	var versions []release.Version
	for _, v := range []string{prev, upgUnderTest} {
		versions = append(versions, release.Version{Version: v, Channel: releaseChannel, Binary: filepath.Join(*m2Flag, "darkpanel-"+v)})
	}
	upgServeReleases(ctx, t, f, versions)
	for _, v := range []*vm.VM{f.controller, f.agent} {
		if err := v.CopyFile(ctx, filepath.Join(*m2Flag, "install.sh"), installScript, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installRole(ctx, t, f.controller, cv, "--role controller --agent-listen "+agentAddress)
	createAdmin(ctx, t, f)
	login(ctx, t, f)
	hosts := waitHosts(ctx, t, f, 2*time.Minute, func(hosts []core.Host) error {
		h, ok := localHost(hosts)
		if !ok {
			return errors.New("no local host listed")
		}
		return hostsAt(cv, h.ID)(hosts)
	})
	local, _ := localHost(hosts)
	f.localID = local.ID
	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent", ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	f.agentID = grant.Host.ID
	writeToken(ctx, t, f.agent, grant.Token)
	installRole(ctx, t, f.agent, av, "--role agent --token-file "+enrollFile)
	removeToken(ctx, t, f.agent)
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(av, f.agentID))
	return f
}
