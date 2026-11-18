package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// libCall runs one function of the script under sh with
// DARKPANEL_INSTALL_SH_LIB=1 and returns its standard output.
func libCall(t *testing.T, scriptPath string, env []string, call string, args ...string) string {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", `. "$1"; shift; ` + call, "sh", scriptPath}, args...)...) //nolint:gosec // G204: a fixed call over test files
	cmd.Env = append(append(os.Environ(), "DARKPANEL_INSTALL_SH_LIB=1"), env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v", call, err)
	}
	return string(out)
}

func TestVersionChoices(t *testing.T) {
	needTools(t, "sh", "sort")
	p := script(t, []trustedkeys.Key{newSigner(t).key}, "https://example.com")
	tests := []struct {
		name, index, want string
	}{
		{"a newer pre-release follows the stable default",
			`{"formatVersion":1,"releases":[{"version":"1.11.0-beta.1","channel":"beta"},{"version":"1.2.0","channel":"stable"},{"version":"1.10.0","channel":"stable"}]}`,
			"1.10.0 stable\n1.11.0-beta.1 pre-release\n"},
		{"no stable release: the newest pre-release",
			`{"formatVersion":1,"releases":[{"version":"1.0.0-beta.1","channel":"beta"},{"version":"1.0.0-rc.1","channel":"beta"}]}`,
			"1.0.0-rc.1 pre-release\n"},
		{"a pre-release of a released version is left out",
			`{"formatVersion":1,"releases":[{"version":"1.0.0-rc.1","channel":"beta"},{"version":"1.0.0","channel":"stable"}]}`,
			"1.0.0 stable\n"},
		{"release candidates compare numerically",
			"{\"formatVersion\": 1, \"releases\": [\n {\"version\": \"2.0.0-rc.2\", \"channel\": \"beta\"},\n {\"version\": \"2.0.0-rc.10\", \"channel\": \"beta\"},\n {\"version\": \"1.9.0\", \"channel\": \"stable\"}\n]}\n",
			"1.9.0 stable\n2.0.0-rc.10 pre-release\n"},
		{"empty list", `{"formatVersion":1,"releases":[]}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := filepath.Join(t.TempDir(), "releases.json")
			writeFile(t, index, tt.index)
			if got := libCall(t, p, nil, `version_choices "$1"`, index); got != tt.want {
				t.Errorf("version_choices = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeTools writes executable scripts named after the keys of tools into a new
// directory and returns a PATH that finds them first.
func fakeTools(t *testing.T, tools map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil { //nolint:gosec // G306: test tools must be executable
			t.Fatal(err)
		}
	}
	return "PATH=" + dir + ":" + os.Getenv("PATH")
}

const (
	fakeIP        = `[ "$*" = "-4 route get 1.1.1.1" ] && echo "1.1.1.1 via 10.0.0.1 dev eth0 src 203.0.113.5 uid 0"` + "\n"
	fakeTailscale = `[ "$*" = "ip -4" ] && echo 100.64.0.5` + "\n"
	noTool        = "exit 1\n"
)

func TestAddressChoices(t *testing.T) {
	needTools(t, "sh")
	p := script(t, []trustedkeys.Key{newSigner(t).key}, "https://example.com")
	tests := []struct {
		name          string
		ip, tailscale string
		want          string
	}{
		{"both", fakeIP, fakeTailscale, "tailscale 100.64.0.5\nroute 203.0.113.5\ncustom\nlater\n"},
		{"no tailscale", fakeIP, noTool, "route 203.0.113.5\ncustom\nlater\n"},
		{"no route", noTool, fakeTailscale, "tailscale 100.64.0.5\ncustom\nlater\n"},
		{"a non-address answer is ignored", `echo "src 203.0.113.5;rm"` + "\n", `echo "100.64.0.5 x"` + "\n", "custom\nlater\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := []string{fakeTools(t, map[string]string{"ip": tt.ip, "tailscale": tt.tailscale})}
			if got := libCall(t, p, env, "address_choices"); got != tt.want {
				t.Errorf("address_choices = %q, want %q", got, tt.want)
			}
		})
	}
}

// term is a process started on a new pseudo-terminal, which is its
// controlling terminal, as a user's terminal is under sudo.
type term struct {
	t      *testing.T
	master *os.File
	cmd    *exec.Cmd
	mu     sync.Mutex
	out    bytes.Buffer
	mark   int
	done   chan struct{}
}

func startTerm(t *testing.T, cmd *exec.Cmd) *term {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile("/dev/pts/"+itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	ws := &unix.Winsize{Row: 24, Col: 80}
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, ws); err != nil {
		t.Fatal(err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	tm := &term{t: t, master: master, cmd: cmd, done: make(chan struct{})}
	go func() {
		defer close(tm.done)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			tm.mu.Lock()
			tm.out.Write(buf[:n])
			tm.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return tm
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// expect waits until the output after the last match holds s.
func (tm *term) expect(s string) {
	tm.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		tm.mu.Lock()
		rest := tm.out.String()[tm.mark:]
		if i := strings.Index(rest, s); i >= 0 {
			tm.mark += i + len(s)
			tm.mu.Unlock()
			return
		}
		tm.mu.Unlock()
		select {
		case <-tm.done:
			tm.t.Fatalf("the script ended before printing %q; output:\n%s", s, tm.output())
		default:
		}
		if time.Now().After(deadline) {
			tm.t.Fatalf("no %q within 30 seconds; output:\n%s", s, tm.output())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (tm *term) send(line string) {
	tm.t.Helper()
	if _, err := tm.master.WriteString(line + "\r"); err != nil {
		tm.t.Fatal(err)
	}
}

func (tm *term) output() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.out.String()
}

// wait returns the exit error and the whole output once the script ends.
func (tm *term) wait() (string, error) {
	tm.t.Helper()
	err := tm.cmd.Wait()
	select {
	case <-tm.done:
	case <-time.After(10 * time.Second):
	}
	return tm.output(), err
}

// wizard starts install.sh with no arguments on a terminal against the
// fixture, with fake ip and tailscale commands, the fake tools of extra and
// env added.
func (f *fixture) wizard(t *testing.T, extra map[string]string, env ...string) *term {
	t.Helper()
	tools := map[string]string{"ip": fakeIP, "tailscale": fakeTailscale}
	for name, body := range extra {
		tools[name] = body
	}
	return startTerm(t, f.command(append([]string{fakeTools(t, tools)}, env...)))
}

func TestWizardInstallsAControllerWithPlainPrompts(t *testing.T) {
	f := newFixture(t)
	tm := f.wizard(t, nil, "TERM=dumb")
	tm.expect("What should this server be?")
	tm.expect("1) Controller")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("")
	tm.expect("Which version should be installed?")
	tm.expect("1) 1.10.0 - stable release")
	tm.expect("2) 1.11.0-beta.1 - pre-release")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("9")
	tm.expect("Enter a number from 1 to 3, or q to quit.")
	tm.send("1")
	tm.expect("How will agents reach this controller?")
	tm.expect("1) Tailscale 100.64.0.5:7443")
	tm.expect("2) This server's address 203.0.113.5:7443")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("3")
	tm.expect("Address agents connect to (host or host:port): ")
	tm.send("panel example")
	tm.expect("address panel example:7443 is not host:port")
	tm.expect("Address agents connect to (host or host:port): ")
	tm.send("panel.example.com")
	tm.expect("Address of this server to listen on (ip:port) [203.0.113.5:7443]: ")
	tm.send("")
	tm.expect("Open the web UI on a public address? It serves plain HTTP: passwords, sessions and the tokens and keys the panel shows cross the network unencrypted.")
	tm.expect("1) This server's address http://203.0.113.5:8080 (port 8080 must be open)")
	tm.expect("2) Another address of this server")
	tm.expect("3) No - reach it through an SSH tunnel or Tailscale")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("3")
	tm.expect("Web UI: SSH tunnel or Tailscale only")
	tm.expect("sudo sh install.sh --role controller --version 1.10.0 --agent-listen 203.0.113.5:7443 --agent-advertise panel.example.com:7443")
	tm.expect("Install now? [Y/n]: ")
	tm.send("y")
	out, err := tm.wait()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	want := "system\ninstall\n--role\ncontroller\n--agent-listen\n203.0.113.5:7443\n--agent-advertise\npanel.example.com:7443\n"
	if got := readFile(t, f.logBase+".args"); got != want {
		t.Errorf("darkpanel ran with %q, want %q", got, want)
	}
	for _, s := range []string{"Installed darkpanel 1.10.0", "sudo darkpanel user create --username admin", "ssh -L 8080:127.0.0.1:8080", "https://100.64.0.5:8443"} {
		if !strings.Contains(out, s) {
			t.Errorf("output does not mention %q:\n%s", s, out)
		}
	}
}

func TestWizardInstallsAnAgentWithPlainPrompts(t *testing.T) {
	f := newFixture(t)
	tm := f.wizard(t, nil, "TERM=dumb")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("2")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("")
	tm.expect("Enrollment token or bootstrap bundle: ")
	tm.send("")
	tm.expect("Enter the enrollment token or bootstrap bundle, or press Ctrl-C to quit.")
	tm.expect("Enrollment token or bootstrap bundle: ")
	tm.send("dpe1.secret-token")
	tm.expect("sudo sh install.sh --role agent --version 1.10.0 --token-file <file holding the token>")
	tm.expect("Install now? [Y/n]: ")
	tm.send("")
	out, err := tm.wait()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if got := readFile(t, f.logBase+".args"); got != "system\ninstall\n--role\nagent\n" {
		t.Errorf("darkpanel ran with %q, want system install --role agent", got)
	}
	if strings.Contains(out, "Open the web UI on a public address?") {
		t.Errorf("the agent path asked the web question:\n%s", out)
	}
	if got := readFile(t, f.logBase+".stdin"); got != "dpe1.secret-token\n" {
		t.Errorf("darkpanel read %q on standard input, want the token", got)
	}
	if strings.Contains(out, "dpe1.secret-token") {
		t.Errorf("the token was echoed:\n%s", out)
	}
	if !strings.Contains(out, "Hosts page") {
		t.Errorf("output does not say where the agent appears:\n%s", out)
	}
}

func TestWizardCancelChangesNothing(t *testing.T) {
	for _, tt := range []struct {
		name    string
		answers []string
	}{
		{"q at the first question", []string{"q"}},
		{"no at the summary", []string{"", "", "4", "3", "n"}},
		{"q at the web question", []string{"", "", "4", "q"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tm := f.wizard(t, nil, "TERM=dumb")
			for _, a := range tt.answers {
				tm.expect("]: ")
				tm.send(a)
			}
			out, err := tm.wait()
			if err == nil || !strings.Contains(out, "Installation cancelled; nothing was changed.") {
				t.Errorf("err %v, output:\n%s", err, out)
			}
			if got := f.tarballRequests.Load(); got != 0 {
				t.Errorf("a cancelled install fetched %d tarballs", got)
			}
			if _, err := os.Stat(f.bin()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a cancelled install installed the binary: %v", err)
			}
		})
	}
}

// fakeWhiptail is a whiptail that answers each call with the next line of
// answers, "<exit status> <value>", writing the value to standard error as
// whiptail does, and logs the arguments of each call as one line joined by "|".
func fakeWhiptail(t *testing.T, answers ...string) (tools map[string]string, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	writeFile(t, filepath.Join(dir, "answers"), strings.Join(answers, "\n")+"\n")
	body := `n=$(($(cat "` + dir + `/count" 2>/dev/null || echo 0) + 1))
echo "$n" >"` + dir + `/count"
(IFS='|'; printf '%s\n---\n' "$*") >>"` + log + `"
line=$(sed -n "${n}p" "` + dir + `/answers")
printf '%s' "${line#* }" >&2
exit "${line%% *}"
`
	return map[string]string{"whiptail": body}, log
}

func TestWizardUsesWhiptail(t *testing.T) {
	f := newFixture(t)
	tools, log := fakeWhiptail(t,
		"0 controller",
		"0 other",
		"0 1.10",
		"0 ",
		"0 1.10.0",
		"0 route",
		"0 no",
		"0 ",
	)
	tm := f.wizard(t, tools, "TERM=xterm")
	out, err := tm.wait()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	calls := strings.Split(strings.TrimSuffix(readFile(t, log), "\n---\n"), "\n---\n")
	want := []string{
		"--title|Darkpanel installer|--notags|--default-item|controller|--menu|What should this server be?",
		"--title|Darkpanel installer|--notags|--default-item|1.10.0|--menu|Which version should be installed?",
		"--title|Darkpanel installer|--inputbox|Version to install, for example 1.2.0 or 1.3.0-rc.1",
		"--title|Darkpanel installer|--msgbox|version 1.10 is not a semantic version",
		"--title|Darkpanel installer|--inputbox|Version to install, for example 1.2.0 or 1.3.0-rc.1",
		"--title|Darkpanel 1.10.0 installer|--notags|--default-item|tailscale|--menu|How will agents reach this controller?",
		"--title|Darkpanel 1.10.0 installer|--notags|--default-item|route|--menu|Open the web UI on a public address?",
		"--title|Darkpanel 1.10.0 installer|--yesno|",
	}
	if len(calls) != len(want) {
		t.Fatalf("whiptail calls:\n%s\nwant %d calls", strings.Join(calls, "\n"), len(want))
	}
	for i, w := range want {
		if !strings.HasPrefix(calls[i], w) {
			t.Errorf("call %d = %q, want it to start with %q", i+1, calls[i], w)
		}
	}
	if !strings.Contains(calls[7], "sudo sh install.sh --role controller --version 1.10.0 --agent-listen 203.0.113.5:7443 --agent-advertise 203.0.113.5:7443") {
		t.Errorf("the summary does not show the command line: %q", calls[7])
	}
	want2 := "system\ninstall\n--role\ncontroller\n--agent-listen\n203.0.113.5:7443\n--agent-advertise\n203.0.113.5:7443\n"
	if got := readFile(t, f.logBase+".args"); got != want2 {
		t.Errorf("darkpanel ran with %q, want %q", got, want2)
	}
}

func TestWizardWhiptailCancel(t *testing.T) {
	for _, status := range []string{"1", "255"} {
		t.Run("exit "+status, func(t *testing.T) {
			f := newFixture(t)
			tools, _ := fakeWhiptail(t, "0 agent", status+" ")
			tm := f.wizard(t, tools, "TERM=xterm")
			out, err := tm.wait()
			if err == nil || !strings.Contains(out, "Installation cancelled; nothing was changed.") {
				t.Errorf("err %v, output:\n%s", err, out)
			}
			if got := f.tarballRequests.Load(); got != 0 {
				t.Errorf("a cancelled install fetched %d tarballs", got)
			}
		})
	}
}

func TestWizardNeedsATerminal(t *testing.T) {
	f := newFixture(t)
	cmd := f.command(nil)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "usage: install.sh --role controller|agent") {
		t.Errorf("err %v, output %q; want the usage error", err, out)
	}
}

// startInBackground starts cmd on a terminal in a process group other than
// the terminal's foreground one, where sudo with use_pty puts a script piped
// into it: the terminal opens, but no key typed on it arrives.
func startInBackground(t *testing.T, cmd *exec.Cmd) *term {
	t.Helper()
	wrapper := exec.Command("sh", append([]string{"-c", `set -m; "$@" & wait $!`, "sh"}, cmd.Args...)...) //nolint:gosec // G204: the generated test script
	wrapper.Env = cmd.Env
	return startTerm(t, wrapper)
}

const backgroundRefusal = "download install.sh and run it as sudo sh install.sh"

func TestWizardRefusesABackgroundTerminal(t *testing.T) {
	f := newFixture(t)
	tm := startInBackground(t, f.command([]string{"TERM=dumb"}))
	tm.expect(backgroundRefusal)
	if _, err := tm.wait(); err == nil {
		t.Error("the wizard succeeded in the background of its terminal")
	}
	if got := f.tarballRequests.Load(); got != 0 {
		t.Errorf("the refused wizard fetched %d tarballs, want none", got)
	}
}

func TestTokenPromptRefusesABackgroundTerminal(t *testing.T) {
	f := newFixture(t)
	tm := startInBackground(t, f.command(nil, "--role", "agent"))
	tm.expect(backgroundRefusal + ", or pass --token-file")
	if _, err := tm.wait(); err == nil {
		t.Error("the token prompt succeeded in the background of its terminal")
	}
}

const fakeIPAddrs = fakeIP + `[ "$*" = "-4 -o addr show" ] && echo "2: eth0    inet 203.0.113.5/24 brd 203.0.113.255 scope global eth0"` + "\n"

// webWizard walks a controller install to the web question: stable version,
// agents later.
func webWizard(t *testing.T, f *fixture) *term {
	t.Helper()
	tm := f.wizard(t, map[string]string{"ip": fakeIPAddrs}, "TERM=dumb")
	for _, a := range []string{"", "", "4"} {
		tm.expect("]: ")
		tm.send(a)
	}
	tm.expect("Open the web UI on a public address?")
	return tm
}

func TestWizardOpensThePublicWebListener(t *testing.T) {
	f := newFixture(t)
	tm := webWizard(t, f)
	tm.expect("Choice [1, q to quit]: ")
	tm.send("")
	tm.expect("Web UI: http://203.0.113.5:8080 (plain HTTP, not encrypted)")
	tm.expect("sudo sh install.sh --role controller --version 1.10.0 --web-listen http://203.0.113.5:8080")
	tm.expect("Install now? [Y/n]: ")
	tm.send("")
	out, err := tm.wait()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if got := readFile(t, f.logBase+".args"); got != "system\ninstall\n--role\ncontroller\n--web-listen\nhttp://203.0.113.5:8080\n" {
		t.Errorf("darkpanel ran with %q", got)
	}
	open := strings.Index(out, "2. Open the panel at http://203.0.113.5:8080")
	tunnel := strings.Index(out, "ssh -L 8080:127.0.0.1:8080")
	tailnet := strings.Index(out, "or, over Tailscale, to https://100.64.0.5:8443")
	if open < 0 || tunnel < open || tailnet < tunnel {
		t.Errorf("closing steps do not name the public URL first, then the tunnel and Tailscale routes:\n%s", out)
	}
}

func TestWizardWebDefaultsToNoWithoutAnAddress(t *testing.T) {
	f := newFixture(t)
	tm := f.wizard(t, map[string]string{"ip": "\n"}, "TERM=dumb")
	tm.expect("What should this server be?")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("")
	tm.expect("Which version should be installed?")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("")
	tm.expect("How will agents reach this controller?")
	tm.expect("Choice [1, q to quit]: ")
	tm.send("3")
	tm.expect("Open the web UI on a public address?")
	tm.expect("1) Another address of this server")
	tm.expect("2) No - reach it through an SSH tunnel or Tailscale")
	tm.expect("Choice [2, q to quit]: ")
	tm.send("")
	tm.expect("Web UI: SSH tunnel or Tailscale only")
	tm.expect("Install now? [Y/n]: ")
	tm.send("n")
	if out, err := tm.wait(); err == nil {
		t.Fatalf("declining the summary succeeded:\n%s", out)
	}
}

func TestWizardCustomWebAddress(t *testing.T) {
	f := newFixture(t)
	tm := webWizard(t, f)
	tm.expect("Choice [1, q to quit]: ")
	tm.send("2")
	for _, bad := range []string{"0.0.0.0", "256.1.1.1", "203.0.113.5:0", "2001:db8::7", "panel.example", "198.51.100.9"} {
		tm.expect("Address of this server for the web UI (IPv4, optionally :port) [203.0.113.5:8080]: ")
		tm.send(bad)
		tm.expect("is not an IPv4 address of this server")
	}
	tm.expect("Address of this server for the web UI (IPv4, optionally :port) [203.0.113.5:8080]: ")
	tm.send("203.0.113.5:80")
	tm.expect("port 80 needs root and the panel runs as darkpanel; use port 1024 or higher, such as 8080")
	tm.expect("Address of this server for the web UI (IPv4, optionally :port) [203.0.113.5:8080]: ")
	tm.send("203.0.113.5")
	tm.expect("--web-listen http://203.0.113.5:8080")
	tm.expect("Install now? [Y/n]: ")
	tm.send("n")
	if out, err := tm.wait(); err == nil {
		t.Fatalf("declining the summary succeeded:\n%s", out)
	}
}

func TestWizardWebDefaultFollowsTheDropIn(t *testing.T) {
	dropIn := func(f *fixture, execStart string) {
		p := filepath.Join(f.root, "etc/systemd/system/darkpanel-controller.service.d/10-agent.conf")
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, "[Service]\nExecStart=\nExecStart="+execStart+"\n")
	}
	for _, tt := range []struct {
		name, execStart, prompt string
	}{
		{"no web value", "/usr/local/lib/darkpanel/darkpanel serve", "Choice [3, q to quit]: "},
		{"the detected address", "/usr/local/lib/darkpanel/darkpanel serve --listen loopback --listen tailscale --listen http://203.0.113.5:8080", "Choice [1, q to quit]: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dropIn(f, tt.execStart)
			tm := webWizard(t, f)
			tm.expect(tt.prompt)
			tm.send("q")
			_, _ = tm.wait()
		})
	}
	t.Run("another address is the custom default", func(t *testing.T) {
		f := newFixture(t)
		dropIn(f, "/usr/local/lib/darkpanel/darkpanel serve --listen loopback --listen tailscale --listen http://203.0.113.5:9090")
		tm := webWizard(t, f)
		tm.expect("Choice [2, q to quit]: ")
		tm.send("")
		// ui_input has no q: accept the prefilled answer, decline at the summary.
		tm.expect("[203.0.113.5:9090]: ")
		tm.send("")
		tm.expect("Web UI: http://203.0.113.5:9090 (plain HTTP, not encrypted)")
		tm.expect("--web-listen http://203.0.113.5:9090")
		tm.expect("Install now? [Y/n]: ")
		tm.send("n")
		if out, err := tm.wait(); err == nil {
			t.Fatalf("declining the summary succeeded:\n%s", out)
		}
	})
	for _, kept := range []string{"https://203.0.113.5:9443", "http://[2001:db8::7]:8080", "http://203.0.113.5:8080 http://203.0.113.6:8080"} {
		t.Run("kept "+kept, func(t *testing.T) {
			f := newFixture(t)
			dropIn(f, "/usr/local/lib/darkpanel/darkpanel serve --listen loopback --listen tailscale --listen "+strings.ReplaceAll(kept, " ", " --listen "))
			tm := f.wizard(t, map[string]string{"ip": fakeIPAddrs}, "TERM=dumb")
			for _, a := range []string{"", "", "4"} {
				tm.expect("]: ")
				tm.send(a)
			}
			tm.expect("Web UI: " + kept + " (kept from the current install)")
			tm.expect("--web-listen " + strings.ReplaceAll(kept, " ", " --web-listen "))
			tm.expect("Install now? [Y/n]: ")
			tm.send("n")
			out, _ := tm.wait()
			if strings.Contains(out, "Open the web UI on a public address?") {
				t.Errorf("the wizard asked although the drop-in holds %s:\n%s", kept, out)
			}
		})
	}
}

func TestWizardWhiptailWebRoute(t *testing.T) {
	f := newFixture(t)
	tools, log := fakeWhiptail(t, "0 controller", "0 1.10.0", "0 later", "0 route", "0 ")
	tools["ip"] = fakeIPAddrs
	tm := f.wizard(t, tools, "TERM=xterm")
	out, err := tm.wait()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	calls := strings.Split(strings.TrimSuffix(readFile(t, log), "\n---\n"), "\n---\n")
	if len(calls) != 5 || !strings.HasPrefix(calls[3], "--title|Darkpanel 1.10.0 installer|--notags|--default-item|route|--menu|Open the web UI on a public address?") {
		t.Fatalf("whiptail calls:\n%s", strings.Join(calls, "\n"))
	}
	if !strings.Contains(calls[4], "Web UI: http://203.0.113.5:8080 (plain HTTP, not encrypted)") || !strings.Contains(calls[4], "--web-listen http://203.0.113.5:8080") {
		t.Errorf("the summary does not show the web listener: %q", calls[4])
	}
	if got := readFile(t, f.logBase+".args"); got != "system\ninstall\n--role\ncontroller\n--web-listen\nhttp://203.0.113.5:8080\n" {
		t.Errorf("darkpanel ran with %q", got)
	}
}

func TestWizardWhiptailCancelAtTheWebQuestion(t *testing.T) {
	for _, status := range []string{"1", "255"} {
		t.Run("exit "+status, func(t *testing.T) {
			f := newFixture(t)
			tools, _ := fakeWhiptail(t, "0 controller", "0 1.10.0", "0 later", status+" ")
			tm := f.wizard(t, tools, "TERM=xterm")
			out, err := tm.wait()
			if err == nil || !strings.Contains(out, "Installation cancelled; nothing was changed.") {
				t.Errorf("err %v, output:\n%s", err, out)
			}
			if got := f.tarballRequests.Load(); got != 0 {
				t.Errorf("a cancelled install fetched %d tarballs", got)
			}
		})
	}
}

func TestWizardWebOnAnOldBinary(t *testing.T) {
	f := newFixtureStub(t, oldStub)
	tm := webWizard(t, f)
	tm.expect("Choice [1, q to quit]: ")
	tm.send("")
	tm.expect("Install now? [Y/n]: ")
	tm.send("")
	out, err := tm.wait()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cannot open a public web listener; installing without it") || strings.Contains(out, "Open the panel at http://203.0.113.5:8080") {
		t.Errorf("the old binary's install kept the public URL in the closing steps (spec A18):\n%s", out)
	}
}
