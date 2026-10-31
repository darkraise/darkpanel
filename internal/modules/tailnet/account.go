package tailnet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	// The sealed client secret's associated data names this table and column.
	accountSecretTable  = "tailnet_accounts"
	accountSecretColumn = "client_secret_ct"

	accountResourcePrefix = "tailnet.account:"
)

// accountReadCapabilities are probed with a harmless read, in this order, and
// accountWriteCapabilities stay unverified until a write succeeds. The web account
// panel shows the names verbatim.
var (
	accountReadCapabilities  = []string{"devices:core:read", "auth_keys:read", "policy_file:read"}
	accountWriteCapabilities = []string{"devices:core", "devices:routes", "auth_keys", "policy_file"}
)

var (
	accountTailnetPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,253}$`)
	accountTagPattern     = regexp.MustCompile(`^tag:[A-Za-z][A-Za-z0-9-]*$`)
)

// connected loads the one account, opens its secret and builds its api;
// cloud.not_connected when there is none.
func (t tn) connected(ctx context.Context, db *store.DB, s core.Sealer, clock core.Clock) (*api, account, error) {
	a, ok, err := loadAccount(ctx, db.Read())
	if err != nil {
		return nil, account{}, err
	}
	if !ok {
		return nil, account{}, errcode.New(errcode.CloudNotConnected, "no tailnet is connected")
	}
	secret, err := cloud.OpenCredential(s, accountSecretTable, accountSecretColumn, a.ID, a.SecretCT)
	if err != nil {
		return nil, account{}, fmt.Errorf("tailnet: open the account secret: %w", err)
	}
	c, err := t.newAPI(a, secret, clock)
	if err != nil {
		return nil, account{}, fmt.Errorf("tailnet: build the account client: %w", err)
	}
	return c, a, nil
}

// cloudEffect is the effect of one provider write: not local, so it may
// still commit for cloud.CommitWait after the call. A nil before is the
// state of an absent resource.
func cloudEffect(kind core.EffectKind, resource string, before json.RawMessage) core.Effect {
	if before == nil {
		before = json.RawMessage("null")
	}
	return core.Effect{
		Kind: kind, Resource: resource, Local: false, InverseLocal: false,
		CommitWait: cloud.CommitWait, Before: core.StateDoc{State: before},
	}
}

func accountResource(id string) string { return accountResourcePrefix + id }

// accountState is the state document of an account row. It holds no secret
// and no capability: a second probe can change the capabilities between the
// connect and its undo, which the engine would read as a conflict.
func accountState(id, tailnet, clientID string, tags []string) (json.RawMessage, error) {
	sorted := slices.Sorted(slices.Values(tags))
	if sorted == nil {
		sorted = []string{}
	}
	b, err := json.Marshal(struct {
		ID          string   `json:"id"`
		Tailnet     string   `json:"tailnet"`
		ClientID    string   `json:"clientId"`
		TagsAllowed []string `json:"tagsAllowed"`
	}{id, tailnet, clientID, sorted})
	if err != nil {
		return nil, fmt.Errorf("tailnet: encode the account state: %w", err)
	}
	return b, nil
}

// accountView is the client-facing form of a stored account, without its
// secret.
func accountView(a account) AccountView {
	return AccountView{
		Tailnet: a.Tailnet, ClientID: a.ClientID, TagsAllowed: a.TagsAllowed,
		Capabilities: a.Capabilities, ConnectedAt: a.ConnectedAt,
	}
}

// checkConnect refuses an invalid connect input with one field error per
// problem and answers the input with its tags de-duplicated and sorted. No
// message quotes an input value, since a value may sit in the secret's field.
func checkConnect(in ConnectInput) (ConnectInput, error) {
	var fields []errcode.FieldError
	bad := func(path, msg string) { fields = append(fields, errcode.FieldError{Path: path, Message: msg}) }
	if !accountTailnetPattern.MatchString(in.Tailnet) {
		bad("/tailnet", "the tailnet name holds 1 to 253 letters, digits, '.', '_', '@' or '-'")
	}
	if in.ClientID == "" {
		bad("/clientId", "the OAuth client id is required")
	}
	if in.ClientSecret == "" {
		bad("/clientSecret", "the OAuth client secret is required")
	}
	if len(in.TagsAllowed) == 0 {
		bad("/tagsAllowed", "at least one tag the client may assign is required")
	}
	for i, tag := range in.TagsAllowed {
		if !accountTagPattern.MatchString(tag) {
			bad(fmt.Sprintf("/tagsAllowed/%d", i), "a tag is \"tag:\" followed by a letter, then letters, digits or '-'")
		}
	}
	if len(fields) > 0 {
		return ConnectInput{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: "the connect input is invalid", Fields: fields})
	}
	in.TagsAllowed = slices.Compact(slices.Sorted(slices.Values(in.TagsAllowed)))
	return in, nil
}

// probeAccount proves the client's credentials and read capabilities with harmless
// reads; it never writes to the tailnet. A refused client is a definite
// cloud.auth_failed; every other failed read is a missing capability.
func (t tn) probeAccount(ctx context.Context, env core.Env, in ConnectInput) ([]cloud.Capability, error) {
	c, err := t.newAPI(account{ID: env.ChangeID, Tailnet: in.Tailnet, ClientID: in.ClientID}, []byte(in.ClientSecret), env.Clock)
	if err != nil {
		return nil, fmt.Errorf("tailnet: build the probe client: %w", err)
	}
	reads := []func(ctx context.Context) error{
		func(ctx context.Context) error { _, err := c.Devices(ctx); return err },
		func(ctx context.Context) error { _, err := c.Keys(ctx); return err },
		func(ctx context.Context) error { _, _, err := c.Policy(ctx); return err },
	}
	probes := make([]cloud.Probe, len(accountReadCapabilities))
	for i, name := range accountReadCapabilities {
		probes[i] = cloud.Probe{Name: name, Read: reads[i]}
	}
	caps := cloud.ProbeReads(ctx, probes, accountWriteCapabilities)
	for _, c := range caps {
		if c.State == cloud.CapabilityMissing && c.Detail == string(errcode.CloudAuthFailed) {
			return nil, core.Definite(errcode.New(errcode.CloudAuthFailed, "the OAuth client id or secret was refused"))
		}
	}
	return caps, nil
}

// accountHash is the hex SHA-256 of lines, each ended by a newline.
func accountHash(lines ...string) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// accountCleanupDetail names every pending cleanup line.
func accountCleanupDetail(lines []string) string {
	return "cleanup is pending: " + strings.Join(lines, "; ")
}
