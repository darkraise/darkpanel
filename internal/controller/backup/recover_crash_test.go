package backup

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"testing"
)

func rcSide(before bool) string {
	if before {
		return "before"
	}
	return "after"
}

// TestRcCrashMatrix stops one restore at every Ops call, on both sides of
// it, and recovers. The end state must be exactly the original installation
// when the crash came before phase 6 began, and the restored, quarantined
// installation from then on; a second recovery changes nothing.
func TestRcCrashMatrix(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)

	counter := newCrashOps(OSOps(), math.MaxInt, false)
	h := l.host
	h.Ops = counter
	if _, err := Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	total := counter.Calls()
	rcAssertRestored(t, l, bk, pristine, tx)
	t.Logf("one restore makes %d Ops calls", total)

	step := 1
	if testing.Short() {
		step = 7
	}
	restoredFrom := 0
	for n := 1; n <= total; n += step {
		for _, before := range []bool{true, false} {
			t.Run(fmt.Sprintf("call %03d %s", n, rcSide(before)), func(t *testing.T) {
				rcReset(t, l, pristine)
				rcCrashRestore(t, l, bk, n, before)
				rcAssertNothingLost(t, l, pristine, tx)
				rcAssertAsideBeforeInstall(t, l)

				restored := rcExpectRestored(t, l, pristine)
				switch {
				case restored && restoredFrom == 0:
					restoredFrom = n
				case !restored && restoredFrom != 0:
					t.Errorf("a crash at call %d must roll back although one at call %d completes", n, restoredFrom)
				}

				if err := Recover(context.Background(), l.host); err != nil {
					t.Fatalf("Recover: %v", err)
				}
				rcAssertEnd(t, l, bk, pristine, tx, restored)

				settled := l.digest(t)
				if err := Recover(context.Background(), l.host); err != nil {
					t.Fatalf("second Recover: %v", err)
				}
				if diff := rtDiff(settled, l.digest(t)); diff != "" {
					t.Errorf("a second Recover changed the installation: %s", diff)
				}
			})
		}
	}
	if restoredFrom == 0 {
		t.Error("no crash point leaves a restore that recovery completes")
	}
}

// TestRcCrashInsideRecover stops recovery itself at every Ops call, on both
// sides of it, for restores that stopped in each phase, and recovers again:
// the end state is the one an uninterrupted recovery reaches.
func TestRcCrashInsideRecover(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)

	first := map[string]int{}
	for _, p := range points {
		key := fmt.Sprintf("%s/%d/%d", p.journal.Phase, rcDone(p.journal, PhaseMovedAside), rcDone(p.journal, PhaseInstalled))
		if _, ok := first[key]; !ok {
			first[key] = p.call
		}
	}
	var samples []int
	for _, call := range first {
		samples = append(samples, call)
	}
	slices.Sort(samples)
	if testing.Short() {
		var thin []int
		for i, call := range samples {
			if i%3 == 0 {
				thin = append(thin, call)
			}
		}
		samples = thin
	}

	for _, s := range samples {
		t.Run(fmt.Sprintf("restore stopped after call %d", s), func(t *testing.T) {
			rcReset(t, l, pristine)
			rcCrashRestore(t, l, bk, s, false)
			crashed := rcSave(t, l)
			restored := rcExpectRestored(t, l, pristine)

			counter := newCrashOps(OSOps(), math.MaxInt, false)
			h := l.host
			h.Ops = counter
			if err := Recover(context.Background(), h); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			for k := 1; k <= counter.Calls(); k++ {
				for _, before := range []bool{true, false} {
					rcReset(t, l, crashed)
					h.Ops = newCrashOps(OSOps(), k, before)
					_ = Recover(context.Background(), h)
					if err := Recover(context.Background(), l.host); err != nil {
						t.Fatalf("recovery stopped at call %d %s, then Recover: %v", k, rcSide(before), err)
					}
					rcAssertEnd(t, l, bk, pristine, tx, restored)
				}
			}
		})
	}
}

// TestRcTwoRestoresWithACrashInTheSecond crashes the second of two restores
// in a row. Until the second completes the first restore's installation and
// its .pre files stay exactly as they were; once it completes, the first
// restore's .pre files are gone, or, when the crash came after the second
// restore's journal was removed, are the files the first restore set aside.
func TestRcTwoRestoresWithACrashInTheSecond(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	rtRestore(t, l, bk)
	first := rcSave(t, l)
	l.host.NewTx = func() string { return tx2 }

	counter := newCrashOps(OSOps(), math.MaxInt, false)
	h := l.host
	h.Ops = counter
	if _, err := Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass)); err != nil {
		t.Fatalf("second Restore: %v", err)
	}
	total := counter.Calls()
	rcAssertRestored(t, l, bk, first, tx2)

	stride := max(1, total/20)
	if testing.Short() {
		stride *= 3
	}
	var calls []int
	for n := 1; n <= total; n += stride {
		calls = append(calls, n)
	}
	if calls[len(calls)-1] != total {
		calls = append(calls, total)
	}
	for _, n := range calls {
		for _, before := range []bool{true, false} {
			t.Run(fmt.Sprintf("call %03d %s", n, rcSide(before)), func(t *testing.T) {
				rcReset(t, l, first)
				rcCrashRestore(t, l, bk, n, before)
				rcAssertNothingLost(t, l, first, tx2)
				js, err := ListJournals(l.paths.ControllerDir)
				if err != nil {
					t.Fatal(err)
				}
				hadJournal := len(js) > 0
				restored := rcExpectRestored(t, l, first)

				if err := Recover(context.Background(), l.host); err != nil {
					t.Fatalf("Recover: %v", err)
				}
				if !restored {
					rcAssertOriginals(t, l, first, tx2, true)
					return
				}
				rcAssertRestored(t, l, bk, first, tx2)
				for _, f := range Targets(l.paths) {
					orig, existed := pristine.files[f]
					old := AsideName(f, tx1)
					switch {
					case !existed:
					case hadJournal && rcExists(old):
						t.Errorf("recovery completed the second restore but left %s", old)
					case rcExists(old) && rcSumAt(old) != rtSum(orig.data):
						t.Errorf("%s no longer holds the original it was set aside with", old)
					}
				}
			})
		}
	}
}
