//go:build harness

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/test/harness/vm"
)

// labIndexes is how many labs one process can hand out: an index fills one
// octet of the lab's addresses, and 0 and 255 stay unused.
const labIndexes = 254

// labRole is a VM role: the hostname suffix after the lab's slug.
var labRole = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// labRoleMax keeps a VM name, slug and role, within a 63-byte hostname.
const labRoleMax = 31

// labSlugMax bounds the readable part of a lab's slug, so a VM name built
// from it stays a short hostname.
const labSlugMax = 24

// The multicast ports of the labs: a process picks a random even base in
// [labPortMin, labPortMin+labPortSpan), above the ports TestM1 and TestM2
// draw (20000 to 40001), and lab i uses base+2i and base+2i+1.
const (
	labPortMin  = 40100
	labPortSpan = 9500
)

// NIC names one of a VM's network interfaces in a lab MAC address.
type NIC int

// The interfaces of a harness VM (vm.Config).
const (
	NICInet NIC = iota // the "internet" link
	NICMgmt            // the user-mode management NIC
	NICPriv            // the private link that stands in for the tailnet
)

// Lab is one harness test's share of the runner: a slug that prefixes its
// VM names, a run directory, and multicast links and addresses that no
// other lab of the process uses, all derived from the test's name. Two labs
// may boot VMs at the same time without sharing a link, a file or an
// address.
type Lab struct {
	Slug          string // a lowercase form of the test name, then six hex digits of its hash
	Dir           string // the run directory: -workdir or a test temporary directory, then Slug
	Index         int    // 1 to 254, distinct among the labs of the process
	McastPort     int    // the internet link
	PrivMcastPort int    // the private link
}

// labAllocator hands out lab indexes. An index starts at a hash of the test
// name and moves to the next free one on a collision, so a name keeps its
// index within a process and two names never share one.
type labAllocator struct {
	mu     sync.Mutex
	base   int // the internet link port of index 0
	byName map[string]int
	used   [labIndexes + 1]bool
}

func newLabAllocator(base int) *labAllocator {
	return &labAllocator{base: base, byName: map[string]int{}}
}

var labs = newLabAllocator(labPortMin + 2*rand.IntN(labPortSpan/2)) //nolint:gosec // G404: a link port, not a secret

// index returns name's lab index.
func (a *labAllocator) index(name string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.byName[name]; ok {
		return i, nil
	}
	sum := sha256.Sum256([]byte(name))
	start := int(binary.BigEndian.Uint16(sum[3:5])) % labIndexes
	for k := range labIndexes {
		i := (start+k)%labIndexes + 1
		if !a.used[i] {
			a.used[i] = true
			a.byName[name] = i
			return i, nil
		}
	}
	return 0, fmt.Errorf("harness: all %d lab indexes are in use", labIndexes)
}

// lab returns the lab of the test name with its run directory under base.
func (a *labAllocator) lab(name, base string) (*Lab, error) {
	i, err := a.index(name)
	if err != nil {
		return nil, err
	}
	slug := labSlug(name)
	return &Lab{
		Slug: slug, Dir: filepath.Join(base, slug), Index: i,
		McastPort: a.base + 2*i, PrivMcastPort: a.base + 2*i + 1,
	}, nil
}

// labSlug is name in lowercase letters, digits and single dashes, at most
// labSlugMax of them, then a dash and six hex digits of name's SHA-256, so
// two names that read alike still get distinct slugs.
func labSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := b.String()
	if len(s) > labSlugMax {
		s = s[:labSlugMax]
	}
	s = strings.TrimSuffix(s, "-")
	if s == "" {
		s = "t"
	}
	sum := sha256.Sum256([]byte(name))
	return s + "-" + hex.EncodeToString(sum[:3])
}

// NewLab returns tb's lab. Its run directory is under -workdir when given,
// so the workflow keeps the logs, and under a test temporary directory
// otherwise.
func NewLab(tb testing.TB) *Lab {
	tb.Helper()
	base := *workdirFlag
	if base == "" {
		base = tb.TempDir()
	}
	l, err := labs.lab(tb.Name(), base)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Logf("lab %s: index %d, run directory %s, links %s:%d and %s:%d",
		l.Slug, l.Index, l.Dir, vm.McastGroup, l.McastPort, vm.McastGroup, l.PrivMcastPort)
	return l
}

// checkHost panics on a host number outside 1 to 254: a caller passing one
// is a mistake in the test, not a run-time condition.
func checkHost(host int) {
	if host < 1 || host > 254 {
		panic(fmt.Sprintf("harness: lab host number %d is outside 1 to 254", host))
	}
}

// VMName is the name of the lab's VM with role, such as target or scanner:
// its hostname and the prefix of its files in Dir.
func (l *Lab) VMName(role string) string {
	if len(role) > labRoleMax || !labRole.MatchString(role) {
		panic(fmt.Sprintf("harness: VM role %q is not up to %d lowercase letters, digits and single dashes", role, labRoleMax))
	}
	return l.Slug + "-" + role
}

// MAC is the address of host's nic in the lab, in QEMU's 52:54:00 range.
func (l *Lab) MAC(nic NIC, host int) string {
	checkHost(host)
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", l.Index, int(nic), host)
}

// InetIPv4 is host's address on the lab's internet link, outside every
// private range like M1's 198.18.0.0/15.
func (l *Lab) InetIPv4(host int) string {
	checkHost(host)
	return fmt.Sprintf("198.18.%d.%d", l.Index, host)
}

// InetIPv6 is host's IPv6 address on the lab's internet link.
func (l *Lab) InetIPv6(host int) string {
	checkHost(host)
	return fmt.Sprintf("2001:db8:d0:%x::%x", l.Index, host)
}

// InetAddrs are host's internet-link addresses in CIDR form, for
// vm.Config.InetAddrs.
func (l *Lab) InetAddrs(host int) []string {
	return []string{l.InetIPv4(host) + "/15", l.InetIPv6(host) + "/64"}
}

// PrivPrefix is the lab's private-link range. TestM2 keeps 10.77.0.0/24.
func (l *Lab) PrivPrefix() netip.Prefix {
	return netip.MustParsePrefix(fmt.Sprintf("10.77.%d.0/24", l.Index))
}

// PrivAddr is host's private-link address.
func (l *Lab) PrivAddr(host int) string {
	checkHost(host)
	return fmt.Sprintf("10.77.%d.%d", l.Index, host)
}

// Boot boots cfgs in the lab's run directory within the VM limit and stops
// each VM when the test ends.
func (l *Lab) Boot(ctx context.Context, t *testing.T, cfgs []vm.Config) []*vm.VM {
	t.Helper()
	for i := range cfgs {
		cfgs[i].Dir = l.Dir
	}
	return bootVMs(ctx, t, l.Dir, cfgs)
}
