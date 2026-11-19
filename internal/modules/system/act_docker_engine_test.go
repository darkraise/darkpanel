package system

import (
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestEngineInstallPlanListsFilesAndPackages(t *testing.T) {
	h := newSysHost(t)
	h.osRelease("ubuntu", "noble", "amd64")
	plan := h.plan(t, "system.docker.engine-install", `{}`)
	for _, want := range []string{"/etc/apt/sources.list.d/docker.list", "/usr/share/keyrings/docker-archive-keyring.gpg", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"} {
		if !strings.Contains(plan.Text(), want) {
			t.Errorf("plan lacks %s", want)
		}
	}
}

func TestEngineInstallRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *sysHost)
		code  errcode.Code
	}{
		{"distro docker present", func(h *sysHost) { h.installed("docker.io") }, "system.docker_present"},
		{"differing docker.list", func(h *sysHost) {
			h.file("/etc/apt/sources.list.d/docker.list", []byte("deb https://download.docker.com/linux/ubuntu noble stable\n"))
		}, "system.docker_repo_conflict"},
		{"other entry for download.docker.com", func(h *sysHost) {
			h.file("/etc/apt/sources.list.d/my.list", []byte("deb [signed-by=/x.gpg] https://download.docker.com/linux/ubuntu noble stable\n"))
		}, "system.docker_repo_conflict"},
		{"keyring fingerprint wrong", func(h *sysHost) { h.keyringOverride([]byte("not the pinned key")) }, "system.docker_keyring"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSysHost(t)
			h.osRelease("ubuntu", "noble", "amd64")
			c.setup(h)
			_, err := h.applyTask("system.docker.engine-install", `{}`)
			if errcode.From(err).Code != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if c.code == "system.docker_keyring" && len(h.writtenPaths()) != 0 {
				t.Fatalf("wrote %v before the keyring check", h.writtenPaths())
			}
		})
	}
}

func TestEngineUpdate(t *testing.T) {
	h := newSysHost(t)
	h.installed(dockerPackages...)
	h.dockerDaemon(true)
	h.dockerCounts(2, 0, 0, nil)
	h.run(planVec("install-only-upgrade", dockerPackages), "Inst docker-ce [5:27.0] (5:27.3 Docker:noble [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	p := h.plan(t, "system.docker.engine-update", `{}`)
	if p.Phrase != h.hostName() || !strings.Contains(p.Text(), "2 running containers will restart") {
		t.Fatalf("plan:\n%s phrase=%q", p.Text(), p.Phrase)
	}
	a, err := h.action("system.docker.engine-update")
	if err != nil {
		t.Fatal(err)
	}
	if spec := a.Spec; spec.Class != core.ClassIrreversible || !slices.Equal(spec.Rechecks, []string{"docker"}) {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestEngineInstallUndoRemovesWhatItAdded(t *testing.T) {
	h := newSysHost(t)
	h.osRelease("ubuntu", "noble", "amd64")
	h.run("/usr/bin/apt-get "+strings.Join(platform.SystemAptUpdate("/etc/apt/sources.list.d/docker.list"), " "), "Hit:1 https://download.docker.com/linux/ubuntu noble InRelease\n", 0)
	h.run(resimVec("install", dockerPackages), "Inst docker-ce (5:27.3 Docker:noble [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.runChanges(runVec("install", dockerPackages), "", 0, append([]string{"bash"}, dockerPackages...)...)
	out, err := h.applyTask("system.docker.engine-install", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"installed":["containerd.io","docker-buildx-plugin","docker-ce","docker-ce-cli","docker-compose-plugin"]`) {
		t.Fatalf("A37: output = %s", out)
	}
	removeAll := "Remv docker-compose-plugin [2]\nRemv docker-buildx-plugin [0.17]\nRemv docker-ce [5:27.3]\nRemv docker-ce-cli [5:27.3]\nRemv containerd.io [1.7]\n0 upgraded, 0 newly installed, 5 to remove and 0 not upgraded.\n"
	h.run(planVec("remove", sortedDockerPackages), removeAll, 0)
	h.runChanges(runVec("remove", sortedDockerPackages), "", 0, "bash")
	if err := h.undo("system.docker.engine-install"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.calls(), runVec("remove", sortedDockerPackages)) {
		t.Fatal("A42: undo did not remove the installed packages")
	}
	if h.exists("/etc/apt/sources.list.d/docker.list") || h.exists("/usr/share/keyrings/docker-archive-keyring.gpg") {
		t.Fatal("A42: repository files left after undo")
	}
}

func TestEngineInstallRefusesAnyRemovalAndStrictRefresh(t *testing.T) {
	h := newSysHost(t)
	h.osRelease("ubuntu", "noble", "amd64")
	h.run("/usr/bin/apt-get "+strings.Join(platform.SystemAptUpdate("/etc/apt/sources.list.d/docker.list"), " "), "Err:1 https://download.docker.com/linux/ubuntu noble InRelease\nW: Failed to fetch\n", 0)
	if _, err := h.applyTask("system.docker.engine-install", `{}`); errcode.From(err).Code != "system.repo_signature" {
		t.Fatalf("strict refresh: err = %v", err)
	}
	h2 := newSysHost(t)
	h2.osRelease("ubuntu", "noble", "amd64")
	h2.run("/usr/bin/apt-get "+strings.Join(platform.SystemAptUpdate("/etc/apt/sources.list.d/docker.list"), " "), "Hit:1 https://download.docker.com/linux/ubuntu noble InRelease\n", 0)
	h2.run(resimVec("install", dockerPackages), "Inst docker-ce (5:27.3 Docker:noble [amd64])\nRemv runc [1.1]\n0 upgraded, 1 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	if _, err := h2.applyTask("system.docker.engine-install", `{}`); errcode.From(err).Code != "system.removal_refused" {
		t.Fatalf("removal: err = %v", err)
	}
}

// Additions to the plan's cases.

func TestEngineInstallWritesThePinnedRepositoryAndJournalsWhatItWrote(t *testing.T) {
	h := newSysHost(t)
	h.osRelease("debian", "bookworm", "arm64")
	h.run(aptGet+" "+strings.Join(platform.SystemAptUpdate(dockerListPath), " "), "Hit:1 https://download.docker.com/linux/debian bookworm InRelease\n", 0)
	h.run(resimVec("install", dockerPackages), "Inst docker-ce (5:27.3 Docker:bookworm [arm64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.runChanges(runVec("install", dockerPackages), "", 0, append([]string{"bash"}, dockerPackages...)...)
	if _, err := h.applyTask("system.docker.engine-install", `{}`); err != nil {
		t.Fatal(err)
	}
	want := "deb [arch=arm64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian bookworm stable\n"
	if got := string(h.written(dockerListPath)); got != want {
		t.Fatalf("docker.list = %q, want %q", got, want)
	}
	fps, err := platform.KeyringFingerprints(h.written(dockerKeyringPath))
	if err != nil || !slices.Contains(fps, dockerKeyFingerprint) {
		t.Fatalf("the written keyring holds %v (%v), not %s", fps, err, dockerKeyFingerprint)
	}
	var resources []string
	for _, e := range h.journal.of(h.last["system.docker.engine-install"]) {
		if e.State != core.EffectMade {
			t.Fatalf("effect %s is %s", e.Resource, e.State)
		}
		resources = append(resources, e.Resource)
	}
	if !slices.Equal(resources, []string{"system.file:" + dockerKeyringPath, "system.file:" + dockerListPath, "system.packages:containerd.io,docker-buildx-plugin,docker-ce,docker-ce-cli,docker-compose-plugin"}) {
		t.Fatalf("effects = %v", resources)
	}
	// The update's strict refresh ran before the re-simulation.
	calls := h.calls()
	if slices.Index(calls, aptGet+" "+strings.Join(platform.SystemAptUpdate(dockerListPath), " ")) > slices.Index(calls, resimVec("install", dockerPackages)) {
		t.Fatalf("the re-simulation ran before the Docker source's update: %v", calls)
	}
}

func TestEngineInstallUsesIdenticalFilesWithoutRecordingThem(t *testing.T) {
	h := newSysHost(t)
	keyring, list, err := h.mod().dockerRepoFiles()
	if err != nil {
		t.Fatal(err)
	}
	h.file(dockerKeyringPath, keyring)
	h.file(dockerListPath, list)
	p := h.plan(t, "system.docker.engine-install", `{}`)
	if !strings.Contains(p.Text(), dockerListPath+" is already present and identical") {
		t.Fatalf("plan:\n%s", p.Text())
	}
	h.run(aptGet+" "+strings.Join(platform.SystemAptUpdate(dockerListPath), " "), "Hit:1 https://download.docker.com/linux/ubuntu noble InRelease\n", 0)
	h.run(resimVec("install", dockerPackages), "Inst docker-ce (5:27.3 Docker:noble [amd64])\nRemv runc [1.1]\n0 upgraded, 1 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	_, err = h.applyTask("system.docker.engine-install", `{}`)
	if errcode.From(err).Code != errcode.SystemRemovalRefused || !core.IsDefinite(err) || !strings.Contains(err.Error(), "runc") {
		t.Fatalf("err = %v; want a definite removal refusal naming runc, nothing having been written", err)
	}
	if len(h.writtenPaths()) != 0 || len(h.journal.of("c1")) != 0 {
		t.Fatalf("wrote %v, journaled %+v", h.writtenPaths(), h.journal.of("c1"))
	}
}

func TestEngineInstallStrictRefreshFailsOnAnErrorExit(t *testing.T) {
	h := newSysHost(t)
	h.run(aptGet+" "+strings.Join(platform.SystemAptUpdate(dockerListPath), " "),
		"Get:1 https://download.docker.com/linux/ubuntu noble InRelease\nE: The repository 'https://download.docker.com/linux/ubuntu noble InRelease' is not signed.\n", 100)
	_, err := h.applyTask("system.docker.engine-install", `{}`)
	if errcode.From(err).Code != errcode.SystemRepoSignature || core.IsDefinite(err) {
		t.Fatalf("err = %v; want system.repo_signature, not definite: the files were written", err)
	}
	if slices.Contains(h.calls(), resimVec("install", dockerPackages)) {
		t.Fatal("simulated the install after a failed refresh")
	}
}

func TestEngineInstallRefusesInstalledDockerCEAndOtherEntriesInDeb822(t *testing.T) {
	h := newSysHost(t)
	h.file("/etc/apt/sources.list.d/docker.sources", []byte("Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: noble\n"))
	if _, err := h.planErr("system.docker.engine-install", `{}`); errcode.From(err).Code != errcode.SystemDockerRepoConflict || !strings.Contains(err.Error(), "docker.sources") {
		t.Fatalf("deb822 entry: err = %v", err)
	}
	h2 := newSysHost(t)
	h2.file("/etc/apt/sources.list", []byte("# deb https://download.docker.com/linux/ubuntu noble stable\ndeb http://archive.ubuntu.com/ubuntu noble main\n"))
	h2.installed("bash", "docker-ce")
	h2.run(shJoin("/usr/bin/apt-cache", platform.SystemAptCachePolicy([]string{"docker-ce"})), "docker-ce:\n  Installed: 1.0-1\n  Candidate: 1.0-1\n", 0)
	if _, err := h2.planErr("system.docker.engine-install", `{}`); errcode.From(err).Code != errcode.SystemDockerPresent || !strings.Contains(err.Error(), "docker-ce") {
		t.Fatalf("docker-ce installed: err = %v", err)
	}
}

func TestEngineInstallUndoExemptsOnlyTheDockerPackages(t *testing.T) {
	h := newSysHost(t)
	h.installed("bash", "openssh-server")
	h.run(aptGet+" "+strings.Join(platform.SystemAptUpdate(dockerListPath), " "), "Hit:1 https://download.docker.com/linux/ubuntu noble InRelease\n", 0)
	h.run(resimVec("install", dockerPackages), "Inst docker-ce (5:27.3 Docker:noble [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.runChanges(runVec("install", dockerPackages), "", 0, append([]string{"bash", "openssh-server"}, dockerPackages...)...)
	if _, err := h.applyTask("system.docker.engine-install", `{}`); err != nil {
		t.Fatal(err)
	}
	// The Docker packages are protected from every other package change; this undo may remove them.
	removeAll := "Remv docker-compose-plugin [2]\nRemv docker-buildx-plugin [0.17]\nRemv docker-ce [5:27.3]\nRemv docker-ce-cli [5:27.3]\nRemv containerd.io [1.7]\n0 upgraded, 0 newly installed, 5 to remove and 0 not upgraded.\n"
	h.run(planVec("remove", sortedDockerPackages), removeAll, 0)
	h.runChanges(runVec("remove", sortedDockerPackages), "", 0, "bash", "openssh-server")
	if err := h.undo("system.docker.engine-install"); err != nil {
		t.Fatalf("the Docker packages are exempt: %v", err)
	}
	h2 := newSysHost(t)
	h2.installed("bash", "openssh-server")
	h2.run(aptGet+" "+strings.Join(platform.SystemAptUpdate(dockerListPath), " "), "Hit:1 https://download.docker.com/linux/ubuntu noble InRelease\n", 0)
	h2.run(resimVec("install", dockerPackages), "Inst docker-ce (5:27.3 Docker:noble [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h2.runChanges(runVec("install", dockerPackages), "", 0, append([]string{"bash", "openssh-server"}, dockerPackages...)...)
	if _, err := h2.applyTask("system.docker.engine-install", `{}`); err != nil {
		t.Fatal(err)
	}
	h2.run(planVec("remove", sortedDockerPackages), strings.Replace(removeAll, "0 upgraded, 0 newly installed, 5 to remove", "Remv openssh-server [1:9.6]\n0 upgraded, 0 newly installed, 6 to remove", 1), 0)
	err := h2.undo("system.docker.engine-install")
	if errcode.From(err).Code != errcode.SystemRemovalRefused || !strings.Contains(err.Error(), "openssh-server") || !core.IsDefinite(err) {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(h2.calls(), runVec("remove", sortedDockerPackages)) {
		t.Fatal("the undo removed packages after refusing")
	}
}

func TestEngineUpdateRunsTheConfirmedSet(t *testing.T) {
	h := newSysHost(t)
	h.installed(dockerPackages...)
	sim := "Inst docker-ce [5:27.0] (5:27.3 Docker:noble [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(planVec("install-only-upgrade", dockerPackages), sim, 0)
	h.run(resimVec("install-only-upgrade", dockerPackages), sim, 0)
	h.runChanges(runVec("install-only-upgrade", dockerPackages), "", 0, "docker-ce=5:27.3", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin")
	out, err := h.applyPlan(h.plan(t, "system.docker.engine-update", `{}`))
	if err != nil || out != `{"installed":[],"upgraded":["docker-ce"],"removed":[]}` {
		t.Fatalf("update = %s, %v", out, err)
	}
}
