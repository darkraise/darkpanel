package npm

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker"
)

func init() {
	docker.RegisterLogSecrets(ID, revealLogSecrets)
}

func (n nm) adminRevealPassword() core.ActionDef {
	return core.DefineReveal[RevealInput, AdminPassword](core.ActionSpec{
		Name: ID + ".admin.reveal-password", Summary: "Reveal the Nginx Proxy Manager administrator's password.", Version: 1,
		Side: core.SideAgent, MinScope: core.ScopeAdmin,
	}, func(ctx context.Context, env core.Env, _ RevealInput) (core.Revealed[AdminPassword], error) {
		email, password, ok, err := storedAdmin(ctx, env)
		if err != nil {
			return core.Revealed[AdminPassword]{}, err
		}
		if !ok {
			return core.Revealed[AdminPassword]{}, errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host")
		}
		return core.Revealed[AdminPassword]{
			Identity: "npm admin password, host " + env.HostID,
			Value:    AdminPassword{Email: email, Password: string(password)},
		}, nil
	})
}

// revealLogSecrets is the docker module's source of the secrets in npm's containers: the stored administrator
// password, or none before an instance exists. A password that cannot be opened is an error, so the log read is
// refused rather than served unmasked.
func revealLogSecrets(ctx context.Context, env core.Env, _ string) ([][]byte, error) {
	// The container id is not compared with the instance row: docker asks only about containers labelled
	// darkpanel.module=npm, and the new container of an upgrade in progress has an id the row does not hold yet.
	_, password, ok, err := storedAdmin(ctx, env)
	if err != nil || !ok {
		return nil, err
	}
	return [][]byte{password}, nil
}

// storedAdmin reads the stored administrator's email and unsealed password from the instance row, and whether an
// instance exists. It calls neither NPM nor Docker.
func storedAdmin(ctx context.Context, env core.Env) (email string, password []byte, ok bool, err error) {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil || !ok {
		return "", nil, ok, err
	}
	password, err = env.Sealer.Open("npm_instances.password_ct.1", row.PasswordCT)
	if err != nil {
		return "", nil, false, fmt.Errorf("npm: open the admin password: %w", err)
	}
	return row.AdminEmail, password, true, nil
}
