package firewall

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

func TestCaptureRecordsTheLogicalState(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	gc := &core.GuardContext{AgentPeers: []netip.Addr{netip.MustParseAddr("192.0.2.7")}, Complete: true}
	raw, err := capture(ctx, h.env(), h.deps(), gc, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := getSnapshot(ctx, h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := append(append([]string(nil), fwstate.StateFiles...), "/etc/ufw/sysctl.conf")
	if len(s.Entries) != len(wantPaths) {
		t.Fatalf("%d entries, want %d", len(s.Entries), len(wantPaths))
	}
	for i, e := range s.Entries {
		if e.Path != wantPaths[i] {
			t.Errorf("entry %d is %s, want %s", i, e.Path, wantPaths[i])
		}
		data, err := os.ReadFile(filepath.Join(h.root, e.Path))
		if err != nil {
			t.Fatal(err)
		}
		if !e.Present || string(e.Data) != string(data) || e.Mode != 0o600 {
			t.Errorf("entry %s = present %v mode %o, %d bytes; want the file", e.Path, e.Present, e.Mode, len(e.Data))
		}
		if s.Hashes[e.Path] != fileHash(e) {
			t.Errorf("hash of %s = %s, want %s", e.Path, s.Hashes[e.Path], fileHash(e))
		}
	}
	if len(s.Hashes) != len(wantPaths) {
		t.Errorf("%d hashes, want %d", len(s.Hashes), len(wantPaths))
	}
	defaults := dockerfw.DefaultSettings()
	want := snapshotState{Enabled: true, Sysctl: h.sysctl, Guard: gc, DockerSettings: &defaults}
	if !reflect.DeepEqual(s.State, want) {
		t.Errorf("state = %+v, want %+v", s.State, want)
	}
	if s.Actor != "user:u1" || s.System || !s.Baseline || s.ChangeID != "" || s.PostHashes != nil {
		t.Errorf("snapshot = actor %s system %v baseline %v change %q post %v", s.Actor, s.System, s.Baseline, s.ChangeID, s.PostHashes)
	}
	if !s.CreatedAt.Equal(h.clock.Now()) {
		t.Errorf("created at %v, want %v", s.CreatedAt, h.clock.Now())
	}
}

func TestCaptureRepairBeforeImage(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	raw, err := capture(ctx, h.env(), h.deps(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	s, err := getSnapshot(ctx, h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.State.Image, takeImage(h.loaded)) {
		t.Errorf("before-image = %+v, want the loaded state's", s.State.Image)
	}
	if s.State.Guard != nil {
		t.Errorf("guard = %+v, want none", s.State.Guard)
	}
}

func TestCaptureAbsentFileAndSystemActor(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	if err := os.Remove(filepath.Join(h.root, "/etc/ufw/after6.rules")); err != nil {
		t.Fatal(err)
	}
	env := h.env()
	env.Principal.Actor = "system:firewall.reconcile"
	raw, err := capture(ctx, env, h.deps(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := getSnapshot(ctx, h.db, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !s.System {
		t.Error("a firewall background task's snapshot is not system")
	}
	if e := s.Entries[5]; e.Path != "/etc/ufw/after6.rules" || e.Present || s.Hashes[e.Path] != "absent" {
		t.Errorf("absent file recorded as %+v, hash %s", e, s.Hashes[e.Path])
	}
	for actor, want := range map[string]bool{
		"system:firewall.reconcile": true, "system:recovery": false, "system:revert-timer": false,
		"user:u1": false, "system:firewallx.a": false,
	} {
		if got := systemActor(actor); got != want {
			t.Errorf("systemActor(%q) = %v, want %v", actor, got, want)
		}
	}
}

func TestCaptureFailsWithoutSaving(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	h.loadErr = errors.New("save failed")
	if _, err := capture(ctx, h.env(), h.deps(), nil, false); err == nil {
		t.Error("capture succeeded without the loaded state")
	}
	h.loadErr = nil
	if err := os.Remove(filepath.Join(h.root, "/etc/ufw/ufw.conf")); err != nil {
		t.Fatal(err)
	}
	if _, err := capture(ctx, h.env(), h.deps(), nil, false); err == nil {
		t.Error("capture succeeded without ufw.conf")
	}
	if ids := stTestIDs(t, h.db); len(ids) != 0 {
		t.Errorf("a failed capture saved %v", ids)
	}
}

func TestSnapshotEntries(t *testing.T) {
	files := map[string]core.RestoreEntry{}
	for _, p := range fwstate.StateFiles {
		files[p] = core.RestoreEntry{Path: p, Present: true}
	}
	entries, err := snapshotEntries(fwstate.Config{Files: maps.Clone(files)})
	if err != nil || len(entries) != len(fwstate.StateFiles) {
		t.Errorf("without a sysctl file: %d entries, %v", len(entries), err)
	}
	missing := maps.Clone(files)
	delete(missing, "/etc/ufw/user6.rules")
	if _, err := snapshotEntries(fwstate.Config{Files: missing}); err == nil {
		t.Error("a configuration without user6.rules gave entries")
	}
	foreign := maps.Clone(files)
	foreign["/etc/sysctl.conf"] = core.RestoreEntry{Path: "/etc/sysctl.conf", Present: true}
	if _, err := snapshotEntries(fwstate.Config{Files: foreign}); err == nil {
		t.Error("a file outside /etc/ufw became an entry")
	}
}

func TestAttachAndPostHashes(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	env, d := h.env(), h.deps()
	raw, err := capture(ctx, env, d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	id, err := attach(ctx, env)
	if err != nil || id != string(raw) {
		t.Fatalf("attach = %s, %v; want %s", id, err, raw)
	}
	noChange := env
	noChange.ChangeID = ""
	if _, err := attach(ctx, noChange); err == nil {
		t.Error("attach without a change id succeeded")
	}
	s := h.state()
	s.Rules4 = s.Rules4[:1]
	h.setState(s)
	if err := recordPostHashes(ctx, env, d, id); err != nil {
		t.Fatal(err)
	}
	got, err := getSnapshot(ctx, h.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if want := fileHashes(h.config().Files); !reflect.DeepEqual(got.PostHashes, want) {
		t.Errorf("post hashes = %v, want %v", got.PostHashes, want)
	}
	if got.PostHashes["/etc/ufw/user.rules"] == got.Hashes["/etc/ufw/user.rules"] {
		t.Error("the post-apply hash of a changed file equals its capture hash")
	}
	if got.ChangeID != "c1" {
		t.Errorf("attached to %q, want c1", got.ChangeID)
	}
}
