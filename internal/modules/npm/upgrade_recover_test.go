package npm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	urChange   = "c1"
	urResource = "npm.instance:image"
	urNewID    = "new1"
)

var (
	urFrom = "jc21/nginx-proxy-manager:2.13.4@sha256:" + strings.Repeat("a", 64)
	urTo   = "jc21/nginx-proxy-manager:2.13.5@sha256:" + strings.Repeat("b", 64)
	urData = []byte("archive of /data")
	urCert = []byte("archive of /etc/letsencrypt")
)

func urSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// urFixture is a host whose NPM container runs urFrom, with the transaction rows and archives a test adds.
type urFixture struct {
	t    *testing.T
	h    *thHost
	inst thInstance
	n    nm
}

func urSetup(t *testing.T) *urFixture {
	t.Helper()
	h := newTHHost(t)
	f := &urFixture{t: t, h: h, inst: h.deployed(), n: h.nm()}
	f.setImage(f.inst.ContainerID, urFrom)
	return f
}

// handler is the upgrade's handler; it fails the test when the undo or the recovery handler is missing.
func (f *urFixture) handler() core.ErasedHandler {
	f.t.Helper()
	h := f.n.instanceUpgrade().Handler
	if h.Inverse == nil || h.Recover == nil {
		f.t.Fatalf("handler has Inverse %v and Recover %v; want both", h.Inverse != nil, h.Recover != nil)
	}
	return h
}

func (f *urFixture) setImage(id, ref string) {
	f.t.Helper()
	c, ok := f.h.docker.container(id)
	if !ok {
		f.t.Fatalf("container %s is not on the fake Docker", id)
	}
	c.Image, c.Config.Image = ref, ref
	f.h.docker.putContainer(c)
}

func (f *urFixture) setRunning(id string, running bool) {
	f.t.Helper()
	c, ok := f.h.docker.container(id)
	if !ok {
		f.t.Fatalf("container %s is not on the fake Docker", id)
	}
	c.State.Running = running
	f.h.docker.putContainer(c)
}

// replace removes the original container and, when newImage is not empty, puts the container urNewID running
// newImage under the NPM container's name, as an upgrade that reached recreating leaves the host.
func (f *urFixture) replace(newImage string) {
	f.t.Helper()
	if err := f.h.docker.RemoveContainer(context.Background(), f.inst.ContainerID, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
		f.t.Fatal(err)
	}
	if newImage == "" {
		return
	}
	f.h.docker.putContainer(thContainer(containerName, func(c *dockerapi.ContainerJSON) {
		c.ID, c.Image, c.Config.Image, c.State.Running = urNewID, newImage, newImage, true
	}))
}

// archives writes the two verified archives to the backup directory and returns their references.
func (f *urFixture) archives() []archiveRef {
	f.t.Helper()
	refs := []archiveRef{
		{Volume: dataVolume, File: urChange + "-data.tar", SHA256: urSum(urData)},
		{Volume: certVolume, File: urChange + "-letsencrypt.tar", SHA256: urSum(urCert)},
	}
	f.h.files[backupDir+"/"+refs[0].File] = urData
	f.h.files[backupDir+"/"+refs[1].File] = urCert
	f.h.managed[backupDir+"/"+refs[0].File] = urData
	f.h.managed[backupDir+"/"+refs[1].File] = urCert
	return refs
}

// record stores the transaction of change urChange in phase. insertUpgrade always stores phase recorded with no
// archives, so the phase, result and archives are written by setUpgradePhase.
func (f *urFixture) record(phase, result string, archives []archiveRef) {
	f.t.Helper()
	ctx := context.Background()
	err := insertUpgrade(ctx, f.h.db, upgradeRow{
		ChangeID: urChange, FromImage: urFrom, ToImage: urTo, Config: containerConfig{Create: instanceCreate(urFrom), Name: containerName},
		UpdatedAt: f.h.clock.Now(),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := setUpgradePhase(ctx, f.h.db, urChange, phase, result, archives, f.h.clock.Now()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *urFixture) phase() string {
	f.t.Helper()
	u, ok, err := loadUpgrade(context.Background(), f.h.db.Read(), urChange)
	if err != nil || !ok {
		f.t.Fatalf("upgrade %s: %v, %v", urChange, ok, err)
	}
	return u.Phase
}

func (f *urFixture) recover() (core.RecoveryOutcome, error) {
	f.t.Helper()
	return f.handler().Recover(context.Background(), f.h.env(urChange))
}

func (f *urFixture) inverse(resource string) error {
	f.t.Helper()
	_, err := f.handler().Inverse(context.Background(), f.h.env(urChange), core.Effect{ChangeID: urChange, Resource: resource})
	return err
}

// callsSince are the Docker calls made after mark, which is the call count taken earlier.
func (f *urFixture) callsSince(mark int) []string { return f.h.docker.calls()[mark:] }

func (f *urFixture) mark() int { return len(f.h.docker.calls()) }

func urCalled(calls []string, method string) bool {
	for _, c := range calls {
		if strings.Contains(c, method) {
			return true
		}
	}
	return false
}

// restored fails the test unless the NPM container is the original again: a new container running urFrom, not the
// container urNewID the upgrade created, with both volumes in place and the transaction rolled back.
func (f *urFixture) restored() {
	f.t.Helper()
	c, ok := f.h.docker.container(containerName)
	if !ok || c.Config.Image != urFrom || !c.State.Running || c.ID == urNewID {
		f.t.Errorf("container %+v (found %v), want a new running container of %s", c, ok, urFrom)
	}
	if _, ok := f.h.docker.container(urNewID); ok {
		f.t.Errorf("the upgrade's container %s is still there", urNewID)
	}
	for _, v := range []string{dataVolume, certVolume} {
		if _, ok := f.h.docker.volume(v); !ok {
			f.t.Errorf("volume %s is missing", v)
		}
	}
	if got := f.phase(); got != "rolled_back" {
		f.t.Errorf("phase %q, want rolled_back", got)
	}
}

func TestUrHandlerHasInverseAndRecover(t *testing.T) {
	urSetup(t).handler()
}

// TestUrRecoverBeforeRecreation: until the original container is removed, recovery only starts it again, drops an
// archive that was being written, and leaves a verified archive alone.
func TestUrRecoverBeforeRecreation(t *testing.T) {
	tests := []struct {
		name       string
		phase      string
		stopped    bool
		partial    bool // a half-written archive lies in the backup directory
		archived   bool // the transaction lists verified archives
		wantStart  bool
		wantKeeps  bool // the archives are still in the backup directory afterwards
		wantNoFile bool // no archive of the change is left
	}{
		{name: "recorded, the original still runs", phase: "recorded"},
		{name: "recorded, the original is stopped", phase: "recorded", stopped: true, wantStart: true},
		{name: "stopped", phase: "stopped", stopped: true, wantStart: true},
		{name: "archiving drops the partial archive", phase: "archiving", stopped: true, partial: true, wantStart: true, wantNoFile: true},
		{name: "archived keeps the verified archives", phase: "archived", stopped: true, archived: true, wantStart: true, wantKeeps: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := urSetup(t)
			if tc.stopped {
				f.setRunning(f.inst.ContainerID, false)
			}
			var refs []archiveRef
			if tc.partial || tc.archived {
				refs = f.archives()
			}
			if !tc.archived {
				refs = nil
			}
			f.record(tc.phase, "", refs)
			mark := f.mark()
			out, err := f.recover()
			if err != nil || out.State != core.RecoveryRolledBack {
				t.Fatalf("outcome %+v, %v; want rolled_back", out, err)
			}
			calls := f.callsSince(mark)
			if urCalled(calls, "StartContainer") != tc.wantStart {
				t.Errorf("calls %v: StartContainer called = %v, want %v", calls, !tc.wantStart, tc.wantStart)
			}
			for _, destructive := range []string{"RemoveContainer", "CreateContainer", "RemoveVolume", "CreateVolume", "CopyToContainer"} {
				if urCalled(calls, destructive) {
					t.Errorf("calls %v include %s, want the original left in place", calls, destructive)
				}
			}
			if c, ok := f.h.docker.container(f.inst.ContainerID); !ok || !c.State.Running {
				t.Errorf("the original container %+v (found %v), want it running", c, ok)
			}
			if got := f.phase(); got != "rolled_back" {
				t.Errorf("phase %q, want rolled_back", got)
			}
			_, dataKept := f.h.managed[backupDir+"/"+urChange+"-data.tar"]
			_, certKept := f.h.managed[backupDir+"/"+urChange+"-letsencrypt.tar"]
			if tc.wantKeeps && (!dataKept || !certKept) {
				t.Errorf("archives kept: data %v, letsencrypt %v, want both", dataKept, certKept)
			}
			if tc.wantNoFile && (dataKept || certKept) {
				t.Errorf("archives left: data %v, letsencrypt %v, want none", dataKept, certKept)
			}
			mark = f.mark()
			again, err := f.recover()
			if err != nil || again.State != core.RecoveryRolledBack || len(f.callsSince(mark)) != 0 {
				t.Errorf("second recovery %+v, %v after Docker calls %v; want rolled_back again without touching Docker", again, err, f.callsSince(mark))
			}
		})
	}
}

// TestUrRecoverWithoutARecord: a crash before the transaction was recorded changed nothing on the host.
func TestUrRecoverWithoutARecord(t *testing.T) {
	f := urSetup(t)
	mark := f.mark()
	out, err := f.recover()
	if err != nil || out.State != core.RecoveryRolledBack || len(f.callsSince(mark)) != 0 {
		t.Errorf("outcome %+v, %v after Docker calls %v; want rolled_back without touching Docker", out, err, f.callsSince(mark))
	}
}

// TestUrRecoverTerminalPhases: a finished transaction is reported as it ended and the host is not touched.
func TestUrRecoverTerminalPhases(t *testing.T) {
	tests := []struct {
		name, phase, result string
		want                string
	}{
		{"done", "done", "", core.RecoveryCompleted},
		{"rolled back", "rolled_back", "", core.RecoveryRolledBack},
		{"failed", "failed", "the API did not answer", core.RecoveryNeedsRepair},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := urSetup(t)
			f.record(tc.phase, tc.result, nil)
			mark := f.mark()
			out, err := f.recover()
			if err != nil || out.State != tc.want || len(f.callsSince(mark)) != 0 {
				t.Fatalf("outcome %+v, %v after Docker calls %v; want %s without touching Docker", out, err, f.callsSince(mark), tc.want)
			}
			switch tc.phase {
			case "done":
				var got Upgraded
				if err := json.Unmarshal(out.Output, &got); err != nil || got != (Upgraded{FromImage: urFrom, ToImage: urTo}) {
					t.Errorf("output %s (%v), want the upgrade from %s to %s", out.Output, err, urFrom, urTo)
				}
			case "failed":
				if !strings.Contains(out.Reason, urTo) || !strings.Contains(out.Reason, tc.result) {
					t.Errorf("reason %q, want the target image and the transaction's result", out.Reason)
				}
			}
		})
	}
}

// TestUrRecoverAfterRecreation: from recreating on, the host is brought back to the original by the rollback,
// whether or not the replacement container exists yet, and also when the replacement runs the same image.
func TestUrRecoverAfterRecreation(t *testing.T) {
	tests := []struct {
		name  string
		phase string
		newTo string // the image of the replacement container; empty: none was created yet
	}{
		{"recreating, the replacement exists", "recreating", urTo},
		{"recreating, the original was removed and nothing replaced it", "recreating", ""},
		{"waiting", "waiting", urTo},
		{"waiting, the replacement runs the same image", "waiting", urFrom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := urSetup(t)
			f.replace(tc.newTo)
			f.record(tc.phase, "", f.archives())
			mark := f.mark()
			out, err := f.recover()
			if err != nil || out.State != core.RecoveryRolledBack {
				t.Fatalf("outcome %+v, %v; want rolled_back", out, err)
			}
			if !urCalled(f.callsSince(mark), "CopyToContainer") {
				t.Errorf("calls %v, want the archives copied back", f.callsSince(mark))
			}
			f.restored()
		})
	}
}

// TestUrRecoverWhenTheRollbackFails: a rollback that cannot finish leaves the transaction open for the operator.
func TestUrRecoverWhenTheRollbackFails(t *testing.T) {
	f := urSetup(t)
	f.replace(urTo)
	f.record("waiting", "", f.archives())
	f.h.docker.Fail("CreateContainer", errors.New("create refused"))
	out, err := f.recover()
	if err != nil || out.State != core.RecoveryNeedsRepair || !strings.Contains(out.Reason, urTo) {
		t.Fatalf("outcome %+v, %v; want needs_repair naming %s", out, err, urTo)
	}
	if got := f.phase(); got != "failed" {
		t.Errorf("phase %q, want failed", got)
	}
}

// TestUrRecoverStartFailures: a Docker error while starting the original is returned, so that the engine retries
// the recovery; an original that is gone needs the operator.
func TestUrRecoverStartFailures(t *testing.T) {
	t.Run("the start fails", func(t *testing.T) {
		f := urSetup(t)
		f.setRunning(f.inst.ContainerID, false)
		f.record("stopped", "", nil)
		f.h.docker.Fail("StartContainer", errors.New("start refused"))
		if _, err := f.recover(); err == nil || !strings.Contains(err.Error(), "start refused") {
			t.Errorf("error %v, want the start's error", err)
		}
		if got := f.phase(); got != "stopped" {
			t.Errorf("phase %q, want stopped: the original did not run again", got)
		}
	})
	t.Run("the original is gone", func(t *testing.T) {
		f := urSetup(t)
		f.replace("")
		f.record("archived", "", f.archives())
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryNeedsRepair || !strings.Contains(out.Reason, containerName) {
			t.Errorf("outcome %+v, %v; want needs_repair naming %s", out, err, containerName)
		}
	})
}

// TestUrInverse: undoing a finished upgrade restores the original, undoing a rolled-back one does nothing, and
// anything else is refused before the host is touched.
func TestUrInverse(t *testing.T) {
	t.Run("done restores the original", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		f.record("done", "", f.archives())
		if err := f.inverse(urResource); err != nil {
			t.Fatal(err)
		}
		f.restored()
	})
	t.Run("rolled back is already undone", func(t *testing.T) {
		f := urSetup(t)
		f.record("rolled_back", "", nil)
		mark := f.mark()
		if err := f.inverse(urResource); err != nil || len(f.callsSince(mark)) != 0 {
			t.Errorf("error %v after Docker calls %v, want none of either", err, f.callsSince(mark))
		}
	})
	for _, phase := range []string{"recorded", "archived", "waiting", "failed"} {
		t.Run(phase+" has nothing to undo", func(t *testing.T) {
			f := urSetup(t)
			f.record(phase, "", f.archives())
			mark := f.mark()
			err := f.inverse(urResource)
			if err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), phase) || len(f.callsSince(mark)) != 0 {
				t.Errorf("error %v (definite %v) after Docker calls %v, want a definite refusal naming %q", err, core.IsDefinite(err), f.callsSince(mark), phase)
			}
		})
	}
	t.Run("no transaction", func(t *testing.T) {
		f := urSetup(t)
		if err := f.inverse(urResource); err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite refusal", err)
		}
	})
	t.Run("another resource", func(t *testing.T) {
		f := urSetup(t)
		f.record("done", "", f.archives())
		if err := f.inverse("npm.container:abc"); err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite refusal", err)
		}
	})
	t.Run("an archive was pruned", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		refs := f.archives()
		delete(f.h.files, backupDir+"/"+refs[1].File)
		f.record("done", "", refs)
		mark := f.mark()
		err := f.inverse(urResource)
		if err == nil || !core.IsDefinite(err) || errcode.From(err).Code != errcode.NotFound || len(f.callsSince(mark)) != 0 {
			t.Errorf("error %v after Docker calls %v, want a definite %s refusal before the host is touched", err, f.callsSince(mark), errcode.NotFound)
		}
		if c, ok := f.h.docker.container(containerName); !ok || c.ID != urNewID {
			t.Errorf("container %+v (found %v), want the upgraded one left in place", c, ok)
		}
	})
	t.Run("an archive does not match its checksum", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		refs := f.archives()
		f.h.files[backupDir+"/"+refs[0].File] = []byte("tampered")
		f.record("done", "", refs)
		mark := f.mark()
		err := f.inverse(urResource)
		if err == nil || !core.IsDefinite(err) || len(f.callsSince(mark)) != 0 {
			t.Errorf("error %v after Docker calls %v, want a definite refusal before the host is touched", err, f.callsSince(mark))
		}
	})
}

// TestW10NuInverseArchiveReadErrorsStartWithThePackageName: an archive that cannot be read is a definite refusal
// whose text starts with "npm:".
func TestW10NuInverseArchiveReadErrorsStartWithThePackageName(t *testing.T) {
	f := urSetup(t)
	f.replace(urTo)
	f.record("done", "", f.archives())
	f.n.d.readFile = func(string) ([]byte, error) { return nil, errors.New("disk exploded") }
	mark := f.mark()
	err := f.inverse(urResource)
	if err == nil || !core.IsDefinite(err) || !strings.HasPrefix(err.Error(), "npm:") || !strings.Contains(err.Error(), "disk exploded") ||
		len(f.callsSince(mark)) != 0 {
		t.Errorf("error %v (definite %v) after Docker calls %v, want a definite refusal that starts with %q before the host is touched",
			err, core.IsDefinite(err), f.callsSince(mark), "npm:")
	}
}
