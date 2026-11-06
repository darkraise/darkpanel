package cloudflare

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
)

func TestClLogSecretsAreTheStoredToken(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	clSeed(t, h, clTunnelA, "container-a")
	clSeed(t, h, clTunnelB, "container-b")

	for tunnel, id := range map[string]string{clTunnelA: "container-a", clTunnelB: "container-b"} {
		token := agToken(tunnel)
		secrets, err := connectorLogSecrets(ctx, h.env(""), id)
		if err != nil || len(secrets) != 1 || string(secrets[0]) != token {
			t.Fatalf("secrets for %s = %q, %v; want exactly the token of tunnel %s", id, secrets, err, tunnel)
		}
		f := logfilter.New(secrets)
		out := string(f.Push("stdout", []byte("registered tunnel connection with "+token+" ok\n"))) + string(f.Flush("stdout"))
		if strings.Contains(out, token) || !strings.Contains(out, logfilter.Mask) {
			t.Errorf("filtered line %q, want the token replaced by %q", out, logfilter.Mask)
		}
	}
}

func TestClLogSecretsOfAnotherContainerAreNone(t *testing.T) {
	h := newAGHost(t)
	clSeed(t, h, clTunnelA, "container-a")
	for _, id := range []string{"container-of-another-module", "", "container-"} {
		secrets, err := connectorLogSecrets(context.Background(), h.env(""), id)
		if secrets != nil || err != nil {
			t.Errorf("secrets for %q = %q, %v; want none and no error", id, secrets, err)
		}
	}
	empty, err := connectorLogSecrets(context.Background(), newAGHost(t).env(""), "container-a")
	if empty != nil || err != nil {
		t.Errorf("secrets before any connector exists = %q, %v; want none and no error", empty, err)
	}
}

// TestClLogSecretsFailClosed: a stored token that cannot be opened is an error, which makes docker refuse the log
// read rather than show the connector's logs unmasked.
func TestClLogSecretsFailClosed(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	clSeed(t, h, clTunnelA, "container-a")
	env := h.env("")
	sealedForAnother, err := env.Sealer.Seal(connectorSealAD(clTunnelB), []byte(agToken(clTunnelA)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := deleteConnector(ctx, tx, clTunnelA); err != nil {
			return err
		}
		return insertConnector(ctx, tx, connectorRow{TunnelID: clTunnelA, ContainerID: "container-a", TokenCT: sealedForAnother,
			ChangeID: "c0", CreatedAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := connectorLogSecrets(ctx, env, "container-a")
	if err == nil || secrets != nil {
		t.Fatalf("secrets = %q, %v; want an error and no secret", secrets, err)
	}
	if strings.Contains(err.Error(), agToken(clTunnelA)) {
		t.Error("the error carries the token")
	}
}

// TestClLogSecretsAreRegistered: the package registered its source with the docker module when it was imported, so
// a second registration for the module id panics.
func TestClLogSecretsAreRegistered(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering a second source for cloudflare did not panic, so the package registered none")
		}
	}()
	docker.RegisterLogSecrets(ID, func(context.Context, core.Env, string) ([][]byte, error) { return nil, nil })
}
