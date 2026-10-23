package dispatch

import "context"

// admitMutation asks Config.Quarantine whether a mutation may run. A
// restored controller suspends every action apply and every change
// mutation until its quarantine is released (spec 10); plans, reads,
// confirm and revert stay admitted.
func (d *Dispatcher) admitMutation(ctx context.Context) error {
	if d.cfg.Quarantine == nil {
		return nil
	}
	return d.cfg.Quarantine(ctx)
}
