package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

const (
	jeHost   = "01J9X4K8Q2M5N7P0R3S6T8V1H2"
	jeChange = "01J9X4K8Q2M5N7P0R3S6T8V1W9"
)

var jeKeysPath = "/api/v2/tailnet/" + fake.Tailnet + "/keys"

var _ core.EnrollJoiner = module{}

// jeEnv is a connected account whose OAuth client may assign tag:ci and tag:web, and the module over the fake.
type jeEnv struct {
	h   *tnHost
	srv *fake.Server
	m   module
}

func jeSetup(t *testing.T) jeEnv {
	t.Helper()
	h := newTNHost(t)
	srv := fake.New(t)
	acct := acConnect(t, h, srv)
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE tailnet_accounts SET tags_allowed_json = ? WHERE id = ?`, `["tag:ci","tag:web"]`, acct.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return jeEnv{h: h, srv: srv, m: module{t: tnWith(srv)}}
}

func (e jeEnv) request(tags ...string) core.JoinRequest {
	return core.JoinRequest{HostID: jeHost, Tags: tags, Expires: e.h.clock.Now().Add(time.Hour)}
}

func (e jeEnv) prepare(tags ...string) (string, error) {
	return e.m.PrepareJoin(context.Background(), e.h.env(jeChange), e.request(tags...))
}

func jeRow(t *testing.T, e jeEnv) keyRow {
	t.Helper()
	r, ok, err := loadKey(context.Background(), e.h.db.Read(), "darkpanel-enroll-"+jeHost)
	if err != nil || !ok {
		t.Fatalf("tailnet_keys row for the enroll key: ok %v, err %v", ok, err)
	}
	return r
}

func jeRows(t *testing.T, e jeEnv) int {
	t.Helper()
	rows, err := listKeys(context.Background(), e.h.db.Read())
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func jeCreates(e jeEnv) int {
	n := 0
	for _, r := range e.srv.Cloud().Requests() {
		if r.Method == http.MethodPost && r.Path == jeKeysPath {
			n++
		}
	}
	return n
}

func jeCode(t *testing.T, err error) *errcode.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	return errcode.From(err)
}

func jeHasField(e *errcode.Error, path string) bool {
	return slices.ContainsFunc(e.Fields, func(f errcode.FieldError) bool { return f.Path == path })
}

func TestJEModuleIsAnEnrollJoiner(t *testing.T) {
	if _, ok := Module().(core.EnrollJoiner); !ok {
		t.Fatal("the tailnet module does not implement core.EnrollJoiner: core.host.create finds it by a type assertion")
	}
}

func TestJEPlanJoin(t *testing.T) {
	e := jeSetup(t)
	ctx := context.Background()

	diff, state, err := e.m.PlanJoin(ctx, e.h.env(""), core.JoinRequest{Tags: []string{"tag:web", "tag:ci", "tag:ci"}})
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Error("the plan state is empty")
	}
	if len(diff) == 0 {
		t.Fatal("no diff lines")
	}
	var text strings.Builder
	for _, l := range diff {
		if l.Op != "+" {
			t.Errorf("line %+v: a join only adds", l)
		}
		text.WriteString(l.Text + "\n")
	}
	for _, want := range []string{"tag:ci, tag:web", "one use", "preauthorized", "not ephemeral", "1h0m0s", "darkpanel-enroll-<host id>", "tailnet_keys"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the diff lacks %q:\n%s", want, text.String())
		}
	}

	diff, _, err = e.m.PlanJoin(ctx, e.h.env(""), e.request("tag:ci"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "darkpanel-enroll-"+jeHost) }) {
		t.Errorf("with a host id the diff names the description: %v", diff)
	}

	if n := jeRows(t, e); n != 0 {
		t.Errorf("planning wrote %d key rows", n)
	}
	if n := jeCreates(e); n != 0 {
		t.Errorf("planning sent %d create requests", n)
	}
}

func TestJEPlanJoinState(t *testing.T) {
	e := jeSetup(t)
	state := func(tags ...string) string {
		_, s, err := e.m.PlanJoin(context.Background(), e.h.env(""), core.JoinRequest{Tags: tags})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if a, b := state("tag:ci", "tag:web"), state("tag:web", "tag:ci", "tag:ci"); a != b {
		t.Errorf("the same tags in another order and with a repeat give different states: %s, %s", a, b)
	}
	if a, b := state("tag:ci"), state("tag:web"); a == b {
		t.Error("different tags give one state")
	}
}

func TestJEPlanJoinRefusals(t *testing.T) {
	e := jeSetup(t)
	ctx := context.Background()
	for name, tags := range map[string][]string{
		"no tags":                   nil,
		"a tag outside tagsAllowed": {"tag:db"},
		"a malformed tag":           {"ci"},
		"one good and one bad tag":  {"tag:ci", "tag:db"},
	} {
		_, _, err := e.m.PlanJoin(ctx, e.h.env(""), core.JoinRequest{Tags: tags})
		if c := jeCode(t, err); c.Code != errcode.InvalidInput || !jeHasField(c, "/tags") {
			t.Errorf("%s: %v, want request.invalid_input naming /tags", name, err)
		}
	}

	bare := jeEnv{h: newTNHost(t), srv: e.srv, m: e.m}
	_, _, err := bare.m.PlanJoin(ctx, bare.h.env(""), core.JoinRequest{Tags: []string{"tag:ci"}})
	if c := jeCode(t, err); c.Code != errcode.CloudNotConnected {
		t.Errorf("without an account: %v, want %s", err, errcode.CloudNotConnected)
	}
}

func TestJEPrepareJoinCreatesTheKey(t *testing.T) {
	e := jeSetup(t)
	key, err := e.prepare("tag:web", "tag:ci")
	if err != nil {
		t.Fatal(err)
	}
	keys := e.srv.Keys()
	if len(keys) != 1 {
		t.Fatalf("the provider holds %d keys, want 1", len(keys))
	}
	k := keys[0]
	if key == "" || key != k.Key {
		t.Errorf("returned key %q, provider key %q: PrepareJoin returns the secret the provider answered", key, k.Key)
	}
	desc := "darkpanel-enroll-" + jeHost
	if k.Description != desc || !keyNamespace.MatchString(desc) {
		t.Errorf("description %q; it must be darkpanel-enroll-<host id> and inside the sweep's namespace", k.Description)
	}
	if k.Reusable || k.Ephemeral || !k.Preauthorized || !reflect.DeepEqual(k.Tags, []string{"tag:ci", "tag:web"}) {
		t.Errorf("key %+v: want one use, preauthorized, not ephemeral, tags sorted", k)
	}
	if k.Expires.Sub(k.Created) != time.Hour {
		t.Errorf("the key lives %v, want 1h", k.Expires.Sub(k.Created))
	}

	var body struct {
		Description   string `json:"description"`
		ExpirySeconds int    `json:"expirySeconds"`
		Capabilities  struct {
			Devices struct {
				Create struct {
					Reusable, Ephemeral, Preauthorized bool
					Tags                               []string
				} `json:"create"`
			} `json:"devices"`
		} `json:"capabilities"`
	}
	for _, r := range e.srv.Cloud().Requests() {
		if r.Method == http.MethodPost && r.Path == jeKeysPath {
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatal(err)
			}
		}
	}
	c := body.Capabilities.Devices.Create
	if body.Description != desc || body.ExpirySeconds != 3600 || c.Reusable || c.Ephemeral || !c.Preauthorized || !reflect.DeepEqual(c.Tags, []string{"tag:ci", "tag:web"}) {
		t.Errorf("create request %+v", body)
	}

	row := jeRow(t, e)
	if row.Kind != "enroll" || row.OwnerID != jeHost || row.State != "live" || row.KeyID != k.ID || row.ExpirySeconds != 3600 ||
		!reflect.DeepEqual(row.Tags, []string{"tag:ci", "tag:web"}) || !row.RequestedAt.Equal(e.h.clock.Now()) {
		t.Errorf("row %+v", row)
	}
	if eff := e.h.journal.effects(); len(eff) != 0 {
		t.Errorf("the journal holds %d effects: the sweep, not the change, cleans up an enroll key", len(eff))
	}
	var stored string
	if err := e.h.db.Read().QueryRow(`SELECT group_concat(description || kind || owner_id || key_id || state || tags_json) FROM tailnet_keys`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, key) {
		t.Error("tailnet_keys holds the key secret")
	}
}

// jeTap observes each request the module sends, before it reaches the fake.
type jeTap struct {
	inner http.RoundTripper
	on    func(*http.Request)
}

func (t jeTap) RoundTrip(r *http.Request) (*http.Response, error) {
	t.on(r)
	return t.inner.RoundTrip(r)
}

// TestJEPrepareJoinRecordsTheRowFirst: when the create request leaves, its row already exists as requested,
// so a key committed whatever happens next is always sweepable.
func TestJEPrepareJoinRecordsTheRowFirst(t *testing.T) {
	e := jeSetup(t)
	seen := ""
	client := e.srv.Client()
	client.Transport = jeTap{inner: client.Transport, on: func(r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == jeKeysPath {
			row, ok, err := loadKey(context.Background(), e.h.db.Read(), "darkpanel-enroll-"+jeHost)
			switch {
			case err != nil:
				seen = "error: " + err.Error()
			case !ok:
				seen = "no row"
			default:
				seen = row.State + "/" + row.Kind + "/" + row.OwnerID
			}
		}
	}}
	e.m.t.d.http = client
	if _, err := e.prepare("tag:ci"); err != nil {
		t.Fatal(err)
	}
	if want := "requested/enroll/" + jeHost; seen != want {
		t.Errorf("at the create request the row was %q, want %q", seen, want)
	}
}

func TestJEPrepareJoinFailures(t *testing.T) {
	t.Run("a definite refusal settles the row as deleted", func(t *testing.T) {
		e := jeSetup(t)
		e.srv.SetScopes("devices:core")
		_, err := e.prepare("tag:ci")
		if c := jeCode(t, err); c.Code != errcode.CloudPermissionMissing || !core.IsDefinite(err) {
			t.Fatalf("error %v (definite %v), want a definite %s", err, core.IsDefinite(err), errcode.CloudPermissionMissing)
		}
		if r := jeRow(t, e); r.State != "deleted" {
			t.Errorf("row %+v, want deleted: nothing was created", r)
		}
		if n := len(e.srv.Keys()); n != 0 {
			t.Errorf("provider holds %d keys", n)
		}
	})
	for name, fault := range map[string]cloudfake.Fault{
		"a server error":           {Status: http.StatusInternalServerError},
		"a lost answer":            {LoseResponse: true},
		"an answer with no id":     {Status: http.StatusOK, Body: `{"key":"tskey-auth-noid","expires":"2026-11-01T00:00:00Z","description":"darkpanel-enroll-` + jeHost + `"}`},
		"an answer with no secret": {Status: http.StatusOK, Body: `{"id":"k-nosecret","key":"","expires":"2026-11-01T00:00:00Z","description":"darkpanel-enroll-` + jeHost + `"}`},
	} {
		t.Run(name+" leaves the row uncertain for the sweep", func(t *testing.T) {
			e := jeSetup(t)
			e.srv.Cloud().Fail(http.MethodPost, jeKeysPath, fault)
			key, err := e.prepare("tag:ci")
			if c := jeCode(t, err); c.Code != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
				t.Fatalf("error %v (definite %v), want an uncertain outcome", err, core.IsDefinite(err))
			}
			if key != "" {
				t.Error("a key was returned with the failure")
			}
			if r := jeRow(t, e); r.State != "uncertain" || r.KeyID != "" || r.Kind != "enroll" || r.OwnerID != jeHost {
				t.Errorf("row %+v, want an uncertain enroll row with no key id", r)
			}
		})
	}
}

// TestJEPrepareJoinRefusesBeforeAnyWrite: a request the account cannot serve records no row and sends nothing.
func TestJEPrepareJoinRefusesBeforeAnyWrite(t *testing.T) {
	e := jeSetup(t)
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"a tag outside tagsAllowed": func() error { _, err := e.prepare("tag:db"); return err },
		"no tags":                   func() error { _, err := e.prepare(); return err },
		"a host id that is not one": func() error {
			_, err := e.m.PrepareJoin(ctx, e.h.env(jeChange), core.JoinRequest{HostID: "host-1", Tags: []string{"tag:ci"}})
			return err
		},
		"no host id": func() error {
			_, err := e.m.PrepareJoin(ctx, e.h.env(jeChange), core.JoinRequest{Tags: []string{"tag:ci"}})
			return err
		},
	} {
		if err := run(); err == nil || !core.IsDefinite(err) {
			t.Errorf("%s: %v, want a definite refusal", name, err)
		}
	}
	bare := jeEnv{h: newTNHost(t), srv: e.srv, m: e.m}
	_, err := bare.prepare("tag:ci")
	if c := jeCode(t, err); c.Code != errcode.CloudNotConnected || !core.IsDefinite(err) {
		t.Errorf("without an account: %v, want a definite %s", err, errcode.CloudNotConnected)
	}
	if n := jeRows(t, e) + jeRows(t, bare); n != 0 {
		t.Errorf("refusals wrote %d key rows", n)
	}
	if n := jeCreates(e); n != 0 {
		t.Errorf("refusals sent %d create requests", n)
	}
}

// TestJEPrepareJoinTwiceForOneHost: a host gets one enroll key; the second call fails on its row before it
// reaches the provider.
func TestJEPrepareJoinTwiceForOneHost(t *testing.T) {
	e := jeSetup(t)
	if _, err := e.prepare("tag:ci"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.prepare("tag:ci"); err == nil {
		t.Fatal("a second key was created for one host")
	}
	if n := jeCreates(e); n != 1 {
		t.Errorf("%d create requests, want 1", n)
	}
	if n := len(e.srv.Keys()); n != 1 {
		t.Errorf("provider holds %d keys, want 1", n)
	}
}
