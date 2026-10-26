//go:build harness

package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// testPackages are RequiredPackages with versions naming distro and checksums
// of digit, so two OSes' scripts can be told apart.
func testPackages(distro, digit string) []Package {
	var out []Package
	for _, name := range RequiredPackages {
		version := "1.0-1~" + distro
		out = append(out, Package{
			Name: name, Version: version,
			URL:    "https://example.org/pool/" + name + "_" + version + "_amd64.deb",
			SHA256: strings.Repeat(digit, 64),
		})
	}
	return out
}

func TestTargetScript(t *testing.T) {
	cases := []struct {
		os    OS
		other string // a string only the other case's script contains
	}{
		{OS{ID: "ubuntu-22.04", Family: "ubuntu", VersionID: "22.04", Packages: testPackages("ubuntu.22.04~jammy", "1")}, "debian.13~trixie"},
		{OS{ID: "debian-13", Family: "debian", VersionID: "13", Packages: testPackages("debian.13~trixie", "2")}, "ubuntu.22.04~jammy"},
	}
	for _, c := range cases {
		s := TargetScript(c.os)
		for _, p := range c.os.Packages {
			if want := "fetch '" + p.Name + "' '" + p.Version + "' '" + p.URL + "' '" + p.SHA256 + "'"; !strings.Contains(s, want) {
				t.Errorf("%s script lacks %q:\n%s", c.os.ID, want, s)
			}
		}
		install := `apt_get install --no-install-recommends "$pkgdir/docker-ce.deb" "$pkgdir/docker-ce-cli.deb" ` +
			`"$pkgdir/containerd.io.deb" "$pkgdir/docker-compose-plugin.deb" "$pkgdir/tailscale.deb"`
		if !strings.Contains(s, install) {
			t.Errorf("%s script lacks %q:\n%s", c.os.ID, install, s)
		}
		if strings.Contains(s, c.other) {
			t.Errorf("%s script names another OS's packages (%s):\n%s", c.os.ID, c.other, s)
		}
		if !strings.Contains(s, "apt_get install --no-install-recommends ca-certificates curl sqlite3 ufw nftables\n") {
			t.Errorf("%s script does not install sqlite3 and nftables beside curl and ufw:\n%s", c.os.ID, s)
		}
		allow, enable := strings.Index(s, "ufw allow 22/tcp"), strings.Index(s, "ufw --force enable")
		if allow < 0 || enable < allow || strings.Index(s, "systemctl enable --now ufw docker tailscaled") < enable {
			t.Errorf("%s script must allow 22/tcp, enable UFW, then start ufw, Docker and tailscaled under systemd:\n%s", c.os.ID, s)
		}
		if agent := strings.Index(s, "ufw allow in on dppriv to any port 7443 proto tcp\n"); agent < allow || agent > enable {
			t.Errorf("%s script must allow 7443/tcp in on dppriv only, before UFW is enabled:\n%s", c.os.ID, s)
		}
		bashSyntax(t, s)
	}
	scanner := ScannerScript()
	if !strings.Contains(scanner, "apt_get install --no-install-recommends nmap") {
		t.Errorf("scanner script does not install nmap:\n%s", scanner)
	}
	bashSyntax(t, scanner)
}

func bashSyntax(t *testing.T, script string) {
	t.Helper()
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("bash -n: %v\n%s", err, out)
	}
}

func TestFetchChecksThePins(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	deb := filepath.Join(src, "docker-ce.deb")
	if err := os.WriteFile(deb, []byte("a package"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("a package"))
	good := hex.EncodeToString(sum[:])
	fetch := func(url, want string) (string, error) {
		script := "pkgdir='" + dst + "'\n" + fetchFunc + "fetch docker-ce 1.0-1 '" + url + "' '" + want + "'\n"
		out, err := exec.Command("bash", "-c", script).CombinedOutput() //nolint:gosec // G204: the test's own script
		return strings.TrimSpace(string(out)), err
	}
	if out, err := fetch("file://"+deb, good); err != nil {
		t.Errorf("fetch of a matching package: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "docker-ce.deb")); err != nil || string(got) != "a package" { //nolint:gosec // G304: the test's own temporary file
		t.Errorf("fetched file = %q, %v; want the package", got, err)
	}
	wrong := strings.Repeat("0", 64)
	out, err := fetch("file://"+deb, wrong)
	if want := "pinned package docker-ce=1.0-1 has SHA-256 " + good + ", want " + wrong + "; update the harness pins"; err == nil || out != want {
		t.Errorf("fetch with a wrong checksum: %v, %q; want a failure saying %q", err, out, want)
	}
	out, err = fetch("file://"+filepath.Join(src, "gone.deb"), good)
	if want := "pinned package docker-ce=1.0-1 missing; update the harness pins"; err == nil || out != want {
		t.Errorf("fetch of a missing package: %v, %q; want a failure saying %q", err, out, want)
	}
}

func TestPullScript(t *testing.T) {
	cs := validContainers()
	s := PullScript(cs...)
	for _, c := range cs {
		if want := "pull_image '" + c.Name + "' '" + c.Ref + "'\n"; !strings.Contains(s, want) {
			t.Errorf("PullScript lacks %q:\n%s", want, s)
		}
	}
	o := OS{ID: "debian-13", Family: "debian", VersionID: "13", Packages: testPackages("debian.13~trixie", "2")}
	bashSyntax(t, TargetScript(o)+s)

	// docker is a function that fails until its call number reaches ok and
	// logs its arguments, so the retries run without a daemon.
	pull := func(ok int) (string, string, error) {
		log := filepath.Join(t.TempDir(), "docker.log")
		script := "pull_retry_delay=0\nn=0\ndocker() { n=$((n+1)); echo \"$*\" >> '" + log + "'; [ \"$n\" -ge " +
			strconv.Itoa(ok) + " ]; }\n" + PullScript(cs[0]) + "echo pulled\n"
		out, err := exec.Command("bash", "-c", script).CombinedOutput() //nolint:gosec // G204: the test's own script
		calls, _ := os.ReadFile(log)                                    //nolint:gosec // G304: the test's own temporary file
		return strings.TrimSpace(string(out)), string(calls), err
	}
	out, calls, err := pull(2)
	if want := strings.Repeat("pull --quiet "+cs[0].Ref+"\n", 2); err != nil || out != "pulled" || calls != want {
		t.Errorf("a pull that succeeds on its second attempt: %v, output %q, calls %q; want success after %q", err, out, calls, want)
	}
	out, calls, err = pull(4)
	if want := "pinned image pebble (" + cs[0].Ref + ") cannot be pulled; update the harness pins"; err == nil || out != want ||
		strings.Count(calls, "\n") != 3 {
		t.Errorf("a pull that always fails: %v, output %q, calls %q; want a failure saying %q after 3 attempts", err, out, calls, want)
	}
}
