package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	acceptanceWebQuestion = "Open the web UI on a public address?"
	acceptanceCancelled   = "Installation cancelled; nothing was changed."
	// acceptanceRoute, acceptanceTunnel are the summary lines of UX-2.
	acceptanceRoute  = "Web UI: http://203.0.113.5:8080 (plain HTTP, not encrypted)"
	acceptanceTunnel = "Web UI: SSH tunnel or Tailscale only"
	// The wizard's answer that leaves the agent-address question for later:
	// the fourth option with a route and Tailscale address, the second with
	// neither.
	acceptanceLaterWithRoute = "4"
	acceptanceLaterNoRoute   = "2"
)

// acceptanceDropIn writes the controller drop-in system install leaves, with
// execStart as its ExecStart line.
func acceptanceDropIn(t *testing.T, f *fixture, execStart string) {
	t.Helper()
	p := filepath.Join(f.root, "etc/systemd/system/darkpanel-controller.service.d/10-agent.conf")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "[Service]\nExecStart=\nExecStart="+execStart+"\n")
}

// acceptanceNoServer is a base URL nothing listens on: a run that tries to
// download anything fails with a download error, not with the refusal the
// tests look for.
const acceptanceNoServer = "http://127.0.0.1:1"

const acceptanceServe = "/usr/local/lib/darkpanel/darkpanel serve"

// acceptancePlain walks the plain wizard of a controller to the question
// after the agent-address one: stable version, agents "later". It leaves the
// terminal at the first prompt after that question.
func acceptancePlain(t *testing.T, f *fixture, tools map[string]string, later string) *term {
	t.Helper()
	tm := f.wizard(t, tools, "TERM=dumb")
	for _, a := range []string{"", "", later} {
		tm.expect("]: ")
		tm.send(a)
	}
	return tm
}

// acceptanceFinish accepts the summary and returns the whole output of a
// successful run.
func acceptanceFinish(t *testing.T, tm *term) string {
	t.Helper()
	tm.expect("Install now? [Y/n]: ")
	tm.send("y")
	out, err := tm.wait()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	return out
}

// acceptanceDecline declines the summary and returns the whole output.
func acceptanceDecline(t *testing.T, tm *term) string {
	t.Helper()
	tm.expect("Install now? [Y/n]: ")
	tm.send("n")
	out, _ := tm.wait()
	return out
}

func acceptanceArgs(t *testing.T, f *fixture) string { return readFile(t, f.logBase+".args") }

func acceptanceNothingInstalled(t *testing.T, id string, f *fixture) {
	t.Helper()
	if got := f.tarballRequests.Load(); got != 0 {
		t.Errorf("%s: %d tarballs were fetched, want none", id, got)
	}
	if _, err := os.Stat(f.bin()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: the binary was installed: %v", id, err)
	}
	if _, err := os.Stat(f.logBase + ".args"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: system install ran", id)
	}
}

// TestA5ShRefusesWebListenForAnAgent covers the install.sh half of A5.
func TestA5ShRefusesWebListenForAnAgent(t *testing.T) {
	for _, args := range [][]string{
		{"--role", "agent", "--web-listen", "http://203.0.113.5:8080"},
		{"--web-listen", "http://203.0.113.5:8080", "--role", "agent"},
		{"--role", "agent", "--token-file", "TOKEN", "--web-listen", "http://203.0.113.5:8080"},
	} {
		f := newFixture(t)
		for i, a := range args {
			if a == "TOKEN" {
				args[i] = f.tokenFile(t, "dpe1.example-token\n")
			}
		}
		out, err := f.run(append(args, "--base-url", acceptanceNoServer)...)
		if err == nil || !strings.Contains(string(out), "--web-listen") {
			t.Errorf("A5: install.sh %v: err %v, output %q; want a refusal naming --web-listen", args, err, out)
		}
		acceptanceNothingInstalled(t, "A5", f)
	}
}

// TestA6ShPassesWebListenValuesAndChecksTheirShape covers A6.
func TestA6ShPassesWebListenValuesAndChecksTheirShape(t *testing.T) {
	t.Run("A6: values pass unchanged", func(t *testing.T) {
		f := newFixture(t)
		values := []string{"http://203.0.113.5:8080", "https://203.0.113.5:9443", "http://[2001:db8::7]:8080"}
		args := []string{"--role", "controller", "--base-url", f.srv.URL}
		want := "system\ninstall\n--role\ncontroller\n"
		for _, v := range values {
			args = append(args, "--web-listen", v)
			want += "--web-listen\n" + v + "\n"
		}
		if out, err := f.run(args...); err != nil {
			t.Fatalf("A6: install.sh: %v\n%s", err, out)
		}
		if got := acceptanceArgs(t, f); got != want {
			t.Errorf("A6: system install ran with %q, want %q", got, want)
		}
	})
	for _, bad := range []string{
		"http://myhost:8080", "ftp://203.0.113.5:8080", "http://203.0.113.5", "203.0.113.5:8080", "loopback",
		"tailscale", "http://203.0.113.5:8080/x", "http://[::1]", "http://203.0.113.5:80 --foo", "http://panel.example.com:8080",
		"http://:8080", "https://",
	} {
		t.Run("A6: refuses "+bad, func(t *testing.T) {
			f := newFixture(t)
			out, err := f.run("--role", "controller", "--web-listen", bad, "--base-url", acceptanceNoServer)
			if err == nil || !strings.Contains(string(out), "--web-listen") {
				t.Errorf("A6: err %v, output %q; want a refusal naming --web-listen", err, out)
			}
			acceptanceNothingInstalled(t, "A6", f)
		})
	}
}

// TestA7TheWizardOffersThePublicListener covers A7 in plain and whiptail mode.
func TestA7TheWizardOffersThePublicListener(t *testing.T) {
	t.Run("A7: plain", func(t *testing.T) {
		f := newFixture(t)
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("1) This server's address http://203.0.113.5:8080")
		tm.expect("Choice [1, q to quit]: ")
		tm.send("")
		tm.expect("Install now? [Y/n]: ")
		summary := tm.output()
		for _, want := range []string{acceptanceRoute, "--web-listen http://203.0.113.5:8080"} {
			if !strings.Contains(summary, want) {
				t.Errorf("A7: the summary lacks %q:\n%s", want, summary)
			}
		}
		tm.send("y")
		if out, err := tm.wait(); err != nil {
			t.Fatalf("A7: install.sh: %v\n%s", err, out)
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n--web-listen\nhttp://203.0.113.5:8080\n"; got != want {
			t.Errorf("A7: system install ran with %q, want %q", got, want)
		}
	})
	t.Run("A7: whiptail", func(t *testing.T) {
		f := newFixture(t)
		tools, log := fakeWhiptail(t, "0 controller", "0 1.10.0", "0 later", "0 route", "0 ")
		tools["ip"] = fakeIPAddrs
		out, err := f.wizard(t, tools, "TERM=xterm").wait()
		if err != nil {
			t.Fatalf("A7: install.sh: %v\n%s", err, out)
		}
		calls := strings.Split(strings.TrimSuffix(readFile(t, log), "\n---\n"), "\n---\n")
		if len(calls) != 5 {
			t.Fatalf("A7: whiptail calls:\n%s", strings.Join(calls, "\n"))
		}
		if !strings.Contains(calls[3], "--default-item|route|--menu|"+acceptanceWebQuestion) {
			t.Errorf("A7: call 4 = %q, want the web menu with route preselected", calls[3])
		}
		for _, want := range []string{acceptanceRoute, "--web-listen http://203.0.113.5:8080"} {
			if !strings.Contains(calls[4], want) {
				t.Errorf("A7: the summary lacks %q: %q", want, calls[4])
			}
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n--web-listen\nhttp://203.0.113.5:8080\n"; got != want {
			t.Errorf("A7: system install ran with %q, want %q", got, want)
		}
	})
}

// TestA8TheWizardDefaultsAndAnswers covers A8.
func TestA8TheWizardDefaultsAndAnswers(t *testing.T) {
	t.Run("A8: no leaves the flag out", func(t *testing.T) {
		f := newFixture(t)
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [1, q to quit]: ")
		tm.send("3")
		tm.expect("Install now? [Y/n]: ")
		summary := tm.output()
		if !strings.Contains(summary, acceptanceTunnel) {
			t.Errorf("A8: the summary lacks %q:\n%s", acceptanceTunnel, summary)
		}
		if strings.Contains(summary, "--web-listen") {
			t.Errorf("A8: the command shows --web-listen after no:\n%s", summary)
		}
		tm.send("y")
		if out, err := tm.wait(); err != nil {
			t.Fatalf("A8: install.sh: %v\n%s", err, out)
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n"; got != want {
			t.Errorf("A8: system install ran with %q, want %q", got, want)
		}
	})
	t.Run("A8: no detected address defaults to no", func(t *testing.T) {
		f := newFixture(t)
		tm := acceptancePlain(t, f, map[string]string{"ip": noTool, "tailscale": noTool}, acceptanceLaterNoRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [")
		tm.send("")
		out := acceptanceDecline(t, tm)
		if !strings.Contains(out, acceptanceTunnel) || strings.Contains(out, "--web-listen") {
			t.Errorf("A8: accepting the default without an address did not mean no:\n%s", out)
		}
		if strings.Contains(out, "This server's address http://") {
			t.Errorf("A8: the menu offered a route address that was not detected:\n%s", out)
		}
	})
	t.Run("A8: whiptail no", func(t *testing.T) {
		f := newFixture(t)
		tools, log := fakeWhiptail(t, "0 controller", "0 1.10.0", "0 later", "0 no", "0 ")
		tools["ip"] = fakeIPAddrs
		out, err := f.wizard(t, tools, "TERM=xterm").wait()
		if err != nil {
			t.Fatalf("A8: install.sh: %v\n%s", err, out)
		}
		calls := strings.Split(strings.TrimSuffix(readFile(t, log), "\n---\n"), "\n---\n")
		if len(calls) != 5 || !strings.Contains(calls[4], acceptanceTunnel) || strings.Contains(calls[4], "--web-listen") {
			t.Errorf("A8: whiptail calls:\n%s", strings.Join(calls, "\n"))
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n"; got != want {
			t.Errorf("A8: system install ran with %q, want %q", got, want)
		}
	})
	t.Run("A8: a drop-in without a web value defaults to no", func(t *testing.T) {
		f := newFixture(t)
		acceptanceDropIn(t, f, acceptanceServe+" --agent-listen 203.0.113.5:7443")
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [3, q to quit]: ")
		tm.send("")
		if out := acceptanceDecline(t, tm); !strings.Contains(out, acceptanceTunnel) {
			t.Errorf("A8: accepting the default did not mean no:\n%s", out)
		}
	})
	t.Run("A8: a drop-in with the detected address defaults to route", func(t *testing.T) {
		f := newFixture(t)
		acceptanceDropIn(t, f, acceptanceServe+" --listen loopback --listen tailscale --listen http://203.0.113.5:8080")
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [1, q to quit]: ")
		tm.send("")
		if out := acceptanceDecline(t, tm); !strings.Contains(out, acceptanceRoute) {
			t.Errorf("A8: accepting the default did not mean route:\n%s", out)
		}
	})
	t.Run("A8: a drop-in with another address defaults to custom, prefilled", func(t *testing.T) {
		f := newFixture(t)
		acceptanceDropIn(t, f, acceptanceServe+" --listen loopback --listen tailscale --listen http://203.0.113.5:9090")
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [2, q to quit]: ")
		tm.send("")
		tm.expect("[203.0.113.5:9090]: ")
		tm.send("")
		out := acceptanceDecline(t, tm)
		if !strings.Contains(out, "Web UI: http://203.0.113.5:9090 (plain HTTP, not encrypted)") {
			t.Errorf("A8: the prefilled answer was not taken:\n%s", out)
		}
	})
	for _, kept := range [][]string{
		{"https://203.0.113.5:9443"},
		{"http://[2001:db8::7]:8080"},
		{"http://203.0.113.5:8080", "http://203.0.113.6:8080"},
	} {
		name := strings.Join(kept, " ")
		t.Run("A8: a drop-in with "+name+" is kept", func(t *testing.T) {
			f := newFixture(t)
			acceptanceDropIn(t, f, acceptanceServe+" --listen loopback --listen tailscale --listen "+strings.Join(kept, " --listen "))
			tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
			tm.expect("Install now? [Y/n]: ")
			summary := tm.output()
			if strings.Contains(summary, acceptanceWebQuestion) {
				t.Errorf("A8: the wizard asked the web question although the drop-in holds %s:\n%s", name, summary)
			}
			if !strings.Contains(summary, "Web UI: "+name+" (kept from the current install)") {
				t.Errorf("A8: the summary lacks %q:\n%s", "Web UI: "+name+" (kept from the current install)", summary)
			}
			tm.send("y")
			if out, err := tm.wait(); err != nil {
				t.Fatalf("A8: install.sh: %v\n%s", err, out)
			}
			want := "system\ninstall\n--role\ncontroller\n"
			for _, v := range kept {
				want += "--web-listen\n" + v + "\n"
			}
			if got := acceptanceArgs(t, f); got != want {
				t.Errorf("A8: system install ran with %q, want %q", got, want)
			}
		})
	}
}

// TestA9TheCustomWebAddress covers A9.
func TestA9TheCustomWebAddress(t *testing.T) {
	custom := func(t *testing.T) (*fixture, *term) {
		t.Helper()
		f := newFixture(t)
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [1, q to quit]: ")
		tm.send("2")
		return f, tm
	}
	const prompt = "Address of this server for the web UI (IPv4, optionally :port)"
	t.Run("A9: a bare address gets :8080", func(t *testing.T) {
		f, tm := custom(t)
		tm.expect(prompt)
		tm.send("203.0.113.5")
		tm.expect("Install now? [Y/n]: ")
		if summary := tm.output(); !strings.Contains(summary, "--web-listen http://203.0.113.5:8080") {
			t.Errorf("A9: the command lacks the :8080 address:\n%s", summary)
		}
		tm.send("y")
		if out, err := tm.wait(); err != nil {
			t.Fatalf("A9: install.sh: %v\n%s", err, out)
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n--web-listen\nhttp://203.0.113.5:8080\n"; got != want {
			t.Errorf("A9: system install ran with %q, want %q", got, want)
		}
	})
	t.Run("A9: an address with a port keeps it", func(t *testing.T) {
		f, tm := custom(t)
		tm.expect(prompt)
		tm.send("203.0.113.5:9090")
		if out := acceptanceFinish(t, tm); !strings.Contains(out, "Web UI: http://203.0.113.5:9090") {
			t.Errorf("A9: the summary lacks the chosen port:\n%s", out)
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n--web-listen\nhttp://203.0.113.5:9090\n"; got != want {
			t.Errorf("A9: system install ran with %q, want %q", got, want)
		}
	})
	t.Run("A9: refused answers ask again", func(t *testing.T) {
		_, tm := custom(t)
		for _, bad := range []string{"0.0.0.0", "256.1.1.1", "203.0.113.5:0", "203.0.113.5:65536", "2001:db8::7", "panel.example", "198.51.100.9"} {
			tm.expect(prompt)
			tm.send(bad)
			tm.expect("address " + bad + " is not an IPv4 address of this server")
		}
		tm.expect(prompt)
		tm.send("203.0.113.5")
		acceptanceFinish(t, tm)
	})
}

// TestA10TheAgentPathNeverAsksTheWebQuestion covers A10.
func TestA10TheAgentPathNeverAsksTheWebQuestion(t *testing.T) {
	t.Run("A10: plain", func(t *testing.T) {
		f := newFixture(t)
		acceptanceDropIn(t, f, acceptanceServe+" --listen loopback --listen tailscale --listen http://203.0.113.5:8080")
		tm := f.wizard(t, map[string]string{"ip": fakeIPAddrs}, "TERM=dumb")
		tm.expect("Choice [1, q to quit]: ")
		tm.send("2")
		tm.expect("Choice [1, q to quit]: ")
		tm.send("")
		tm.expect("Enrollment token or bootstrap bundle: ")
		tm.send("dpe1.secret-token")
		out := acceptanceFinish(t, tm)
		if strings.Contains(out, acceptanceWebQuestion) || strings.Contains(out, "Web UI:") || strings.Contains(out, "--web-listen") {
			t.Errorf("A10: the agent path touched the web listener:\n%s", out)
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\nagent\n"; got != want {
			t.Errorf("A10: system install ran with %q, want %q", got, want)
		}
	})
	t.Run("A10: whiptail", func(t *testing.T) {
		f := newFixture(t)
		tools, log := fakeWhiptail(t, "0 agent", "0 1.10.0", "0 dpe1.secret-token", "0 ")
		tools["ip"] = fakeIPAddrs
		out, err := f.wizard(t, tools, "TERM=xterm").wait()
		if err != nil {
			t.Fatalf("A10: install.sh: %v\n%s", err, out)
		}
		if calls := readFile(t, log); strings.Contains(calls, acceptanceWebQuestion) || strings.Contains(calls, "--web-listen") {
			t.Errorf("A10: whiptail was asked about the web listener:\n%s", calls)
		}
	})
}

// TestA11TheClosingStepsNameThePanelAddress covers A11.
func TestA11TheClosingStepsNameThePanelAddress(t *testing.T) {
	t.Run("A11: with a web listener", func(t *testing.T) {
		for _, tt := range []struct{ answer, url string }{{"1", "http://203.0.113.5:8080"}} {
			f := newFixture(t)
			tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
			tm.expect(acceptanceWebQuestion)
			tm.expect("Choice [1, q to quit]: ")
			tm.send(tt.answer)
			out := acceptanceFinish(t, tm)
			if !strings.Contains(out, "Open the panel at "+tt.url) {
				t.Errorf("A11: the closing steps do not name %s:\n%s", tt.url, out)
			}
			if !strings.Contains(out, "ssh -L 8080:127.0.0.1:8080") {
				t.Errorf("A11: the closing steps lost the SSH tunnel route:\n%s", out)
			}
		}
	})
	t.Run("A11: a custom address and port", func(t *testing.T) {
		f := newFixture(t)
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [1, q to quit]: ")
		tm.send("2")
		tm.expect("Address of this server for the web UI")
		tm.send("203.0.113.5:9090")
		if out := acceptanceFinish(t, tm); !strings.Contains(out, "Open the panel at http://203.0.113.5:9090") {
			t.Errorf("A11: the closing steps do not name http://203.0.113.5:9090:\n%s", out)
		}
	})
	t.Run("A11: without a web listener", func(t *testing.T) {
		f := newFixture(t)
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [1, q to quit]: ")
		tm.send("3")
		out := acceptanceFinish(t, tm)
		_, closing, found := strings.Cut(out, "Next steps:")
		if !found {
			t.Fatalf("A11: the output has no closing steps:\n%s", out)
		}
		out = closing
		for _, want := range []string{"  2. Open the panel from your computer:", "ssh -L 8080:127.0.0.1:8080 <this server>", "then browse to http://127.0.0.1:8080", "https://100.64.0.5:8443"} {
			if !strings.Contains(out, want) {
				t.Errorf("A11: the closing steps lack %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "Open the panel at") || strings.Contains(out, "http://203.0.113.5") {
			t.Errorf("A11: the closing steps name a public address although none was opened:\n%s", out)
		}
	})
}

// TestA18AnOlderBinaryInstallsWithoutTheWebListener covers A18.
func TestA18AnOlderBinaryInstallsWithoutTheWebListener(t *testing.T) {
	const warning = "darkpanel 1.10.0 cannot open a public web listener; installing without it"
	t.Run("A18: flags", func(t *testing.T) {
		f := newFixtureStub(t, oldStub)
		out, err := f.run("--role", "controller", "--agent-listen", "203.0.113.5:7443", "--web-listen", "http://203.0.113.5:8080", "--base-url", f.srv.URL)
		if err != nil {
			t.Fatalf("A18: install.sh: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), warning) {
			t.Errorf("A18: output lacks %q:\n%s", warning, out)
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n--agent-listen\n203.0.113.5:7443\n"; got != want {
			t.Errorf("A18: system install ran with %q, want %q", got, want)
		}
		if _, err := os.Stat(f.bin()); err != nil {
			t.Errorf("A18: the binary was not installed: %v", err)
		}
	})
	t.Run("A18: a binary with the flag is not warned about", func(t *testing.T) {
		f := newFixture(t)
		out, err := f.run("--role", "controller", "--web-listen", "http://203.0.113.5:8080", "--base-url", f.srv.URL)
		if err != nil || strings.Contains(string(out), "cannot open a public web listener") {
			t.Errorf("A18: err %v, output %q; want no warning", err, out)
		}
	})
	t.Run("A18: wizard closing steps", func(t *testing.T) {
		f := newFixtureStub(t, oldStub)
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [1, q to quit]: ")
		tm.send("")
		out := acceptanceFinish(t, tm)
		if !strings.Contains(out, warning) {
			t.Errorf("A18: output lacks %q:\n%s", warning, out)
		}
		if strings.Contains(out, "Open the panel at") || !strings.Contains(out, "Open the panel from your computer:") {
			t.Errorf("A18: the closing steps are not the ones without a listener:\n%s", out)
		}
		if got, want := acceptanceArgs(t, f), "system\ninstall\n--role\ncontroller\n"; got != want {
			t.Errorf("A18: system install ran with %q, want %q", got, want)
		}
	})
}

// TestA20CancellingTheWebQuestionChangesNothing covers A20.
func TestA20CancellingTheWebQuestionChangesNothing(t *testing.T) {
	t.Run("A20: q in plain mode", func(t *testing.T) {
		f := newFixture(t)
		tm := acceptancePlain(t, f, map[string]string{"ip": fakeIPAddrs}, acceptanceLaterWithRoute)
		tm.expect(acceptanceWebQuestion)
		tm.expect("Choice [1, q to quit]: ")
		tm.send("q")
		out, err := tm.wait()
		if err == nil || !strings.Contains(out, acceptanceCancelled) {
			t.Errorf("A20: err %v, output:\n%s", err, out)
		}
		acceptanceNothingInstalled(t, "A20", f)
	})
	for _, status := range []string{"1", "255"} {
		t.Run("A20: whiptail exit "+status, func(t *testing.T) {
			f := newFixture(t)
			tools, _ := fakeWhiptail(t, "0 controller", "0 1.10.0", "0 later", status+" ")
			tools["ip"] = fakeIPAddrs
			out, err := f.wizard(t, tools, "TERM=xterm").wait()
			if err == nil || !strings.Contains(out, acceptanceCancelled) {
				t.Errorf("A20: err %v, output:\n%s", err, out)
			}
			acceptanceNothingInstalled(t, "A20", f)
		})
	}
}
