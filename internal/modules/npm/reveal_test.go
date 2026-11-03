package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
)

// rvReveal runs the action's reveal handler the way the agent's Engine.Reveal does.
func rvReveal(h *thHost) (identity string, raw json.RawMessage, err error) {
	return h.nm().adminRevealPassword().Handler.Reveal(context.Background(), h.env(""), json.RawMessage(`{}`))
}

// rvCorrupt replaces the sealed password of the stored instance with bytes the sealer cannot open.
func rvCorrupt(t *testing.T, h *thHost) {
	t.Helper()
	ctx := context.Background()
	row, ok, err := loadInstance(ctx, h.db.Read())
	if err != nil || !ok {
		t.Fatalf("instance row: found %v, error %v", ok, err)
	}
	row.PasswordCT = []byte("not a sealed value")
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return saveInstance(ctx, tx, row) }); err != nil {
		t.Fatal(err)
	}
}

func TestRvDeclaration(t *testing.T) {
	def := newTHHost(t).nm().adminRevealPassword()
	s := def.Spec
	if s.Name != "npm.admin.reveal-password" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		!s.Reveal || s.Internal || s.MinScope != core.ScopeAdmin || len(s.LockKeys) != 0 || len(s.Guards) != 0 ||
		s.Available != nil || s.Summary == "" {
		t.Errorf("spec %+v, want the admin-scope reveal read npm.admin.reveal-password v1 on the agent with no lock or guard", s)
	}
	if def.InputType != reflect.TypeFor[RevealInput]() || def.OutputType != reflect.TypeFor[AdminPassword]() {
		t.Errorf("types %v and %v, want RevealInput and AdminPassword", def.InputType, def.OutputType)
	}
	if def.Handler.Reveal == nil || def.Handler.Read != nil || def.Handler.Plan != nil || def.Handler.Apply != nil {
		t.Errorf("handler %+v, want the reveal handler only", def.Handler)
	}
	field, ok := reflect.TypeFor[AdminPassword]().FieldByName("Password")
	if !ok || field.Tag.Get("secret") != "true" {
		t.Errorf("AdminPassword.Password tag %q, want secret:\"true\"", field.Tag)
	}
}

// TestRvRevealReturnsTheStoredAdmin: the answer is the stored email and the unsealed password, the identity names the
// host and not the value, and the read touches neither NPM nor Docker.
func TestRvRevealReturnsTheStoredAdmin(t *testing.T) {
	h := newTHHost(t)
	inst := h.deployed()
	requests, calls := len(h.npm.Requests()), len(h.docker.calls())

	identity, raw, err := rvReveal(h)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "npm admin password, host h1" {
		t.Errorf("identity = %q, want %q", identity, "npm admin password, host h1")
	}
	var members map[string]string
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"email": inst.Email, "password": inst.Password}
	if !reflect.DeepEqual(members, want) {
		t.Errorf("value members %v, want exactly email and password: %v", members, want)
	}
	if strings.Contains(identity, inst.Password) {
		t.Error("the identity carries the password")
	}
	if got := len(h.npm.Requests()); got != requests {
		t.Errorf("the reveal sent %d requests to NPM, want none", got-requests)
	}
	if got := len(h.docker.calls()); got != calls {
		t.Errorf("the reveal made %d Docker calls, want none", got-calls)
	}
}

func TestRvRevealRefusals(t *testing.T) {
	t.Run("no instance is deployed", func(t *testing.T) {
		_, _, err := rvReveal(newTHHost(t))
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.NotFound {
			t.Errorf("error %v, want %s", err, errcode.NotFound)
		}
	})
	t.Run("the sealed password cannot be opened", func(t *testing.T) {
		h := newTHHost(t)
		inst := h.deployed()
		rvCorrupt(t, h)
		_, raw, err := rvReveal(h)
		if err == nil || len(raw) != 0 {
			t.Errorf("value %s, error %v; want an error and no value", raw, err)
		}
		if err != nil && strings.Contains(err.Error(), inst.Password) {
			t.Error("the error carries the password")
		}
	})
}

// TestRvLogSecretsAreThePassword: the source registered for the module answers the stored password for any
// container carrying the module's label, whichever container id it is asked about, and the docker log filter built
// from it masks the password wherever a log line holds it.
func TestRvLogSecretsAreThePassword(t *testing.T) {
	h := newTHHost(t)
	inst := h.deployed()

	for _, id := range []string{inst.ContainerID, "an-upgraded-container-the-row-does-not-hold-yet"} {
		secrets, err := revealLogSecrets(context.Background(), h.env(""), id)
		if err != nil || len(secrets) != 1 || string(secrets[0]) != inst.Password {
			t.Fatalf("secrets for %q = %q, %v; want exactly the stored password", id, secrets, err)
		}
		f := logfilter.New(secrets)
		out := string(f.Push("stdout", []byte("created user with secret "+inst.Password+" ok\n"))) + string(f.Flush("stdout"))
		if strings.Contains(out, inst.Password) || !strings.Contains(out, logfilter.Mask) {
			t.Errorf("filtered line %q, want the password replaced by %q", out, logfilter.Mask)
		}
	}
}

func TestRvLogSecretsWithoutAnInstance(t *testing.T) {
	secrets, err := revealLogSecrets(context.Background(), newTHHost(t).env(""), "c1")
	if secrets != nil || err != nil {
		t.Errorf("secrets = %q, %v; want none and no error before an instance exists", secrets, err)
	}
}

// TestRvLogSecretsFailClosed: a stored password that cannot be opened is an error, which makes docker refuse the log
// read rather than show the logs of the container unmasked.
func TestRvLogSecretsFailClosed(t *testing.T) {
	h := newTHHost(t)
	inst := h.deployed()
	rvCorrupt(t, h)
	secrets, err := revealLogSecrets(context.Background(), h.env(""), inst.ContainerID)
	if err == nil || secrets != nil {
		t.Errorf("secrets = %q, %v; want an error and no secret", secrets, err)
	}
}

// TestRvLogSecretsAreRegistered: the package registered its source with the docker module when it was imported, so
// a second registration for the module id panics.
func TestRvLogSecretsAreRegistered(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering a second source for npm did not panic, so the package registered none")
		}
	}()
	docker.RegisterLogSecrets(ID, func(context.Context, core.Env, string) ([][]byte, error) { return nil, nil })
}
