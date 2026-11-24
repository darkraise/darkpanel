//go:build harness

package harness

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	metricsctl "github.com/darkraise/darkpanel/internal/modules/metrics/controller"
	"github.com/darkraise/darkpanel/internal/modules/notify"
	"github.com/darkraise/darkpanel/internal/modules/notify/send"
)

var m10Flag = flag.Bool("m10", false, "run TestM10; needs -os and -m2, and is a go test run of its own")

const (
	m10Deadline = 30 * time.Minute
	// m10MetricsWithin is how soon after enrollment the agent's first minute must be stored (A34).
	m10MetricsWithin = 2 * time.Minute
	// m10AlertWithin covers the rule's minute, the next poll and evaluation, and the outbox's delivery.
	m10AlertWithin = 6 * time.Minute
	m10RuleName    = "M10 disk over 1 percent"
	m10Channel     = "M10 runner"
	m10Group       = "m10-agents"
	m10Member      = "m10-viewer"
	// m10HookHost is the runner as a VM's user-mode network sees it: QEMU forwards its gateway's address to the
	// runner's loopback.
	m10HookHost = "10.0.2.2"
	m10HookPath = "/m10"
	m10HookMax  = 64 << 10
	m10Port     = 8093
)

// m10Run is what the steps of TestM10 share.
type m10Run struct {
	ctx context.Context
	f   *fleet
	// enrolled is when the agent's install, and with it its enrollment, finished.
	enrolled time.Time
}

// TestM10 is the M10 harness job (A34): metrics arrive within m10MetricsWithin of an agent's enrollment, a disk
// rule at 1 % fires to a webhook channel on the runner, which receives a signed alert.firing, and a viewer member
// is refused a firewall change on a host outside its group as not found and on a host inside it as forbidden. The
// workflow selects it with -run, -os, -m2 and -m10.
func TestM10(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m10Flag {
		t.Skip("no -os, -m2 or -m10 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m10Deadline)
	t.Cleanup(cancel)
	f := bootFleet(ctx, t)
	serveReleases(ctx, t, f)
	r := &m10Run{ctx: ctx, f: f}
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, func(t *testing.T) { scenarioInstall(ctx, t, f); r.enrolled = time.Now() }},
		{"metrics", false, r.metrics},
		{"alert-webhook", false, r.alertWebhook},
		{"member-access", false, r.memberAccess},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// metrics waits for the agent's first stored minute, at most m10MetricsWithin after its enrollment.
func (r *m10Run) metrics(t *testing.T) {
	var h metricsctl.History
	err := eventually(r.ctx, time.Until(r.enrolled.Add(m10MetricsWithin)), func(ctx context.Context) error {
		in := metricsctl.HistoryInput{HostID: r.f.agentID, Range: "1h"}
		if err := r.f.api.Read(ctx, "", "metrics.history.get/v1", in, &h); err != nil {
			return err
		}
		return m10xFirstPoint(h)
	})
	if err != nil {
		t.Fatalf("no metrics within %s of enrollment: %v", m10MetricsWithin, err)
	}
	t.Logf("first point %s, %d points", h.Points[0].TS.Format(time.RFC3339), len(h.Points))
}

// alertWebhook adds a webhook channel pointing at a receiver on the runner and a disk rule at 1 %, which every
// host's root filesystem passes, and waits for the receiver to get the agent's alert.firing, signed with the
// channel's key. The alert list then shows the alert firing.
func (r *m10Run) alertWebhook(t *testing.T) {
	hook := m10StartHook(t, rand.Text())
	probe := "curl -fsS --max-time 10 -o /dev/null -X POST --data '{}' " + hook.url()
	if err := eventually(r.ctx, time.Minute, func(ctx context.Context) error {
		_, err := r.f.controller.Run(ctx, probe)
		return err
	}); err != nil {
		t.Fatalf("the controller cannot reach the runner's webhook at %s: %v", hook.url(), err)
	}
	ch := notify.ChannelInput{Kind: "webhook", Name: m10Channel, MinSeverity: "info",
		Webhook: &notify.WebhookInput{URL: hook.url(), Secret: hook.secret}}
	if _, err := r.f.api.Apply(r.ctx, "", "notify.channel.create/v1", ch, nil); err != nil {
		t.Fatal(err)
	}
	var rule metricsctl.Rule
	in := metricsctl.RuleInput{Name: m10RuleName, Metric: "disk", Comparator: ">", Threshold: 1, DurationMin: 1,
		Severity: "warning", Target: "*"}
	if _, err := r.f.api.Apply(r.ctx, "", "metrics.alert.create/v1", in, &rule); err != nil {
		t.Fatal(err)
	}
	if err := eventually(r.ctx, m10AlertWithin, func(context.Context) error {
		return m10xFiring(hook.deliveries(), r.f.agentID, m10RuleName)
	}); err != nil {
		t.Fatalf("the webhook got no alert.firing for the agent: %v", err)
	}
	var list metricsctl.AlertList
	if err := r.f.api.Read(r.ctx, "", "metrics.alert.list/v1", metricsctl.NoInput{}, &list); err != nil {
		t.Fatal(err)
	}
	if err := m10xListed(list, rule.ID, r.f.agentID); err != nil {
		t.Error(err)
	}
}

// memberAccess puts the agent's host in a group, invites a member with a viewer grant on it, enrolls the member
// through its link, and checks that the member lists only the agent's host, at read, and that its firewall change
// is not found on the controller's own host and forbidden on the agent's.
func (r *m10Run) memberAccess(t *testing.T) {
	var g coreactions.Group
	if _, err := r.f.api.Apply(r.ctx, "", "core.group.create/v1", coreactions.GroupCreateInput{Name: m10Group}, &g); err != nil {
		t.Fatal(err)
	}
	members := coreactions.GroupMembersInput{ID: g.ID, HostIDs: []string{r.f.agentID}}
	if _, err := r.f.api.Apply(r.ctx, "", "core.group.set-members/v1", members, nil); err != nil {
		t.Fatal(err)
	}
	var inv coreactions.Invitation
	invite := coreactions.InviteInput{Username: m10Member, Role: auth.RoleMember, Grants: []auth.Grant{{Target: g.ID, Role: "viewer"}}}
	if _, err := r.f.api.Apply(r.ctx, "", "core.user.invite/v1", invite, &inv); err != nil {
		t.Fatal(err)
	}
	member := NewAPI(apiURL, r.f.controller.DialContext)
	if err := m10Enroll(r.ctx, member, inv.Token, rand.Text()); err != nil {
		t.Fatal(err)
	}
	hosts, err := member.Hosts(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := m10xMemberHosts(hosts, r.f.agentID); err != nil {
		t.Error(err)
	}
	_, err = member.Apply(r.ctx, r.f.localID, "firewall.rule.add/v1", m3PortRule(m10Port), nil)
	if err := m10xRefused(err, http.StatusNotFound, "request.not_found"); err != nil {
		t.Errorf("a firewall change on the controller's host, outside the group: %v", err)
	}
	_, err = member.Apply(r.ctx, r.f.agentID, "firewall.rule.add/v1", m3PortRule(m10Port), nil)
	if err := m10xRefused(err, http.StatusForbidden, "auth.forbidden"); err != nil {
		t.Errorf("a firewall change on the agent's host, inside the group: %v", err)
	}
}

// m10Enroll uses the enrollment link's token as the enrollment page does: begin returns the TOTP secret, and
// finish sets password, checks the secret's current code and signs a in.
func m10Enroll(ctx context.Context, a *API, token, password string) error {
	var start auth.EnrollmentStart
	if err := a.do(ctx, http.MethodPost, "/api/v1/enroll/begin", map[string]string{"token": token}, &start); err != nil {
		return fmt.Errorf("enroll begin: %w", err)
	}
	code, err := auth.TOTPCode(start.Secret, time.Now())
	if err != nil {
		return err
	}
	var s struct {
		CSRFToken *string `json:"csrfToken"`
	}
	body := map[string]string{"token": token, "password": password, "code": code}
	if err := a.do(ctx, http.MethodPost, "/api/v1/enroll/finish", body, &s); err != nil {
		return fmt.Errorf("enroll finish: %w", err)
	}
	if s.CSRFToken == nil || *s.CSRFToken == "" {
		return errors.New("harness: enroll finish answered no CSRF token")
	}
	a.csrf = *s.CSRFToken
	return nil
}

// m10Delivery is one body the receiver got, and whether its signature matched the channel's key.
type m10Delivery struct {
	Kind, Severity, HostID, Title string
	Signed                        bool
}

// m10Hook is the webhook receiver on the runner's loopback.
type m10Hook struct {
	port   int
	secret string
	mu     sync.Mutex
	got    []m10Delivery
}

// m10StartHook starts a receiver that checks each body's signature against secret; the test's cleanup stops it.
func m10StartHook(t *testing.T, secret string) *m10Hook {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &m10Hook{port: ln.Addr().(*net.TCPAddr).Port, secret: secret}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return h
}

func (h *m10Hook) url() string {
	return "http://" + net.JoinHostPort(m10HookHost, strconv.Itoa(h.port)) + m10HookPath
}

func (h *m10Hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, m10HookMax))
	if err != nil || r.Method != http.MethodPost || r.URL.Path != m10HookPath {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if d, ok := m10xDelivery(body, r.Header.Get(send.SignatureHeader), h.secret); ok {
		h.mu.Lock()
		h.got = append(h.got, d)
		h.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *m10Hook) deliveries() []m10Delivery {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.got)
}

// m10xDelivery decodes a webhook body; a body with no kind, such as the reachability probe's, is not a delivery.
func m10xDelivery(body []byte, signature, secret string) (m10Delivery, bool) {
	var b struct {
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
		HostID   string `json:"hostId"`
		Title    string `json:"title"`
	}
	if json.Unmarshal(body, &b) != nil || b.Kind == "" {
		return m10Delivery{}, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return m10Delivery{Kind: b.Kind, Severity: b.Severity, HostID: b.HostID, Title: b.Title,
		Signed: hmac.Equal([]byte(signature), []byte(want))}, true
}

// m10xFirstPoint accepts a history with a point that reads like a real host's: memory and a root filesystem.
func m10xFirstPoint(h metricsctl.History) error {
	if len(h.Points) == 0 {
		if h.Status != nil && h.Status.LastErrorCode != nil {
			return fmt.Errorf("harness: no points yet; the last poll failed with %s", *h.Status.LastErrorCode)
		}
		return errors.New("harness: no points yet")
	}
	p := h.Points[0]
	if p.MemTotal == 0 {
		return fmt.Errorf("harness: the point at %s has no memory total", p.TS.Format(time.RFC3339))
	}
	for _, fs := range p.FS {
		if fs.Mount == "/" && fs.Total > 0 {
			return nil
		}
	}
	return fmt.Errorf("harness: the point at %s has no root filesystem", p.TS.Format(time.RFC3339))
}

// m10xFiring accepts deliveries holding a signed alert.firing of rule on host.
func m10xFiring(ds []m10Delivery, host, rule string) error {
	var seen []string
	for _, d := range ds {
		if d.Kind == core.NotifyAlertFiring && d.HostID == host && strings.HasPrefix(d.Title, rule+" on ") {
			if !d.Signed {
				return fmt.Errorf("harness: the alert.firing %q has no valid signature", d.Title)
			}
			return nil
		}
		seen = append(seen, d.Kind+" "+d.HostID)
	}
	return fmt.Errorf("harness: no alert.firing of %q on %s among %d deliveries %v", rule, host, len(ds), seen)
}

// m10xListed accepts an alert list holding rule's alert on host as firing.
func m10xListed(l metricsctl.AlertList, rule, host string) error {
	for _, a := range l.Alerts {
		if a.RuleID == rule && a.HostID == host && a.State == "firing" {
			return nil
		}
	}
	return fmt.Errorf("harness: the alert list has no firing alert of rule %s on %s: %+v", rule, host, l.Alerts)
}

// m10xMemberHosts accepts a member's host list holding only host, at read.
func m10xMemberHosts(hosts []core.Host, host string) error {
	if len(hosts) != 1 || hosts[0].ID != host || hosts[0].Scope != core.ScopeRead {
		got := make([]string, len(hosts))
		for i, h := range hosts {
			got[i] = h.ID + " at " + string(h.Scope)
		}
		return fmt.Errorf("harness: the member lists %v, want only %s at read", got, host)
	}
	return nil
}

// m10xRefused accepts an error that is an API answer of status with code.
func m10xRefused(err error, status int, code string) error {
	var apiErr *APIError
	switch {
	case err == nil:
		return fmt.Errorf("harness: the change was planned, want %d %s", status, code)
	case !errors.As(err, &apiErr):
		return fmt.Errorf("harness: %w, want the API's %d %s", err, status, code)
	case apiErr.Status != status || apiErr.Code != code:
		return fmt.Errorf("harness: the API answered %d %s, want %d %s", apiErr.Status, apiErr.Code, status, code)
	}
	return nil
}
