package tailscale

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	unAptGet  = "/usr/bin/apt-get"
	unAptMark = "/usr/bin/apt-mark"
)

var (
	unShowhold = []string{unAptMark, "showhold"}
	unUnhold   = []string{unAptMark, "unhold", "tailscale"}
	unHold     = []string{unAptMark, "hold", "tailscale"}
	unRemove   = append([]string{unAptGet}, platform.AptRemoveTailscale()...)
)

// unEnv is the environment the uninstall gives a module: a database, the runner, the file provider, the sealer and
// the clock, with no change, no journal and no unit client.
func unEnv(h *tsHost) core.Env {
	return core.Env{
		DB: h.db, Exec: h, Files: h, Sealer: coretest.Sealer{}, Clock: h.clock,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// unDeadlines records, by vector, how long the context of each run had left.
type unDeadlines struct {
	next core.Runner
	left map[string]time.Duration
}

func (r unDeadlines) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if dl, ok := ctx.Deadline(); ok {
		r.left[strings.Join(append([]string{cmd.Path}, cmd.Args...), " ")] = time.Until(dl)
	}
	return r.next.Run(ctx, cmd)
}

// unInstalled records the install the way package.install does.
func unInstalled(t *testing.T, h *tsHost) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertInstall(ctx, tx, "c-install", "1.104.1", h.clock.Now()) }); err != nil {
		t.Fatal(err)
	}
}

func unRemovedAt(t *testing.T, h *tsHost) string {
	t.Helper()
	var at string
	if err := h.db.Read().QueryRowContext(context.Background(), `SELECT removed_at FROM tailscale_installs WHERE change_id = 'c-install'`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func unHeld(h *tsHost) {
	h.script(strings.Join(unShowhold, " "), tsRun{res: core.CmdResult{Stdout: []byte("other\ntailscale\n")}})
}

func unOpts(v string) map[string]string { return map[string]string{"package": v} }

func TestUnOptions(t *testing.T) {
	var _ core.Uninstaller = module{}
	want := []core.UninstallOption{{Key: "package", Values: []string{"keep", "remove"}, Default: "keep", Help: "remove the tailscale package if Darkpanel installed it"}}
	if got := (module{}).UninstallOptions(); !reflect.DeepEqual(got, want) {
		t.Errorf("options %+v, want %+v", got, want)
	}
}

func TestUnKeepChangesNothing(t *testing.T) {
	for _, keepData := range []bool{false, true} {
		h := newTSHost(t)
		unInstalled(t, h)
		if err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), keepData, unOpts("keep")); err != nil {
			t.Fatal(err)
		}
		if len(h.ran) != 0 || unRemovedAt(t, h) != "" {
			t.Errorf("keepData %v: ran %v and removed_at %q, want nothing changed", keepData, h.ran, unRemovedAt(t, h))
		}
	}
}

func TestUnRemove(t *testing.T) {
	tests := []struct {
		name     string
		held     bool
		keepData bool
		want     [][]string
	}{
		{"the package is not held", false, false, [][]string{unShowhold, unRemove}},
		{"the package is held", true, false, [][]string{unShowhold, unUnhold, unRemove}},
		{"keepData does not change it", true, true, [][]string{unShowhold, unUnhold, unRemove}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			unInstalled(t, h)
			if tc.held {
				unHeld(h)
			}
			env := unEnv(h)
			left := map[string]time.Duration{}
			env.Exec = unDeadlines{next: h, left: left}
			m := module{t: h.ts()}
			if err := m.Uninstall(context.Background(), env, tc.keepData, unOpts("remove")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(h.ran, tc.want) {
				t.Errorf("ran %v, want %v", h.ran, tc.want)
			}
			if got, want := unRemovedAt(t, h), h.clock.Now().UTC().Format(storeTimeFormat); got != want {
				t.Errorf("removed_at %q, want %q", got, want)
			}
			if d := left[strings.Join(unRemove, " ")]; d <= 4*time.Minute || d > 5*time.Minute {
				t.Errorf("apt-get remove had %v left, want a context bounded by 5 minutes (the runner's own default is 30 s)", d)
			}
			// A second uninstall finds nothing to do.
			h.ran = nil
			if err := m.Uninstall(context.Background(), unEnv(h), tc.keepData, unOpts("remove")); err != nil || len(h.ran) != 0 {
				t.Errorf("a second uninstall = %v and ran %v, want success and nothing run", err, h.ran)
			}
		})
	}
}

func TestUnRemoveLeavesAPackageDarkpanelDidNotInstall(t *testing.T) {
	h := newTSHost(t)
	if err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), false, unOpts("remove")); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 0 {
		t.Errorf("ran %v with no install recorded, want nothing", h.ran)
	}
}

func TestUnRemoveWhenThePackageIsAlreadyGone(t *testing.T) {
	h := newTSHost(t)
	unInstalled(t, h)
	h.reads[thDpkgPath] = thDpkgStatus("")
	if err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), false, unOpts("remove")); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 0 || unRemovedAt(t, h) == "" {
		t.Errorf("ran %v and removed_at %q, want no command and the install recorded as removed", h.ran, unRemovedAt(t, h))
	}
}

func TestUnRemoveFailure(t *testing.T) {
	failed := tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("W: x\nE: Could not get lock /var/lib/dpkg/lock\n")}}
	tests := []struct {
		name string
		held bool
		want [][]string
	}{
		{"not held", false, [][]string{unShowhold, unRemove}},
		{"held: the hold is put back", true, [][]string{unShowhold, unUnhold, unRemove, unHold}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			unInstalled(t, h)
			if tc.held {
				unHeld(h)
			}
			h.script(strings.Join(unRemove, " "), failed)
			m := module{t: h.ts()}
			err := m.Uninstall(context.Background(), unEnv(h), false, unOpts("remove"))
			if err == nil || !strings.HasPrefix(err.Error(), "tailscale:") || !strings.Contains(err.Error(), "Could not get lock") {
				t.Fatalf("error %v, want the apt-get failure", err)
			}
			if !reflect.DeepEqual(h.ran, tc.want) || unRemovedAt(t, h) != "" {
				t.Errorf("ran %v and removed_at %q, want %v and the install still live", h.ran, unRemovedAt(t, h), tc.want)
			}
			// Running the uninstall again finishes it.
			h.ran = nil
			if err := m.Uninstall(context.Background(), unEnv(h), false, unOpts("remove")); err != nil || unRemovedAt(t, h) == "" {
				t.Errorf("the second uninstall = %v with removed_at %q, want success", err, unRemovedAt(t, h))
			}
		})
	}
}

func TestUnRefusesAnOptionItDoesNotDeclare(t *testing.T) {
	for _, opts := range []map[string]string{unOpts("purge"), unOpts(""), {}} {
		h := newTSHost(t)
		unInstalled(t, h)
		err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), false, opts)
		if err == nil || !strings.HasPrefix(err.Error(), "tailscale:") || !strings.Contains(err.Error(), "package") {
			t.Errorf("options %v: error %v, want a refusal naming the option", opts, err)
		}
		if len(h.ran) != 0 || unRemovedAt(t, h) != "" {
			t.Errorf("options %v: ran %v, removed_at %q, want nothing changed", opts, h.ran, unRemovedAt(t, h))
		}
	}
}
