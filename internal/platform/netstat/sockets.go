// Package netstat reads the host's socket table, interface addresses and
// routes (spec 1.8): sockets from /proc/net joined to their processes
// through /proc/<pid>/fd, routes through hand-written rtnetlink.
package netstat

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Family is an address family.
type Family int

// The address families.
const (
	IPv4 Family = 4
	IPv6 Family = 6
)

// Socket is one entry of the kernel's TCP or UDP socket table. Addresses
// are as the kernel reports them: an IPv4 peer of a dual-stack IPv6 socket
// appears IPv4-mapped (::ffff:a.b.c.d), so callers comparing addresses
// unmap them.
type Socket struct {
	Proto     string // "tcp" or "udp"
	Family    Family // of the table the entry came from
	Local     netip.AddrPort
	Remote    netip.AddrPort
	State     string // a TCP state name; for UDP "established" (connected) or "close" (unconnected)
	UID       uint32
	Inode     uint64
	Processes []Process // every process holding the socket; empty when none is visible
}

// Listening reports whether the socket accepts new connections or
// datagrams: a TCP socket in LISTEN, or an unconnected UDP socket.
func (s Socket) Listening() bool {
	if s.Proto == "tcp" {
		return s.State == "listen"
	}
	return s.State == "close"
}

// Process is a process holding a socket.
type Process struct {
	PID  int
	Comm string // /proc/<pid>/comm
}

// tcpStates are the kernel's TCP states (include/net/tcp_states.h).
var tcpStates = map[uint64]string{
	0x01: "established", 0x02: "syn-sent", 0x03: "syn-recv", 0x04: "fin-wait1",
	0x05: "fin-wait2", 0x06: "time-wait", 0x07: "close", 0x08: "close-wait",
	0x09: "last-ack", 0x0A: "listen", 0x0B: "closing", 0x0C: "new-syn-recv",
}

// Table reads the socket table under a proc root.
type Table struct {
	Root string // "/proc" on a host
}

// Sockets reads the host's sockets from /proc.
func Sockets() ([]Socket, error) { return Table{Root: "/proc"}.Sockets() }

// Sockets reads every TCP and UDP socket of both families with the
// processes holding them. A table the kernel does not provide (IPv6
// disabled) is skipped.
func (t Table) Sockets() ([]Socket, error) {
	var out []Socket
	for _, src := range []struct {
		file   string
		proto  string
		family Family
	}{
		{"tcp", "tcp", IPv4}, {"tcp6", "tcp", IPv6}, {"udp", "udp", IPv4}, {"udp6", "udp", IPv6},
	} {
		socks, err := t.readTable(filepath.Join(t.Root, "net", src.file), src.proto, src.family)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, socks...)
	}
	owners, err := t.owners()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Inode != 0 {
			out[i].Processes = owners[out[i].Inode]
		}
	}
	return out, nil
}

func (t Table) readTable(path, proto string, family Family) ([]Socket, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the proc root joined with a fixed table name
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Socket
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		if line == 1 {
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		s, err := parseEntry(fields, proto, family)
		if err != nil {
			return nil, fmt.Errorf("netstat: %s line %d: %w", path, line, err)
		}
		out = append(out, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("netstat: read %s: %w", path, err)
	}
	return out, nil
}

// parseEntry parses one line of /proc/net/{tcp,udp}{,6}: sl, local
// address, remote address, state, queues, timer, retransmits, uid,
// timeout, inode, and fields this reader ignores.
func parseEntry(fields []string, proto string, family Family) (Socket, error) {
	if len(fields) < 10 {
		return Socket{}, fmt.Errorf("%d fields, want at least 10", len(fields))
	}
	local, err := parseAddrPort(fields[1], family)
	if err != nil {
		return Socket{}, fmt.Errorf("local address: %w", err)
	}
	remote, err := parseAddrPort(fields[2], family)
	if err != nil {
		return Socket{}, fmt.Errorf("remote address: %w", err)
	}
	st, err := strconv.ParseUint(fields[3], 16, 8)
	if err != nil {
		return Socket{}, fmt.Errorf("state %q: %w", fields[3], err)
	}
	state, ok := tcpStates[st]
	if !ok {
		return Socket{}, fmt.Errorf("unknown state %q", fields[3])
	}
	uid, err := strconv.ParseUint(fields[7], 10, 32)
	if err != nil {
		return Socket{}, fmt.Errorf("uid %q: %w", fields[7], err)
	}
	inode, err := strconv.ParseUint(fields[9], 10, 64)
	if err != nil {
		return Socket{}, fmt.Errorf("inode %q: %w", fields[9], err)
	}
	return Socket{
		Proto: proto, Family: family, Local: local, Remote: remote,
		State: state, UID: uint32(uid), Inode: inode,
	}, nil
}

// parseAddrPort decodes the kernel's "ADDR:PORT" hex form. The address is
// printed as 32-bit words in host byte order (four words for IPv6), the
// port as a plain hex number.
func parseAddrPort(s string, family Family) (netip.AddrPort, error) {
	addrHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("%q has no port", s)
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("port %q: %w", portHex, err)
	}
	want := 8
	if family == IPv6 {
		want = 32
	}
	if len(addrHex) != want {
		return netip.AddrPort{}, fmt.Errorf("address %q has %d hex digits, want %d", addrHex, len(addrHex), want)
	}
	raw, err := hex.DecodeString(addrHex)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("address %q: %w", addrHex, err)
	}
	b := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(b[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	addr, _ := netip.AddrFromSlice(b)
	return netip.AddrPortFrom(addr, uint16(port)), nil
}

// owners maps socket inodes to the processes holding them. Processes that
// exit while being read, and descriptors this process may not read, are
// skipped: the table is a snapshot either way.
func (t Table) owners() (map[uint64][]Process, error) {
	entries, err := os.ReadDir(t.Root)
	if err != nil {
		return nil, fmt.Errorf("netstat: read %s: %w", t.Root, err)
	}
	out := map[uint64][]Process{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join(t.Root, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		var proc *Process
		seen := map[uint64]bool{}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inodeText, ok := strings.CutPrefix(target, "socket:[")
			if !ok || !strings.HasSuffix(inodeText, "]") {
				continue
			}
			inode, err := strconv.ParseUint(strings.TrimSuffix(inodeText, "]"), 10, 64)
			if err != nil || seen[inode] {
				continue
			}
			seen[inode] = true
			if proc == nil {
				comm, _ := os.ReadFile(filepath.Join(t.Root, e.Name(), "comm"))
				proc = &Process{PID: pid, Comm: strings.TrimSuffix(string(comm), "\n")}
			}
			out[inode] = append(out[inode], *proc)
		}
	}
	return out, nil
}
