package system

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestUpdatesResource(t *testing.T) {
	h := newSysHost(t)
	read := "/usr/bin/apt-get " + strings.Join(platform.SystemAptRead("dist-upgrade", nil, nil), " ")
	phased := "/usr/bin/apt-get " + strings.Join(platform.SystemAptRead("dist-upgrade", []string{"-o", "APT::Get::Always-Include-Phased-Updates=true"}, nil), " ")
	h.run(read, "Inst openssl [3.0.2-0ubuntu1.15] (3.0.2-0ubuntu1.18 Ubuntu:22.04/jammy-security [amd64])\nInst curl [7.81.0-1] (7.81.0-1ubuntu1.20 Ubuntu:22.04/jammy-updates [amd64])\nRemv oldlib [1.0]\n2 upgraded, 0 newly installed, 1 to remove and 2 not upgraded.\n", 0)
	h.run(phased, "Inst openssl [3.0.2-0ubuntu1.15] (3.0.2-0ubuntu1.18 Ubuntu:22.04/jammy-security [amd64])\nInst curl [7.81.0-1] (7.81.0-1ubuntu1.20 Ubuntu:22.04/jammy-updates [amd64])\nInst systemd [249.11-0ubuntu3.12] (249.11-0ubuntu3.16 Ubuntu:22.04/jammy-updates [amd64])\nRemv oldlib [1.0]\n3 upgraded, 0 newly installed, 1 to remove and 1 not upgraded.\n", 0)
	h.run("/usr/bin/apt-mark showhold", "nginx\n", 0)
	h.run("/usr/bin/apt-cache policy -- nginx", "nginx:\n  Installed: 1.18.0-6ubuntu14.4\n  Candidate: 1.18.0-6ubuntu14.6\n", 0)
	h.run("/usr/bin/dpkg --audit", "", 0)
	h.file("/var/lib/apt/periodic/update-success-stamp", nil)
	h.file("/var/lib/darkpanel/system/failed-sources", []byte("https://bad.example stable\n"))
	out := h.readResource(t, "updates")
	want := `{"upgradable":[{"name":"curl","from":"7.81.0-1","to":"7.81.0-1ubuntu1.20","origin":"Ubuntu:22.04/jammy-updates","security":false},{"name":"openssl","from":"3.0.2-0ubuntu1.15","to":"3.0.2-0ubuntu1.18","origin":"Ubuntu:22.04/jammy-security","security":true}],"removals":["oldlib"],"heldBack":[{"name":"nginx","version":"1.18.0-6ubuntu14.6","reason":"held"},{"name":"systemd","version":"249.11-0ubuntu3.16","reason":"phased"}],"refreshedAt":"2026-11-19T12:00:00Z","failedSources":["https://bad.example stable"],"dpkgInterrupted":false}`
	if out != want {
		t.Fatalf("updates =\n%s\nwant\n%s", out, want)
	}
	n := len(h.calls())
	_ = h.readResource(t, "updates")
	if len(h.calls()) != n {
		t.Fatal("second read within 60 s ran apt again")
	}
	// Additions to the plan's cases: invalidation and expiry read again.
	invalidateUpdates("h1")
	_ = h.readResource(t, "updates")
	if len(h.calls()) == n {
		t.Fatal("a read after invalidateUpdates answered from the cache")
	}
	n = len(h.calls())
	h.clock.Advance(updatesTTL)
	_ = h.readResource(t, "updates")
	if len(h.calls()) == n {
		t.Fatal("a read 60 s later answered from the cache")
	}
}

func TestUpdatesReportsInterruptedDpkgAsAField(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/dpkg --audit", "The following packages are only half configured:\n curl\n", 0)
	out := h.readResource(t, "updates")
	if !strings.Contains(out, `"dpkgInterrupted":true`) {
		t.Fatalf("updates = %s", out)
	}
}

func TestSearchIsASubstringFilter(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "a.out-tools\nabc\nlibva.2\n", 0)
	h.run("/usr/bin/apt-cache policy -- a.out-tools libva.2", "a.out-tools:\n  Installed: (none)\n  Candidate: 1.0\nlibva.2:\n  Installed: (none)\n  Candidate: 2.1\n", 0)
	h.installed("abc")
	out := h.callRead(t, "system.packages.search", `{"term":"a."}`)
	if !strings.HasPrefix(out, `[{"name":"a.out-tools","candidate":`) || strings.Contains(out, `"abc"`) || !strings.Contains(out, `{"name":"libva.2","candidate":`) || !strings.Contains(out, `"installed":false}`) {
		t.Fatalf("search = %s", out)
	}
	if _, err := h.callReadErr("system.packages.search", `{"term":"-o"}`); errcode.From(err).Code != "system.name_invalid" {
		t.Fatalf("option-shaped term not refused: %v", err)
	}
}

// Additions to the plan's cases.

func TestSearchCapsAtFiftyAndMarksInstalled(t *testing.T) {
	h := newSysHost(t)
	var all, policy, first []string
	for i := range 60 {
		n := "lib" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		all = append(all, n)
		if i < 50 {
			first = append(first, n)
			policy = append(policy, n+":\n  Installed: (none)\n  Candidate: 1.0")
		}
	}
	h.run("/usr/bin/apt-cache pkgnames", strings.Join(all, "\n")+"\n", 0)
	h.run("/usr/bin/apt-cache policy -- "+strings.Join(first, " "), strings.Join(policy, "\n")+"\n", 0)
	h.installed("libaa")
	out := h.callRead(t, "system.packages.search", `{"term":"lib"}`)
	if strings.Count(out, `"name":`) != 50 || !strings.HasPrefix(out, `[{"name":"libaa","candidate":"1.0","installed":true}`) {
		t.Fatalf("search = %s", out)
	}
	if out := h.callRead(t, "system.packages.search", `{"term":"zz"}`); out != "[]" {
		t.Fatalf("search with no match = %s, want []", out)
	}
}

func TestPackagesResourceMarksProtected(t *testing.T) {
	h := newSysHost(t)
	h.installed("jq", "openssh-server")
	out := h.readResource(t, "packages")
	if !strings.Contains(out, `{"name":"jq","version":"1.0-1","arch":"amd64","description":"jq package","protected":null}`) ||
		!strings.Contains(out, `{"name":"openssh-server","version":"1.0-1","arch":"amd64","description":"openssh-server package","protected":"remote access to the host"}`) {
		t.Fatalf("packages = %s", out)
	}
}

func TestParseSimReadsOrigins(t *testing.T) {
	sim, err := ParseSim([]byte("Inst libc6 [2.35-0ubuntu3.15] (2.35-0ubuntu3.18 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"))
	if err != nil {
		t.Fatal(err)
	}
	u := upgradable(sim.Ops[0])
	if len(sim.Ops[0].Origins) != 2 || u.Origin != "Ubuntu:22.04/jammy-security" || !u.Security || u.To != "2.35-0ubuntu3.18" {
		t.Fatalf("op = %+v, upgradable = %+v", sim.Ops[0], u)
	}
}

func TestUpdatesCacheIsPerAgent(t *testing.T) {
	a, b := newSysHost(t), newSysHost(t)
	a.run("/usr/bin/dpkg --audit", "half configured\n", 0)
	if out := a.readResource(t, "updates"); !strings.Contains(out, `"dpkgInterrupted":true`) {
		t.Fatalf("a = %s", out)
	}
	b.updatesReadable()
	if out := b.readResource(t, "updates"); !strings.Contains(out, `"dpkgInterrupted":false`) {
		t.Fatalf("b answered from a's cache: %s", out)
	}
}
