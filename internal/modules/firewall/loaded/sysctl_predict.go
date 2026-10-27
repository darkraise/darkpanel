package loaded

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

const (
	keyV6AllFwd  = "net/ipv6/conf/all/forwarding"
	prefixV4Conf = "net/ipv4/conf/"
	prefixV6Conf = "net/ipv6/conf/"
	ifaceDefault = "default"
	ifaceAll     = "all"
	suffixFwd    = "/forwarding"
)

// The value patterns of the firewall's sysctl owned paths (spec 2.1).
var (
	sysctlBoolean  = regexp.MustCompile("^[01]$")
	sysctlTristate = regexp.MustCompile("^[012]$")
)

// PredictSysctl returns the supported sysctl values (in /proc/sys form, as
// in Loaded.Sysctl) after tr, starting from cur. UFW applies its sysctl
// file only when it starts: on TransEnable, and on TransReload while
// cfg.Enabled (ufw reload stops and starts UFW, and skips both when UFW is
// not enabled). Any other transition returns a copy of cur.
//
// The file's supported settings apply in file order; a key that cur does
// not hold (an absent interface, IPv6 disabled) is
// skipped, as sysctl -e skips a missing key. The kernel's side effects on
// supported keys are modelled: writing net/ipv6/conf/all/forwarding sets
// default and every interface's forwarding, even when the value does not
// change. Writing net/ipv4/ip_forward changes no other supported key (the
// kernel sets every forwarding value under net/ipv4/conf and resets
// all/accept_redirects, none of them supported). Writing an IPv4 default
// value can be copied by the kernel to every interface never set
// explicitly, which the live values cannot tell, so a write that could
// change such an interface is an error, as is a value the firewall's
// sysctl declarations would not let a revert write back. Both errors are
// firewall.unsupported_configuration.
func PredictSysctl(cfg fwstate.Config, cur map[string]string, tr fwstate.Transition) (map[string]string, error) {
	out := maps.Clone(cur)
	if out == nil {
		out = map[string]string{}
	}
	if tr != fwstate.TransEnable && (tr != fwstate.TransReload || !cfg.Enabled) {
		return out, nil
	}
	written := map[string]bool{}
	for _, s := range cfg.Sysctl {
		if !conf.SupportedSysctl(s.Key) {
			continue
		}
		pattern := sysctlPattern(s.Key)
		if !pattern.MatchString(s.Value) {
			return nil, errcode.New(errcode.FirewallUnsupportedConfiguration,
				fmt.Sprintf("%s sets %s to %q; Darkpanel supports only values matching %s", cfg.SysctlFile, s.Key, s.Value, pattern))
		}
		old, ok := out[s.Key]
		if !ok {
			continue
		}
		if err := sysctlDefaultCopy(cfg, out, written, s, old); err != nil {
			return nil, err
		}
		out[s.Key] = s.Value
		written[s.Key] = true
		if s.Key == keyV6AllFwd {
			for k := range out {
				if strings.HasPrefix(k, prefixV6Conf) && strings.HasSuffix(k, suffixFwd) {
					out[k] = s.Value
				}
			}
		}
	}
	return out, nil
}

// sysctlPattern is the value pattern of a supported key.
func sysctlPattern(key string) *regexp.Regexp {
	if strings.HasSuffix(key, "/accept_ra") || strings.HasSuffix(key, "/rp_filter") {
		return sysctlTristate
	}
	return sysctlBoolean
}

// sysctlDefaultCopy refuses a write of an IPv4 default value that the kernel
// might copy to an interface: one not written earlier in the file whose
// value equals the old default. An interface whose value differs from the
// default has been set explicitly, so the kernel leaves it alone.
func sysctlDefaultCopy(cfg fwstate.Config, cur map[string]string, written map[string]bool, s fwstate.SysctlSetting, old string) error {
	param, ok := strings.CutPrefix(s.Key, prefixV4Conf+ifaceDefault+"/")
	if !ok || s.Value == old {
		return nil
	}
	for _, k := range slices.Sorted(maps.Keys(cur)) {
		iface, rest, _ := strings.Cut(strings.TrimPrefix(k, prefixV4Conf), "/")
		if !strings.HasPrefix(k, prefixV4Conf) || rest != param || iface == ifaceAll || iface == ifaceDefault || written[k] || cur[k] != old {
			continue
		}
		return errcode.New(errcode.FirewallUnsupportedConfiguration, fmt.Sprintf(
			"%s sets %s to %s, which the kernel may also copy to %s; Darkpanel cannot predict that, so set the interface's own value earlier in the file",
			cfg.SysctlFile, s.Key, s.Value, k))
	}
	return nil
}
