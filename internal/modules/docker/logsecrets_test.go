package docker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// lsSource answers secrets and err and appends the container ids it is asked about to asked.
func lsSource(asked *[]string, secrets [][]byte, err error) SecretSource {
	return func(_ context.Context, _ core.Env, id string) ([][]byte, error) {
		*asked = append(*asked, id)
		return secrets, err
	}
}

func lsOwnedBy(module string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.Config.Labels[core.LabelModule] = module }
}

func lsNamed(name string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.ID, c.Name = dkID(name), "/"+name }
}

// lsHost holds web (unlabelled), proxy (owned by npm) and tunnel (owned by cloudflare).
func lsHost(t *testing.T) *dkHost {
	t.Helper()
	h := newDKHost(t)
	h.docker.put(rdWeb())
	h.docker.put(rdWeb(rdOwned, lsNamed("proxy")))
	h.docker.put(rdWeb(lsOwnedBy("cloudflare"), lsNamed("tunnel")))
	return h
}

// lsPanics runs do and returns the text of the panic it raised; no panic fails the test.
func lsPanics(t *testing.T, do func()) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Error("no panic")
			return
		}
		msg = fmt.Sprint(r)
	}()
	do()
	return ""
}

func TestLsRegistrationRefusesWhatWouldHideASource(t *testing.T) {
	src := lsSource(new([]string), nil, nil)
	r := newSecretRegistry()
	r.register("npm", src)
	for _, tc := range []struct {
		name string
		do   func()
	}{
		{"an empty module id", func() { r.register("", src) }},
		{"a nil source", func() { r.register("cloudflare", nil) }},
		{"a second registration for a module", func() { r.register("npm", src) }},
		{"RegisterLogSecrets with an empty module id", func() { RegisterLogSecrets("", src) }},
		{"RegisterLogSecrets with a nil source", func() { RegisterLogSecrets("npm", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if msg := lsPanics(t, tc.do); !strings.HasPrefix(msg, "docker: ") {
				t.Errorf("panic %q, want a message that starts with \"docker: \"", msg)
			}
		})
	}
	if _, ok := r.lookup("cloudflare"); ok {
		t.Error("a refused registration was stored")
	}
	if _, ok := r.lookup("npm"); !ok {
		t.Error("the first registration was lost")
	}
}

func TestLsSecretsAreAskedOfTheModuleThatOwnsTheContainer(t *testing.T) {
	h := lsHost(t)
	var asked []string
	r := newSecretRegistry()
	r.register("npm", lsSource(&asked, [][]byte{[]byte("hunter2")}, nil))
	secrets := r.secretsVia(h.docker)
	ctx := context.Background()

	got, err := secrets(ctx, h.env(""), dkID("proxy"))
	if err != nil || !reflect.DeepEqual(got, [][]byte{[]byte("hunter2")}) || !reflect.DeepEqual(asked, []string{dkID("proxy")}) {
		t.Fatalf("secrets of the npm container = %q, %v; asked %v; want hunter2 from one lookup of its id", got, err, asked)
	}
	for _, id := range []string{dkID("tunnel"), dkID("web")} {
		if got, err := secrets(ctx, h.env(""), id); got != nil || err != nil {
			t.Errorf("secrets of %s = %q, %v; want none for a module with no source and for an unlabelled container", id, got, err)
		}
	}
	if len(asked) != 1 {
		t.Errorf("the npm source was asked %v, want only the npm container", asked)
	}
}

func TestLsSecretsFailWhenTheContainerOrItsSourceFails(t *testing.T) {
	h := lsHost(t)
	broken := errors.New("sealed store unreadable")
	r := newSecretRegistry()
	r.register("npm", lsSource(new([]string), nil, broken))
	secrets := r.secretsVia(h.docker)
	ctx := context.Background()

	if got, err := secrets(ctx, h.env(""), dkID("proxy")); !errors.Is(err, broken) || got != nil {
		t.Errorf("a failing source: %q, %v; want its error and no secrets", got, err)
	}
	if _, err := secrets(ctx, h.env(""), "ghost"); errcode.From(err).Code != errcode.NotFound {
		t.Errorf("an absent container: %v, want %s", err, errcode.NotFound)
	}
	h.docker.fail("InspectContainer", fmt.Errorf("%w: dial unix", dockerapi.ErrUnavailable))
	if _, err := secrets(ctx, h.env(""), dkID("proxy")); errcode.From(err).Code != errcode.DockerEngineUnavailable {
		t.Errorf("an unreachable Engine: %v, want %s", err, errcode.DockerEngineUnavailable)
	}
}

func TestLsContainerLogsMaskWhatTheRegisteredSourceReturns(t *testing.T) {
	h, k, _ := rdLogsHost(t, nil, nil, rdOut("token=hun"), rdOut("ter2 ok\n"))
	r := newSecretRegistry()
	r.register("npm", lsSource(new([]string), [][]byte{[]byte("hunter2")}, nil))
	k.d.secrets = r.secretsVia(h.docker)
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"proxy"}`)
	if err != nil {
		t.Fatal(err)
	}
	rdSame(t, raw, `{"lines":[{"stream":"stdout","text":"token=[redacted] ok"}]}`)
}

func TestLsContainerLogsOfAnOwnedContainerFailBeforeTheEngineWhenTheSourceFails(t *testing.T) {
	h, k, d := rdLogsHost(t, nil, nil, rdOut("hunter2\n"))
	broken := errors.New("sealed store unreadable")
	r := newSecretRegistry()
	r.register("npm", lsSource(new([]string), nil, broken))
	k.d.secrets = r.secretsVia(h.docker)
	raw, err := rdRead(t, k.containerLogs(), h, `{"container":"proxy"}`)
	if !errors.Is(err, broken) || len(raw) != 0 || len(d.opts) != 0 {
		t.Fatalf("answer %s, error %v, %d Logs requests: want the source's error and no request for logs", raw, err, len(d.opts))
	}
}
