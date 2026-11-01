package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	puDpkgStatus = "/var/lib/dpkg/status"
	puAptGet     = "/usr/bin/apt-get"
	puAptMark    = "/usr/bin/apt-mark"
)

// puStatus is a dpkg status file that lists tailscale in the given state at
// the given version; an empty version leaves the package out.
func puStatus(version, status string) []byte {
	var b strings.Builder
	b.WriteString("Package: base-files\nStatus: install ok installed\nArchitecture: amd64\nVersion: 13ubuntu10\n\n")
	if version != "" {
		b.WriteString("Package: tailscale\nStatus: " + status + "\nPriority: optional\nSection: net\nArchitecture: amd64\nVersion: " + version +
			"\nDescription: The easiest, most secure way to use WireGuard and 2FA.\n\n")
	}
	b.WriteString("Package: zlib1g\nStatus: install ok installed\nArchitecture: amd64\nVersion: 1:1.3.dfsg-3.1ubuntu2\n")
	return []byte(b.String())
}

// puVector is the key the host's scripted runs use: the program and its arguments.
func puVector(path string, args []string) string {
	return strings.Join(append([]string{path}, args...), " ")
}

// puSetup is a host with tailscale installed at version and a supported OS.
func puSetup(t *testing.T, version string) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	if h.reads == nil {
		h.reads = map[string][]byte{}
	}
	tt := h.ts()
	h.reads[puDpkgStatus] = puStatus(version, "install ok installed")
	tt.d.osRelease = func() (osInfo, error) { return osInfo{ID: "ubuntu", Codename: "noble"}, nil }
	tt.d.arch = "amd64"
	tt.d.hostname = func() (string, error) { return "host-1", nil }
	return h, tt
}

// puRunner runs vectors on the host and, when the install vector (APT-2)
// succeeds, leaves the package at version in dpkg's status file, as apt-get
// would; an empty version leaves it uninstalled.
type puRunner struct {
	h       *tsHost
	version string
}

func (r puRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	res, err := r.h.Run(ctx, cmd)
	if err == nil && res.ExitCode == 0 && cmd.Path == puAptGet && slices.Equal(cmd.Args, platform.AptInstallTailscale()) {
		r.h.reads[puDpkgStatus] = puStatus(r.version, "install ok installed")
	}
	return res, err
}

// puEnv is the environment of the update's task unit, whose apt-get install
// leaves version installed.
func puEnv(h *tsHost, version string) core.Env {
	env := h.env("c1")
	env.Exec = puRunner{h: h, version: version}
	return env
}

func TestPuActionDefinition(t *testing.T) {
	_, tt := puSetup(t, "1.82.0")
	def := tt.packageUpdate()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.package.update" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || s.ApplyTimeout != 15*time.Minute || !s.Task || !s.LongRunning || s.Available == nil || len(s.Guards) != 0 {
		t.Errorf("spec %+v, want an irreversible agent task locking %q, with a 15 minute timeout and an availability check", s, lockKey)
	}
	if def.InputType != reflect.TypeFor[PackageUpdateInput]() || def.OutputType != reflect.TypeFor[PackageUpdated]() {
		t.Errorf("input %v and output %v, want PackageUpdateInput and PackageUpdated", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil || h.Observe != nil || h.Inverse != nil {
		t.Error("handler lacks Plan, Task or Verify, or has an Apply, Observe or Inverse")
	}
}

func TestPuPlan(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	out, err := tt.packageUpdate().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: " ", Text: "tailscale 1.82.0 is installed"},
		{Op: "+", Text: "update to the version the Tailscale repository offers (apt-get update of that repository, then apt-get install tailscale)"},
	}
	if !reflect.DeepEqual(out.Diff, want) || out.Confirmation != "host-1" || len(out.StateHash) != 64 || len(out.Guards) != 0 {
		t.Errorf("plan %+v, want diff %v, the host name as the confirmation, a 64-digit hash and no guards", out, want)
	}
	if wantRan := [][]string{{puAptMark, "showhold"}}; !reflect.DeepEqual(h.ran, wantRan) || len(h.files) != 0 {
		t.Errorf("plan ran %v and wrote %v, want only the read of the holds", h.ran, h.files)
	}
}

func TestPuPlanRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *tsHost, tt *ts)
		code  errcode.Code // "" when the module's own code is not fixed
	}{
		{"the package is not installed", func(h *tsHost, _ *ts) { h.reads[puDpkgStatus] = puStatus("", "") }, errcode.TailscaleUnavailable},
		{"only the configuration of a removed package remains", func(h *tsHost, _ *ts) {
			h.reads[puDpkgStatus] = puStatus("1.82.0", "deinstall ok config-files")
		}, errcode.TailscaleUnavailable},
		{"the package is held", func(h *tsHost, _ *ts) {
			h.script(puAptMark+" showhold", tsRun{res: core.CmdResult{Stdout: []byte("tailscale\n")}})
		}, errcode.TailscalePackageHeld},
		{"the OS cannot be read", func(_ *tsHost, tt *ts) {
			tt.d.osRelease = func() (osInfo, error) { return osInfo{}, errors.New("no os-release") }
		}, ""},
		{"the architecture has no repository", func(_ *tsHost, tt *ts) { tt.d.arch = "riscv64" }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := puSetup(t, "1.82.0")
			tc.setup(h, &tt)
			_, err := tt.packageUpdate().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
			if err == nil || !core.IsDefinite(err) || (tc.code != "" && errcode.From(err).Code != tc.code) {
				t.Errorf("err = %v (definite %v), want a definite refusal with code %q", err, core.IsDefinite(err), tc.code)
			}
			if slices.ContainsFunc(h.ran, func(v []string) bool { return v[0] == puAptGet }) || len(h.files) != 0 {
				t.Errorf("plan ran %v and wrote %v, want no apt-get and no file", h.ran, h.files)
			}
		})
	}
}

func TestPuPlanStateHash(t *testing.T) {
	hash := func(version string) string {
		t.Helper()
		h, tt := puSetup(t, version)
		out, err := tt.packageUpdate().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	first, again, other := hash("1.82.0"), hash("1.82.0"), hash("1.84.2")
	if first != again || first == other {
		t.Error("the state hash must be the same for one installed version and differ between two")
	}
}

func TestPuTaskWritesTheRepositoryAndUpdates(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	raw, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.90.1"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"from":"1.82.0","to":"1.90.1"}` {
		t.Errorf("output %s, want the version before and after", raw)
	}
	wantRan := [][]string{append([]string{puAptGet}, platform.AptUpdateTailscale()...), append([]string{puAptGet}, platform.AptInstallTailscale()...)}
	if !reflect.DeepEqual(h.ran, wantRan) {
		t.Errorf("ran %v, want APT-1 then APT-2 and nothing else", h.ran)
	}
	keyring, sources, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.files, map[string][]byte{keyringPath: keyring, sourcesPath: sources}) {
		t.Errorf("files written %v, want the keyring and the source list this host's repository needs", h.files)
	}
	if es := h.journal.effects(); len(es) != 0 {
		t.Errorf("journaled %+v, want no effect: update has no Observe, so the repository files it writes are owned but not journaled", es)
	}
}

func TestPuTaskUsesTheRepositoryInPlace(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	keyring, sources, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	h.reads[keyringPath], h.reads[sourcesPath] = keyring, sources
	if _, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.82.0"), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(h.files) != 0 || len(h.ran) != 2 {
		t.Errorf("wrote %v and ran %v, want the files left as they are and APT-1 and APT-2 run", h.files, h.ran)
	}
}

func TestPuTaskRefusesAForeignRepository(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	keyring, _, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	h.reads[keyringPath], h.reads[sourcesPath] = keyring, []byte("deb https://example.com/apt stable main\n")
	_, err = tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.90.1"), json.RawMessage(`{}`))
	if e := errcode.From(err); e.Code != errcode.TailscaleRepoConflict {
		t.Errorf("err = %v, want %s", err, errcode.TailscaleRepoConflict)
	}
	if len(h.ran) != 0 || len(h.files) != 0 {
		t.Errorf("ran %v and wrote %v, want nothing after a conflict", h.ran, h.files)
	}
}

func TestPuTaskAptFailures(t *testing.T) {
	update := puVector(puAptGet, platform.AptUpdateTailscale())
	install := puVector(puAptGet, platform.AptInstallTailscale())
	failed := tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Unable to locate package tailscale")}}
	tests := []struct {
		name, fails string
		wantRan     int
	}{
		{"the repository refresh fails", update, 1},
		{"the install fails", install, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := puSetup(t, "1.82.0")
			h.script(tc.fails, failed)
			if _, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.90.1"), json.RawMessage(`{}`)); err == nil {
				t.Fatal("task succeeded, want the apt-get failure")
			}
			if len(h.ran) != tc.wantRan {
				t.Errorf("ran %v, want %d apt-get runs", h.ran, tc.wantRan)
			}
		})
	}
}

func TestPuTaskFailsWhenThePackageIsGone(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	if _, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, ""), json.RawMessage(`{}`)); err == nil {
		t.Error("task succeeded with no tailscale installed afterwards, want an error")
	}
}

func TestPuVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := puSetup(t, "1.82.0")
	def, in := tt.packageUpdate(), json.RawMessage(`{}`)
	out, err := def.Handler.Task(ctx, puEnv(h, "1.90.1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after the task: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.reads[puDpkgStatus] = puStatus("1.88.0", "install ok installed")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil || !strings.Contains(err.Error(), "1.88.0") {
		t.Errorf("verify with another version installed = %v, want an error naming it", err)
	}
	h.reads[puDpkgStatus] = puStatus("", "")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed with the package gone, want an error")
	}
}
