package fail2ban

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// Jail is one running jail with its counters.
type Jail struct {
	Name        string `json:"name"`
	Banned      int    `json:"banned"`      // addresses banned now
	Failed      int    `json:"failed"`      // failures counted now
	FailedTotal int    `json:"failedTotal"` // failures since the jail started
}

// Status is the answer of fail2ban.status.get.
type Status struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Service   string `json:"service"` // fail2ban.service's active state; "" when not installed
	Jails     []Jail `json:"jails"`
	CrowdSec  bool   `json:"crowdsec"` // crowdsec.service is installed; Darkpanel then installs nothing
	// Settings are those of Darkpanel's settings file; nil without it.
	Settings *Settings `json:"settings,omitempty" jsonschema:"nullable"`
	// IgnoreFixed are the entries every ignore list keeps.
	IgnoreFixed []string `json:"ignoreFixed"`
}

// BanListInput selects the jail of fail2ban.ban.list.
type BanListInput struct {
	Jail string `json:"jail,omitempty" jsonschema_description:"The jail whose bans to list; every running jail when empty."`
}

// Ban is one banned address.
type Ban struct {
	Jail    string     `json:"jail"`
	Address string     `json:"address"`
	Start   time.Time  `json:"start"`
	End     *time.Time `json:"end,omitempty" jsonschema:"nullable"` // nil for a ban without end
}

// BanList is the answer of fail2ban.ban.list.
type BanList struct {
	Bans []Ban `json:"bans"`
}

func (m module) reads() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[struct{}, Status](core.ActionSpec{
			Name: ID + ".status.get", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "Read fail2ban's version, service state, jails and settings, and whether CrowdSec is installed.",
		}, statusReader{m: m}),
		core.DefineRead[BanListInput, BanList](core.ActionSpec{
			Name: ID + ".ban.list", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "List the addresses fail2ban bans, with each ban's start and end.",
		}, banLister{}),
	}
}

type statusReader struct{ m module }

func (r statusReader) Read(ctx context.Context, env core.Env, _ struct{}) (Status, error) {
	out := Status{Jails: []Jail{}, IgnoreFixed: fixedIgnore(env)}
	var err error
	if out.CrowdSec, err = loaded(ctx, env, crowdsecUnit); err != nil {
		return Status{}, err
	}
	st, err := env.Units.State(ctx, fail2banUnit)
	if err != nil {
		return Status{}, fmt.Errorf("fail2ban: read %s: %w", fail2banUnit, err)
	}
	if st.LoadState != "loaded" {
		return out, nil
	}
	out.Installed, out.Service = true, st.ActiveState
	v, err := client(ctx, env, platform.Fail2banVersion())
	if err != nil {
		return Status{}, err
	}
	out.Version = parseVersion(v)
	if out.Settings, err = r.m.currentSettings(env); err != nil {
		return Status{}, err
	}
	if st.ActiveState != "active" {
		return out, nil
	}
	names, err := jailNames(ctx, env)
	if err != nil {
		return Status{}, err
	}
	for _, name := range names {
		j, _, err := jailStatus(ctx, env, name)
		if err != nil {
			return Status{}, err
		}
		out.Jails = append(out.Jails, j)
	}
	return out, nil
}

type banLister struct{}

func (banLister) Read(ctx context.Context, env core.Env, in BanListInput) (BanList, error) {
	out := BanList{Bans: []Ban{}}
	st, err := env.Units.State(ctx, fail2banUnit)
	if err != nil {
		return BanList{}, fmt.Errorf("fail2ban: read %s: %w", fail2banUnit, err)
	}
	if st.LoadState != "loaded" || st.ActiveState != "active" {
		return out, nil
	}
	names, err := jailNames(ctx, env)
	if err != nil {
		return BanList{}, err
	}
	if in.Jail != "" {
		if !slices.Contains(names, in.Jail) {
			return BanList{}, invalid("/jail", "no jail "+in.Jail+" is running")
		}
		names = []string{in.Jail}
	}
	for _, name := range names {
		b, err := client(ctx, env, platform.Fail2banBanTimes(name))
		if err != nil {
			return BanList{}, err
		}
		bans, err := parseBanTimes(name, string(b))
		if err != nil {
			return BanList{}, err
		}
		out.Bans = append(out.Bans, bans...)
	}
	return out, nil
}

// parseVersion reads `fail2ban-client -V`: "1.0.2", or "Fail2Ban v0.11.2"
// on older releases.
func parseVersion(out []byte) string {
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return strings.TrimPrefix(f[len(f)-1], "v")
}

// jailNames reads the jail list of `fail2ban-client status`.
func jailNames(ctx context.Context, env core.Env) ([]string, error) {
	out, err := client(ctx, env, platform.Fail2banStatus(""))
	if err != nil {
		return nil, err
	}
	list, ok := statusField(string(out), "Jail list")
	if !ok {
		return nil, errors.New("fail2ban: fail2ban-client status printed no jail list")
	}
	names := []string{}
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// jailStatus reads `fail2ban-client status <jail>`: its counters and the
// addresses it bans now.
func jailStatus(ctx context.Context, env core.Env, name string) (Jail, []string, error) {
	out, err := client(ctx, env, platform.Fail2banStatus(name))
	if err != nil {
		return Jail{}, nil, err
	}
	j := Jail{Name: name}
	for _, f := range []struct {
		label string
		to    *int
	}{{"Currently banned", &j.Banned}, {"Currently failed", &j.Failed}, {"Total failed", &j.FailedTotal}} {
		v, ok := statusField(string(out), f.label)
		n, err := strconv.Atoi(v)
		if !ok || err != nil {
			return Jail{}, nil, fmt.Errorf("fail2ban: fail2ban-client status %s printed no %q count", name, f.label)
		}
		*f.to = n
	}
	list, _ := statusField(string(out), "Banned IP list")
	return j, strings.Fields(list), nil
}

// statusField is the value after "<label>:" on a line of fail2ban-client's
// status tree, such as "|- Currently banned:\t2".
func statusField(out, label string) (string, bool) {
	for line := range strings.Lines(out) {
		_, rest, ok := strings.Cut(line, label+":")
		if ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// banTimeLine is one line of `get <jail> banip --with-time`:
// "<addr> \t<start> + <seconds> = <end>", times in the host's local time.
var banTimeLine = regexp.MustCompile(`^(\S+)\s+(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d) \+ (-?\d+) = (.+)$`)

const banTimeLayout = "2006-01-02 15:04:05"

func parseBanTimes(jail, out string) ([]Ban, error) {
	bans := []Ban{}
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := banTimeLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("fail2ban: fail2ban-client get %s banip printed %q", jail, line)
		}
		start, err := time.ParseInLocation(banTimeLayout, m[2], time.Local)
		if err != nil {
			return nil, fmt.Errorf("fail2ban: ban start %q: %w", m[2], err)
		}
		b := Ban{Jail: jail, Address: m[1], Start: start}
		if secs, _ := strconv.Atoi(m[3]); secs >= 0 {
			end, err := time.ParseInLocation(banTimeLayout, strings.TrimSpace(m[4]), time.Local)
			if err != nil {
				return nil, fmt.Errorf("fail2ban: ban end %q: %w", m[4], err)
			}
			b.End = &end
		}
		bans = append(bans, b)
	}
	return bans, nil
}

// readSettingsFile is Darkpanel's settings file, nil when it does not exist.
func (m module) readSettingsFile() ([]byte, error) {
	b, err := m.d.readFile(SettingsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fail2ban: read %s: %w", SettingsFile, err)
	}
	return b, nil
}
