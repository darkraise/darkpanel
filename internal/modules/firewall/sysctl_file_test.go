package firewall

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	sfFile     = "/etc/ufw/sysctl.conf"
	sfSnapshot = "net/ipv4/ip_forward=0\n"
	sfHost     = "net/ipv4/ip_forward=1\n"
)

func sfRead(t *testing.T, h *fakeHost, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.root, path)) //nolint:gosec // G304: a file under the fake host's temporary root
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRestoreSysctlFileUnset: a snapshot taken while /etc/default/ufw set
// IPT_SYSCTL is restored on a host that leaves it unset and whose sysctl
// file differs. The restore writes the file as an effect, and the revert
// puts the host's file back whether it restores the change's snapshot
// whole or undoes the effects one by one: the change's own snapshot holds
// the file although the host did not apply it.
func TestRestoreSysctlFileUnset(t *testing.T) {
	ctx := context.Background()
	for _, edited := range []bool{false, true} {
		h, f, env := ruHost(t)
		h.ufw["--force enable"] = func(s *fhState) int { s.Enabled = true; return 0 }
		h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
		id := snCapture(t, h, f)
		s := h.state()
		s.NoSysctl = true
		h.setState(s)
		h.writeFile(sfFile, []byte(sfHost))
		def := f.snapshotRestore()

		_, out := ruApply(t, env, def, `{"id":"`+id+`"}`)
		if got := sfRead(t, h, sfFile); got != sfSnapshot {
			t.Fatalf("edited %v: %s is %q after the restore, want the snapshot's %q", edited, sfFile, got, sfSnapshot)
		}
		if edited {
			s := h.state()
			s.LogLevel = "high"
			h.setState(s)
		}
		rev, err := def.Handler.Revert(ctx, env, []byte(out.Snapshot))
		if err != nil {
			t.Fatalf("edited %v: revert: %v", edited, err)
		}
		for _, e := range rev.Effects {
			if e.State != core.EffectUndone {
				t.Errorf("edited %v: effect %d is %s, want undone", edited, e.Seq, e.State)
			}
		}
		if got := sfRead(t, h, sfFile); got != sfHost {
			t.Errorf("edited %v: %s is %q after the revert, want the host's %q", edited, sfFile, got, sfHost)
		}
		if cfg := h.config(); cfg.SysctlFile != "" {
			t.Errorf("edited %v: IPT_SYSCTL is %q after the revert, want it unset as before", edited, cfg.SysctlFile)
		}
	}
}
