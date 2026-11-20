package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Acceptance tests of the ui-revamp-server-management spec, platform layer.

// qaAdmits reports whether any production exec rule admits path with args.
func qaAdmits(path string, args []string) bool {
	for _, r := range ProductionRules() {
		if r.Path == path && r.Match(args) == nil {
			return true
		}
	}
	return false
}

func TestA21_NoExecRuleAdmitsAnOptionOrShellShapedNameInANamePosition(t *testing.T) {
	hostile := []string{"-o", "--help", "-oAPT::Get::AllowUnauthenticated=1", "--allow-unauthenticated", "jq;rm", "jq rm", "$(id)", "Jq", "jq-", "", "a", strings.Repeat("a", 65)}
	for _, verb := range []string{"install", "remove", "purge", "install-only-upgrade"} {
		for _, name := range hostile {
			for label, vec := range map[string][]string{
				"run":   SystemAptRun(verb, []string{name}),
				"plan":  SystemAptPlan(verb, []string{name}),
				"resim": SystemAptResim(verb, []string{name}),
				"read":  SystemAptRead(verb, nil, []string{name}),
			} {
				if strings.HasPrefix(name, "jq") && name == "jq-" {
					continue // a syntactically valid name; exactness is checked before apt runs
				}
				if qaAdmits("/usr/bin/apt-get", vec) {
					t.Errorf("%s %s with name %q was admitted: %v", label, verb, name, vec)
				}
			}
		}
	}
	for _, name := range []string{"-o", "--help", "-x"} {
		if qaAdmits("/usr/bin/apt-cache", SystemAptCachePolicy([]string{name})) {
			t.Errorf("apt-cache policy with %q was admitted", name)
		}
	}
	if !qaAdmits("/usr/bin/apt-get", SystemAptRun("install", []string{"jq"})) {
		t.Error("the control vector, install jq, is refused")
	}
}

func TestA24_LogindScheduleShutdownRebootAndAReasonedRefusal(t *testing.T) {
	var gotMethod string
	var gotArgs []any
	ok := qaBus(func(_ context.Context, _, _, method string, args ...any) error {
		gotMethod, gotArgs = method, args
		return nil
	})
	at := time.Date(2026, 11, 19, 12, 0, 15, 0, time.UTC)
	if err := newLogindReboot(ok).ScheduleReboot(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "org.freedesktop.login1.Manager.ScheduleShutdown" || len(gotArgs) != 2 || gotArgs[0] != "reboot" || gotArgs[1] != uint64(at.UnixMicro()) {
		t.Fatalf("called %s %v", gotMethod, gotArgs)
	}
	refuse := qaBus(func(context.Context, string, string, string, ...any) error {
		return dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied", Body: []any{"Operation inhibited by Xyzzy backup"}}
	})
	err := newLogindReboot(refuse).ScheduleReboot(context.Background(), at)
	var le *LogindError
	if !errors.As(err, &le) || !strings.Contains(le.Reason, "Operation inhibited by Xyzzy backup") {
		t.Fatalf("err = %v, want a LogindError with logind's reason", err)
	}
}

type qaBus func(ctx context.Context, dest, path, method string, args ...any) error

func (b qaBus) Call(ctx context.Context, dest, path, method string, args ...any) error {
	return b(ctx, dest, path, method, args...)
}

func TestA31_PurgeRemovesExactlyTheTwoDirectoriesAndRefusesSymlinksAndMounts(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o750); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sentinel := filepath.Join(mk("var/lib/other"), "keep")
	if err := os.WriteFile(sentinel, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	none := func() ([]string, error) { return nil, nil }

	for _, p := range []string{"/var/lib/docker", "/var/lib/containerd"} {
		d := mk(strings.TrimPrefix(p, "/") + "/overlay2/a")
		_ = os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o600)
		if err := newPurge(root, none).PurgeDir(p); err != nil {
			t.Fatalf("PurgeDir(%s): %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s still exists after the purge: %v", p, err)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("a neighbouring directory was touched: %v", err)
	}
	for _, other := range []string{"/var/lib", "/etc", "/var/lib/other", "/"} {
		if err := newPurge(root, none).PurgeDir(other); err == nil {
			t.Errorf("PurgeDir(%s) was allowed", other)
		}
	}

	t.Run("a data path that is a symlink is refused and the target survives", func(t *testing.T) {
		target := mk("elsewhere")
		_ = os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o600)
		_ = os.RemoveAll(filepath.Join(root, "var/lib/docker"))
		if err := os.Symlink(target, filepath.Join(root, "var/lib/docker")); err != nil {
			t.Fatal(err)
		}
		p := newPurge(root, none)
		if err := p.CheckPurgeable("/var/lib/docker"); err == nil {
			t.Error("CheckPurgeable accepted a symlink")
		}
		if err := p.PurgeDir("/var/lib/docker"); err == nil {
			t.Error("PurgeDir accepted a symlink")
		}
		if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
			t.Errorf("the symlink target was touched: %v", err)
		}
		_ = os.Remove(filepath.Join(root, "var/lib/docker"))
	})
	t.Run("a mount point, or a path containing one, is refused", func(t *testing.T) {
		mk("var/lib/containerd/io/mnt")
		for name, mounts := range map[string][]string{
			"is a mount point":  {"/var/lib/containerd"},
			"contains a mount":  {"/var/lib/containerd/io/mnt"},
			"contains a deeper": {"/var/lib/containerd/io/mnt/x"},
		} {
			p := newPurge(root, func() ([]string, error) { return mounts, nil })
			if err := p.CheckPurgeable("/var/lib/containerd"); err == nil {
				t.Errorf("%s: CheckPurgeable accepted it", name)
			}
			if err := p.PurgeDir("/var/lib/containerd"); err == nil {
				t.Errorf("%s: PurgeDir accepted it", name)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "var/lib/containerd/io/mnt")); err != nil {
			t.Errorf("a refused purge deleted data: %v", err)
		}
	})
}
