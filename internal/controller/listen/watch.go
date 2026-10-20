package listen

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// TailscaleWatcher follows the addresses of tailscale0 for a listener (ADR
// 0006); the web and the agent listener each run one. It is not safe for
// concurrent use: its owner serialises Refresh and Addrs.
type TailscaleWatcher struct {
	db      *store.DB
	ifaces  InterfaceAddrs
	log     *slog.Logger
	addrs   []netip.Addr
	unsaved bool // addrs differ from those saved in core_meta
}

// NewTailscaleWatcher starts from the addresses saved in core_meta,
// replaced by those of tailscale0 when that interface exists, so a restart
// serves the tailnet before tailscaled is up. Nil ifaces means
// SystemInterfaceAddrs; a nil log discards.
func NewTailscaleWatcher(ctx context.Context, db *store.DB, ifaces InterfaceAddrs, log *slog.Logger) (*TailscaleWatcher, error) {
	if ifaces == nil {
		ifaces = SystemInterfaceAddrs
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	saved, err := LoadTailscaleAddrs(ctx, db)
	if err != nil {
		return nil, err
	}
	w := &TailscaleWatcher{db: db, ifaces: ifaces, log: log, addrs: saved}
	w.Refresh(ctx)
	return w, nil
}

// Refresh reads tailscale0. An absent interface means tailscaled is not up,
// so the last addresses stay; a present one replaces them, even with none.
// A failed save is retried at every Refresh.
func (w *TailscaleWatcher) Refresh(ctx context.Context) {
	list, present, err := w.ifaces(TailscaleInterface)
	if err != nil {
		w.log.Warn("listen: cannot read the Tailscale addresses", "err", err)
	} else if next := TailscaleAddrs(list); present && !slices.Equal(next, w.addrs) {
		w.log.Info("listen: Tailscale addresses changed", "from", w.addrs, "to", next)
		w.addrs = next
		w.unsaved = true
	}
	if !w.unsaved {
		return
	}
	if err := SaveTailscaleAddrs(ctx, w.db, w.addrs); err != nil {
		w.log.Warn("listen: cannot save the Tailscale addresses", "err", err)
		return
	}
	w.unsaved = false
}

// Addrs returns the current Tailscale addresses, sorted, or nil.
func (w *TailscaleWatcher) Addrs() []netip.Addr { return slices.Clone(w.addrs) }
