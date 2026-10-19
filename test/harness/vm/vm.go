//go:build harness

package vm

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// Limits of the VM life cycle. A first boot on a GitHub runner, with package
// installs, takes several minutes.
const (
	bootTimeout   = 25 * time.Minute
	rebootTimeout = 5 * time.Minute
	stopTimeout   = 15 * time.Second
	dialInterval  = 2 * time.Second
	dialTimeout   = 10 * time.Second
	diskSize      = "20G"
	tailLines     = 40 // log lines an error quotes
)

// VM is one running virtual machine. Its methods are safe for concurrent use.
type VM struct {
	cfg     Config
	sshAddr string        // 127.0.0.1:<port>, forwarded to the VM's port 22
	hostKey ssh.PublicKey // pinned: cloud-init installs it as the only host key
	cmd     *exec.Cmd
	exited  chan struct{} // closed when QEMU has exited
	waitErr error         // QEMU's exit, written before exited is closed

	mu     sync.Mutex
	client *ssh.Client // nil while disconnected
}

// Boot creates cfg's disk overlay and cloud-init seed in cfg.Dir, starts
// QEMU, and waits until SSH answers and the provisioning script succeeded.
// On an error the VM is stopped, and the error quotes the end of its console
// log, which stays in cfg.Dir.
func Boot(ctx context.Context, cfg Config) (*VM, error) {
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("vm %s: %w", cfg.Name, err)
	}
	image, err := filepath.Abs(cfg.Image)
	if err != nil {
		return nil, fmt.Errorf("vm %s: %w", cfg.Name, err)
	}
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("vm %s: host key: %w", cfg.Name, err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		return nil, fmt.Errorf("vm %s: host key: %w", cfg.Name, err)
	}
	s, err := seedFiles(cfg, hostKey)
	if err != nil {
		return nil, err
	}
	seedPath, err := writeSeed(ctx, cfg, s)
	if err != nil {
		return nil, err
	}
	overlay := filepath.Join(cfg.Dir, cfg.Name+".qcow2")
	if err := run(ctx, "qemu-img", "create", "-q", "-f", "qcow2", "-F", "qcow2", "-b", image, overlay, diskSize); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("vm %s: %w", cfg.Name, err)
	}
	qemuLog, err := os.Create(qemuLogPath(cfg)) //nolint:gosec // G304: a file in the run directory
	if err != nil {
		return nil, fmt.Errorf("vm %s: %w", cfg.Name, err)
	}
	v := &VM{
		cfg: cfg, sshAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		hostKey: hostSigner.PublicKey(), exited: make(chan struct{}),
	}
	// Not CommandContext: the VM outlives ctx, and Shutdown stops it.
	v.cmd = exec.Command("qemu-system-x86_64", qemuArgs(cfg, overlay, seedPath, port)...) //nolint:gosec // G204: arguments the harness builds
	v.cmd.Stdout, v.cmd.Stderr = qemuLog, qemuLog
	if err := v.cmd.Start(); err != nil {
		_ = qemuLog.Close()
		return nil, fmt.Errorf("vm %s: start QEMU: %w", cfg.Name, err)
	}
	go func() {
		v.waitErr = v.cmd.Wait()
		_ = qemuLog.Close()
		close(v.exited)
	}()
	bootCtx, cancel := context.WithTimeout(ctx, bootTimeout)
	defer cancel()
	if err := v.connect(bootCtx); err != nil {
		v.Shutdown()
		return nil, v.withConsole(err)
	}
	if err := v.waitProvisioned(bootCtx); err != nil {
		v.Shutdown()
		return nil, v.withConsole(err)
	}
	return v, nil
}

// qemuArgs are the QEMU arguments of cfg: KVM, the overlay and the seed as
// virtio disks, the serial console to a file, a user-mode management NIC
// forwarding sshPort on 127.0.0.1 to the VM's port 22, and the "internet"
// NIC on the run's multicast link, which needs no bridge, tap device or root.
func qemuArgs(cfg Config, overlay, seed string, sshPort int) []string {
	return []string{
		"-name", cfg.Name,
		"-machine", "q35,accel=kvm",
		"-cpu", "host",
		"-smp", strconv.Itoa(cfg.CPUs),
		"-m", strconv.Itoa(cfg.MemoryMiB),
		"-display", "none",
		"-monitor", "none",
		"-serial", "file:" + consolePath(cfg),
		"-drive", "file=" + overlay + ",if=virtio,format=qcow2",
		"-drive", "file=" + seed + ",if=virtio,format=raw,readonly=on",
		"-netdev", fmt.Sprintf("user,id=mgmt,hostfwd=tcp:127.0.0.1:%d-:22", sshPort),
		"-device", "virtio-net-pci,netdev=mgmt,mac=" + cfg.MgmtMAC,
		"-netdev", fmt.Sprintf("socket,id=inet,mcast=%s:%d", McastGroup, cfg.McastPort),
		"-device", "virtio-net-pci,netdev=inet,mac=" + cfg.InetMAC,
	}
}

func consolePath(cfg Config) string { return filepath.Join(cfg.Dir, cfg.Name+"-console.log") }

func qemuLogPath(cfg Config) string { return filepath.Join(cfg.Dir, cfg.Name+"-qemu.log") }

// writeSeed writes s into cfg.Dir and builds the NoCloud seed image with
// cloud-localds.
func writeSeed(ctx context.Context, cfg Config, s seed) (string, error) {
	base := filepath.Join(cfg.Dir, cfg.Name)
	userData, metaData, netCfg := base+"-user-data", base+"-meta-data", base+"-network-config"
	for path, data := range map[string][]byte{userData: s.UserData, metaData: s.MetaData, netCfg: s.NetworkConfig} {
		// 0600: user-data holds the VM's host private key.
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return "", fmt.Errorf("vm %s: %w", cfg.Name, err)
		}
	}
	out := base + "-seed.img"
	if err := run(ctx, "cloud-localds", "-N", netCfg, out, userData, metaData); err != nil {
		return "", err
	}
	return out, nil
}

// run runs a local tool and quotes its output when it fails.
func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // G204: the harness's own tool calls
	if err != nil {
		return fmt.Errorf("vm: %s: %w: %s", name, err, bytes.TrimSpace(out))
	}
	return nil
}

// freePort returns a TCP port on 127.0.0.1 that was free a moment ago.
func freePort() (int, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// connect dials SSH until it answers, ctx ends or QEMU exits. Until
// cloud-init has created User and installed the pinned host key, the dial
// fails and is retried.
func (v *VM) connect(ctx context.Context) error {
	conf := &ssh.ClientConfig{
		User:              User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(v.cfg.Key)},
		HostKeyCallback:   ssh.FixedHostKey(v.hostKey),
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
	}
	for {
		c, err := v.dial(ctx, conf)
		if err == nil {
			v.mu.Lock()
			v.client = c
			v.mu.Unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("vm %s: SSH on %s did not answer: %w; last error: %w", v.cfg.Name, v.sshAddr, ctx.Err(), err)
		case <-v.exited:
			b, _ := os.ReadFile(qemuLogPath(v.cfg)) //nolint:gosec // G304: a file in the run directory
			exit := v.waitErr
			if exit == nil {
				exit = errors.New("exit status 0")
			}
			return fmt.Errorf("vm %s: QEMU exited (%w): %s", v.cfg.Name, exit, lastLines(string(b), tailLines))
		case <-time.After(dialInterval):
		}
	}
}

// dial opens one SSH connection. ssh.ClientConfig.Timeout bounds only the
// TCP connect, so the handshake gets a deadline of its own, and ctx ending
// closes the connection mid-handshake.
func (v *VM) dial(ctx context.Context, conf *ssh.ClientConfig) (*ssh.Client, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", v.sshAddr)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(dialTimeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, v.sshAddr, conf)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = c.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// waitProvisioned waits for cloud-init to finish, then checks the marker the
// runcmd wrapper writes when the provisioning script exits 0. cloud-init
// status --wait also exits non-zero on recoverable warnings, so the marker,
// not its exit status, decides.
func (v *VM) waitProvisioned(ctx context.Context) error {
	if _, err := v.Run(ctx, "cloud-init status --wait >/dev/null 2>&1 || true"); err != nil {
		return err
	}
	if _, err := v.Run(ctx, "test -e "+provisionedMark); err == nil {
		return nil
	}
	tail, _ := v.Run(ctx, "sudo tail -n "+strconv.Itoa(tailLines)+" "+provisionLog)
	return fmt.Errorf("vm %s: provisioning failed; %s ends with:\n%s", v.cfg.Name, provisionLog, tail)
}

// Run runs cmd through User's shell on the VM and returns its standard
// output. A non-zero exit status is an error that quotes standard error; the
// standard output is still returned with it.
func (v *VM) Run(ctx context.Context, cmd string) (string, error) {
	return v.runWith(ctx, cmd, nil)
}

func (v *VM) runWith(ctx context.Context, cmd string, stdin io.Reader) (string, error) {
	v.mu.Lock()
	c := v.client
	v.mu.Unlock()
	if c == nil {
		return "", fmt.Errorf("vm %s: not connected", v.cfg.Name)
	}
	var stdout, stderr bytes.Buffer
	opened := make(chan *ssh.Session, 1)
	done := make(chan error, 1)
	// NewSession waits for the server as well, so it runs in the goroutine
	// that ctx can abandon.
	go func() {
		s, err := c.NewSession()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = s.Close() }()
		s.Stdout, s.Stderr, s.Stdin = &stdout, &stderr, stdin
		opened <- s
		done <- s.Run(cmd)
	}()
	select {
	case err := <-done:
		if err != nil {
			return stdout.String(), fmt.Errorf("vm %s: %s: %w: %s", v.cfg.Name, cmd, err, bytes.TrimSpace(stderr.Bytes()))
		}
		return stdout.String(), nil
	case <-ctx.Done():
		// The goroutine may still write the buffers, so they are not read.
		select {
		case s := <-opened:
			_ = s.Close() // ends a command that hangs
		default:
			v.closeClient() // the session never opened: the connection is stuck
		}
		return "", fmt.Errorf("vm %s: %s: %w", v.cfg.Name, cmd, ctx.Err())
	}
}

// CopyFile copies the local file src to dst on the VM as root, with mode.
func (v *VM) CopyFile(ctx context.Context, src, dst string, mode fs.FileMode) error {
	f, err := os.Open(src) //nolint:gosec // G304: a file the test names
	if err != nil {
		return fmt.Errorf("vm %s: %w", v.cfg.Name, err)
	}
	defer func() { _ = f.Close() }()
	q := shellQuote(dst)
	_, err = v.runWith(ctx, fmt.Sprintf("sudo tee %s >/dev/null && sudo chmod %o %s", q, uint32(mode.Perm()), q), f)
	return err
}

// Reboot reboots the VM and waits until SSH answers from the new boot.
func (v *VM) Reboot(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, rebootTimeout)
	defer cancel()
	before, err := v.Run(ctx, "cat /proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	// The connection may drop before the exit status arrives, so the result
	// says nothing; a reboot that never happens fails the boot_id wait below.
	_, _ = v.Run(ctx, "sudo systemctl --no-block reboot")
	v.closeClient()
	for {
		if err := v.connect(ctx); err != nil {
			return err
		}
		if v.bootChanged(ctx, before) {
			return nil
		}
		v.closeClient()
		select {
		case <-ctx.Done():
			return fmt.Errorf("vm %s: no new boot: %w", v.cfg.Name, ctx.Err())
		case <-time.After(dialInterval):
		}
	}
}

// probeTimeout bounds one boot_id probe during Reboot. A variable so a test
// can shorten it.
var probeTimeout = dialTimeout

// bootChanged reports whether the connected VM has a boot_id other than
// before. SSH may answer once more before the old boot stops it, and a
// connection that reached the old boot as it went down can hang without a
// reset (QEMU's user network keeps the host side open), so each probe gets
// probeTimeout rather than the rest of the reboot deadline.
func (v *VM) bootChanged(ctx context.Context, before string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	after, err := v.Run(ctx, "cat /proc/sys/kernel/random/boot_id")
	return err == nil && after != before
}

// KillProcess sends SIGKILL to every process on the VM whose name is exactly
// name. It is an error when no process matched.
func (v *VM) KillProcess(ctx context.Context, name string) error {
	if _, err := v.Run(ctx, killCommand(name)); err != nil {
		return fmt.Errorf("vm %s: kill %s: %w", v.cfg.Name, name, err)
	}
	return nil
}

// killCommand builds the pkill command line. pkill reads its pattern as a
// regular expression, so QuoteMeta makes name literal; -- keeps a name that
// starts with a dash from being read as an option.
func killCommand(name string) string {
	return "sudo pkill -KILL -x -- " + shellQuote(regexp.QuoteMeta(name))
}

// ConsoleLog returns the VM's serial console output so far.
func (v *VM) ConsoleLog() (string, error) {
	b, err := os.ReadFile(consolePath(v.cfg)) //nolint:gosec // G304: a file in the run directory
	return string(b), err
}

// Shutdown stops QEMU and waits for it to exit. It is safe to call more than
// once; the disk is thrown away with the run directory.
func (v *VM) Shutdown() {
	v.closeClient()
	select {
	case <-v.exited:
		return
	default:
	}
	_ = v.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-v.exited:
	case <-time.After(stopTimeout):
		_ = v.cmd.Process.Kill()
		<-v.exited
	}
}

func (v *VM) closeClient() {
	v.mu.Lock()
	c := v.client
	v.client = nil
	v.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// withConsole adds the end of the console log to err.
func (v *VM) withConsole(err error) error {
	log, rerr := v.ConsoleLog()
	if rerr != nil {
		return fmt.Errorf("%w (console log: %w)", err, rerr)
	}
	return fmt.Errorf("%w\nconsole %s ends with:\n%s", err, consolePath(v.cfg), lastLines(log, tailLines))
}

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
