package panel

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// TestLsStatus: the status answers the live exposure with its state, and nothing once no exposure is live, which
// includes an exposure revoked by a removal that completed.
func TestLsStatus(t *testing.T) {
	ctx := context.Background()
	def := statusAction()
	read := func(t *testing.T, r *lsRig) (StatusOutput, string) {
		t.Helper()
		raw, err := def.Handler.Read(ctx, r.env(core.CallContext{}, ""), lsEmptyInput)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return lsDecode[StatusOutput](t, raw), string(raw)
	}

	t.Run("no exposure", func(t *testing.T) {
		got, raw := read(t, lsNewRig(t))
		if got.Exposed || got.Exposure != nil || raw != `{"exposed":false}` {
			t.Errorf("status %s, want {\"exposed\":false}", raw)
		}
	})

	t.Run("a gone exposure", func(t *testing.T) {
		r := lsNewRig(t)
		fdInsertChange(t, r.db, lsRemove, ActionRemove, "completed")
		fdInsertExposure(t, r.db, lsRow(lsAdd, StateRevoked, lsRemove))
		if got, raw := read(t, r); got.Exposed || got.Exposure != nil {
			t.Errorf("status %s, want no exposure", raw)
		}
	})

	tests := []struct {
		name     string
		row      string
		removal  string
		change   string // the state of the removal change, "" for none
		listener string
	}{
		{"an open exposure", StateOpen, "", "", core.ListenerOpen},
		{"a revoked exposure with no removal", StateRevoked, "", "", core.ListenerClosed},
		{"a revoked exposure whose removal failed", StateRevoked, lsRemove, "failed", core.ListenerClosed},
		{"an exposure that needs repair", StateNeedsRepair, "", "", core.ListenerClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			if tc.change != "" {
				fdInsertChange(t, r.db, lsRemove, ActionRemove, tc.change)
			}
			row := r.expose(tc.row, tc.removal, tc.listener)
			got, raw := read(t, r)
			if !got.Exposed || got.Exposure == nil {
				t.Fatalf("status %s, want the live exposure", raw)
			}
			created := got.Exposure.CreatedAt
			got.Exposure.CreatedAt = fdNow
			want := ExposureView{
				ID: lsAdd, Via: ViaNPM, Hostname: lsName, HostID: lsHost, Subnet: lsSubnet, Gateway: lsGateway, Proxy: lsProxy,
				Port: DefaultPort, State: tc.row, ListenerID: row.ListenerID, CreatedAt: fdNow,
			}
			if *got.Exposure != want || !created.Equal(fdNow) {
				t.Errorf("exposure %+v created %v, want %+v created %v", *got.Exposure, created, want, fdNow)
			}
		})
	}

	t.Run("an exposure through a tunnel", func(t *testing.T) {
		r := lsNewRig(t)
		row := lsRow(lsAdd, StateOpen, "")
		row.Via, row.Tunnel, row.Port = ViaTunnel, lsTunnel, 9444
		fdInsertExposure(t, r.db, row)
		got, raw := read(t, r)
		if !got.Exposed || got.Exposure == nil || got.Exposure.Via != ViaTunnel || got.Exposure.Tunnel != lsTunnel || got.Exposure.Port != 9444 {
			t.Errorf("status %s, want the tunnel exposure on port 9444", raw)
		}
	})
}
