package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const timeFormat = "2006-01-02T15:04:05.000000000Z"

// The channel kinds.
const (
	kindNtfy     = "ntfy"
	kindTelegram = "telegram"
	kindDiscord  = "discord"
	kindEmail    = "email"
	kindWebhook  = "webhook"
)

const (
	defaultNtfyServer = "https://ntfy.sh"
	maxRecipients     = 10
	maxSecretBytes    = 512
)

var (
	channelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,47}$`)
	ntfyTopicPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	botTokenPattern    = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{1,128}$`)
	chatIDPattern      = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z][A-Za-z0-9_]{4,31})$`)
	hostLabels         = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	discordHosts       = []string{"discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com"}
)

// The keys of a channel's sealed secrets.
const (
	secretTopic      = "topic"
	secretToken      = "token"
	secretBotToken   = "botToken"
	secretWebhookURL = "webhookUrl"
	secretPassword   = "password"
	secretURL        = "url"
	secretHMAC       = "secret"
)

// storedConfig is config_json: a channel's settings that are not
// write-only, and which write-only values are stored.
type storedConfig struct {
	Server      string   `json:"server,omitempty"`
	TopicMask   string   `json:"topicMask,omitempty"`
	TokenSet    bool     `json:"tokenSet,omitempty"`
	ChatID      string   `json:"chatId,omitempty"`
	Host        string   `json:"host,omitempty"`
	Port        int      `json:"port,omitempty"`
	Security    string   `json:"security,omitempty"`
	Username    string   `json:"username,omitempty"`
	PasswordSet bool     `json:"passwordSet,omitempty"`
	From        string   `json:"from,omitempty"`
	Recipients  []string `json:"recipients,omitempty"`
	URLMask     string   `json:"urlMask,omitempty"`
	SecretSet   bool     `json:"secretSet,omitempty"`
}

// channelRow is a stored channel.
type channelRow struct {
	ID, Kind, Name, MinSeverity string
	Config                      storedConfig
	SecretCT                    []byte
	Kinds, GroupIDs             []string
	Enabled                     bool
	CreatedAt                   time.Time
}

// definition is what create and update both take.
type definition struct {
	Name, MinSeverity string
	Kinds, GroupIDs   []string
	Ntfy              *NtfyInput
	Telegram          *TelegramInput
	Discord           *DiscordInput
	Email             *EmailInput
	Webhook           *WebhookInput
}

func channelActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[ChannelInput, Channel](core.ActionSpec{
			Name: ID + ".channel.create", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: ID + ".channel.delete",
			Summary: "Add a notification channel: ntfy, Telegram, Discord, email or a webhook.",
		}, channelCreate{}),
		core.DefineMutating[ChannelUpdateInput, Channel](core.ActionSpec{
			Name: ID + ".channel.update", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: ID + ".channel.update",
			Summary: "Change a notification channel's settings and routing; omitted write-only values are kept.",
		}, channelUpdate{}),
		core.DefineMutating[ChannelIDInput, Channel](core.ActionSpec{
			Name: ID + ".channel.delete", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			MinScope: core.ScopeAdmin,
			Summary:  "Delete a notification channel with its pending deliveries; the confirmation phrase is its name.",
		}, channelDelete{}),
		core.DefineRead[NoInput, ChannelList](core.ActionSpec{
			Name: ID + ".channel.list", Version: 1, Side: core.SideController, HostData: core.HostDataFleet,
			Summary: "List the notification channels with their routing and last delivery; write-only values are never returned.",
		}, channelListRead{}),
	}
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func errChannelNotFound(id string) error {
	return errcode.New(errcode.NotFound, "no notification channel "+id)
}

// refusal is one invalid_input error holding every field error.
func refusal(fields []errcode.FieldError) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "the channel settings are invalid", Fields: fields}
}

// mask is s's first 4 characters and an ellipsis; a value of 4 characters
// or fewer shows none.
func mask(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return "…"
	}
	return string(r[:4]) + "…"
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// checkSecret is the message refusing a write-only value, or "".
func checkSecret(s string) string {
	switch {
	case len(s) > maxSecretBytes:
		return fmt.Sprintf("is at most %d bytes", maxSecretBytes)
	case strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return "holds no space or control character"
	}
	return ""
}

// httpURL reports whether s is an http or https URL with a host, no user
// information and no fragment.
func httpURL(s string) bool {
	if hasControl(s) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil &&
		u.Fragment == "" && !strings.Contains(s, "#")
}

func discordURL(s string) bool {
	if !httpURL(s) {
		return false
	}
	u, _ := url.Parse(s)
	return u.Scheme == "https" && u.Port() == "" && slices.Contains(discordHosts, strings.ToLower(u.Hostname())) &&
		strings.HasPrefix(u.Path, "/api/webhooks/") && u.RawQuery == ""
}

func loopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	a, err := netip.ParseAddr(h)
	return err == nil && a.Unmap().IsLoopback()
}

func mailAddress(s string) bool {
	if hasControl(s) {
		return false
	}
	_, err := mail.ParseAddress(s)
	return err == nil
}

// build checks d's settings for a kind channel and returns its stored
// config and secrets. prev holds the stored secrets an update keeps; nil on
// create.
func build(kind string, d definition, prev map[string]string) (storedConfig, map[string]string, []errcode.FieldError) {
	var fields []errcode.FieldError
	add := func(path, msg string) { fields = append(fields, errcode.FieldError{Path: path, Message: msg}) }
	given := map[string]bool{kindNtfy: d.Ntfy != nil, kindTelegram: d.Telegram != nil, kindDiscord: d.Discord != nil,
		kindEmail: d.Email != nil, kindWebhook: d.Webhook != nil}
	for _, k := range []string{kindNtfy, kindTelegram, kindDiscord, kindEmail, kindWebhook} {
		switch {
		case k == kind && !given[k]:
			add("/"+k, "the "+k+" settings are required")
		case k != kind && given[k]:
			add("/"+k, "a "+kind+" channel takes no "+k+" settings")
		}
	}
	if len(fields) > 0 {
		return storedConfig{}, nil, fields
	}
	// value is the write-only value given, or the stored one an update keeps.
	value := func(path, key, given string, required bool, check func(string) string) string {
		if given != "" {
			if msg := check(given); msg != "" {
				add(path, msg)
			}
			return given
		}
		v := prev[key]
		if v == "" && required {
			add(path, "is required")
		}
		return v
	}
	var c storedConfig
	s := map[string]string{}
	switch kind {
	case kindNtfy:
		n := d.Ntfy
		c.Server = n.Server
		if c.Server == "" {
			c.Server = defaultNtfyServer
		}
		if u, err := url.Parse(c.Server); !httpURL(c.Server) || err != nil || u.RawQuery != "" {
			add("/ntfy/server", "is an http or https URL with a host and no user information, query or fragment")
		}
		s[secretTopic] = value("/ntfy/topic", secretTopic, n.Topic, true, func(v string) string {
			if !ntfyTopicPattern.MatchString(v) {
				return "is 1 to 64 letters, digits, hyphens or underscores"
			}
			return ""
		})
		s[secretToken] = value("/ntfy/token", secretToken, n.Token, false, checkSecret)
		c.TopicMask, c.TokenSet = mask(s[secretTopic]), s[secretToken] != ""
	case kindTelegram:
		t := d.Telegram
		s[secretBotToken] = value("/telegram/botToken", secretBotToken, t.BotToken, true, func(v string) string {
			if !botTokenPattern.MatchString(v) {
				return "is a bot token as BotFather issues it: digits, a colon, then letters, digits, hyphens or underscores"
			}
			return ""
		})
		if !chatIDPattern.MatchString(t.ChatID) {
			add("/telegram/chatId", "is a number, or @ and a public channel's name")
		}
		c.ChatID = t.ChatID
	case kindDiscord:
		s[secretWebhookURL] = value("/discord/webhookUrl", secretWebhookURL, d.Discord.WebhookURL, true, func(v string) string {
			if !discordURL(v) {
				return "is a Discord webhook URL: https://discord.com/api/webhooks/..."
			}
			return ""
		})
	case kindEmail:
		e := d.Email
		if a, err := netip.ParseAddr(e.Host); (err != nil || a.Zone() != "") && (len(e.Host) > 253 || !hostLabels.MatchString(e.Host)) {
			add("/email/host", "is a host name or an IP address")
		}
		if e.Port < 1 || e.Port > 65535 {
			add("/email/port", "is 1 to 65535")
		}
		switch e.Security {
		case "starttls", "tls":
		case "none":
			if !loopbackHost(e.Host) {
				add("/email/security", "none is allowed only to a loopback host; use starttls or tls")
			}
		default:
			add("/email/security", "is starttls, tls or none")
		}
		if len(e.Username) > 256 || hasControl(e.Username) {
			add("/email/username", "is at most 256 characters with no control character")
		}
		if e.Username != "" {
			s[secretPassword] = value("/email/password", secretPassword, e.Password, true, func(v string) string {
				if len(v) > maxSecretBytes || hasControl(v) {
					return fmt.Sprintf("is at most %d bytes with no control character", maxSecretBytes)
				}
				return ""
			})
		} else if e.Password != "" {
			add("/email/password", "a password needs a user name")
		}
		if !mailAddress(e.From) {
			add("/email/from", "is an email address")
		}
		if n := len(e.Recipients); n < 1 || n > maxRecipients {
			add("/email/recipients", fmt.Sprintf("holds 1 to %d addresses", maxRecipients))
		}
		for i, r := range e.Recipients {
			if !mailAddress(r) {
				add(fmt.Sprintf("/email/recipients/%d", i), "is an email address")
			}
		}
		c.Host, c.Port, c.Security, c.Username, c.From = e.Host, e.Port, e.Security, e.Username, e.From
		c.Recipients = slices.Clone(e.Recipients)
		c.PasswordSet = s[secretPassword] != ""
	case kindWebhook:
		w := d.Webhook
		s[secretURL] = value("/webhook/url", secretURL, w.URL, true, func(v string) string {
			if !httpURL(v) {
				return "is an http or https URL with a host and no user information or fragment"
			}
			return ""
		})
		s[secretHMAC] = value("/webhook/secret", secretHMAC, w.Secret, false, func(v string) string {
			if len(v) > maxSecretBytes || hasControl(v) {
				return fmt.Sprintf("is at most %d bytes with no control character", maxSecretBytes)
			}
			return ""
		})
		c.URLMask, c.SecretSet = mask(s[secretURL]), s[secretHMAC] != ""
	}
	for k, v := range s {
		if v == "" {
			delete(s, k)
		}
	}
	return c, s, fields
}

// checkDefinition checks d for a kind channel named by id ("" on create)
// and returns its stored config and secrets.
func checkDefinition(ctx context.Context, q querier, id, kind string, d definition,
	prev map[string]string) (storedConfig, map[string]string, error) {
	var fields []errcode.FieldError
	add := func(path, msg string) { fields = append(fields, errcode.FieldError{Path: path, Message: msg}) }
	if !channelNamePattern.MatchString(d.Name) {
		add("/name", "is 1 to 48 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit")
	} else {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM notify_channels WHERE name = ? COLLATE NOCASE AND id <> ?`,
			d.Name, id).Scan(&n); err != nil {
			return storedConfig{}, nil, fmt.Errorf("notify: check the channel name: %w", err)
		}
		if n > 0 {
			add("/name", "another channel already has this name")
		}
	}
	if !slices.Contains([]string{core.SeverityInfo, core.SeverityWarning, core.SeverityCritical}, d.MinSeverity) {
		add("/minSeverity", "is info, warning or critical")
	}
	for i, k := range d.Kinds {
		at := fmt.Sprintf("/kinds/%d", i)
		switch {
		case !slices.Contains(core.NotifyKinds(), k):
			add(at, "is not an event kind")
		case slices.Contains(d.Kinds[:i], k):
			add(at, "names an event kind another entry names")
		}
	}
	for i, g := range d.GroupIDs {
		at := fmt.Sprintf("/groupIds/%d", i)
		if slices.Contains(d.GroupIDs[:i], g) {
			add(at, "names a group another entry names")
			continue
		}
		var n int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_host_groups WHERE id = ?`, g).Scan(&n); err != nil {
			return storedConfig{}, nil, fmt.Errorf("notify: read host groups: %w", err)
		}
		if n == 0 {
			add(at, "is not a host group")
		}
	}
	c, s, more := build(kind, d, prev)
	fields = append(fields, more...)
	if len(fields) > 0 {
		return storedConfig{}, nil, refusal(fields)
	}
	return c, s, nil
}

func sealAD(id string) string { return "notify_channels.secret_ct." + id }

func sealSecrets(env core.Env, id string, s map[string]string) ([]byte, error) {
	plain, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("notify: encode the channel secrets: %w", err)
	}
	ct, err := env.Sealer.Seal(sealAD(id), plain)
	if err != nil {
		return nil, fmt.Errorf("notify: seal the channel secrets: %w", err)
	}
	return ct, nil
}

func openSecrets(env core.Env, r channelRow) (map[string]string, error) {
	plain, err := env.Sealer.Open(sealAD(r.ID), r.SecretCT)
	if err != nil {
		return nil, fmt.Errorf("notify: open the secrets of channel %s: %w", r.ID, err)
	}
	s := map[string]string{}
	if err := json.Unmarshal(plain, &s); err != nil {
		return nil, fmt.Errorf("notify: read the secrets of channel %s: %w", r.ID, err)
	}
	return s, nil
}

func jsonList(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(s) // a string slice always marshals
	return string(b)
}

// loadChannels reads every channel, or only id when it is not "".
func loadChannels(ctx context.Context, q querier, id string) ([]channelRow, error) {
	query := `SELECT id, kind, name, config_json, secret_ct, min_severity, kinds_json, group_ids_json, enabled, created_at
		FROM notify_channels`
	var args []any
	if id != "" {
		query += ` WHERE id = ?`
		args = append(args, id)
	}
	rows, err := q.QueryContext(ctx, query+` ORDER BY name COLLATE NOCASE, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("notify: read channels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []channelRow
	for rows.Next() {
		var r channelRow
		var config, kinds, groups, created string
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &config, &r.SecretCT, &r.MinSeverity, &kinds, &groups, &r.Enabled, &created); err != nil {
			return nil, fmt.Errorf("notify: read channels: %w", err)
		}
		for _, f := range []struct {
			raw string
			to  any
		}{{config, &r.Config}, {kinds, &r.Kinds}, {groups, &r.GroupIDs}} {
			if err := json.Unmarshal([]byte(f.raw), f.to); err != nil {
				return nil, fmt.Errorf("notify: channel %s: %w", r.ID, err)
			}
		}
		if r.CreatedAt, err = time.Parse(timeFormat, created); err != nil {
			return nil, fmt.Errorf("notify: channel %s created_at: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadChannel(ctx context.Context, q querier, id string) (channelRow, error) {
	rows, err := loadChannels(ctx, q, id)
	if err != nil {
		return channelRow{}, err
	}
	if len(rows) == 0 {
		return channelRow{}, errChannelNotFound(id)
	}
	return rows[0], nil
}

// lastDeliveries is each channel's latest finished delivery.
func lastDeliveries(ctx context.Context, q querier) (map[string]*Delivery, error) {
	rows, err := q.QueryContext(ctx, `SELECT channel_id, state, updated_at, last_error FROM (
		SELECT channel_id, state, updated_at, last_error,
			row_number() OVER (PARTITION BY channel_id ORDER BY updated_at DESC, event_id DESC) AS n
		FROM notify_deliveries WHERE state IN ('sent', 'failed')) WHERE n = 1`)
	if err != nil {
		return nil, fmt.Errorf("notify: read deliveries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]*Delivery{}
	for rows.Next() {
		var id, at string
		d := &Delivery{}
		if err := rows.Scan(&id, &d.State, &at, &d.Error); err != nil {
			return nil, fmt.Errorf("notify: read deliveries: %w", err)
		}
		if d.At, err = time.Parse(timeFormat, at); err != nil {
			return nil, fmt.Errorf("notify: delivery of channel %s updated_at: %w", id, err)
		}
		out[id] = d
	}
	return out, rows.Err()
}

func view(r channelRow, last *Delivery) Channel {
	ch := Channel{ID: r.ID, Kind: r.Kind, Name: r.Name, MinSeverity: r.MinSeverity, Kinds: r.Kinds, GroupIDs: r.GroupIDs,
		Enabled: r.Enabled, CreatedAt: r.CreatedAt, LastDelivery: last}
	if ch.Kinds == nil {
		ch.Kinds = []string{}
	}
	if ch.GroupIDs == nil {
		ch.GroupIDs = []string{}
	}
	c := r.Config
	switch r.Kind {
	case kindNtfy:
		ch.Ntfy = &NtfyView{Server: c.Server, Topic: c.TopicMask, TokenSet: c.TokenSet}
	case kindTelegram:
		ch.Telegram = &TelegramView{ChatID: c.ChatID}
	case kindDiscord:
		ch.Discord = &DiscordView{}
	case kindEmail:
		recipients := c.Recipients
		if recipients == nil {
			recipients = []string{}
		}
		ch.Email = &EmailView{Host: c.Host, Port: c.Port, Security: c.Security, Username: c.Username,
			PasswordSet: c.PasswordSet, From: c.From, Recipients: recipients}
	case kindWebhook:
		ch.Webhook = &WebhookView{URL: c.URLMask, SecretSet: c.SecretSet}
	}
	return ch
}

// channelView is channel id as reads show it.
func channelView(ctx context.Context, q querier, id string) (Channel, error) {
	r, err := loadChannel(ctx, q, id)
	if err != nil {
		return Channel{}, err
	}
	last, err := lastDeliveries(ctx, q)
	if err != nil {
		return Channel{}, err
	}
	return view(r, last[id]), nil
}

// rowHash is a stored channel's state hash; the sealed secrets count by
// their ciphertext, which every write seals anew.
func rowHash(r channelRow) string {
	b, _ := json.Marshal(r.Config) // a storedConfig always marshals
	var sb strings.Builder
	for _, part := range []string{r.ID, r.Kind, r.Name, r.MinSeverity, string(b), jsonList(r.Kinds), jsonList(r.GroupIDs),
		fmt.Sprint(r.Enabled), hex.EncodeToString(r.SecretCT)} {
		fmt.Fprintf(&sb, "%d:%s;", len(part), part)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// routing describes d's routing for a plan.
func routing(d definition) string {
	kinds, hosts := "every event kind", "every host"
	if len(d.Kinds) > 0 {
		kinds = strings.Join(d.Kinds, ", ")
	}
	if len(d.GroupIDs) > 0 {
		hosts = fmt.Sprintf("hosts of %d %s", len(d.GroupIDs), map[bool]string{true: "group", false: "groups"}[len(d.GroupIDs) == 1])
	}
	return fmt.Sprintf("%s and above, %s, %s", d.MinSeverity, kinds, hosts)
}

func (in ChannelInput) definition() definition {
	return definition{Name: in.Name, MinSeverity: in.MinSeverity, Kinds: in.Kinds, GroupIDs: in.GroupIDs,
		Ntfy: in.Ntfy, Telegram: in.Telegram, Discord: in.Discord, Email: in.Email, Webhook: in.Webhook}
}

func (in ChannelUpdateInput) definition() definition {
	return definition{Name: in.Name, MinSeverity: in.MinSeverity, Kinds: in.Kinds, GroupIDs: in.GroupIDs,
		Ntfy: in.Ntfy, Telegram: in.Telegram, Discord: in.Discord, Email: in.Email, Webhook: in.Webhook}
}

func writeChannel(ctx context.Context, tx *sql.Tx, insert bool, r channelRow) error {
	config, err := json.Marshal(r.Config)
	if err != nil {
		return fmt.Errorf("notify: encode the channel settings: %w", err)
	}
	if insert {
		_, err = tx.ExecContext(ctx, `INSERT INTO notify_channels (id, kind, name, config_json, secret_ct, min_severity,
			kinds_json, group_ids_json, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.Kind, r.Name, string(config), r.SecretCT, r.MinSeverity, jsonList(r.Kinds), jsonList(r.GroupIDs),
			r.Enabled, r.CreatedAt.UTC().Format(timeFormat))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE notify_channels SET name = ?, config_json = ?, secret_ct = ?, min_severity = ?,
			kinds_json = ?, group_ids_json = ?, enabled = ? WHERE id = ?`,
			r.Name, string(config), r.SecretCT, r.MinSeverity, jsonList(r.Kinds), jsonList(r.GroupIDs), r.Enabled, r.ID)
	}
	if err != nil {
		return fmt.Errorf("notify: store the channel: %w", err)
	}
	return nil
}

type channelCreate struct{}

func (channelCreate) Plan(ctx context.Context, env core.Env, in ChannelInput) (core.PlanOutcome, error) {
	d := in.definition()
	if _, _, err := checkDefinition(ctx, env.DB.Read(), "", in.Kind, d, nil); err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("%s channel %s: %s", in.Kind, in.Name, routing(d))},
			{Op: " ", Text: "compensated by notify.channel.delete"},
		},
		Guards: []core.Guard{}, StateHash: "no channel " + strings.ToLower(in.Name),
	}, nil
}

func (channelCreate) Apply(ctx context.Context, env core.Env, in ChannelInput) (Channel, error) {
	now := env.Clock.Now()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return Channel{}, fmt.Errorf("notify: channel id: %w", err)
	}
	r := channelRow{ID: id.String(), Kind: in.Kind, Name: in.Name, MinSeverity: in.MinSeverity, Kinds: in.Kinds,
		GroupIDs: in.GroupIDs, Enabled: true, CreatedAt: now}
	var out Channel
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		c, s, err := checkDefinition(ctx, tx, "", in.Kind, in.definition(), nil)
		if err != nil {
			return core.Definite(err)
		}
		r.Config = c
		if r.SecretCT, err = sealSecrets(env, r.ID, s); err != nil {
			return err
		}
		if err := writeChannel(ctx, tx, true, r); err != nil {
			return err
		}
		out, err = channelView(ctx, tx, r.ID)
		return err
	})
	return out, err
}

// Verify finds the channel by name, the only key the input holds.
func (channelCreate) Verify(ctx context.Context, env core.Env, in ChannelInput, _ Channel) error {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, `SELECT count(*) FROM notify_channels WHERE name = ? AND kind = ?`,
		in.Name, in.Kind).Scan(&n); err != nil {
		return fmt.Errorf("notify: verify the channel: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("notify: no %s channel is named %q", in.Kind, in.Name)
	}
	return nil
}

type channelUpdate struct{}

func (channelUpdate) Plan(ctx context.Context, env core.Env, in ChannelUpdateInput) (core.PlanOutcome, error) {
	r, err := loadChannel(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	prev, err := openSecrets(env, r)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	d := in.definition()
	if _, _, err := checkDefinition(ctx, env.DB.Read(), r.ID, r.Kind, d, prev); err != nil {
		return core.PlanOutcome{}, err
	}
	state := "on"
	if !in.Enabled {
		state = "off"
	}
	was := definition{Name: r.Name, MinSeverity: r.MinSeverity, Kinds: r.Kinds, GroupIDs: r.GroupIDs}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("%s channel %s: %s", r.Kind, r.Name, routing(was))},
			{Op: "+", Text: fmt.Sprintf("%s channel %s (%s): %s", r.Kind, in.Name, state, routing(d))},
			{Op: " ", Text: "compensated by notify.channel.update with the previous settings; write-only values are entered again"},
		},
		Guards: []core.Guard{}, StateHash: rowHash(r),
	}, nil
}

func (channelUpdate) Apply(ctx context.Context, env core.Env, in ChannelUpdateInput) (Channel, error) {
	var out Channel
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadChannel(ctx, tx, in.ID)
		if err != nil {
			return core.Definite(err)
		}
		prev, err := openSecrets(env, r)
		if err != nil {
			return err
		}
		c, s, err := checkDefinition(ctx, tx, r.ID, r.Kind, in.definition(), prev)
		if err != nil {
			return core.Definite(err)
		}
		r.Name, r.MinSeverity, r.Kinds, r.GroupIDs, r.Enabled, r.Config = in.Name, in.MinSeverity, in.Kinds, in.GroupIDs, in.Enabled, c
		if r.SecretCT, err = sealSecrets(env, r.ID, s); err != nil {
			return err
		}
		if err := writeChannel(ctx, tx, false, r); err != nil {
			return err
		}
		out, err = channelView(ctx, tx, r.ID)
		return err
	})
	return out, err
}

func (channelUpdate) Verify(ctx context.Context, env core.Env, in ChannelUpdateInput, _ Channel) error {
	r, err := loadChannel(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return err
	}
	if r.Name != in.Name || r.MinSeverity != in.MinSeverity || r.Enabled != in.Enabled ||
		jsonList(r.Kinds) != jsonList(in.Kinds) || jsonList(r.GroupIDs) != jsonList(in.GroupIDs) {
		return fmt.Errorf("notify: channel %s does not hold the updated settings", in.ID)
	}
	return nil
}

type channelDelete struct{}

func (channelDelete) Plan(ctx context.Context, env core.Env, in ChannelIDInput) (core.PlanOutcome, error) {
	r, err := loadChannel(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var pending int
	if err := env.DB.Read().QueryRowContext(ctx, `SELECT count(*) FROM notify_deliveries WHERE channel_id = ? AND state = 'pending'`,
		r.ID).Scan(&pending); err != nil {
		return core.PlanOutcome{}, fmt.Errorf("notify: count pending deliveries: %w", err)
	}
	diff := []core.DiffLine{{Op: "-", Text: fmt.Sprintf("%s channel %s", r.Kind, r.Name)}}
	if pending > 0 {
		diff = append(diff, core.DiffLine{Op: "-", Text: fmt.Sprintf("%d pending %s, never sent", pending,
			map[bool]string{true: "delivery", false: "deliveries"}[pending == 1])})
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: rowHash(r), Confirmation: r.Name}, nil
}

func (channelDelete) Apply(ctx context.Context, env core.Env, in ChannelIDInput) (Channel, error) {
	var out Channel
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if out, err = channelView(ctx, tx, in.ID); err != nil {
			return core.Definite(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM notify_channels WHERE id = ?`, in.ID); err != nil {
			return fmt.Errorf("notify: delete the channel: %w", err)
		}
		return nil
	})
	return out, err
}

func (channelDelete) Verify(ctx context.Context, env core.Env, in ChannelIDInput, _ Channel) error {
	_, err := loadChannel(ctx, env.DB.Read(), in.ID)
	switch {
	case errcode.From(err).Code == errcode.NotFound:
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("notify: channel %s still exists", in.ID)
}

type channelListRead struct{}

func (channelListRead) Read(ctx context.Context, env core.Env, _ NoInput) (ChannelList, error) {
	q := env.DB.Read()
	rows, err := loadChannels(ctx, q, "")
	if err != nil {
		return ChannelList{}, err
	}
	last, err := lastDeliveries(ctx, q)
	if err != nil {
		return ChannelList{}, err
	}
	out := ChannelList{Channels: make([]Channel, 0, len(rows))}
	for _, r := range rows {
		out.Channels = append(out.Channels, view(r, last[r.ID]))
	}
	return out, nil
}
