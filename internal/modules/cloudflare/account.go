package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	// The sealed token's associated data names this table and column.
	accountSealTable  = "cloudflare_accounts"
	accountSealColumn = "token_ct"

	accountResourcePrefix = "cloudflare.account:"
)

// The capabilities a connect reports. The reads are probed with a harmless
// read; the writes stay unverified until a write succeeds. The web account
// panel shows the names verbatim.
const (
	capTunnelsRead  = "tunnels.read"
	capZonesRead    = "zones.read"
	capTunnelsWrite = "tunnels.write"
	capDNSWrite     = "dns.write"
)

// connected loads the one account, opens its token and builds its api;
// cloud.not_connected when there is none.
func (c cf) connected(ctx context.Context, db *store.DB, s core.Sealer, clock core.Clock) (*api, account, error) {
	acct, ok, err := loadAccount(ctx, db.Read())
	if err != nil {
		return nil, account{}, err
	}
	if !ok {
		return nil, account{}, errcode.New(errcode.CloudNotConnected, "no Cloudflare account is connected")
	}
	token, err := cloud.OpenCredential(s, accountSealTable, accountSealColumn, acct.ID, acct.TokenCT)
	if err != nil {
		return nil, account{}, fmt.Errorf("cloudflare: open the account token: %w", err)
	}
	a, err := c.newAPI(acct.AccountID, token, clock)
	if err != nil {
		return nil, account{}, err
	}
	return a, acct, nil
}

// cloudEffect is the effect of one provider write: not local, so it may
// still commit for cloud.CommitWait after the call. An empty before is the
// state of an absent resource.
func cloudEffect(kind core.EffectKind, resource string, before json.RawMessage) core.Effect {
	if len(before) == 0 {
		before = json.RawMessage("null")
	}
	return core.Effect{
		Kind: kind, Resource: resource, Local: false, InverseLocal: false,
		CommitWait: cloud.CommitWait, Before: core.StateDoc{State: before},
	}
}

// accountView is the client-facing form of a stored account, without its
// token.
func accountView(a account) AccountView {
	caps := a.Capabilities
	if caps == nil {
		caps = []cloud.Capability{}
	}
	return AccountView{AccountID: a.AccountID, Capabilities: caps, ConnectedAt: a.ConnectedAt}
}

// accountState is the state document of an account row. It holds no token
// and no capability: a second probe can change the capabilities between the
// connect and its undo, which the engine would read as a conflict.
func accountState(accountID string) json.RawMessage {
	// Marshalling a struct of one string cannot fail.
	b, _ := json.Marshal(struct {
		AccountID string `json:"accountId"`
	}{accountID})
	return b
}

// probeCapabilities proves the token's read capabilities with harmless reads
// and reports the write capabilities unverified; it never writes.
func probeCapabilities(ctx context.Context, a *api) []cloud.Capability {
	return cloud.ProbeReads(ctx, []cloud.Probe{
		{Name: capTunnelsRead, Read: func(ctx context.Context) error { _, err := a.Tunnels(ctx, ""); return err }},
		{Name: capZonesRead, Read: func(ctx context.Context) error { _, err := a.Zones(ctx); return err }},
	}, []string{capTunnelsWrite, capDNSWrite})
}

// cleanupDetail names every pending cleanup item.
func cleanupDetail(pending []string) string {
	return "cleanup is pending: " + strings.Join(pending, "; ")
}

func (c cf) accountGet() core.ActionDef {
	return core.DefineRead[AccountGetInput, AccountStatus](core.ActionSpec{
		Name: ID + ".account.get", Summary: "Show the connected Cloudflare account.", Version: 1, Side: core.SideController,
	}, accountGetHandler{})
}

// accountGetHandler reads the stored account; it calls no provider.
type accountGetHandler struct{}

func (accountGetHandler) Read(ctx context.Context, env core.Env, _ AccountGetInput) (AccountStatus, error) {
	a, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return AccountStatus{}, err
	}
	if !ok {
		return AccountStatus{Connected: false}, nil
	}
	v := accountView(a)
	return AccountStatus{Connected: true, Account: &v}, nil
}
