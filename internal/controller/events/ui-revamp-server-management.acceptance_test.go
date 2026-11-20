package events

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// A49: a change of an action declaring Rechecks that reaches a terminal
// state or needs_repair rechecks each named module on the change's host, for
// a controller-side composite and an ingested agent task alike; nothing else
// does.
func TestA49_RechecksFireOnTerminalStatesAndNeedsRepairOnly(t *testing.T) {
	for _, state := range []string{core.StateCompleted, core.StateConfirmed, core.StateReverted, core.StateFailed, core.StateNeedsRepair} {
		for _, kind := range []string{"controller-composite", "agent-task"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				rec := &recordRechecker{got: make(chan struct{}, 4)}
				f := newWatchFixture(t, rec)
				f.firstPoll()
				if kind == "controller-composite" {
					f.ctrlChange("qa-c1", "tre.thing.compose", "host-9", state)
					f.poll()
				} else {
					f.agentChange("host-9", "qa-a1", "tre.thing.task", state)
				}
				if calls := rec.settled(true); len(calls) != 1 || calls[0] != "host-9:docker" {
					t.Fatalf("calls = %v, want one recheck of docker on host-9", calls)
				}
			})
		}
	}
	for _, state := range []string{core.StateApplying, core.StateAwaitingConfirmation} {
		for _, kind := range []string{"controller-composite", "agent-task"} {
			t.Run("no recheck/"+kind+"/"+state, func(t *testing.T) {
				rec := &recordRechecker{got: make(chan struct{}, 4)}
				f := newWatchFixture(t, rec)
				f.firstPoll()
				if kind == "controller-composite" {
					f.ctrlChange("qa-c2", "tre.thing.compose", "host-9", state)
					f.poll()
				} else {
					f.agentChange("host-9", "qa-a2", "tre.thing.task", state)
				}
				if calls := rec.settled(false); len(calls) != 0 {
					t.Fatalf("a change in %s was rechecked: %v", state, calls)
				}
			})
		}
	}
}
