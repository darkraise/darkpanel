package schema_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/secret"
)

type redactLeaf struct {
	Name string `json:"name"`
	Key  string `json:"key" secret:"true"`
}

type redactTree struct {
	Label string       `json:"label"`
	Inner redactLeaf   `json:"inner"`
	Items []redactLeaf `json:"items"`
}

func encode(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRedact(t *testing.T) {
	created := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	user := auth.User{ID: "01U", Username: "alice", Role: auth.RoleAdmin, State: auth.StatePending, CreatedAt: created}
	token := auth.TokenInfo{ID: "01T", Name: "ci", Scope: "read", CreatedAt: created, ExpiresAt: created.Add(time.Hour)}
	cases := []struct {
		name string
		t    reflect.Type
		in   any
		want string
	}{
		{
			// core.user.create and core.user.reset-totp return an Enrollment.
			"enrollment", reflect.TypeFor[coreactions.Enrollment](),
			coreactions.Enrollment{User: user, TOTPSecret: "JBSWY3DPEHPK3PXP"}, //nolint:gosec // G101: a test value, not a credential
			`{"totpSecret":"[redacted]","user":{"createdAt":"2026-10-17T12:00:00Z","id":"01U","role":"admin","state":"pending","username":"alice"}}`,
		},
		{
			// core.token.create returns a NewToken; the embedded TokenInfo is flattened.
			"new token", reflect.TypeFor[coreactions.NewToken](),
			coreactions.NewToken{TokenInfo: token, Token: "dpt_01T_SECRETSECRET"}, //nolint:gosec // G101: a test value, not a credential
			`{"createdAt":"2026-10-17T12:00:00Z","expiresAt":"2026-10-17T13:00:00Z","id":"01T","lastUsedAt":null,` +
				`"name":"ci","revokedAt":null,"scope":"read","token":"[redacted]"}`,
		},
		{
			"nested and array", reflect.TypeFor[redactTree](),
			redactTree{Label: "l", Inner: redactLeaf{Name: "a", Key: "k1"}, Items: []redactLeaf{{Name: "b", Key: "k2"}, {Name: "c", Key: "k3"}}},
			`{"inner":{"key":"[redacted]","name":"a"},"items":[{"key":"[redacted]","name":"b"},{"key":"[redacted]","name":"c"}],"label":"l"}`,
		},
		{
			// The audit list's inputs were redacted when recorded and pass unchanged.
			"audit list", reflect.TypeFor[coreactions.AuditList](),
			coreactions.AuditList{Entries: []coreactions.AuditEntry{{
				ID: "01A", At: created, Actor: "user:01U", Action: "core.token.create", Version: 1, Phase: "apply",
				Input: coreactions.AuditInput(`{"name":"ci","nested":{"password":"[redacted]"}}`), Result: "ok",
			}}},
			`{"entries":[{"action":"core.token.create","actor":"user:01U","at":"2026-10-17T12:00:00Z","changeId":null,` +
				`"errorCode":null,"hostId":null,"id":"01A","input":{"name":"ci","nested":{"password":"[redacted]"}},` +
				`"phase":"apply","result":"ok","source":null,"version":1}],"nextBefore":null}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := schema.Redact(c.t, encode(t, c.in)); string(got) != c.want {
				t.Errorf("Redact =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestRedactFailsClosed(t *testing.T) {
	// schema.Output fails for a nil type: nothing of the value may come back.
	if got := schema.Redact(nil, json.RawMessage(`{"token":"dpt_x"}`)); string(got) != `"[redacted]"` {
		t.Errorf("Redact(nil) = %s, want \"[redacted]\"", got)
	}
	if got := schema.Redact(reflect.TypeFor[coreactions.NewToken](), json.RawMessage(`{"token":`)); string(got) != `"[redacted]"` {
		t.Errorf("Redact(invalid JSON) = %s, want \"[redacted]\"", got)
	}
}

// TestRedactReusesTheSchema holds Redact to the cost of redacting over a
// schema already built: building one reflects the whole type.
func TestRedactReusesTheSchema(t *testing.T) {
	typ := reflect.TypeFor[redactTree]()
	raw := encode(t, redactTree{Label: "l", Inner: redactLeaf{Name: "a", Key: "k1"}, Items: []redactLeaf{{Name: "b", Key: "k2"}}})
	s, err := schema.Output(typ)
	if err != nil {
		t.Fatal(err)
	}
	schema.Redact(typ, raw)
	redact := testing.AllocsPerRun(20, func() { schema.Redact(typ, raw) })
	direct := testing.AllocsPerRun(20, func() { secret.RedactJSON(raw, s) })
	if redact > direct {
		t.Errorf("Redact allocates %v times a call, RedactJSON over the built schema %v: Redact rebuilds the schema", redact, direct)
	}
}
