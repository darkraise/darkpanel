//go:build harness

package vm

import (
	"context"
	"net"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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
	}
	for _, c := range cases {
		if got := qemuArgs(c.cfg, c.overlay, c.seed, c.port); !slices.Equal(got, c.want) {
			t.Errorf("qemuArgs for %s =\n%q\nwant\n%q", c.cfg.Name, got, c.want)
		}
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
