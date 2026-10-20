//go:build harness

package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestQemuArgs(t *testing.T) {
	cases := []struct {
		cfg           Config
		overlay, seed string
		port          int
		want          []string
	}{
		{
			Config{Name: "target", Dir: "/run/h", MemoryMiB: 2048, CPUs: 2,
				MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10", McastPort: 23456},
			"/run/h/target.qcow2", "/run/h/target-seed.img", 40022,
			[]string{
				"-name", "target", "-machine", "q35,accel=kvm", "-cpu", "host", "-smp", "2", "-m", "2048",
				"-display", "none", "-monitor", "none", "-serial", "file:/run/h/target-console.log",
				"-drive", "file=/run/h/target.qcow2,if=virtio,format=qcow2",
				"-drive", "file=/run/h/target-seed.img,if=virtio,format=raw,readonly=on",
				"-netdev", "user,id=mgmt,hostfwd=tcp:127.0.0.1:40022-:22",
				"-device", "virtio-net-pci,netdev=mgmt,mac=52:54:00:d0:01:10",
				"-netdev", "socket,id=inet,mcast=230.0.0.1:23456",
				"-device", "virtio-net-pci,netdev=inet,mac=52:54:00:d0:00:10",
			},
		},
		{
			Config{Name: "scanner", Dir: "/tmp/run2", MemoryMiB: 1024, CPUs: 1,
				MgmtMAC: "52:54:00:d0:01:20", InetMAC: "52:54:00:d0:00:20", McastPort: 31007},
			"/tmp/run2/scanner.qcow2", "/tmp/run2/scanner-seed.img", 41999,
			[]string{
				"-name", "scanner", "-machine", "q35,accel=kvm", "-cpu", "host", "-smp", "1", "-m", "1024",
				"-display", "none", "-monitor", "none", "-serial", "file:/tmp/run2/scanner-console.log",
				"-drive", "file=/tmp/run2/scanner.qcow2,if=virtio,format=qcow2",
				"-drive", "file=/tmp/run2/scanner-seed.img,if=virtio,format=raw,readonly=on",
				"-netdev", "user,id=mgmt,hostfwd=tcp:127.0.0.1:41999-:22",
				"-device", "virtio-net-pci,netdev=mgmt,mac=52:54:00:d0:01:20",
				"-netdev", "socket,id=inet,mcast=230.0.0.1:31007",
				"-device", "virtio-net-pci,netdev=inet,mac=52:54:00:d0:00:20",
			},
		},
		{
			Config{Name: "controller", Dir: "/run/h", MemoryMiB: 2048, CPUs: 2,
				MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10", McastPort: 23456,
				PrivMAC: "52:54:00:d0:02:10", PrivMcastPort: 23457},
			"/run/h/controller.qcow2", "/run/h/controller-seed.img", 40022,
			[]string{
				"-name", "controller", "-machine", "q35,accel=kvm", "-cpu", "host", "-smp", "2", "-m", "2048",
				"-display", "none", "-monitor", "none", "-serial", "file:/run/h/controller-console.log",
				"-drive", "file=/run/h/controller.qcow2,if=virtio,format=qcow2",
				"-drive", "file=/run/h/controller-seed.img,if=virtio,format=raw,readonly=on",
				"-netdev", "user,id=mgmt,hostfwd=tcp:127.0.0.1:40022-:22",
				"-device", "virtio-net-pci,netdev=mgmt,mac=52:54:00:d0:01:10",
				"-netdev", "socket,id=inet,mcast=230.0.0.1:23456",
				"-device", "virtio-net-pci,netdev=inet,mac=52:54:00:d0:00:10",
				"-netdev", "socket,id=priv,mcast=230.0.0.1:23457",
				"-device", "virtio-net-pci,netdev=priv,mac=52:54:00:d0:02:10",
			},
		},
	}
	for _, c := range cases {
		if got := qemuArgs(c.cfg, c.overlay, c.seed, c.port); !slices.Equal(got, c.want) {
			t.Errorf("qemuArgs for %s =\n%q\nwant\n%q", c.cfg.Name, got, c.want)
		}
	}
}

// TestBootRefusesOneLinkTwice: a private link on the internet link's
// multicast port would join both NICs to one link, so Boot refuses it before
// it creates any file.
func TestBootRefusesOneLinkTwice(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "run")
	cfg := Config{
		Name: "controller", Dir: dir, Image: "missing.qcow2", MemoryMiB: 2048, CPUs: 2,
		MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10", McastPort: 23456,
		PrivMAC: "52:54:00:d0:02:10", PrivMcastPort: 23456, Key: key,
	}
	_, err = Boot(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "multicast port 23456") {
		t.Errorf("Boot with one multicast port for both links: %v, want a refusal naming port 23456", err)
	}
	if _, serr := os.Stat(dir); !errors.Is(serr, fs.ErrNotExist) {
		t.Errorf("Boot created %s before refusing the config (%v)", dir, serr)
	}
}

// TestConnectGivesUpOnASilentPeer: a peer that accepts TCP and never speaks
// SSH must not hold connect past its context.
func TestConnectGivesUpOnASilentPeer(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	v := &VM{cfg: Config{Name: "silent", Key: key}, sshAddr: ln.Addr().String(), hostKey: key.PublicKey(), exited: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = v.connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("connect to a silent peer: %v, want a refusal saying SSH did not answer", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("connect returned %v after start; its context ended at 300ms", elapsed)
	}
}

func TestShellQuote(t *testing.T) {
	for _, s := range []string{"/usr/local/bin/darkpanel", "it's", "", "a b;$(reboot)", `"\`} {
		out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(s)).Output() //nolint:gosec // G204: the test's own quoting
		if err != nil || string(out) != s {
			t.Errorf("sh read shellQuote(%q) = %q as %q (%v)", s, shellQuote(s), out, err)
		}
	}
}

func TestKillCommand(t *testing.T) {
	cases := map[string]string{
		"darkpanel": "sudo pkill -KILL -x -- 'darkpanel'",
		"worker.1":  "sudo pkill -KILL -x -- 'worker\\.1'",
	}
	for name, want := range cases {
		if got := killCommand(name); got != want {
			t.Errorf("killCommand(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestLastLines(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"a\nb\nc\n", 2, "b\nc"},
		{"a\nb", 5, "a\nb"},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := lastLines(c.in, c.n); got != c.want {
			t.Errorf("lastLines(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// TestBootChangedGivesUpOnAHungSession: a connection that reached the old
// boot as it went down can accept SSH and then never answer; the probe must
// give up within probeTimeout so Reboot can dial the new boot.
func TestBootChangedGivesUpOnAHungSession(t *testing.T) {
	hostKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	srv := &ssh.ServerConfig{NoClientAuth: true}
	srv.AddHostKey(hostKey)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, srv)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, creqs, err := nc.Accept()
					if err != nil {
						continue
					}
					t.Cleanup(func() { _ = ch.Close() })
					go func() {
						for range creqs { // never replies: the exec hangs
						}
					}()
				}
			}()
		}
	}()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })
	v := &VM{cfg: Config{Name: "hung", Key: key}, sshAddr: ln.Addr().String(), hostKey: hostKey.PublicKey(), exited: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := v.connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer v.closeClient()
	start := time.Now()
	if v.bootChanged(ctx, "old") {
		t.Error("bootChanged on a hung session = true, want false")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("bootChanged returned after %v; probeTimeout was 200ms", elapsed)
	}
}

// stallConn blocks every Write once stall is set, as a socket whose send
// buffer is full does, until the connection is closed.
type stallConn struct {
	net.Conn
	stall atomic.Bool
	gone  chan struct{}
	once  sync.Once
}

func (c *stallConn) Write(p []byte) (int, error) {
	if c.stall.Load() {
		<-c.gone
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *stallConn) Close() error {
	c.once.Do(func() { close(c.gone) })
	return c.Conn.Close()
}

// TestRunWithAbandonsASessionThatCannotClose: closing a session writes to
// the socket, which blocks when the connection is stuck; runWith must still
// return and drop the connection.
func TestRunWithAbandonsASessionThatCannotClose(t *testing.T) {
	hostKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	srv := &ssh.ServerConfig{NoClientAuth: true}
	srv.AddHostKey(hostKey)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	raw, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn := &stallConn{Conn: raw, gone: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, chans, reqs, err := ssh.NewServerConn(c, srv)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for nc := range chans {
			conn.stall.Store(true)
			ch, creqs, err := nc.Accept()
			if err != nil {
				continue
			}
			t.Cleanup(func() { _ = ch.Close() })
			go func() {
				for range creqs { // never replies: the exec hangs
				}
			}()
		}
	}()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	conf := &ssh.ClientConfig{User: User, Auth: []ssh.AuthMethod{ssh.PublicKeys(key)}, HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey())}
	sc, chans, creqs, err := ssh.NewClientConn(conn, ln.Addr().String(), conf)
	if err != nil {
		t.Fatal(err)
	}
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })
	v := &VM{cfg: Config{Name: "stuck", Key: key}, client: ssh.NewClient(sc, chans, creqs)}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := v.runWith(ctx, "sleep 60", nil)
		returned <- err
	}()
	select {
	case err := <-returned:
		if err == nil {
			t.Error("runWith = nil, want the context error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWith still blocked 3s after its context ended; closing the session hangs on the stuck socket")
	}
	v.mu.Lock()
	c := v.client
	v.mu.Unlock()
	if c != nil {
		t.Error("the connection is still in use; a stuck close must drop it")
	}
}

// TestRebootGivesUpOnAHungRebootCommand: the reboot command can hang the
// same way a probe can, and must not use up the whole reboot deadline.
func TestRebootGivesUpOnAHungRebootCommand(t *testing.T) {
	hostKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	srv := &ssh.ServerConfig{NoClientAuth: true}
	srv.AddHostKey(hostKey)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			boot := "old\n"
			if conns.Add(1) > 1 {
				boot = "new\n"
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, srv)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, creqs, err := nc.Accept()
					if err != nil {
						continue
					}
					t.Cleanup(func() { _ = ch.Close() })
					go func() {
						for r := range creqs {
							var req struct{ Command string }
							if r.Type != "exec" || ssh.Unmarshal(r.Payload, &req) != nil || strings.Contains(req.Command, "reboot") {
								continue // the reboot command never replies
							}
							_ = r.Reply(true, nil)
							_, _ = ch.Write([]byte(boot))
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							_ = ch.Close()
						}
					}()
				}
			}()
		}
	}()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })
	v := &VM{cfg: Config{Name: "hung", Key: key}, sshAddr: ln.Addr().String(), hostKey: hostKey.PublicKey(), exited: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := v.connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer v.closeClient()
	start := time.Now()
	if err := v.Reboot(ctx); err != nil {
		t.Fatalf("Reboot: %v (after %v)", err, time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Errorf("Reboot returned after %v; the hung reboot command should end within probeTimeout", elapsed)
	}
}

// sshTestServer is an SSH server on 127.0.0.1 with no client authentication.
type sshTestServer struct {
	addr     string
	hostKey  ssh.PublicKey
	opens    chan int32  // the connection number of each session request, as it arrives
	forwards chan string // the target of each direct-tcpip channel it forwards
}

// testServer starts an sshTestServer. reply gives the output of each exec
// request, with n counting connections from 1; ok false leaves the request
// unanswered, so the command hangs. Unless acceptFirst, the first connection
// never answers a session or direct-tcpip request, as a stuck connection
// does. A direct-tcpip channel is forwarded to its target on this host.
func testServer(t *testing.T, acceptFirst bool, reply func(n int32, cmd string) (out string, ok bool)) *sshTestServer {
	t.Helper()
	hostKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	srv := &ssh.ServerConfig{NoClientAuth: true}
	srv.AddHostKey(hostKey)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshTestServer{
		addr: ln.Addr().String(), hostKey: hostKey.PublicKey(),
		opens: make(chan int32, 16), forwards: make(chan string, 16),
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	var count atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			n := count.Add(1)
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, srv)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					select {
					case s.opens <- n:
					default:
					}
					if n == 1 && !acceptFirst {
						continue // neither accepted nor rejected: NewSession waits
					}
					if nc.ChannelType() == "direct-tcpip" {
						go s.forward(nc)
						continue
					}
					ch, creqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go func() {
						for r := range creqs {
							var req struct{ Command string }
							if r.Type != "exec" || ssh.Unmarshal(r.Payload, &req) != nil {
								_ = r.Reply(false, nil)
								continue
							}
							out, ok := reply(n, req.Command)
							if !ok {
								continue
							}
							_ = r.Reply(true, nil)
							_, _ = ch.Write([]byte(out))
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							_ = ch.Close()
						}
					}()
				}
			}()
		}
	}()
	return s
}

// forward connects a direct-tcpip channel to its target (RFC 4254 section
// 7.2), or rejects it when the target refuses.
func (s *sshTestServer) forward(nc ssh.NewChannel) {
	var req struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &req); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	target := net.JoinHostPort(req.Host, strconv.FormatUint(uint64(req.Port), 10))
	select {
	case s.forwards <- target:
	default:
	}
	tc, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		_ = tc.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		_, _ = io.Copy(ch, tc)
		_ = ch.CloseWrite()
	}()
	_, _ = io.Copy(tc, ch)
	_ = tc.Close()
	_ = ch.Close()
}

// vmOf returns a VM that dials s, connected.
func vmOf(ctx context.Context, t *testing.T, s *sshTestServer, name string) *VM {
	t.Helper()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	v := &VM{cfg: Config{Name: name, Key: key}, sshAddr: s.addr, hostKey: s.hostKey, exited: make(chan struct{})}
	if err := v.connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.closeClient)
	return v
}

// TestRunReconnectsAfterATimedOutRun: a Run whose context ends on a stuck
// connection drops it, and the next Run must dial a new connection rather
// than fail with "not connected".
func TestRunReconnectsAfterATimedOutRun(t *testing.T) {
	s := testServer(t, false, func(n int32, _ string) (string, bool) { return fmt.Sprintf("conn %d\n", n), true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "flaky")
	short, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err := v.Run(short, "true")
	cancelShort()
	if err == nil {
		t.Fatal("Run on a stuck connection = nil, want the context error")
	}
	if out, err := v.Run(ctx, "true"); err != nil || out != "conn 2\n" {
		t.Errorf("Run after a timed-out Run = %q, %v; want the answer of a new connection", out, err)
	}
}

// TestDialContextReachesAGuestListener: the harness's HTTP client reaches a
// listener on the guest's loopback interface through the SSH connection, and
// a target that refuses fails the dial without dropping the connection.
func TestDialContextReachesAGuestListener(t *testing.T) {
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(guest.Close)
	s := testServer(t, true, func(int32, string) (string, bool) { return "", true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "controller")
	transport := &http.Transport{DialContext: v.DialContext}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, guest.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("GET /healthz through the VM = %s %q, %v", resp.Status, body, err)
	}
	select {
	case got := <-s.forwards:
		if want := guest.Listener.Addr().String(); got != want {
			t.Errorf("the SSH server forwarded to %s, want %s", got, want)
		}
	default:
		t.Error("the request did not go through a direct-tcpip channel")
	}

	closed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := closed.Addr().String()
	_ = closed.Close()
	v.mu.Lock()
	before := v.client
	v.mu.Unlock()
	if c, err := v.DialContext(ctx, "tcp", refused); err == nil {
		_ = c.Close()
		t.Errorf("DialContext to the closed %s = nil, want the refusal", refused)
	}
	v.mu.Lock()
	kept := v.client == before
	v.mu.Unlock()
	if !kept {
		t.Error("a refused dial dropped a working connection")
	}
}

// TestDialContextReconnectsAfterATimedOutDial: a dial whose context ends on a
// stuck connection drops it, as a stuck Run does, and the next dial uses a
// new connection.
func TestDialContextReconnectsAfterATimedOutDial(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("hello\n"))
			_ = c.Close()
		}
	}()
	s := testServer(t, false, func(int32, string) (string, bool) { return "", true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "flaky")
	short, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
	c, err := v.DialContext(short, "tcp", ln.Addr().String())
	cancelShort()
	if err == nil {
		_ = c.Close()
		t.Fatal("DialContext on a stuck connection = nil, want the context error")
	}
	c, err = v.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("DialContext after a timed-out dial: %v; want a new connection", err)
	}
	defer func() { _ = c.Close() }()
	if b, err := io.ReadAll(c); err != nil || string(b) != "hello\n" {
		t.Errorf("read through the new connection = %q, %v", b, err)
	}
}

// TestTimedOutRunKeepsAReplacement: a Run that times out on a stuck
// connection must close that connection, not one a concurrent reconnect put
// in its place.
func TestTimedOutRunKeepsAReplacement(t *testing.T) {
	s := testServer(t, false, func(int32, string) (string, bool) { return "ok\n", true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "swap") // connection 1: stuck
	conf := &ssh.ClientConfig{User: User, Auth: []ssh.AuthMethod{ssh.PublicKeys(v.cfg.Key)}, HostKeyCallback: ssh.FixedHostKey(s.hostKey)}
	cur, err := v.dial(ctx, conf) // connection 2: answers
	if err != nil {
		t.Fatal(err)
	}
	short, cancelShort := context.WithTimeout(ctx, 5*time.Second)
	defer cancelShort()
	returned := make(chan error, 1)
	go func() {
		_, err := v.Run(short, "true")
		returned <- err
	}()
	select {
	case <-s.opens: // the Run is waiting on the stuck connection's session
	case <-time.After(3 * time.Second):
		t.Fatal("the Run never asked the stuck connection for a session")
	}
	v.mu.Lock()
	stuck := v.client
	v.client = cur
	v.mu.Unlock()
	cancelShort()
	if err := <-returned; err == nil {
		t.Fatal("Run on a stuck connection = nil, want the context error")
	}
	v.mu.Lock()
	kept := v.client == cur
	v.mu.Unlock()
	if !kept {
		t.Error("the timed-out Run dropped the replacement connection")
	}
	closed := make(chan struct{})
	go func() {
		_ = stuck.Wait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Error("the stuck connection is still open after its Run timed out")
	}
	if out, err := v.Run(ctx, "true"); err != nil || out != "ok\n" {
		t.Errorf("Run on the replacement = %q, %v", out, err)
	}
}

// TestRebootGivesUpOnAHungFirstProbe: the boot_id read before the reboot
// command gets probeTimeout, not the whole reboot deadline.
func TestRebootGivesUpOnAHungFirstProbe(t *testing.T) {
	s := testServer(t, true, func(int32, string) (string, bool) { return "", false })
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "hung")
	start := time.Now()
	if err := v.Reboot(ctx); err == nil {
		t.Fatal("Reboot with a hung boot_id read = nil, want an error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Reboot returned after %v; the first probe should end within probeTimeout", elapsed)
	}
}

// TestKillProcessNameLength: pkill -x matches the kernel's 15-character
// process name, so a 15-character name is sent and a 16-character one is
// refused without running anything.
func TestKillProcessNameLength(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	s := testServer(t, true, func(_ int32, cmd string) (string, bool) {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		return "", true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "target")
	if err := v.KillProcess(ctx, "abcdefghijklmno"); err != nil {
		t.Errorf("KillProcess of a 15-character name: %v", err)
	}
	err := v.KillProcess(ctx, "abcdefghijklmnop")
	if err == nil || !strings.Contains(err.Error(), "first 15 characters") {
		t.Errorf("KillProcess of a 16-character name: %v, want a refusal naming the 15-character limit", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{killCommand("abcdefghijklmno")}; !slices.Equal(cmds, want) {
		t.Errorf("commands run = %q, want %q", cmds, want)
	}
}
