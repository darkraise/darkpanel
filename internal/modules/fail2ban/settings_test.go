package fail2ban

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestSettingsFile(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	prev := []byte("# the previous settings\n")
	r.files[SettingsFile] = prev
	h := settingsHandler{m: r.module()}
	in := SettingsInput{Settings: settings()}
	unresolved := r.env(adminP)
	unresolved.ControllerAddrs, unresolved.ControllerAddrsErr = nil, errors.New("no such host")
	if _, err := h.Plan(ctx, unresolved, in); err == nil || !strings.Contains(err.Error(), "controller's addresses") {
		t.Errorf("plan with the controller unresolved = %v; want a refusal, not a list without it", err)
	}
	plan, err := h.Plan(ctx, r.env(adminP), in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Diff, core.DiffLine{Op: "+", Text: "maxretry = 5"}) || len(plan.Guards) != 0 {
		t.Errorf("plan %+v", plan)
	}
	out, err := h.Apply(ctx, r.env(adminP), in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(r.files[SettingsFile]), testdata(t, "zz-darkpanel.local"); got != want {
		t.Errorf("settings file\n%s\nwant\n%s", got, want)
	}
	if !slices.Equal(out.IgnoreIP, settings().IgnoreIP) {
		t.Errorf("output ignore %v", out.IgnoreIP)
	}
	test, reload := "run "+key(platform.Fail2banClientPath, platform.Fail2banTest()), "run "+key(platform.Fail2banClientPath, platform.Fail2banReload())
	if i, j := slices.Index(r.events, test), slices.Index(r.events, reload); i < 0 || j < i {
		t.Errorf("events %v: want -t, then reload", r.events)
	}
	if len(r.effects.intended) != 1 || !slices.Equal(r.effects.made, []int{1}) || *decode(t, r.effects.intended[0].Before) != string(prev) {
		t.Errorf("effects %+v", r.effects)
	}
	if err := h.Verify(ctx, r.env(adminP), in, out); err != nil {
		t.Errorf("verify: %v", err)
	}

	// The status read reports the settings back, its own entries after the
	// fixed ones.
	st, err := statusReader{m: r.module()}.Read(ctx, r.env(adminP), struct{}{})
	if err != nil || st.Settings == nil || !slices.Equal(st.Settings.IgnoreIP, settings().IgnoreIP) || st.Settings.MaxRetry != 5 {
		t.Errorf("status settings %+v, %v", st.Settings, err)
	}
	if !slices.Equal(st.IgnoreFixed, settings().IgnoreIP[:5]) {
		t.Errorf("fixed entries %v", st.IgnoreFixed)
	}

	// Undo puts the previous file back and reloads.
	r.events = nil
	if _, err := h.Inverse(ctx, r.env(adminP), r.effects.intended[0]); err != nil {
		t.Fatal(err)
	}
	if string(r.files[SettingsFile]) != string(prev) || !slices.Contains(r.events, reload) {
		t.Errorf("undo left %q, events %v", r.files[SettingsFile], r.events)
	}

	// Each fixed entry is required; an entry not in canonical form is refused.
	for _, fixed := range settings().IgnoreIP[:5] {
		bad := in
		bad.IgnoreIP = slices.DeleteFunc(slices.Clone(in.IgnoreIP), func(e string) bool { return e == fixed })
		_, err := h.Plan(ctx, r.env(adminP), bad)
		wantCode(t, "without "+fixed, err, errcode.InvalidInput)
	}
	for _, entry := range []string{"203.0.113.7/24", "example.com", "fe80::1%eth0", "010.0.0.1"} {
		bad := in
		bad.IgnoreIP = append(slices.Clone(in.IgnoreIP), entry)
		_, err := h.Plan(ctx, r.env(adminP), bad)
		wantCode(t, "extra entry "+entry, err, errcode.InvalidInput)
	}
	for name, edit := range map[string]func(*Settings){
		"bantime 0":            func(s *Settings) { s.BantimeMinutes = 0 },
		"bantime over 30 days": func(s *Settings) { s.BantimeMinutes = 43201 },
		"findtime over a day":  func(s *Settings) { s.FindtimeMinutes = 1441 },
		"maxretry 21":          func(s *Settings) { s.MaxRetry = 21 },
	} {
		bad := in
		edit(&bad.Settings)
		_, err := h.Plan(ctx, r.env(adminP), bad)
		wantCode(t, name, err, errcode.InvalidInput)
	}

	// A file fail2ban-client -t refuses is replaced by the previous one, and
	// nothing reloads.
	r.files[SettingsFile] = prev
	r.effects = &effects{}
	r.events = nil
	r.fail(platform.Fail2banClientPath, platform.Fail2banTest(), 255, "ERROR  Failed during configuration: bad value")
	_, err = h.Apply(ctx, r.env(adminP), in)
	if err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), "bad value") {
		t.Errorf("apply with -t failing: %v, want a definite error with fail2ban's message", err)
	}
	if string(r.files[SettingsFile]) != string(prev) || slices.Contains(r.events, reload) || !slices.Equal(r.effects.skipped, []int{1}) {
		t.Errorf("after -t failed: file %q, events %v, effects %+v", r.files[SettingsFile], r.events, r.effects)
	}
	// Without a previous file, the refused one is removed.
	delete(r.files, SettingsFile)
	if _, err := h.Apply(ctx, r.env(adminP), in); err == nil {
		t.Error("apply with -t failing succeeded")
	}
	if _, ok := r.files[SettingsFile]; ok {
		t.Error("the refused file was left in place")
	}
}

func TestRecidiveGuards(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	h := settingsHandler{m: r.module()}
	in := SettingsInput{Settings: settings()}
	in.Recidive = true

	plan, err := h.Plan(ctx, r.env(adminP), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Guards) != 1 || plan.Guards[0].ID != GuardSelfBan || plan.Guards[0].OK || !strings.Contains(plan.Guards[0].Message, "192.0.2.1") {
		t.Errorf("guards %+v, want a failing self-ban guard naming 192.0.2.1", plan.Guards)
	}
	over := in
	over.Overrides = []string{GuardSelfBan}
	if plan, err := h.Plan(ctx, r.env(adminP), over); err != nil || !plan.Guards[0].OK || !plan.Guards[0].Overridden {
		t.Errorf("overridden: %+v, %v", plan.Guards, err)
	}
	added := in
	added.IgnoreIP = append(slices.Clone(in.IgnoreIP), "192.0.2.0/24")
	if plan, err := h.Plan(ctx, r.env(adminP), added); err != nil || !plan.Guards[0].OK || plan.Guards[0].Overridden {
		t.Errorf("caller ignored: %+v, %v", plan.Guards, err)
	}
	proxied := adminP
	proxied.Source = "192.0.2.1 via 172.18.0.2"
	if plan, err := h.Plan(ctx, r.env(proxied), in); err != nil || plan.Guards[0].OK {
		t.Errorf("proxied caller: %+v, %v", plan.Guards, err)
	}
	if plan, err := h.Plan(ctx, r.env(core.Principal{Actor: "local:root@h1", Scope: core.ScopeAdmin, Source: "socket"}), in); err != nil || !plan.Guards[0].OK {
		t.Errorf("socket caller: %+v, %v", plan.Guards, err)
	}
	off := in
	off.Recidive = false
	if plan, err := h.Plan(ctx, r.env(adminP), off); err != nil || len(plan.Guards) != 0 {
		t.Errorf("recidive off: %+v, %v", plan.Guards, err)
	}

	// Elsewhere recidive keeps its every-port action; on the controller's
	// own host it bans the SSH ports only.
	if _, err := h.Apply(ctx, r.env(adminP), over); err != nil {
		t.Fatal(err)
	}
	if got := string(r.files[SettingsFile]); !strings.HasSuffix(got, "\n[recidive]\nenabled = true\nbackend = auto\n") {
		t.Errorf("recidive section of\n%s", got)
	}
	r.units[controllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
	r.answer(platform.SshdPath, platform.SshdEffective(), "port 22\nport 2222\n")
	if _, err := h.Apply(ctx, r.env(adminP), over); err != nil {
		t.Fatal(err)
	}
	got := string(r.files[SettingsFile])
	if !strings.HasSuffix(got, "\n[recidive]\nenabled = true\nbackend = auto\nbanaction = nftables-multiport\nport = 22,2222\n") ||
		!strings.Contains(got, "[sshd]\nenabled = true\nport = 22,2222\n") {
		t.Errorf("controller host settings\n%s", got)
	}
}
