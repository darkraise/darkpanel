package firewall

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// unSetup is a fake host with ufw installed, the module over it and an env
// as uninstall builds it (no change, no journal) whose log is returned.
func unSetup(t *testing.T) (*fakeHost, module, core.Env, *bytes.Buffer) {
	t.Helper()
	h, f, _ := ruHost(t)
	h.writeFile(ufwPath, []byte("#!/bin/sh\n"))
	var log bytes.Buffer
	env := core.Env{DB: h.db, Exec: h, Files: h, Clock: h.clock, Log: slog.New(slog.NewTextHandler(&log, nil))}
	return h, module{f: f}, env, &log
}

func unRan(h *fakeHost, from int) []string {
	var out []string
	for _, c := range h.calls[from:] {
		if v, ok := strings.CutPrefix(c, "run /usr/sbin/ufw "); ok {
			out = append(out, v)
		}
	}
	return out
}

// TestUninstallOptions: the module declares the option rules (spec 2.9).
func TestUninstallOptions(t *testing.T) {
	var _ core.Uninstaller = module{}
	got := module{}.UninstallOptions()
	if len(got) != 1 || got[0].Key != "rules" || !reflect.DeepEqual(got[0].Values, []string{"remove", "keep", "baseline"}) ||
		got[0].Default != "remove" || got[0].Help == "" {
		t.Errorf("options %+v, want rules=remove|keep|baseline, default remove", got)
	}
}

// TestUninstallRemove: remove deletes every Darkpanel rule, keeps manual
// rules and reloads an active UFW; run again it does nothing.
func TestUninstallRemove(t *testing.T) {
	ctx := context.Background()
	h, m, env, _ := unSetup(t)
	n := len(h.calls)
	if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "remove"}); err != nil {
		t.Fatal(err)
	}
	if got := unRan(h, n); !reflect.DeepEqual(got, []string{"delete allow in proto tcp from any to any port 22", "reload"}) {
		t.Errorf("ran %q, want the Darkpanel rule deleted and a reload", got)
	}
	if s := h.state(); !reflect.DeepEqual(s.Rules4, []string{fhRule3000v4}) || len(s.Rules6) != 0 {
		t.Errorf("rules %+v %+v, want only the manual rule", s.Rules4, s.Rules6)
	}
	n = len(h.calls)
	if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "remove"}); err != nil || len(unRan(h, n)) != 0 {
		t.Errorf("a second uninstall = %v and ran %q, want nothing", err, unRan(h, n))
	}
}

// TestUninstallKeepAndRefusals: keep runs nothing; a host without UFW has
// nothing to do; an unknown value is an error.
func TestUninstallKeepAndRefusals(t *testing.T) {
	ctx := context.Background()
	h, m, env, _ := unSetup(t)
	n := len(h.calls)
	if err := m.Uninstall(ctx, env, true, map[string]string{"rules": "keep"}); err != nil || len(h.calls) != n {
		t.Errorf("keep = %v and ran %q, want nothing", err, h.calls[n:])
	}
	if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "all"}); err == nil {
		t.Error("rules=all is accepted")
	}
	h2, m2, env2, _ := unSetup(t)
	pfRemove(t, h2.root, ufwPath)
	for _, v := range []string{"remove", "baseline"} {
		if err := m2.Uninstall(ctx, env2, false, map[string]string{"rules": v}); err != nil || len(h2.calls) != 0 {
			t.Errorf("rules=%s without UFW = %v and ran %q, want nothing", v, err, h2.calls)
		}
	}
}

// TestUninstallBaseline: baseline logs the manual rules the restore adds
// or removes, restores the baseline snapshot, and does it again when run
// again; without a baseline it does nothing.
func TestUninstallBaseline(t *testing.T) {
	ctx := context.Background()
	h, m, env, log := unSetup(t)
	if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "baseline"}); err != nil || len(h.calls) != 0 {
		t.Errorf("baseline without a snapshot = %v and ran %q, want nothing", err, h.calls)
	}
	snCapture(t, h, m.f)
	before := h.state()
	s := h.state()
	s.Rules4 = []string{fhRule22v4}
	h.setState(s)

	for range 2 {
		if err := m.Uninstall(ctx, env, false, map[string]string{"rules": "baseline"}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.state(), before) {
			t.Errorf("state %+v, want the baseline's %+v", h.state(), before)
		}
	}
	if !strings.Contains(log.String(), "adds the manual rule") || !strings.Contains(log.String(), "port 3000") {
		t.Errorf("log %q, want the manual rule the restore adds", log.String())
	}
}
