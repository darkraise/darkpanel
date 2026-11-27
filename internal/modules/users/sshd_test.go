package users

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// fakeSshdDefaults are what the fake sshd -T prints for a keyword no file
// sets, prohibit-password under its older name as sshd prints it.
var fakeSshdDefaults = map[string]string{
	"port": "22", "authorizedkeysfile": defaultKeysFile, "pubkeyauthentication": "yes", "authenticationmethods": "any",
	"passwordauthentication": "yes", "kbdinteractiveauthentication": "yes", "permitrootlogin": "without-password",
	"maxauthtries": "6", "logingracetime": "120", "clientaliveinterval": "0", "clientalivecountmax": "3", "x11forwarding": "no",
}

var fakeSshdLists = []string{"allowusers", "allowgroups", "denyusers", "denygroups"}

// liveSshd makes sshd -T print what sshd makes of the rig's files: for
// each keyword the first value in the order sshd reads them (the drop-ins,
// then sshd_config) outside Match blocks, the lists of every file, and the
// defaults for the rest. sshd -t passes.
func (r *rig) liveSshd() {
	k := key(platform.SshdPath, platform.SshdEffective())
	delete(r.answers, k)
	r.live[k] = func() core.CmdResult {
		var paths []string
		for name := range r.files {
			if strings.HasPrefix(name, dropInDir+"/") && strings.HasSuffix(name, ".conf") {
				paths = append(paths, name)
			}
		}
		slices.Sort(paths)
		got := map[string]string{}
		var lists []string
		for _, p := range append(paths, sshdConfig) {
			for line := range strings.Lines(r.files[p]) {
				f := strings.Fields(line)
				if len(f) == 0 || strings.HasPrefix(f[0], "#") {
					continue
				}
				kw := strings.ToLower(f[0])
				if kw == "match" {
					break
				}
				switch {
				case slices.Contains(fakeSshdLists, kw):
					for _, v := range f[1:] {
						lists = append(lists, kw+" "+v)
					}
				case kw != "include" && got[kw] == "":
					got[kw] = strings.ToLower(strings.Join(f[1:], " "))
				}
			}
		}
		var out []string
		for kw, def := range fakeSshdDefaults {
			v := got[kw]
			if v == "" {
				v = def
			}
			if kw == "permitrootlogin" && v == "prohibit-password" {
				v = "without-password"
			}
			out = append(out, kw+" "+v)
		}
		slices.Sort(out)
		return core.CmdResult{Stdout: []byte(strings.Join(append(out, lists...), "\n") + "\n")}
	}
	r.answer(platform.SshdPath, platform.SshdTest(), "")
}

func ptr[T any](v T) *T { return &v }

// sideEffects keeps the events that change the host or test sshd's files.
func sideEffects(events []string) []string {
	var out []string
	for _, e := range events {
		if strings.HasPrefix(e, "write ") || strings.HasPrefix(e, "remove ") || strings.HasPrefix(e, "reload ") ||
			e == "run "+key(platform.SshdPath, platform.SshdTest()) {
			out = append(out, e)
		}
	}
	return out
}

func plan(t *testing.T, eng *lifecycle.Engine, action, input string) core.PlanView {
	t.Helper()
	pv, err := eng.Plan(context.Background(), adminP, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		t.Fatalf("plan %s %s: %v", action, input, err)
	}
	return pv
}

var sshdTestRun = "run " + key(platform.SshdPath, platform.SshdTest())

func TestApplyWritesDropIn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.liveSshd()
	r.files[dropInDir+"/50-cloud-init.conf"] = "PasswordAuthentication yes\n"
	r.keys["alice"] = ed25519Line(t, "alice@laptop")
	eng, _, _ := engine(t, r, &bytes.Buffer{})

	pv := plan(t, eng, "users.sshd.apply", `{"passwordAuthentication":false,"permitRootLogin":"prohibit-password","maxAuthTries":3}`)
	if got, want := diffText(pv.Diff), []string{"+ PasswordAuthentication no", "+ PermitRootLogin prohibit-password", "+ MaxAuthTries 3"}; !slices.Equal(got, want) {
		t.Errorf("diff %q, want %q", got, want)
	}
	if len(pv.Guards) != 1 || !pv.Guards[0].OK || !strings.Contains(pv.Guards[0].Message, "alice (key)") {
		t.Errorf("guards %+v, want users.no_way_in passing on alice's key", pv.Guards)
	}
	r.events = nil
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("apply: %+v, %v", cv, err)
	}
	golden, err := os.ReadFile("testdata/00-darkpanel.conf")
	if err != nil {
		t.Fatal(err)
	}
	if r.files[dropInFile] != string(golden) {
		t.Errorf("drop-in\n%s\nwant\n%s", r.files[dropInFile], golden)
	}
	if got, want := sideEffects(r.events), []string{"write " + dropInFile, sshdTestRun, "reload " + sshUnit}; !slices.Equal(got, want) {
		t.Errorf("apply did %q, want %q", got, want)
	}

	// sshd -T takes the drop-in's values over 50-cloud-init.conf's.
	st, err := sshdGetter{m: r.module()}.Read(ctx, r.env(adminP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	set := SshdSettings{PasswordAuthentication: ptr(false), PermitRootLogin: "prohibit-password", MaxAuthTries: ptr(3)}
	e := st.Effective
	if !reflect.DeepEqual(st.Set, set) || *e.PasswordAuthentication || e.PermitRootLogin != "prohibit-password" || *e.MaxAuthTries != 3 ||
		*e.KbdInteractiveAuthentication || !*e.X11Forwarding || len(st.ReadOnly) != 0 || st.ListsSetBy != "" {
		t.Errorf("sshd.get %+v", st)
	}
	// Verify compares sshd -T with the input.
	h := sshdHandler{m: r.module()}
	if err := h.Verify(ctx, r.env(adminP), SshdInput{SshdSettings: set}, SshdApplied{}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := h.Verify(ctx, r.env(adminP), SshdInput{SshdSettings: SshdSettings{MaxAuthTries: ptr(4)}}, SshdApplied{}); err == nil {
		t.Error("verify passed a value sshd -T does not report")
	}

	// The revert removes the drop-in, tests and reloads.
	r.events = nil
	if cv, err = eng.Revert(ctx, adminP, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert: %+v, %v", cv, err)
	}
	if _, ok := r.files[dropInFile]; ok {
		t.Error("the drop-in is still there after the revert")
	}
	if got, want := sideEffects(r.events), []string{"remove " + dropInFile, sshdTestRun, "reload " + sshUnit}; !slices.Equal(got, want) {
		t.Errorf("revert did %q, want %q", got, want)
	}

	// Under socket activation ssh.service is inactive: nothing reloads.
	r.sshd = "inactive"
	pv = plan(t, eng, "users.sshd.apply", `{"x11Forwarding":false}`)
	r.events = nil
	if cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{ConfirmNow: true}); err != nil || cv.State != core.StateConfirmed {
		t.Fatalf("apply confirmed now: %+v, %v", cv, err)
	}
	if got, want := sideEffects(r.events), []string{"write " + dropInFile, sshdTestRun}; !slices.Equal(got, want) {
		t.Errorf("apply with ssh.service inactive did %q, want %q", got, want)
	}
	_, err = eng.Plan(ctx, adminP, core.ActionRef{Name: "users.sshd.apply", Version: 1}, json.RawMessage(`{"x11Forwarding":false}`), core.PlanOptions{})
	wantCode(t, "the settings the drop-in holds", err, errcode.InvalidInput)
}

func TestApplyRevertsBadConfig(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.liveSshd()
	r.keys["alice"] = ed25519Line(t, "a")
	prior := dropInHeader + "MaxAuthTries 4\n"
	r.files[dropInFile] = prior
	eng, _, _ := engine(t, r, &bytes.Buffer{})
	pv := plan(t, eng, "users.sshd.apply", `{"maxAuthTries":3}`)
	if got, want := diffText(pv.Diff), []string{"- MaxAuthTries 4", "+ MaxAuthTries 3"}; !slices.Equal(got, want) {
		t.Errorf("diff %q, want %q", got, want)
	}
	r.answers[key(platform.SshdPath, platform.SshdTest())] = core.CmdResult{ExitCode: 255,
		Stderr: []byte(dropInFile + " line 2: Bad configuration option: MaxAuthTries")}
	r.events = nil
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err == nil && cv.State != core.StateReverted {
		t.Fatalf("apply of a refused file: %+v", cv)
	}
	if msg := errText(cv, err); !strings.Contains(msg, "the previous "+dropInFile+" is back") || !strings.Contains(msg, "Bad configuration option") {
		t.Errorf("the refusal says %q", msg)
	}
	if r.files[dropInFile] != prior {
		t.Errorf("drop-in after a refused file\n%s\nwant\n%s", r.files[dropInFile], prior)
	}
	if got, want := sideEffects(r.events), []string{"write " + dropInFile, sshdTestRun, "write " + dropInFile}; !slices.Equal(got, want) {
		t.Errorf("apply of a refused file did %q, want %q and no reload", got, want)
	}

	// A first drop-in sshd refuses is removed again.
	delete(r.files, dropInFile)
	pv = plan(t, eng, "users.sshd.apply", `{"maxAuthTries":3}`)
	r.events = nil
	_, _ = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if _, ok := r.files[dropInFile]; ok {
		t.Error("a refused first drop-in stayed")
	}
	if got, want := sideEffects(r.events), []string{"write " + dropInFile, sshdTestRun, "remove " + dropInFile}; !slices.Equal(got, want) {
		t.Errorf("apply of a refused first file did %q, want %q", got, want)
	}
}

// errText is the failure of an apply: its error, or the failed change's.
func errText(cv core.ChangeView, err error) string {
	if err != nil {
		return err.Error()
	}
	if cv.Error != nil {
		return cv.Error.Message
	}
	return ""
}

func TestAllowUsersElsewhere(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.liveSshd()
	r.keys["alice"] = ed25519Line(t, "a")
	r.files[sshdConfig] = fxSshdConfig + "AllowUsers deploy\n"
	h := sshdHandler{m: r.module()}

	st, err := sshdGetter{m: r.module()}.Read(ctx, r.env(adminP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.ReadOnly, []string{"allowUsers", "allowGroups"}) || st.ListsSetBy != sshdConfig || !slices.Equal(st.Effective.AllowUsers, []string{"deploy"}) {
		t.Errorf("sshd.get %+v, want AllowUsers and AllowGroups read-only, set by %s", st, sshdConfig)
	}
	for _, in := range []SshdSettings{{AllowUsers: []string{"alice"}}, {AllowGroups: []string{"sudo"}}} {
		_, err := h.Plan(ctx, r.env(adminP), SshdInput{SshdSettings: in})
		wantCode(t, "a list another file sets", err, errcode.InvalidInput)
		if err == nil || !strings.Contains(err.Error(), sshdConfig) {
			t.Errorf("the refusal %v does not name %s", err, sshdConfig)
		}
	}
	// The guard judges the merged list sshd -T prints: alice is shut out.
	out, err := h.Plan(ctx, r.env(adminP), SshdInput{SshdSettings: SshdSettings{PasswordAuthentication: ptr(false)}})
	if err != nil {
		t.Fatal(err)
	}
	if g := out.Guards[0]; g.OK || !strings.Contains(g.Message, "alice (shut out by the allow and deny lists)") {
		t.Errorf("guard %+v, want alice shut out by AllowUsers deploy", g)
	}

	// A deny list in another drop-in leaves the allow lists to it too.
	r.files[sshdConfig] = fxSshdConfig
	r.files[dropInDir+"/10-site.conf"] = "DenyGroups nologin\n"
	if st, err = (sshdGetter{m: r.module()}).Read(ctx, r.env(adminP), struct{}{}); err != nil || st.ListsSetBy != dropInDir+"/10-site.conf" {
		t.Errorf("sshd.get with DenyGroups in a drop-in: %+v, %v", st, err)
	}

	// With no other list, the input's AllowUsers is the list judged.
	delete(r.files, dropInDir+"/10-site.conf")
	for users, ok := range map[string]bool{"bob": false, "al*": true, "alice@192.0.2.0/24": true} {
		out, err := h.Plan(ctx, r.env(adminP), SshdInput{SshdSettings: SshdSettings{PasswordAuthentication: ptr(false), AllowUsers: []string{users}}})
		if err != nil || out.Guards[0].OK != ok {
			t.Errorf("AllowUsers %s: %+v, %v; want the guard OK %v", users, out, err, ok)
		}
	}
	for _, bad := range [][]string{{"alice bob"}, {"alice", "alice"}, {"-o"}, {"a\nb"}} {
		_, err := h.Plan(ctx, r.env(adminP), SshdInput{SshdSettings: SshdSettings{AllowUsers: bad}})
		wantCode(t, "AllowUsers "+strings.Join(bad, ","), err, errcode.InvalidInput)
	}
}

func TestNoInclude(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	available := map[string]func(context.Context, core.Env) error{}
	for _, a := range r.module().Manifest().Actions {
		available[a.Spec.Name] = a.Spec.Available
	}
	sshdHalf := []string{"users.sshd.get", "users.sshd.apply", "users.sshd.repair"}
	for name, config := range map[string]string{
		"no Include":              "PasswordAuthentication no\n",
		"Include after a setting": "Port 22\nInclude /etc/ssh/sshd_config.d/*.conf\n",
		"another directory":       "Include /etc/ssh/other/*.conf\n",
	} {
		r.files[sshdConfig] = config
		for _, action := range sshdHalf {
			err := available[action](ctx, r.env(adminP))
			if err == nil || err.Error() != sshdConfig+" does not include "+dropInDir+"/*.conf before its other settings" {
				t.Errorf("%s: %s available: %v", name, action, err)
			}
		}
		if err := available["users.key.list"](ctx, r.env(adminP)); err != nil {
			t.Errorf("%s: the key half is unavailable: %v", name, err)
		}
	}
	for _, config := range []string{"# c\n\nInclude sshd_config.d/*.conf\n", "Include=/etc/ssh/extra.conf /etc/ssh/sshd_config.d/*.conf\n"} {
		r.files[sshdConfig] = config
		if err := available["users.sshd.apply"](ctx, r.env(adminP)); err != nil {
			t.Errorf("%q: %v", config, err)
		}
	}
	delete(r.files, sshdConfig)
	if err := available["users.sshd.get"](ctx, r.env(adminP)); err == nil || !strings.Contains(err.Error(), "the OpenSSH server is not installed") {
		t.Errorf("without sshd_config: %v", err)
	}
}

func TestRepair(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.liveSshd()
	r.keys["alice"] = ed25519Line(t, "a")
	eng, _, _ := engine(t, r, &bytes.Buffer{})
	pv := plan(t, eng, "users.sshd.apply", `{"passwordAuthentication":false,"maxAuthTries":3}`)
	confirmed, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{ConfirmNow: true})
	if err != nil || confirmed.State != core.StateConfirmed {
		t.Fatalf("apply: %+v, %v", confirmed, err)
	}
	want := r.files[dropInFile]
	// A later change that was reverted was never confirmed.
	pv = plan(t, eng, "users.sshd.apply", `{"maxAuthTries":5}`)
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cv, err = eng.Revert(ctx, adminP, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert: %+v, %v", cv, err)
	}
	// A value edited in by hand is left out of what Darkpanel set.
	r.files[dropInFile] = "PasswordAuthentication maybe\nMaxAuthTries 9\n"
	if st, err := (sshdGetter{m: r.module()}).Read(ctx, r.env(adminP), struct{}{}); err != nil ||
		!reflect.DeepEqual(st.Set, SshdSettings{MaxAuthTries: ptr(9)}) {
		t.Errorf("sshd.get of a hand-edited drop-in: %+v, %v", st.Set, err)
	}

	pv = plan(t, eng, "users.sshd.repair", `{}`)
	if got, want := diffText(pv.Diff), []string{
		"  Rewrite " + dropInFile + " from change " + confirmed.ID,
		"- PasswordAuthentication maybe", "- MaxAuthTries 9", "+ PasswordAuthentication no", "+ MaxAuthTries 3",
	}; !slices.Equal(got, want) {
		t.Errorf("repair diff %q, want %q", got, want)
	}
	r.events = nil
	if cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{}); err != nil || cv.State != core.StateCompleted {
		t.Fatalf("repair: %+v, %v", cv, err)
	}
	if r.files[dropInFile] != want {
		t.Errorf("drop-in after the repair\n%s\nwant\n%s", r.files[dropInFile], want)
	}
	if got, want := sideEffects(r.events), []string{"write " + dropInFile, sshdTestRun, "reload " + sshUnit}; !slices.Equal(got, want) {
		t.Errorf("repair did %q, want %q", got, want)
	}
	if pv = plan(t, eng, "users.sshd.repair", `{}`); len(pv.Diff) != 0 {
		t.Errorf("a second repair plans %q", diffText(pv.Diff))
	}

	// With no confirmed change, the repair removes the drop-in.
	r2 := newRig(t)
	r2.liveSshd()
	r2.files[dropInFile] = dropInHeader + "MaxAuthTries 2\n"
	eng2, _, _ := engine(t, r2, &bytes.Buffer{})
	pv = plan(t, eng2, "users.sshd.repair", `{}`)
	if got := diffText(pv.Diff); !slices.Equal(got, []string{"- Remove " + dropInFile + ": no settings were confirmed"}) {
		t.Errorf("repair diff with nothing confirmed %q", got)
	}
	r2.events = nil
	if cv, err = eng2.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{}); err != nil || cv.State != core.StateCompleted {
		t.Fatalf("repair with nothing confirmed: %+v, %v", cv, err)
	}
	if _, ok := r2.files[dropInFile]; ok {
		t.Error("the drop-in is still there")
	}
	if got, want := sideEffects(r2.events), []string{"remove " + dropInFile, sshdTestRun, "reload " + sshUnit}; !slices.Equal(got, want) {
		t.Errorf("repair with nothing confirmed did %q, want %q", got, want)
	}

	specs := map[string]core.ActionSpec{}
	for _, a := range r.module().Manifest().Actions {
		specs[a.Spec.Name] = a.Spec
	}
	if s := specs["users.sshd.apply"]; s.Class != core.ClassRevertible || s.RepairedBy != "users.sshd.repair" ||
		!slices.Equal(s.LockKeys, []string{"sshd"}) || s.MinScope != core.ScopeAdmin {
		t.Errorf("sshd.apply spec %+v", s)
	}
}

// TestRevertKeepsAnEditedDropIn: a revert leaves a drop-in changed since the
// apply as it is, with its effect unresolved as a conflict, and an effect in
// doubt blocks the revert before it touches the file.
func TestRevertKeepsAnEditedDropIn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.liveSshd()
	r.keys["alice"] = ed25519Line(t, "alice@laptop")
	eng, _, _ := engine(t, r, &bytes.Buffer{})
	pv := plan(t, eng, "users.sshd.apply", `{"maxAuthTries":3}`)
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("apply: %+v, %v", cv, err)
	}
	const edited = "MaxAuthTries 3\nX11Forwarding no\n"
	r.files[dropInFile] = edited
	r.events = nil
	if _, err := eng.Revert(ctx, adminP, cv.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if r.files[dropInFile] != edited {
		t.Errorf("the revert replaced the edited drop-in with %q", r.files[dropInFile])
	}
	if got := sideEffects(r.events); len(got) != 0 {
		t.Errorf("the revert of an edited drop-in did %q, want nothing", got)
	}
	views, err := eng.Effects(ctx, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].State != core.EffectUnresolved || views[0].Reason != core.ReasonConflict {
		t.Errorf("effects after the revert %+v, want one unresolved conflict", views)
	}

	env := r.env(adminP)
	env.Effects = listedEffects{effects: []core.Effect{{Seq: 1, State: core.EffectIntended}}}
	r.events = nil
	_, err = sshdHandler{m: r.module()}.Revert(ctx, env, []byte("null"))
	if err == nil || !strings.Contains(err.Error(), "repair the change") {
		t.Errorf("revert with an effect in doubt = %v, want a refusal naming the repair", err)
	}
	if r.files[dropInFile] != edited || len(sideEffects(r.events)) != 0 {
		t.Errorf("a revert blocked by an effect in doubt changed the host: %q", sideEffects(r.events))
	}
}

// listedEffects is a journal that only lists its effects.
type listedEffects struct {
	core.EffectJournal
	effects []core.Effect
}

func (l listedEffects) List(context.Context) ([]core.Effect, error) { return l.effects, nil }

func TestEffectiveOutsideInputRange(t *testing.T) {
	r := newRig(t)
	r.answer(platform.SshdPath, platform.SshdEffective(),
		"maxauthtries 20\npermitrootlogin forced-commands-only\nlogingracetime 0\nauthorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\n")
	st, err := sshdGetter{m: r.module()}.Read(context.Background(), r.env(adminP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	e := st.Effective
	if e.MaxAuthTries == nil || *e.MaxAuthTries != 20 || e.LoginGraceTime == nil || *e.LoginGraceTime != 0 || e.PermitRootLogin != "forced-commands-only" {
		t.Errorf("effective = %+v, want maxAuthTries 20, loginGraceTime 0, permitRootLogin forced-commands-only", e)
	}
	raw, err := schema.Output(reflect.TypeOf(st))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:test:sshd.get", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("urn:test:sshd.get")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(st)
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(v); err != nil {
		t.Errorf("users.sshd.get's answer %s does not match its output schema: %v", out, err)
	}
}
