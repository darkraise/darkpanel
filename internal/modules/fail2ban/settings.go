package fail2ban

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// SettingsFile is Darkpanel's fail2ban settings. fail2ban reads jail.d's
// .local files last and in name order, so this one wins.
const SettingsFile = "/etc/fail2ban/jail.d/zz-darkpanel.local"

// settingsNoun names the effect of a settings change.
const settingsNoun = ID + ".settings:" + SettingsFile

// The fixed ignore entries besides the controller's addresses: loopback,
// then Tailscale's ranges. The admin's extra entries follow the last.
var (
	loopbackIgnore  = []string{"127.0.0.1/8", "::1"}
	tailscaleIgnore = []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"}
)

// maxExtraIgnore caps the admin's extra ignore entries.
const maxExtraIgnore = 64

// Settings are the jail settings Darkpanel writes.
type Settings struct {
	BantimeMinutes  int `json:"bantimeMinutes" jsonschema:"minimum=1,maximum=43200" jsonschema_description:"How long a ban lasts, 1 minute to 30 days."`
	FindtimeMinutes int `json:"findtimeMinutes" jsonschema:"minimum=1,maximum=1440" jsonschema_description:"The window failures are counted in, 1 minute to 1 day."`
	MaxRetry        int `json:"maxRetry" jsonschema:"minimum=1,maximum=20" jsonschema_description:"Failures within the window that ban an address."`
	// IgnoreIP is every address and network fail2ban never bans: the fixed
	// entries fail2ban.status.get lists, then the admin's own.
	IgnoreIP []string `json:"ignoreIp" jsonschema:"maxItems=80" jsonschema_description:"Addresses and networks never banned. Keep every fixed entry fail2ban.status.get lists; the rest are this host's own entries."`
	Recidive bool     `json:"recidive" jsonschema_description:"Ban for a week, on every port, an address banned again and again."`
}

// defaultSettings are what install writes.
var defaultSettings = Settings{BantimeMinutes: 10, FindtimeMinutes: 10, MaxRetry: 5}

// SettingsInput is the input of fail2ban.settings.apply.
type SettingsInput struct {
	core.Guarded
	Settings
}

func (m module) settingsAction() core.ActionDef {
	return core.DefineMutating[SettingsInput, Settings](core.ActionSpec{
		Name: ID + ".settings.apply", Version: 1,
		Summary: "Write fail2ban's jail settings and reload it; undo puts the previous settings back.",
		Class:   core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey}, MinScope: core.ScopeAdmin,
		Guards: []core.GuardSpec{{ID: GuardSelfBan, Overridable: true}},
	}, settingsHandler{m: m})
}

type settingsHandler struct{ m module }

// fixedIgnore is the fixed part of every ignore list, in file order.
func fixedIgnore(env core.Env) []string {
	return slices.Concat(loopbackIgnore, env.ControllerAddrs, tailscaleIgnore)
}

// ignorePrefix reads an ignore entry, an address or a network, in its
// canonical form.
func ignorePrefix(s string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Zone() == "" {
		return p, p == p.Masked() && p.String() == s
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a, a.BitLen()), a.String() == s
}

// ignored reports whether addr falls in one of the entries.
func ignored(entries []string, addr netip.Addr) bool {
	for _, e := range entries {
		// A fixed entry such as 127.0.0.1/8 is not canonical; it still matches.
		p, err := netip.ParsePrefix(e)
		if err != nil {
			a, err := netip.ParseAddr(e)
			if err != nil {
				continue
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ignoreList checks the input's list against the fixed entries and returns
// the whole list to write: the fixed entries, then the extra ones.
func ignoreList(env core.Env, in []string) ([]string, error) {
	fixed := fixedIgnore(env)
	for _, f := range fixed {
		if !slices.Contains(in, f) {
			return nil, invalid("/ignoreIp", "the fixed entry "+f+" cannot be removed")
		}
	}
	var extra []string
	for i, e := range in {
		if slices.Contains(fixed, e) || slices.Contains(extra, e) {
			continue
		}
		if _, ok := ignorePrefix(e); !ok {
			return nil, invalid(fmt.Sprintf("/ignoreIp/%d", i), e+" is not an address or a network in canonical form, such as 203.0.113.0/24")
		}
		extra = append(extra, e)
	}
	if len(extra) > maxExtraIgnore {
		return nil, invalid("/ignoreIp", fmt.Sprintf("at most %d entries of your own", maxExtraIgnore))
	}
	return append(fixed, extra...), nil
}

func checkSettings(s Settings) error {
	switch {
	case s.BantimeMinutes < 1 || s.BantimeMinutes > 43200:
		return invalid("/bantimeMinutes", "a ban lasts 1 minute to 30 days (43200 minutes)")
	case s.FindtimeMinutes < 1 || s.FindtimeMinutes > 1440:
		return invalid("/findtimeMinutes", "the window is 1 minute to 1 day (1440 minutes)")
	case s.MaxRetry < 1 || s.MaxRetry > 20:
		return invalid("/maxRetry", "1 to 20 failures")
	}
	return nil
}

// sshPorts are the ports the SSH daemon listens on, from `sshd -T`.
func sshPorts(ctx context.Context, env core.Env) ([]string, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.SshdPath, Args: platform.SshdEffective()})
	if err != nil {
		return nil, fmt.Errorf("fail2ban: run sshd -T: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("fail2ban: sshd -T exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	var ports []string
	for line := range strings.Lines(string(res.Stdout)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "port "); ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 && !slices.Contains(ports, v) {
				ports = append(ports, v)
			}
		}
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("fail2ban: sshd -T printed no port")
	}
	return ports, nil
}

// settingsContent is the settings file. On the controller's own host the
// recidive jail bans only the SSH ports, so it never drops the panel
// listener.
func settingsContent(s Settings, ignore, ports []string, onController bool) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Written by Darkpanel, which replaces this file on every settings change.\n")
	fmt.Fprintf(&b, "[DEFAULT]\nbanaction = nftables-multiport\nbanaction_allports = nftables-allports\nbackend = systemd\n")
	fmt.Fprintf(&b, "bantime = %dm\nfindtime = %dm\nmaxretry = %d\nignoreip = %s\n", s.BantimeMinutes, s.FindtimeMinutes, s.MaxRetry, strings.Join(ignore, " "))
	fmt.Fprintf(&b, "\n[sshd]\nenabled = true\nport = %s\n", strings.Join(ports, ","))
	if !s.Recidive {
		fmt.Fprintf(&b, "\n[recidive]\nenabled = false\n")
		return b.Bytes()
	}
	// recidive reads fail2ban's own log file, not the journal.
	fmt.Fprintf(&b, "\n[recidive]\nenabled = true\nbackend = auto\n")
	if onController {
		fmt.Fprintf(&b, "banaction = nftables-multiport\nport = %s\n", strings.Join(ports, ","))
	}
	return b.Bytes()
}

// parseSettings reads the settings back from a file settingsContent wrote,
// with the fixed entries of env in place of those the file holds; nil when
// it is not such a file.
func parseSettings(env core.Env, data []byte) *Settings {
	s := Settings{}
	section := ""
	seen := 0
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		key, value, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		var err error
		switch section + key {
		case "[DEFAULT]bantime":
			s.BantimeMinutes, err = strconv.Atoi(strings.TrimSuffix(value, "m"))
		case "[DEFAULT]findtime":
			s.FindtimeMinutes, err = strconv.Atoi(strings.TrimSuffix(value, "m"))
		case "[DEFAULT]maxretry":
			s.MaxRetry, err = strconv.Atoi(value)
		case "[DEFAULT]ignoreip":
			entries := strings.Fields(value)
			// The admin's entries follow the last Tailscale range.
			last := slices.Index(entries, tailscaleIgnore[len(tailscaleIgnore)-1])
			if last < 0 {
				return nil
			}
			s.IgnoreIP = append(fixedIgnore(env), entries[last+1:]...)
		case "[recidive]enabled":
			s.Recidive = value == "true"
		default:
			continue
		}
		if err != nil {
			return nil
		}
		seen++
	}
	if seen != 5 {
		return nil
	}
	return &s
}

// currentSettings are the settings of Darkpanel's file; nil without one.
func (m module) currentSettings(env core.Env) (*Settings, error) {
	b, err := m.readSettingsFile()
	if err != nil || b == nil {
		return nil, err
	}
	return parseSettings(env, b), nil
}

// fileState is the settings file's content as a JSON string, or null.
func fileState(data []byte) core.StateDoc {
	if data == nil {
		return core.StateDoc{State: json.RawMessage("null")}
	}
	s, _ := json.Marshal(string(data))
	return core.StateDoc{State: s}
}

// desired is the file in asks for on this host now.
func (h settingsHandler) desired(ctx context.Context, env core.Env, in Settings) ([]byte, []string, error) {
	if err := checkSettings(in); err != nil {
		return nil, nil, err
	}
	ignore, err := ignoreList(env, in.IgnoreIP)
	if err != nil {
		return nil, nil, err
	}
	ports, err := sshPorts(ctx, env)
	if err != nil {
		return nil, nil, err
	}
	onController, err := loaded(ctx, env, controllerUnit)
	if err != nil {
		return nil, nil, err
	}
	return settingsContent(in, ignore, ports, onController), ignore, nil
}

func (h settingsHandler) Plan(ctx context.Context, env core.Env, in SettingsInput) (core.PlanOutcome, error) {
	if err := requireInstalled(ctx, env); err != nil {
		return core.PlanOutcome{}, err
	}
	data, ignore, err := h.desired(ctx, env, in.Settings)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	cur, err := h.m.readSettingsFile()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256(fileState(cur).State)
	out := core.PlanOutcome{Diff: lineDiff(cur, data), Guards: []core.Guard{}, StateHash: hex.EncodeToString(sum[:])}
	crowdsec, err := loaded(ctx, env, crowdsecUnit)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if crowdsec {
		out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: "CrowdSec is also installed on this host; it bans addresses on its own"})
	}
	if g, ok := selfBanGuard(env.Principal, in, ignore); ok {
		out.Guards = append(out.Guards, g)
	}
	return out, nil
}

// selfBanGuard is the guard of a recidive jail that could ban the caller's
// own address on every port; false when recidive stays off.
func selfBanGuard(p core.Principal, in SettingsInput, ignore []string) (core.Guard, bool) {
	if !in.Recidive {
		return core.Guard{}, false
	}
	addr, ok := callerAddr(p)
	switch {
	case !ok:
		return core.Guard{ID: GuardSelfBan, OK: true, Message: "the change does not come from a client address recidive could ban"}, true
	case ignored(ignore, addr):
		return core.Guard{ID: GuardSelfBan, OK: true, Message: "your address " + addr.String() + " is in the ignore list"}, true
	}
	g := core.Guard{ID: GuardSelfBan, Message: "Recidive bans on every port, and your address " + addr.String() +
		" is not in the ignore list; add it to the list, or override to go on"}
	if slices.Contains(in.Overrides, GuardSelfBan) {
		g.OK, g.Overridden = true, true
	}
	return g, true
}

// lineDiff lists the lines next leaves out of cur and those it adds.
func lineDiff(cur, next []byte) []core.DiffLine {
	split := func(b []byte) []string {
		var out []string
		for l := range strings.Lines(string(b)) {
			if l = strings.TrimRight(l, "\n"); l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
		return out
	}
	old, now := split(cur), split(next)
	diff := []core.DiffLine{}
	for _, l := range old {
		if !slices.Contains(now, l) {
			diff = append(diff, core.DiffLine{Op: "-", Text: l})
		}
	}
	for _, l := range now {
		if !slices.Contains(old, l) {
			diff = append(diff, core.DiffLine{Op: "+", Text: l})
		}
	}
	return diff
}

// Apply writes the file and checks it with fail2ban-client -t; a file
// fail2ban refuses is replaced by the previous one before anything reloads.
func (h settingsHandler) Apply(ctx context.Context, env core.Env, in SettingsInput) (Settings, error) {
	if err := requireInstalled(ctx, env); err != nil {
		return Settings{}, err
	}
	data, ignore, err := h.desired(ctx, env, in.Settings)
	if err != nil {
		return Settings{}, core.Definite(err)
	}
	cur, err := h.m.readSettingsFile()
	if err != nil {
		return Settings{}, core.Definite(err)
	}
	after := fileState(data)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: settingsNoun, Local: true, InverseLocal: true, Before: fileState(cur), After: &after,
	}, nil)
	if err != nil {
		return Settings{}, err
	}
	if err := env.Files.WriteFile(ctx, ID, SettingsFile, data, 0o644); err != nil {
		return Settings{}, err
	}
	if _, err := client(ctx, env, platform.Fail2banTest()); err != nil {
		if rerr := restoreFile(ctx, env, cur); rerr != nil {
			return Settings{}, fmt.Errorf("%w; putting the previous file back failed: %w", err, rerr)
		}
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return Settings{}, serr
		}
		return Settings{}, core.Definite(fmt.Errorf("fail2ban refused the settings, so the previous file is back: %w", err))
	}
	if _, err := client(ctx, env, platform.Fail2banReload()); err != nil {
		return Settings{}, err
	}
	if err := env.Effects.Made(ctx, seq, settingsNoun, after); err != nil {
		return Settings{}, err
	}
	out := in.Settings
	out.IgnoreIP = ignore
	return out, nil
}

// restoreFile puts data back as the settings file, or removes the file
// when data is nil.
func restoreFile(ctx context.Context, env core.Env, data []byte) error {
	if data == nil {
		return env.Files.Remove(ctx, ID, SettingsFile)
	}
	return env.Files.WriteFile(ctx, ID, SettingsFile, data, 0o644)
}

// Verify checks the file holds the settings and fail2ban answers.
func (h settingsHandler) Verify(ctx context.Context, env core.Env, in SettingsInput, _ Settings) error {
	cur, err := h.m.readSettingsFile()
	if err != nil {
		return err
	}
	got := parseSettings(env, cur)
	want := in.Settings
	if got == nil || got.BantimeMinutes != want.BantimeMinutes || got.FindtimeMinutes != want.FindtimeMinutes ||
		got.MaxRetry != want.MaxRetry || got.Recidive != want.Recidive {
		return fmt.Errorf("fail2ban: %s does not hold the requested settings", SettingsFile)
	}
	_, err = jailNames(ctx, env)
	return err
}

func (h settingsHandler) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	cur, err := h.m.readSettingsFile()
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: fileState(cur), Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Inverse puts the previous file back, or removes it, and reloads a
// running fail2ban.
func (h settingsHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	var prior *string
	if err := json.Unmarshal(e.Before.State, &prior); err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("fail2ban: read the effect's prior state: %w", err))
	}
	var data []byte
	if prior != nil {
		data = []byte(*prior)
	}
	if err := restoreFile(ctx, env, data); err != nil {
		return core.InverseResult{}, err
	}
	st, err := env.Units.State(ctx, fail2banUnit)
	if err != nil {
		return core.InverseResult{}, fmt.Errorf("fail2ban: read %s: %w", fail2banUnit, err)
	}
	if st.ActiveState == "active" {
		if _, err := client(ctx, env, platform.Fail2banReload()); err != nil {
			return core.InverseResult{}, err
		}
	}
	return core.InverseResult{}, nil
}
