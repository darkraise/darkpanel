package listen

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// BindFunc opens the listening socket of one endpoint. Tests pass their own
// to stand in for addresses the test host lacks.
type BindFunc func(ctx context.Context, e Endpoint) (net.Listener, error)

// Bind is the production BindFunc: TCP on the endpoint's address family,
// with IP_FREEBIND or IPV6_FREEBIND when e.FreeBind is set (ADR 0006).
func Bind(ctx context.Context, e Endpoint) (net.Listener, error) {
	network := "tcp4"
	if e.Addr.Addr().Is6() {
		network = "tcp6"
	}
	var lc net.ListenConfig
	if e.FreeBind {
		lc.Control = freeBind
	}
	ln, err := lc.Listen(ctx, network, e.Addr.String())
	if err != nil {
		return nil, fmt.Errorf("listen: bind %s: %w", e.URL(), err)
	}
	return ln, nil
}

func freeBind(network, _ string, c syscall.RawConn) error {
	level, opt := unix.SOL_IP, unix.IP_FREEBIND
	if network == "tcp6" {
		level, opt = unix.SOL_IPV6, unix.IPV6_FREEBIND
	}
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), level, opt, 1) //nolint:gosec // G115: a file descriptor fits in an int
	}); err != nil {
		return err
	}
	if serr != nil {
		return os.NewSyscallError("setsockopt", serr)
	}
	return nil
}
