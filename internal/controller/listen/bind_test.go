package listen

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// sockopt reads an integer socket option of a TCP listener.
func sockopt(t *testing.T, ln net.Listener, level, opt int) (int, error) {
	t.Helper()
	raw, err := ln.(*net.TCPListener).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var gerr error
	if err := raw.Control(func(fd uintptr) { v, gerr = unix.GetsockoptInt(int(fd), level, opt) }); err != nil { //nolint:gosec // G115: a file descriptor fits in an int
		t.Fatal(err)
	}
	return v, gerr
}

func TestBindSetsFreeBind(t *testing.T) {
	cases := []struct {
		name       string
		addr       string
		level, opt int
	}{
		{"ipv4", "127.0.0.1:0", unix.SOL_IP, unix.IP_FREEBIND},
		{"ipv6", "[::1]:0", unix.SOL_IPV6, unix.IPV6_FREEBIND},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, free := range []bool{true, false} {
				ln, err := Bind(context.Background(), Endpoint{Addr: netip.MustParseAddrPort(c.addr), FreeBind: free})
				switch {
				case c.name == "ipv6" && (errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT)):
					t.Skip("this host has no IPv6 loopback address")
				case errors.Is(err, syscall.EPERM):
					t.Skipf("the kernel refuses the free-bind option to this user: %v", err)
				case err != nil:
					t.Fatal(err)
				}
				v, err := sockopt(t, ln, c.level, c.opt)
				_ = ln.Close()
				if errors.Is(err, syscall.ENOPROTOOPT) {
					t.Skipf("the kernel cannot report the free-bind option: %v", err)
				}
				if err != nil {
					t.Fatal(err)
				}
				if (v == 1) != free {
					t.Fatalf("FreeBind %v: socket option = %d", free, v)
				}
			}
		})
	}
}

func TestBindReportsTheEndpoint(t *testing.T) {
	first, err := Bind(context.Background(), Endpoint{Addr: netip.MustParseAddrPort("127.0.0.1:0")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	busy := netip.MustParseAddrPort(first.Addr().String())
	_, err = Bind(context.Background(), Endpoint{Addr: busy, TLS: true})
	if err == nil || !strings.Contains(err.Error(), "listen: bind https://"+busy.String()) || !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("second bind of %s: error = %v, want a wrapped EADDRINUSE naming the endpoint", busy, err)
	}
}
