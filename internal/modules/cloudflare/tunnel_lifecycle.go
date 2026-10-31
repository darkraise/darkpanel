package cloudflare

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The resource nouns of the tunnel effects: the provider's tunnel and
// Darkpanel's row of it, each followed by the tunnel id.
const (
	tunnelNoun    = "cloudflare.tunnel:"
	tunnelRowNoun = "cloudflare.tunnelrow:"
)

// tunnelDoc is the state document of a present tunnel or tunnel row.
func tunnelDoc(id, name string) json.RawMessage {
	// Marshalling a struct of two strings cannot fail.
	b, _ := json.Marshal(struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{id, name})
	return b
}

// tunnelRefused reports whether Cloudflare refused a write before changing
// anything. The cloud client marks only non-GET, non-DELETE 4xx answers
// Definite, so a refused DELETE is recognised by its status.
func tunnelRefused(err error) bool {
	s := cloud.Status(err)
	return core.IsDefinite(err) || (s >= 400 && s <= 499)
}

// tunnelStateHash is the hex SHA-256 of the JSON array of parts.
func tunnelStateHash(parts ...any) string {
	// The parts are strings and booleans, which always marshal.
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// tunnelSameJSON reports whether a and b hold the same JSON value, ignoring
// key order and insignificant whitespace; a value that is not JSON equals
// nothing.
func tunnelSameJSON(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// tunnelNameOf is the name of the tunnel an effect names: its recovery
// value, else the name its post-state records, else the name its
// before-state records. A made deletion's post-state is null and names no
// tunnel, so its before-state does.
func tunnelNameOf(e core.Effect) string {
	if e.Recovery != nil {
		return e.Recovery.Value
	}
	nameIn := func(doc json.RawMessage) string {
		var s struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(doc, &s) != nil {
			return ""
		}
		return s.Name
	}
	if e.Post != nil {
		if name := nameIn(e.Post.State); name != "" {
			return name
		}
	}
	return nameIn(e.Before.State)
}

// tunnelDeleteAtProvider deletes the tunnel at Cloudflare. A tunnel already
// gone counts as deleted: the cloud client resends a DELETE whose response
// was lost, and the resend finds the tunnel gone.
func tunnelDeleteAtProvider(ctx context.Context, a *api, id string) error {
	err := a.DeleteTunnel(ctx, id)
	switch {
	case err == nil || cloud.Status(err) == http.StatusNotFound:
		return nil
	case tunnelRefused(err):
		return core.Definite(err)
	default:
		return err
	}
}

// tunnelObserver reads the tunnel effects of tunnel.create and
// tunnel.delete, and holds what both actions check.
type tunnelObserver struct{ c cf }

// Observe reads the tunnel row from the database, or the tunnel from
// Cloudflare. It lists the account's tunnels by name rather than reading one
// by id, because Cloudflare keeps answering a deleted tunnel by id.
func (o tunnelObserver) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	null := core.StateDoc{State: json.RawMessage("null")}
	if rest, ok := strings.CutPrefix(e.Resource, tunnelRowNoun); ok {
		id := rest
		if strings.HasPrefix(rest, "?") {
			if e.Recovery == nil {
				return core.Observation{}, fmt.Errorf("cloudflare: creation %s has no recovery value", e.Resource)
			}
			id = e.Recovery.Value
		}
		row, found, err := loadTunnel(ctx, env.DB.Read(), id)
		if err != nil {
			return core.Observation{}, err
		}
		// loadTunnel also matches a name; only the row with this id is the
		// effect's.
		if !found || row.ID != id {
			return core.Observation{Current: null, Exclusive: true}, nil
		}
		return core.Observation{
			Current: core.StateDoc{State: tunnelDoc(row.ID, row.Name)}, Resource: tunnelRowNoun + id, Matches: 1, Exclusive: true,
		}, nil
	}
	rest, ok := strings.CutPrefix(e.Resource, tunnelNoun)
	if !ok {
		return core.Observation{}, fmt.Errorf("cloudflare: %q is not a tunnel effect", e.Resource)
	}
	creation := strings.HasPrefix(rest, "?")
	name := tunnelNameOf(e)
	if name == "" {
		return core.Observation{}, fmt.Errorf("cloudflare: effect %s names no tunnel", e.Resource)
	}
	a, _, err := o.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	listed, err := a.Tunnels(ctx, name)
	if err != nil {
		return core.Observation{}, err
	}
	var kept []Tunnel
	for _, t := range listed {
		if t.Name == name && (creation || t.ID == rest) {
			kept = append(kept, t)
		}
	}
	obs := core.Observation{Current: null, Matches: 1, Exclusive: true}
	if creation {
		obs.Matches = len(kept)
	}
	switch len(kept) {
	case 0:
	case 1:
		obs.Current = core.StateDoc{State: tunnelDoc(kept[0].ID, kept[0].Name)}
		obs.Resource = tunnelNoun + kept[0].ID
	default:
		for _, t := range kept {
			obs.Candidates = append(obs.Candidates, core.Candidate{
				Resource: tunnelNoun + t.ID, State: core.StateDoc{State: tunnelDoc(t.ID, t.Name)},
			})
		}
	}
	return obs, nil
}

// busy refuses a tunnel still in use: one that hostname rows of any phase
// route through or, when provider is set, one whose connectors are
// connected to Cloudflare.
func (o tunnelObserver) busy(ctx context.Context, env core.Env, a *api, row tunnelRow, provider bool) error {
	hosts, err := listHostnames(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	n := 0
	for _, h := range hosts {
		if h.TunnelID == row.ID {
			n++
		}
	}
	if n > 0 {
		return core.Definite(errcode.New(errcode.CloudConflict,
			fmt.Sprintf("tunnel %s still has %d hostnames; remove them first", row.Name, n)))
	}
	if !provider {
		return nil
	}
	conns, err := a.Connections(ctx, row.ID)
	if err != nil {
		return err
	}
	if len(conns) > 0 {
		return core.Definite(errcode.New(errcode.CloudConflict,
			fmt.Sprintf("tunnel %s still has %d connectors; stop them first", row.Name, len(conns))))
	}
	return nil
}

// verify passes when every made effect of the change still observes its
// post-state and at least want effects are made. It only reads, so startup
// recovery can call it.
func (o tunnelObserver) verify(ctx context.Context, env core.Env, want int) error {
	effs, err := env.Effects.List(ctx)
	if err != nil {
		return err
	}
	made := 0
	for _, e := range effs {
		if e.State != core.EffectMade {
			continue
		}
		made++
		obs, err := o.Observe(ctx, env, e)
		if err != nil {
			return err
		}
		if e.Post == nil || !tunnelSameJSON(obs.Current.State, e.Post.State) {
			return fmt.Errorf("cloudflare: %s is not in the state the call left", e.Resource)
		}
	}
	if made < want {
		return fmt.Errorf("cloudflare: %d of %d calls were made", made, want)
	}
	return nil
}

// tunnelCreate declares cloudflare.tunnel.create, which creates a remotely
// managed tunnel and records it.
func (c cf) tunnelCreate() core.ActionDef {
	return core.DefineMutating[TunnelCreateInput, TunnelRef](core.ActionSpec{
		Name: ID + ".tunnel.create", Summary: "Create a remotely managed Cloudflare tunnel.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{ID},
	}, tunnelCreator{tunnelObserver{c}})
}

// tunnelCreator creates the tunnel at Cloudflare, then its row. Its undo
// deletes the row, then the tunnel, each only while nothing uses it.
type tunnelCreator struct{ tunnelObserver }

// Plan names the tunnel the change will create; the change id is not known
// yet.
func (h tunnelCreator) Plan(ctx context.Context, env core.Env, _ TunnelCreateInput) (core.PlanOutcome, error) {
	_, acct, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff:      []core.DiffLine{{Op: "+", Text: "tunnel darkpanel-<change id>, remotely managed, in Cloudflare account " + acct.AccountID}},
		StateHash: tunnelStateHash("tunnel.create", acct.AccountID),
	}, nil
}

// Apply creates the tunnel darkpanel-<change id> and then its row, each
// journaled as its own effect. The journal calls and the database write are
// separate calls: the database has one writer connection.
func (h tunnelCreator) Apply(ctx context.Context, env core.Env, _ TunnelCreateInput) (TunnelRef, error) {
	if env.ChangeID == "" {
		return TunnelRef{}, core.Definite(errors.New("cloudflare: tunnel.create needs a change id"))
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return TunnelRef{}, err
	}
	name := "darkpanel-" + env.ChangeID
	remote := cloudEffect(core.EffectCreate, tunnelNoun+"?"+name, nil)
	remote.Recovery = &core.RecoveryID{Kind: "name", Value: name}
	seq, err := env.Effects.Intend(ctx, remote, nil)
	if err != nil {
		return TunnelRef{}, err
	}
	t, err := a.CreateTunnel(ctx, name)
	if err != nil {
		// Any error but a refusal stays intended for the engine to settle.
		if !tunnelRefused(err) {
			return TunnelRef{}, err
		}
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return TunnelRef{}, errors.Join(err, fmt.Errorf("cloudflare: journal %s: %w", remote.Resource, serr))
		}
		return TunnelRef{}, core.Definite(err)
	}
	doc := core.StateDoc{State: tunnelDoc(t.ID, name)}
	if err := env.Effects.Made(ctx, seq, tunnelNoun+t.ID, doc); err != nil {
		return TunnelRef{}, err
	}

	seq, err = env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: tunnelRowNoun + "?" + t.ID, Recovery: &core.RecoveryID{Kind: "name", Value: t.ID},
		Local: true, InverseLocal: true, Before: core.StateDoc{State: json.RawMessage("null")}, After: &doc,
	}, nil)
	if err != nil {
		return TunnelRef{}, err
	}
	row := tunnelRow{ID: t.ID, Name: name, ChangeID: env.ChangeID, CreatedAt: env.Clock.Now()}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return insertTunnel(ctx, tx, row) }); err != nil {
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return TunnelRef{}, errors.Join(err, serr)
		}
		return TunnelRef{}, err
	}
	if err := env.Effects.Made(ctx, seq, tunnelRowNoun+t.ID, doc); err != nil {
		return TunnelRef{}, err
	}
	return TunnelRef{ID: t.ID, Name: name}, nil
}

// Verify passes when the tunnel and its row both still show what the apply
// left.
func (h tunnelCreator) Verify(ctx context.Context, env core.Env, _ TunnelCreateInput, _ TunnelRef) error {
	return h.verify(ctx, env, 2)
}

// Inverse deletes the row or the tunnel an effect made, refusing while a
// hostname, or for the tunnel a connector, still uses it.
func (h tunnelCreator) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	if id, ok := strings.CutPrefix(e.Resource, tunnelRowNoun); ok {
		row, found, err := loadTunnel(ctx, env.DB.Read(), id)
		if err != nil {
			return core.InverseResult{}, err
		}
		if !found || row.ID != id {
			return core.InverseResult{}, nil
		}
		if err := h.busy(ctx, env, nil, row, false); err != nil {
			return core.InverseResult{}, err
		}
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteTunnel(ctx, tx, id) }); err != nil {
			return core.InverseResult{}, err
		}
		return core.InverseResult{}, nil
	}
	id, ok := strings.CutPrefix(e.Resource, tunnelNoun)
	if !ok {
		return core.InverseResult{}, fmt.Errorf("cloudflare: %q is not a tunnel effect", e.Resource)
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		// Nothing was sent to Cloudflare.
		return core.InverseResult{}, core.Definite(err)
	}
	if err := h.busy(ctx, env, a, tunnelRow{ID: id, Name: tunnelNameOf(e)}, true); err != nil {
		return core.InverseResult{}, err
	}
	if err := tunnelDeleteAtProvider(ctx, a, id); err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

// tunnelDelete declares cloudflare.tunnel.delete, which deletes a managed
// tunnel from Cloudflare and its row from Darkpanel.
func (c cf) tunnelDelete() core.ActionDef {
	return core.DefineMutating[TunnelInput, TunnelRef](core.ActionSpec{
		Name: ID + ".tunnel.delete", Summary: "Delete a managed Cloudflare tunnel.", Version: 1,
		Class: core.ClassIrreversible, Side: core.SideController, LockKeys: []string{ID},
	}, tunnelDeleter{tunnelObserver{c}})
}

// tunnelDeleter deletes a managed tunnel. A deletion cannot be undone, so it
// has no Inverse.
type tunnelDeleter struct{ tunnelObserver }

// tunnelDeletion is what a delete found: the account's api, the account,
// the tunnel's row and whether Cloudflare still lists the tunnel.
type tunnelDeletion struct {
	a       *api
	acct    account
	row     tunnelRow
	present bool
}

// inspect finds the managed tunnel the input names, by id or name, and
// refuses it while it is in use.
func (h tunnelDeleter) inspect(ctx context.Context, env core.Env, in TunnelInput) (tunnelDeletion, error) {
	if in.Tunnel == "" {
		return tunnelDeletion{}, core.Definite(&errcode.Error{
			Code: errcode.InvalidInput, Detail: "the delete input is invalid",
			Fields: []errcode.FieldError{{Path: "/tunnel", Message: "the tunnel id or name is required"}},
		})
	}
	a, acct, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return tunnelDeletion{}, err
	}
	row, found, err := loadTunnel(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return tunnelDeletion{}, err
	}
	if !found {
		return tunnelDeletion{}, core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))
	}
	listed, err := a.Tunnels(ctx, row.Name)
	if err != nil {
		return tunnelDeletion{}, err
	}
	present := false
	for _, t := range listed {
		present = present || t.ID == row.ID
	}
	if err := h.busy(ctx, env, a, row, present); err != nil {
		return tunnelDeletion{}, err
	}
	return tunnelDeletion{a: a, acct: acct, row: row, present: present}, nil
}

// Plan names the tunnel to delete and asks for its name as the confirmation.
func (h tunnelDeleter) Plan(ctx context.Context, env core.Env, in TunnelInput) (core.PlanOutcome, error) {
	d, err := h.inspect(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	named := "tunnel " + d.row.Name + " (" + d.row.ID + ")"
	text := named + " deleted from Cloudflare account " + d.acct.AccountID + " and from Darkpanel"
	if !d.present {
		text = named + " is already gone from Cloudflare; only Darkpanel's record is removed"
	}
	return core.PlanOutcome{
		Diff:         []core.DiffLine{{Op: "-", Text: text}},
		StateHash:    tunnelStateHash("tunnel.delete", d.row.ID, d.row.Name, d.present),
		Confirmation: d.row.Name,
	}, nil
}

// Apply deletes the tunnel at Cloudflare, when it is still there, and then
// its row. The provider call comes first: if the process stops between the
// two, a second delete finds the tunnel absent and removes only the row.
func (h tunnelDeleter) Apply(ctx context.Context, env core.Env, in TunnelInput) (TunnelRef, error) {
	d, err := h.inspect(ctx, env, in)
	if err != nil {
		return TunnelRef{}, err
	}
	id, name := d.row.ID, d.row.Name
	null := core.StateDoc{State: json.RawMessage("null")}
	if d.present {
		remote := cloudEffect(core.EffectDelete, tunnelNoun+id, tunnelDoc(id, name))
		remote.After = &null
		seq, err := env.Effects.Intend(ctx, remote, nil)
		if err != nil {
			return TunnelRef{}, err
		}
		if err := tunnelDeleteAtProvider(ctx, d.a, id); err != nil {
			// Any error but a refusal stays intended for the engine to settle.
			if !core.IsDefinite(err) {
				return TunnelRef{}, err
			}
			if serr := env.Effects.Skipped(ctx, seq); serr != nil {
				return TunnelRef{}, errors.Join(err, fmt.Errorf("cloudflare: journal %s: %w", remote.Resource, serr))
			}
			return TunnelRef{}, err
		}
		if err := env.Effects.Made(ctx, seq, remote.Resource, null); err != nil {
			return TunnelRef{}, err
		}
	}

	local := core.Effect{
		Kind: core.EffectDelete, Resource: tunnelRowNoun + id, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: tunnelDoc(id, name)}, After: &null,
	}
	seq, err := env.Effects.Intend(ctx, local, nil)
	if err != nil {
		return TunnelRef{}, err
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteTunnel(ctx, tx, id) }); err != nil {
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return TunnelRef{}, errors.Join(err, serr)
		}
		return TunnelRef{}, err
	}
	if err := env.Effects.Made(ctx, seq, local.Resource, null); err != nil {
		return TunnelRef{}, err
	}
	return TunnelRef{ID: id, Name: name}, nil
}

// Verify passes when the deletions made are still in place: the tunnel no
// longer listed and the row gone.
func (h tunnelDeleter) Verify(ctx context.Context, env core.Env, _ TunnelInput, _ TunnelRef) error {
	return h.verify(ctx, env, 1)
}
