package notify

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/notify/send"
)

// testTitle is the title of a test notification (spec section 4).
const testTitle = "Test notification from Darkpanel"

// TestResult is the output of notify.channel.test/v1.
type TestResult struct {
	Delivered bool   `json:"delivered" jsonschema_description:"Whether the provider accepted the test notification."`
	Code      string `json:"code" jsonschema_description:"The HTTP status or SMTP reply code the provider answered, or timeout, network, tls or invalid; never the provider's text."`
}

// controllerDeps is what the controller contribution hands the actions that
// need more than core.Env: the audit log notify.channel.test writes to.
var controllerDeps struct {
	sync.RWMutex
	audit core.Audit
}

func setAudit(a core.Audit) {
	controllerDeps.Lock()
	defer controllerDeps.Unlock()
	controllerDeps.audit = a
}

func auditLog() core.Audit {
	controllerDeps.RLock()
	defer controllerDeps.RUnlock()
	return controllerDeps.audit
}

// sender sends every notification of the module.
var sender = send.New(send.Options{})

func testAction() core.ActionDef {
	return core.DefineRead[ChannelIDInput, TestResult](core.ActionSpec{
		Name: ID + ".channel.test", Version: 1, Side: core.SideController, HostData: core.HostDataFleet,
		MinScope: core.ScopeAdmin,
		Summary:  "Send one test notification on a channel, ignoring its routing and the retry schedule; reports the provider's status or reply code.",
	}, channelTestRead{})
}

type channelTestRead struct{}

// Read makes one send attempt and audits it. It sends, so it refuses a
// caller below admin itself rather than trust only the route's scope.
func (channelTestRead) Read(ctx context.Context, env core.Env, in ChannelIDInput) (TestResult, error) {
	p := env.Principal
	if !p.Access.Admin && p.Scope != core.ScopeAdmin {
		return TestResult{}, errcode.New(errcode.Forbidden, "testing a notification channel needs the admin scope")
	}
	audit := auditLog()
	if audit == nil {
		return TestResult{}, errors.New("notify: no audit log to record the test in")
	}
	r, err := loadChannel(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return TestResult{}, err
	}
	secrets, err := openSecrets(env, r)
	if err != nil {
		return TestResult{}, err
	}
	now := env.Clock.Now()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return TestResult{}, fmt.Errorf("notify: test event id: %w", err)
	}
	ev := send.Event{ID: id.String(), Kind: core.NotifyTest, Severity: core.SeverityInfo, Title: testTitle,
		Body: "Channel " + r.Name + " can deliver Darkpanel notifications.", CreatedAt: now}
	code, sendErr := sender.Send(ctx, sendChannel(r, secrets), ev)
	out := TestResult{Delivered: sendErr == nil, Code: code}
	result := code
	if out.Delivered {
		result = "delivered"
	}
	input, err := json.Marshal(ChannelIDInput{ID: r.ID})
	if err != nil {
		return TestResult{}, err
	}
	// The attempt was made, so its record is written even when the caller
	// has gone.
	if err := audit.Record(context.WithoutCancel(ctx), core.AuditEntry{Actor: p.Actor, Action: ID + ".channel.test",
		Phase: "send", Result: result, Source: p.Source, At: now, Version: 1, Input: input}); err != nil {
		return TestResult{}, fmt.Errorf("notify: audit the channel test: %w", err)
	}
	if sendErr != nil {
		env.Log.Info("notify: a channel test was not delivered", "channel", r.ID, "kind", r.Kind, "code", code)
	}
	return out, nil
}

// sendChannel is r with its opened write-only values, as a sender takes it.
func sendChannel(r channelRow, s map[string]string) send.Channel {
	c := r.Config
	return send.Channel{
		ID: r.ID, Kind: r.Kind,
		Server: c.Server, Topic: s[secretTopic], Token: s[secretToken],
		BotToken: s[secretBotToken], ChatID: c.ChatID,
		WebhookURL: s[secretWebhookURL],
		Host:       c.Host, Port: c.Port, Security: c.Security, Username: c.Username, Password: s[secretPassword],
		From: c.From, Recipients: c.Recipients,
		URL: s[secretURL], Secret: s[secretHMAC],
	}
}
