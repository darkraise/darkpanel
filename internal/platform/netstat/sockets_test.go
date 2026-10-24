package netstat

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The fixtures are little-endian kernel output, as on every architecture
// Darkpanel ships for (amd64, arm64).
const (
	skHeader4 = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	skHeader6 = "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	skTCP     = skHeader4 +
		"   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1001 1 0000000000000000 100 0 0 10 0\n" +
		"   1: FA00A8C0:0016 0500A8C0:D431 01 00000000:00000000 02:000AE0C6 00000000     0        0 1002 4 0000000000000000 20 4 29 10 -1\n" +
		"   2: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000   999        0 1003 1 0000000000000000 100 0 0 10 0\n"
	skTCP6 = skHeader6 +
		"   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1004 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0000000000000000FFFF0000FA00A8C0:0016 0000000000000000FFFF00000600A8C0:E001 01 00000000:00000000 00:00000000 00000000     0        0 1005 1 0000000000000000 20 4 30 10 -1\n" +
		"   2: B80D0120000000000000000001000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 100 0 0 10 0\n"
	skUDP = skHeader4 +
		"  123: 00000000:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   101        0 1006 2 0000000000000000 0\n"
)

// skProc builds a fake proc root: the socket tables and, per pid, its comm
// and fd links to socket inodes.
func skProc(t *testing.T, tables map[string]string, procs map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, text := range tables {
		if err := os.WriteFile(filepath.Join(root, "net", name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for pid, spec := range procs {
		fd := filepath.Join(root, pid, "fd")
		if err := os.MkdirAll(fd, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, pid, "comm"), []byte(spec[0]+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for i, target := range spec[1:] {
			if err := os.Symlink(target, filepath.Join(fd, string(rune('3'+i)))); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestSocketsFromProc(t *testing.T) {
	root := skProc(t,
		map[string]string{"tcp": skTCP, "tcp6": skTCP6, "udp": skUDP},
		map[string][]string{
			"812":  {"sshd", "socket:[1001]", "socket:[1004]", "/dev/null"},
			"4410": {"sshd-session", "socket:[1002]", "socket:[1005]", "socket:[1005]"},
			"4411": {"sshd-session", "socket:[1002]"},
			"9000": {"darkpanel", "pipe:[77]", "socket:[1003]"},
			"self": {"ignored", "socket:[1001]"},
		})
	socks, err := Table{Root: root}.Sockets()
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 7 {
		t.Fatalf("read %d sockets, want 7 (udp6 is absent and skipped)", len(socks))
	}
	byInode := map[uint64]Socket{}
	for _, s := range socks {
		byInode[s.Inode] = s
	}
	sshd := byInode[1001]
	if sshd.Proto != "tcp" || sshd.Family != IPv4 || sshd.Local != netip.MustParseAddrPort("0.0.0.0:22") || !sshd.Listening() ||
		!reflect.DeepEqual(sshd.Processes, []Process{{PID: 812, Comm: "sshd"}}) {
		t.Fatalf("sshd listener = %+v", sshd)
	}
	sess := byInode[1002]
	if sess.State != "established" || sess.Listening() || sess.Local != netip.MustParseAddrPort("192.168.0.250:22") ||
		sess.Remote != netip.MustParseAddrPort("192.168.0.5:54321") || len(sess.Processes) != 2 {
		t.Fatalf("ssh session = %+v", sess)
	}
	pids := []int{sess.Processes[0].PID, sess.Processes[1].PID}
	slices.Sort(pids)
	if !slices.Equal(pids, []int{4410, 4411}) {
		t.Fatalf("session owners = %+v", sess.Processes)
	}
	if s := byInode[1003]; s.UID != 999 || s.Local != netip.MustParseAddrPort("127.0.0.1:8080") || s.Processes[0].Comm != "darkpanel" {
		t.Fatalf("loopback listener = %+v", s)
	}
	mapped := byInode[1005]
	if mapped.Family != IPv6 || mapped.Local.Addr() != netip.MustParseAddr("::ffff:192.168.0.250") ||
		mapped.Remote.Addr().Unmap() != netip.MustParseAddr("192.168.0.6") || mapped.Remote.Port() != 0xE001 ||
		len(mapped.Processes) != 1 {
		t.Fatalf("mapped session = %+v", mapped)
	}
	if s := byInode[0]; s.Local != netip.MustParseAddrPort("[2001:db8::1]:443") || s.Processes != nil {
		t.Fatalf("socket without inode = %+v", s)
	}
	if s := byInode[1006]; s.Proto != "udp" || !s.Listening() || s.Local.Port() != 53 || s.UID != 101 || s.Processes != nil {
		t.Fatalf("udp socket = %+v", s)
	}
}

func TestSocketsRejectMalformedTables(t *testing.T) {
	cases := map[string]string{
		"few fields":   skHeader4 + "   0: 00000000:0016 00000000:0000 0A\n",
		"short addr":   skHeader4 + "   0: 0000:0016 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
		"no port":      skHeader4 + "   0: 00000000 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
		"bad hex":      skHeader4 + "   0: 0000000G:0016 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
		"bad state":    skHeader4 + "   0: 00000000:0016 00000000:0000 ZZ 0:0 0:0 0 0 0 1 1\n",
		"bad uid":      skHeader4 + "   0: 00000000:0016 00000000:0000 0A 0:0 0:0 0 x 0 1 1\n",
		"bad inode":    skHeader4 + "   0: 00000000:0016 00000000:0000 0A 0:0 0:0 0 0 0 -1 1\n",
		"port too big": skHeader4 + "   0: 00000000:10000 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
	}
	for name, table := range cases {
		t.Run(name, func(t *testing.T) {
			root := skProc(t, map[string]string{"tcp": table}, nil)
			if _, err := (Table{Root: root}).Sockets(); err == nil || !strings.Contains(err.Error(), "line 2") {
				t.Fatalf("err = %v, want one naming line 2", err)
			}
		})
	}
}

func TestUnknownStateDoesNotHideTheTable(t *testing.T) {
	table := skHeader4 +
		"   0: 00000000:0016 00000000:0000 FF 00000000:00000000 00:00000000 00000000     0        0 1 1 0\n" +
		"   1: 00000000:0017 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1 0\n"
	root := skProc(t, map[string]string{"tcp": table}, nil)
	socks, err := Table{Root: root}.Sockets()
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 2 || socks[0].State != StateUnknown || socks[0].Listening() || !socks[1].Listening() {
		t.Fatalf("sockets = %+v", socks)
	}
}

func TestStateAndProtocolPairing(t *testing.T) {
	for _, tc := range []struct {
		sock      Socket
		listening bool
	}{
		{Socket{Proto: ProtoTCP, State: StateListen}, true},
		{Socket{Proto: ProtoTCP, State: StateClose}, false},
		{Socket{Proto: ProtoUDP, State: StateClose}, true},
		{Socket{Proto: ProtoUDP, State: StateListen}, false},
		{Socket{Proto: ProtoUDP, State: StateEstablished}, false},
		{Socket{Proto: "sctp", State: StateClose}, false},
	} {
		if got := tc.sock.Listening(); got != tc.listening {
			t.Errorf("%s %s: Listening() = %v, want %v", tc.sock.Proto, tc.sock.State, got, tc.listening)
		}
	}
	if got := stateOf(ProtoUDP, 0x0A); got != StateUnknown {
		t.Errorf("a UDP socket in the TCP listen state = %q, want unknown", got)
	}
	if got := stateOf(ProtoUDP, 0x07); got != StateClose {
		t.Errorf("an unconnected UDP socket = %q", got)
	}
	if got := stateOf(ProtoTCP, 0x0A); got != StateListen {
		t.Errorf("a listening TCP socket = %q", got)
	}
}

func TestSocketsLive(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	want := netip.MustParseAddrPort(l.Addr().String())
	socks, err := Sockets()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range socks {
		if s.Proto == "tcp" && s.Local == want && s.Listening() {
			for _, p := range s.Processes {
				if p.PID == os.Getpid() {
					return
				}
			}
			t.Fatalf("listener %v found without this process: %+v", want, s.Processes)
		}
	}
	t.Fatalf("listener %v not in the socket table", want)
}
