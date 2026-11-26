package fail2ban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// BanInput names a jail and an address.
type BanInput struct {
	Jail    string `json:"jail" jsonschema:"pattern=^[A-Za-z0-9_][A-Za-z0-9_-]{0\\,63}$" jsonschema_description:"A running jail, such as sshd."`
	Address string `json:"address" jsonschema_description:"An IPv4 or IPv6 address."`
}

// BanChanged is the answer of fail2ban.ban.add and .remove.
type BanChanged struct {
	Jail    string `json:"jail"`
	Address string `json:"address"`
}

func (m module) banActions() []core.ActionDef {
	spec := func(verb, summary, compensatedBy string, scope core.Scope) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + ".ban." + verb, Version: 1, Summary: summary,
			Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
			MinScope: scope, CompensatedBy: ID + ".ban." + compensatedBy,
		}
	}
	return []core.ActionDef{
		core.DefineMutating[BanInput, BanChanged](spec("add", "Ban an address in a jail.", "remove", core.ScopeAdmin), banHandler{m: m, ban: true}),
		core.DefineMutating[BanInput, BanChanged](spec("remove", "Unban an address in a jail.", "add", ""), banHandler{m: m}),
	}
}

// banHandler bans (ban) or unbans an address.
type banHandler struct {
	m   module
	ban bool
}

// check reads the jail's bans and refuses what the change cannot do: an
// unknown jail, a malformed address, a change that would change nothing,
// and for a ban an ignored address or the caller's own.
func (h banHandler) check(ctx context.Context, env core.Env, in BanInput) (netip.Addr, []string, error) {
	addr, err := netip.ParseAddr(in.Address)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, nil, invalid("/address", in.Address+" is not an IPv4 or IPv6 address")
	}
	addr = addr.Unmap()
	if err := requireInstalled(ctx, env); err != nil {
		return netip.Addr{}, nil, err
	}
	names, err := jailNames(ctx, env)
	if err != nil {
		return netip.Addr{}, nil, err
	}
	if !slices.Contains(names, in.Jail) {
		return netip.Addr{}, nil, invalid("/jail", "no jail "+in.Jail+" is running")
	}
	_, banned, err := jailStatus(ctx, env, in.Jail)
	if err != nil {
		return netip.Addr{}, nil, err
	}
	switch is := slices.Contains(banned, addr.String()); {
	case h.ban && is:
		return netip.Addr{}, nil, invalid("/address", addr.String()+" is already banned in "+in.Jail)
	case !h.ban && !is:
		return netip.Addr{}, nil, invalid("/address", addr.String()+" is not banned in "+in.Jail)
	}
	if h.ban {
		if caller, ok := callerAddr(env.Principal); ok && caller == addr {
			return netip.Addr{}, nil, invalid("/address", "that is your own address")
		}
		ignore := fixedIgnore(env)
		if s, err := h.m.currentSettings(env); err != nil {
			return netip.Addr{}, nil, err
		} else if s != nil {
			ignore = s.IgnoreIP
		}
		if ignored(ignore, addr) {
			return netip.Addr{}, nil, invalid("/address", addr.String()+" is in the ignore list")
		}
	}
	return addr, banned, nil
}

func (h banHandler) Plan(ctx context.Context, env core.Env, in BanInput) (core.PlanOutcome, error) {
	addr, banned, err := h.check(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	slices.Sort(banned)
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\n%v", in.Jail, banned))
	line := core.DiffLine{Op: "+", Text: fmt.Sprintf("Ban %s in %s", addr, in.Jail)}
	if !h.ban {
		line = core.DiffLine{Op: "-", Text: fmt.Sprintf("Unban %s in %s", addr, in.Jail)}
	}
	return core.PlanOutcome{Diff: []core.DiffLine{line}, StateHash: hex.EncodeToString(sum[:])}, nil
}

func (h banHandler) Apply(ctx context.Context, env core.Env, in BanInput) (BanChanged, error) {
	addr, _, err := h.check(ctx, env, in)
	if err != nil {
		return BanChanged{}, core.Definite(err)
	}
	args := platform.Fail2banUnban(in.Jail, addr.String())
	if h.ban {
		args = platform.Fail2banBan(in.Jail, addr.String())
	}
	if _, err := client(ctx, env, args); err != nil {
		return BanChanged{}, err
	}
	return BanChanged{Jail: in.Jail, Address: addr.String()}, nil
}

// Verify checks the jail bans the address, or no longer does.
func (h banHandler) Verify(ctx context.Context, env core.Env, in BanInput, _ BanChanged) error {
	addr, err := netip.ParseAddr(in.Address)
	if err != nil {
		return err
	}
	_, banned, err := jailStatus(ctx, env, in.Jail)
	if err != nil {
		return err
	}
	if slices.Contains(banned, addr.Unmap().String()) != h.ban {
		return fmt.Errorf("fail2ban: jail %s does not show the change of %s", in.Jail, addr)
	}
	return nil
}
