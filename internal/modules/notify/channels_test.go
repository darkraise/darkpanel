package notify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var t0 = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

var admin = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "203.0.113.7",
	Access: core.Access{Admin: true, Fleet: true}}

// syncBuffer is a log sink safe for the engine's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type env struct {
	t      *testing.T
	db     *store.DB
	clock  *coretest.FakeClock
	audit  *coretest.MemAudit
	engine *lifecycle.Engine
	logs   *syncBuffer
}

// newEnv is a controller database with the core and notify migrations, an
// engine over the core and notify modules, and the module's controller
// contribution made. before, when not nil, runs once the core migrations
// and the engine exist, before the notify migration.
func newEnv(t *testing.T, before func(e *env)) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: t0}
	if _, err := store.Migrate(ctx, db, opts, coreSet, nil); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, clock: coretest.NewFakeClock(t0), audit: &coretest.MemAudit{}, logs: &syncBuffer{}}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	params := auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	a := auth.New(db, coretest.Sealer{}, e.clock, e.audit, auth.Options{Params: params})
	m := Module()
	reg, err := core.NewRegistry(coreactions.Module(coreactions.Deps{Auth: a, Audit: e.audit}), m)
	if err != nil {
		t.Fatal(err)
	}
	e.engine, err = lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: e.audit, Redact: schema.Redact,
		Base: core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: e.clock, Log: log, Agents: coretest.MapHosts{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.engine.Close)
	if before != nil {
		before(e)
	}
	set := store.MigrationSet{Module: ID, FS: m.Manifest().Migrations[core.SideController]}
	if _, err := store.Migrate(ctx, db, opts, coreSet, []store.MigrationSet{set}); err != nil {
		t.Fatal(err)
	}
	c, ok := m.(ext.Controller)
	if !ok {
		t.Fatal("the notify module makes no controller contribution")
	}
	if _, _, err := c.Controller(ext.Deps{DB: db, Sealer: coretest.Sealer{}, Clock: e.clock, Log: log, Audit: e.audit}); err != nil {
		t.Fatal(err)
	}
	return e
}

func encode(t *testing.T, in any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (e *env) plan(name string, in any) (core.PlanView, error) {
	return e.engine.Plan(context.Background(), admin, core.ActionRef{Name: name, Version: 1}, encode(e.t, in), core.PlanOptions{})
}

// apply plans and applies name with the plan's confirmation phrase.
func (e *env) apply(name string, in any) (core.PlanView, core.ChangeView, error) {
	pv, err := e.plan(name, in)
	if err != nil {
		return pv, core.ChangeView{}, err
	}
	cv, err := e.engine.Apply(context.Background(), admin, pv.PlanID, core.ApplyOptions{Confirmation: pv.Confirmation})
	return pv, cv, err
}

// run applies name and decodes its output into out, failing on any error.
func (e *env) run(name string, in, out any) {
	e.t.Helper()
	_, cv, err := e.apply(name, in)
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	if cv.State != core.StateCompleted {
		e.t.Fatalf("%s: state %s, error %+v", name, cv.State, cv.Error)
	}
	if out != nil {
		if err := json.Unmarshal(cv.Output, out); err != nil {
			e.t.Fatalf("%s output %s: %v", name, cv.Output, err)
		}
	}
}

func (e *env) list() (ChannelList, json.RawMessage) {
	e.t.Helper()
	raw, err := e.engine.Read(context.Background(), admin, core.ActionRef{Name: "notify.channel.list", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		e.t.Fatal(err)
	}
	var out ChannelList
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("notify.channel.list output %s: %v", raw, err)
	}
	return out, raw
}

// secrets opens channel id's sealed secrets.
func (e *env) secrets(id string) map[string]string {
	e.t.Helper()
	var ct []byte
	var config string
	if err := e.db.Read().QueryRow(`SELECT secret_ct, config_json FROM notify_channels WHERE id = ?`, id).Scan(&ct, &config); err != nil {
		e.t.Fatal(err)
	}
	plain, err := coretest.Sealer{}.Open("notify_channels.secret_ct."+id, ct)
	if err != nil {
		e.t.Fatalf("open the secrets of channel %s: %v", id, err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(plain, &out); err != nil {
		e.t.Fatalf("channel %s secrets %q: %v", id, plain, err)
	}
	for _, v := range out {
		if v != "" && (bytes.Contains(ct, []byte(v)) || strings.Contains(config, v)) {
			e.t.Errorf("channel %s stores the secret %q in the clear", id, v)
		}
	}
	return out
}

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	if got := errcode.From(err).Code; err == nil || got != code {
		t.Fatalf("error %v (code %q); want code %s", err, got, code)
	}
}

// wantField wants an invalid_input refusal with a field error at path.
func wantField(t *testing.T, label string, err error, path string) {
	t.Helper()
	ee := errcode.From(err)
	if err == nil || ee.Code != errcode.InvalidInput {
		t.Errorf("%s: error %v; want invalid_input at %s", label, err, path)
		return
	}
	for _, f := range ee.Fields {
		if f.Path == path {
			return
		}
	}
	t.Errorf("%s: field errors %+v; want one at %s", label, ee.Fields, path)
}

func validInputs(groupID string) map[string]ChannelInput {
	return map[string]ChannelInput{
		"ntfy": {Kind: "ntfy", Name: "Phone", MinSeverity: "warning",
			Ntfy: &NtfyInput{Topic: "darkpanel-alerts-x7", Token: "tk_ntfy_token"}}, //nolint:gosec // G101: test inputs, not credentials
		"telegram": {Kind: "telegram", Name: "telegram ops", MinSeverity: "info",
			Telegram: &TelegramInput{BotToken: "123456:ABC-def_ghi", ChatID: "-100123"}},
		"discord": {Kind: "discord", Name: "discord", MinSeverity: "critical",
			Discord: &DiscordInput{WebhookURL: "https://discord.com/api/webhooks/1/discord-token"}},
		"email": {Kind: "email", Name: "mail", MinSeverity: "warning",
			Email: &EmailInput{Host: "smtp.example.com", Port: 587, Security: "starttls", Username: "ops",
				Password: "smtp-password", From: "panel@example.com", Recipients: []string{"a@example.com", "b@example.com"}}},
		"webhook": {Kind: "webhook", Name: "hook", MinSeverity: "info", Kinds: []string{"change.failed"}, GroupIDs: []string{groupID},
			Webhook: &WebhookInput{URL: "https://hooks.example.com/darkpanel", Secret: "hmac-secret"}},
	}
}

func TestChannelCRUD(t *testing.T) {
	e := newEnv(t, nil)
	var web coreactions.Group
	e.run("core.group.create", coreactions.GroupCreateInput{Name: "web"}, &web)

	ids := map[string]string{}
	for kind, in := range validInputs(web.ID) {
		var ch Channel
		e.run("notify.channel.create", in, &ch)
		if ch.ID == "" || ch.Kind != kind || ch.Name != in.Name || !ch.Enabled {
			t.Fatalf("create %s output %+v", kind, ch)
		}
		ids[kind] = ch.ID
	}
	for kind, want := range map[string]map[string]string{
		"ntfy":     {"topic": "darkpanel-alerts-x7", "token": "tk_ntfy_token"}, //nolint:gosec // G101: test inputs, not credentials
		"telegram": {"botToken": "123456:ABC-def_ghi"},
		"discord":  {"webhookUrl": "https://discord.com/api/webhooks/1/discord-token"},
		"email":    {"password": "smtp-password"},
		"webhook":  {"url": "https://hooks.example.com/darkpanel", "secret": "hmac-secret"},
	} {
		got := e.secrets(ids[kind])
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: sealed %s = %q, want %q", kind, k, got[k], v)
			}
		}
	}

	ok := func(mut func(in *ChannelInput)) ChannelInput {
		in := validInputs(web.ID)["email"]
		e := *in.Email
		e.Recipients = slices.Clone(e.Recipients)
		in.Email = &e
		in.Name = "other"
		mut(&in)
		return in
	}
	ntfy := func(n NtfyInput) ChannelInput {
		return ChannelInput{Kind: "ntfy", Name: "n2", MinSeverity: "info", Ntfy: &n}
	}
	hook := func(url string) ChannelInput {
		return ChannelInput{Kind: "webhook", Name: "h2", MinSeverity: "info", Webhook: &WebhookInput{URL: url}}
	}
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = fmt.Sprintf("r%d@example.com", i)
	}
	for _, c := range []struct {
		label string
		in    ChannelInput
		path  string
	}{
		{"ntfy server scheme", ntfy(NtfyInput{Server: "ftp://ntfy.example.com", Topic: "t1"}), "/ntfy/server"},
		{"ntfy server no host", ntfy(NtfyInput{Server: "https://", Topic: "t1"}), "/ntfy/server"},
		{"ntfy server user info", ntfy(NtfyInput{Server: "https://u:p@ntfy.example.com", Topic: "t1"}), "/ntfy/server"}, //nolint:gosec // G101: test inputs, not credentials
		{"ntfy no topic", ntfy(NtfyInput{}), "/ntfy/topic"},
		{"ntfy topic with a space", ntfy(NtfyInput{Topic: "a b"}), "/ntfy/topic"},
		{"webhook not a URL", hook("not a url"), "/webhook/url"},
		{"webhook javascript", hook("javascript:alert(1)"), "/webhook/url"},
		{"webhook fragment", hook("https://h.example.com/x#y"), "/webhook/url"},
		{"discord other host", ChannelInput{Kind: "discord", Name: "d2", MinSeverity: "info",
			Discord: &DiscordInput{WebhookURL: "https://example.com/api/webhooks/1/x"}}, "/discord/webhookUrl"},
		{"discord plain http", ChannelInput{Kind: "discord", Name: "d2", MinSeverity: "info",
			Discord: &DiscordInput{WebhookURL: "http://discord.com/api/webhooks/1/x"}}, "/discord/webhookUrl"},
		{"telegram token shape", ChannelInput{Kind: "telegram", Name: "t2", MinSeverity: "info",
			Telegram: &TelegramInput{BotToken: "abc", ChatID: "1"}}, "/telegram/botToken"},
		{"telegram chat id", ChannelInput{Kind: "telegram", Name: "t2", MinSeverity: "info",
			Telegram: &TelegramInput{BotToken: "1:a", ChatID: "x y"}}, "/telegram/chatId"},
		{"email port 0", ok(func(in *ChannelInput) { in.Email.Port = 0 }), "/email/port"},
		{"email port 65536", ok(func(in *ChannelInput) { in.Email.Port = 65536 }), "/email/port"},
		{"email no recipients", ok(func(in *ChannelInput) { in.Email.Recipients = nil }), "/email/recipients"},
		{"email 11 recipients", ok(func(in *ChannelInput) { in.Email.Recipients = eleven }), "/email/recipients"},
		{"email bad recipient", ok(func(in *ChannelInput) { in.Email.Recipients = []string{"nope"} }), "/email/recipients/0"},
		{"email header injection", ok(func(in *ChannelInput) { in.Email.From = "a@example.com\r\nBcc: x@example.com" }), "/email/from"},
		{"email no host", ok(func(in *ChannelInput) { in.Email.Host = "" }), "/email/host"},
		{"email bad security", ok(func(in *ChannelInput) { in.Email.Security = "ssl" }), "/email/security"},
		{"email none off loopback", ok(func(in *ChannelInput) { in.Email.Security = "none" }), "/email/security"},
		{"email username without password", ok(func(in *ChannelInput) { in.Email.Password = "" }), "/email/password"},
		{"no kind config", ok(func(in *ChannelInput) { in.Email = nil }), "/email"},
		{"another kind's config", ok(func(in *ChannelInput) { in.Ntfy = &NtfyInput{Topic: "t1"} }), "/ntfy"},
		{"unknown event kind", ok(func(in *ChannelInput) { in.Kinds = []string{"bogus"} }), "/kinds/0"},
		{"unknown group", ok(func(in *ChannelInput) { in.GroupIDs = []string{"nope"} }), "/groupIds/0"},
		{"a group twice", ok(func(in *ChannelInput) { in.GroupIDs = []string{web.ID, web.ID} }), "/groupIds/1"},
		{"bad name", ok(func(in *ChannelInput) { in.Name = " lead" }), "/name"},
		{"taken name", ok(func(in *ChannelInput) { in.Name = "PHONE" }), "/name"},
	} {
		_, err := e.plan("notify.channel.create", c.in)
		wantField(t, c.label, err, c.path)
	}
	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		in := ok(func(in *ChannelInput) { in.Email.Security = "none"; in.Email.Host = host })
		if _, err := e.plan("notify.channel.create", in); err != nil {
			t.Errorf("security none to %s: %v", host, err)
		}
	}

	// The list: kinds, names, routing and the last delivery result; the ntfy
	// topic and the webhook URL masked; no secret.
	e.clock.Advance(time.Minute)
	ctx := context.Background()
	if err := e.db.Write(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO notify_events (id, kind, severity, host_id, title, body, dedupe_key, created_at)
				VALUES ('ev1', 'change.failed', 'warning', '', 't', 'b', 'k1', '2026-11-24T09:00:00.000000000Z'),
				('ev2', 'change.failed', 'warning', '', 't', 'b', 'k2', '2026-11-24T09:01:00.000000000Z')`,
			`INSERT INTO notify_deliveries (event_id, channel_id, state, attempts, next_at, last_error, updated_at)
				VALUES ('ev1', ?1, 'sent', 1, '', '', '2026-11-24T09:00:05.000000000Z'),
				('ev2', ?1, 'failed', 9, '', '503', '2026-11-24T09:30:00.000000000Z'),
				('ev2', ?2, 'pending', 0, '2026-11-24T09:01:00.000000000Z', '', '2026-11-24T09:01:00.000000000Z')`,
		} {
			if _, err := tx.ExecContext(ctx, q, ids["ntfy"], ids["email"]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	list, raw := e.list()
	var names []string
	for _, ch := range list.Channels {
		names = append(names, ch.Name)
	}
	if want := []string{"discord", "hook", "mail", "Phone", "telegram ops"}; !slices.Equal(names, want) {
		t.Fatalf("list names %v, want %v", names, want)
	}
	for _, secret := range []string{"tk_ntfy_token", "darkpanel-alerts-x7", "ABC-def_ghi", "discord-token", "smtp-password",
		"hooks.example.com", "hmac-secret"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("the list output holds %q: %s", secret, raw)
		}
	}
	byKind := map[string]Channel{}
	for _, ch := range list.Channels {
		byKind[ch.Kind] = ch
	}
	n := byKind["ntfy"]
	if n.Ntfy == nil || n.Ntfy.Server != "https://ntfy.sh" || n.Ntfy.Topic != "dark…" || !n.Ntfy.TokenSet || n.MinSeverity != "warning" {
		t.Errorf("ntfy view %+v %+v", n, n.Ntfy)
	}
	if n.LastDelivery == nil || n.LastDelivery.State != "failed" || n.LastDelivery.Error != "503" ||
		!n.LastDelivery.At.Equal(time.Date(2026, 11, 24, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("ntfy last delivery %+v; want failed 503 at 09:30", n.LastDelivery)
	}
	if m := byKind["email"]; m.LastDelivery != nil {
		t.Errorf("email last delivery %+v; a pending delivery is no result", m.LastDelivery)
	}
	w := byKind["webhook"]
	if w.Webhook == nil || w.Webhook.URL != "http…" || !w.Webhook.SecretSet || !slices.Equal(w.Kinds, []string{"change.failed"}) ||
		!slices.Equal(w.GroupIDs, []string{web.ID}) || w.LastDelivery != nil {
		t.Errorf("webhook view %+v %+v", w, w.Webhook)
	}
	m := byKind["email"]
	if m.Email == nil || m.Email.Host != "smtp.example.com" || m.Email.Port != 587 || m.Email.Security != "starttls" ||
		m.Email.Username != "ops" || !m.Email.PasswordSet || m.Email.From != "panel@example.com" || len(m.Email.Recipients) != 2 {
		t.Errorf("email view %+v %+v", m, m.Email)
	}
	if tg := byKind["telegram"]; tg.Telegram == nil || tg.Telegram.ChatID != "-100123" {
		t.Errorf("telegram view %+v", tg)
	}
	if d := byKind["discord"]; d.Discord == nil || len(d.Kinds) != 0 || len(d.GroupIDs) != 0 {
		t.Errorf("discord view %+v", d)
	}

	// Update: omitted secrets are kept, given ones replace them.
	var up Channel
	e.run("notify.channel.update", ChannelUpdateInput{ID: ids["webhook"], Name: "hook 2", MinSeverity: "critical",
		Enabled: false, Webhook: &WebhookInput{Secret: "hmac-2"}}, &up)
	if up.Name != "hook 2" || up.MinSeverity != "critical" || up.Enabled || len(up.Kinds) != 0 || len(up.GroupIDs) != 0 {
		t.Errorf("update output %+v", up)
	}
	if s := e.secrets(ids["webhook"]); s["url"] != "https://hooks.example.com/darkpanel" || s["secret"] != "hmac-2" {
		t.Errorf("after update the webhook secrets are %v", s)
	}
	e.run("notify.channel.update", ChannelUpdateInput{ID: ids["ntfy"], Name: "Phone", MinSeverity: "info", Enabled: true,
		Ntfy: &NtfyInput{Server: "https://ntfy.example.com/"}}, &up)
	if up.Ntfy == nil || up.Ntfy.Server != "https://ntfy.example.com/" || up.Ntfy.Topic != "dark…" {
		t.Errorf("ntfy update output %+v", up.Ntfy)
	}
	if s := e.secrets(ids["ntfy"]); s["topic"] != "darkpanel-alerts-x7" || s["token"] != "tk_ntfy_token" {
		t.Errorf("after update the ntfy secrets are %v", s)
	}
	_, err := e.plan("notify.channel.update", ChannelUpdateInput{ID: ids["ntfy"], Name: "Phone", MinSeverity: "info",
		Telegram: &TelegramInput{ChatID: "1"}})
	wantField(t, "update with another kind's config", err, "/telegram")
	_, err = e.plan("notify.channel.update", ChannelUpdateInput{ID: ids["ntfy"], Name: "mail", MinSeverity: "info", Ntfy: &NtfyInput{}})
	wantField(t, "update to a taken name", err, "/name")
	_, err = e.plan("notify.channel.update", ChannelUpdateInput{ID: "nope", Name: "x", MinSeverity: "info", Ntfy: &NtfyInput{}})
	wantCode(t, err, errcode.NotFound)

	// Delete: confirmed by the channel's name; its deliveries go with it.
	pv, err := e.plan("notify.channel.delete", ChannelIDInput{ID: ids["ntfy"]})
	if err != nil || pv.Confirmation != "Phone" {
		t.Fatalf("delete plan %+v, %v; want confirmation Phone", pv, err)
	}
	e.run("notify.channel.delete", ChannelIDInput{ID: ids["ntfy"]}, nil)
	list, _ = e.list()
	if len(list.Channels) != 4 || slices.ContainsFunc(list.Channels, func(c Channel) bool { return c.ID == ids["ntfy"] }) {
		t.Errorf("after delete the list is %+v", list.Channels)
	}
	var left int
	if err := e.db.Read().QueryRow(`SELECT count(*) FROM notify_deliveries WHERE channel_id = ?`, ids["ntfy"]).Scan(&left); err != nil || left != 0 {
		t.Errorf("the deleted channel keeps %d deliveries, %v", left, err)
	}
	_, err = e.plan("notify.channel.delete", ChannelIDInput{ID: ids["ntfy"]})
	wantCode(t, err, errcode.NotFound)
}

func TestChannelReferencesBlockGroupDelete(t *testing.T) {
	e := newEnv(t, nil)
	var g coreactions.Group
	e.run("core.group.create", coreactions.GroupCreateInput{Name: "ops"}, &g)
	var ch Channel
	e.run("notify.channel.create", ChannelInput{Kind: "discord", Name: "pager", MinSeverity: "critical", GroupIDs: []string{g.ID},
		Discord: &DiscordInput{WebhookURL: "https://discord.com/api/webhooks/1/x"}}, &ch)
	e.run("notify.channel.create", ChannelInput{Kind: "discord", Name: "everything", MinSeverity: "info",
		Discord: &DiscordInput{WebhookURL: "https://discord.com/api/webhooks/2/y"}}, nil)

	_, err := e.plan("core.group.delete", coreactions.GroupIDInput{ID: g.ID})
	wantCode(t, err, errcode.GroupInUse)
	if msg := errcode.From(err).Detail; !strings.Contains(msg, "channel pager") || strings.Contains(msg, "everything") {
		t.Errorf("group.in_use message %q; want it to name channel pager alone", msg)
	}

	e.run("notify.channel.update", ChannelUpdateInput{ID: ch.ID, Name: "pager", MinSeverity: "critical", Enabled: true,
		Discord: &DiscordInput{}}, nil)
	e.run("core.group.delete", coreactions.GroupIDInput{ID: g.ID}, nil)
}

func TestSecretsNeverLeak(t *testing.T) {
	const canary = "canary7f3c9a"
	e := newEnv(t, nil)
	ins := []ChannelInput{
		{Kind: "ntfy", Name: "n", MinSeverity: "info", Ntfy: &NtfyInput{Topic: "zzzz" + canary, Token: "tk_" + canary}},
		{Kind: "telegram", Name: "t", MinSeverity: "info", Telegram: &TelegramInput{BotToken: "1:" + canary, ChatID: "5"}},
		{Kind: "discord", Name: "d", MinSeverity: "info", Discord: &DiscordInput{WebhookURL: "https://discord.com/api/webhooks/1/" + canary}},
		{Kind: "email", Name: "e", MinSeverity: "info", Email: &EmailInput{Host: "127.0.0.1", Port: 25, Security: "none",
			Username: "u", Password: canary, From: "p@example.com", Recipients: []string{"a@example.com"}}},
		{Kind: "webhook", Name: "w", MinSeverity: "info", Webhook: &WebhookInput{URL: "https://h.example.com/" + canary, Secret: canary}},
	}
	var errs []string
	var views []string
	for _, in := range ins {
		var ch Channel
		pv, cv, err := e.apply("notify.channel.create", in)
		if err != nil {
			t.Fatalf("create %s: %v", in.Kind, err)
		}
		views = append(views, fmt.Sprintf("%+v %+v %s", pv, cv, cv.Output))
		if err := json.Unmarshal(cv.Output, &ch); err != nil {
			t.Fatal(err)
		}
		up := ChannelUpdateInput{ID: ch.ID, Name: in.Name + "2", MinSeverity: "warning", Enabled: true,
			Ntfy: in.Ntfy, Telegram: in.Telegram, Discord: in.Discord, Email: in.Email, Webhook: in.Webhook}
		pv, cv, err = e.apply("notify.channel.update", up)
		if err != nil {
			t.Fatalf("update %s: %v", in.Kind, err)
		}
		views = append(views, fmt.Sprintf("%+v %+v %s", pv, cv, cv.Output))
	}
	// Refusals never quote a secret.
	for _, in := range []ChannelInput{
		{Kind: "telegram", Name: "bad", MinSeverity: "info", Telegram: &TelegramInput{BotToken: "x " + canary, ChatID: "5"}},
		{Kind: "discord", Name: "bad", MinSeverity: "info", Discord: &DiscordInput{WebhookURL: "https://evil.example.com/" + canary}},
		{Kind: "ntfy", Name: "bad", MinSeverity: "info", Ntfy: &NtfyInput{Topic: "a b" + canary}},
		{Kind: "webhook", Name: "bad", MinSeverity: "info", Webhook: &WebhookInput{URL: "ftp://" + canary}},
		{Kind: "webhook", Name: "w2", MinSeverity: "info", Webhook: &WebhookInput{URL: "https://h.example.com/" + canary, Secret: canary}},
	} {
		_, err := e.plan("notify.channel.create", in)
		if err == nil {
			t.Fatalf("create %+v was not refused", in)
		}
		b, _ := json.Marshal(errcode.From(err))
		errs = append(errs, err.Error(), string(b))
	}
	_, raw := e.list()
	views = append(views, string(raw))
	list, _ := e.list()
	pv, cv, err := e.apply("notify.channel.delete", ChannelIDInput{ID: list.Channels[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	views = append(views, fmt.Sprintf("%+v %+v %s", pv, cv, cv.Output))

	check := func(where, s string) {
		if strings.Contains(s, canary) {
			t.Errorf("%s holds the canary: %s", where, s)
		}
	}
	for _, v := range views {
		check("an action's plan, change or output", v)
	}
	for _, s := range errs {
		check("a refusal", s)
	}
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 1000})
	if err != nil || len(entries) == 0 {
		t.Fatalf("audit entries %d, %v", len(entries), err)
	}
	for _, a := range entries {
		check("the audit and journal", fmt.Sprintf("%+v %s", a, a.Input))
	}
	check("the log", e.logs.String())

	// Every stored row but the sealed secrets.
	rows, err := e.db.Read().Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	_ = rows.Close()
	for _, table := range tables {
		q, err := e.db.Read().Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := q.Columns()
		for q.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := q.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range vals {
				var s string
				switch v := v.(type) {
				case string:
					s = v
				case []byte:
					s = string(v)
				default:
					continue
				}
				check(fmt.Sprintf("table %s column %s", table, cols[i]), s)
			}
		}
		_ = q.Close()
	}
}

func TestNotifyMigration(t *testing.T) {
	ctx := context.Background()
	fresh := newEnv(t, nil)
	var cursor string
	if err := fresh.db.Read().QueryRow(`SELECT updated_at FROM notify_cursor`).Scan(&cursor); err != nil || cursor != "" {
		t.Errorf("with no change the cursor is %q, %v; want empty", cursor, err)
	}

	var newest string
	e := newEnv(t, func(e *env) {
		e.run("core.group.create", coreactions.GroupCreateInput{Name: "a"}, nil)
		e.clock.Advance(time.Hour)
		e.run("core.group.create", coreactions.GroupCreateInput{Name: "b"}, nil)
		if err := e.db.Read().QueryRow(`SELECT max(updated_at) FROM core_changes`).Scan(&newest); err != nil {
			t.Fatal(err)
		}
	})
	if err := e.db.Read().QueryRow(`SELECT updated_at FROM notify_cursor`).Scan(&cursor); err != nil || cursor != newest || newest == "" {
		t.Errorf("the cursor is %q, %v; want the newest change %q", cursor, err, newest)
	}
	var restored sql.NullString
	if err := e.db.Read().QueryRow(`SELECT restored_tx FROM notify_state WHERE id = 1`).Scan(&restored); err != nil || restored.Valid {
		t.Errorf("notify_state restored_tx %v, %v; want one row holding null", restored, err)
	}

	insert := func(id, kind, key string) error {
		return e.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO notify_events (id, kind, severity, host_id, title, body, dedupe_key, created_at)
				VALUES (?, ?, 'warning', '', 't', 'b', ?, '2026-11-24T09:00:00.000000000Z')`, id, kind, key)
			return err
		})
	}
	for i, c := range []struct {
		kind, key string
		dup       bool
	}{
		{"change.failed", "change.failed:c1", true},
		{"change.auto_reverted", "change.auto_reverted:c1", true},
		{"alert.firing", "alert.firing:r:h:cpu:t", false},
	} {
		if err := insert(fmt.Sprintf("a%d", i), c.kind, c.key); err != nil {
			t.Fatal(err)
		}
		err := insert(fmt.Sprintf("b%d", i), c.kind, c.key)
		if c.dup && (err == nil || !strings.Contains(err.Error(), "UNIQUE")) {
			t.Errorf("%s: a second event with key %s answered %v; want a unique conflict", c.kind, c.key, err)
		}
		if !c.dup && err != nil {
			t.Errorf("%s: a repeat key was refused: %v", c.kind, err)
		}
	}
}
