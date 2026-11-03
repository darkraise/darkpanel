package cloudflare

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker"
)

func init() {
	docker.RegisterLogSecrets(ID, connectorLogSecrets)
}

// connectorLogSecrets is the docker module's source of the secrets in
// cloudflare's containers: the opened tunnel token of the connector whose
// container is containerID, or none for any other container. A token that
// cannot be opened is an error, so the log read is refused rather than served
// unmasked.
func connectorLogSecrets(ctx context.Context, env core.Env, containerID string) ([][]byte, error) {
	row, ok, err := connectorByContainer(ctx, env.DB.Read(), containerID)
	if err != nil || !ok {
		return nil, err
	}
	token, err := env.Sealer.Open(connectorSealAD(row.TunnelID), row.TokenCT)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: open the token of tunnel %s: %w", row.TunnelID, err)
	}
	return [][]byte{token}, nil
}
