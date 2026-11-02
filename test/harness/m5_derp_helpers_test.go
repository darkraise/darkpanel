//go:build harness

package harness

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/stun"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m5dStunScript sends one datagram and prints the answer in hex. The scanner runs it because the runner reaches
// a VM only over SSH, which carries TCP and no UDP.
const m5dStunScript = `import binascii, socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)
s.sendto(binascii.unhexlify(sys.argv[1]), (sys.argv[2], int(sys.argv[3])))
print(binascii.hexlify(s.recvfrom(1500)[0]).decode())
`

// m5dUsers finds the first process name in a line of ss -p.
var m5dUsers = regexp.MustCompile(`users:\(\("([^"]*)"`)

// m5dListener is one socket of ss -ltunp.
type m5dListener struct {
	Proto   string
	Port    int
	Process string
}

// m5dConfig is the server the scenario installs on addr. Client verification is off because the harness
// tailscaled is logged out and -verify-clients fails closed without a tailscaled that answers.
func m5dConfig(addr string) server.Config {
	return server.Config{Mode: server.ModeIP, HostName: addr, VerifyClients: false}
}

// m5dPrepareCommands create the service user, the binary's directory and the service directories.
func m5dPrepareCommands() []string {
	return []string{
		"sudo /usr/sbin/useradd --system --no-create-home --shell /usr/sbin/nologin " + server.ServiceUser,
		"sudo install -d -m 0755 " + path.Dir(server.BinaryPath),
		"sudo install -d -m 0700 -o " + server.ServiceUser + " -g " + server.ServiceUser + " " + server.StateDir + " " + server.CertDir,
	}
}

// m5dStartCommands load the unit file and start the unit.
func m5dStartCommands() []string {
	return []string{"sudo systemctl daemon-reload", "sudo systemctl enable --now " + server.UnitName}
}

// m5dFirewallCommands open the three DERP ports.
func m5dFirewallCommands() []string {
	return []string{
		fmt.Sprintf("sudo ufw allow %d/tcp", server.HTTPPort),
		fmt.Sprintf("sudo ufw allow %d/tcp", server.HTTPSPort),
		fmt.Sprintf("sudo ufw allow %d/udp", server.STUNPort),
	}
}

// m5dStunCommand runs m5dStunScript with the request in hex, the address and the port.
func m5dStunCommand(requestHex, address string, port int) string {
	return "python3 -c " + m3dxShell(m5dStunScript, requestHex, address, strconv.Itoa(port))
}

// m5dDiagnosticCap is the most characters of one diagnostic's output that the log keeps.
const m5dDiagnosticCap = 4000

// m5dDiagnostic is one command that m5dStunDiagnostics runs on the VM of the given role, agent or scanner.
type m5dDiagnostic struct {
	Role    string
	Label   string
	Command string
}

// m5dStunDiagnostics are what a failed STUN step logs. The two self-requests tell "derper does not answer"
// apart from "the network or the firewall drops the packet".
func m5dStunDiagnostics() []m5dDiagnostic {
	request := hex.EncodeToString(stun.Request([12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}))
	return []m5dDiagnostic{
		{"agent", "STUN to loopback from the agent", m5dStunCommand(request, "127.0.0.1", server.STUNPort)},
		{"agent", "STUN to the agent address from the agent", m5dStunCommand(request, agentIPv4, server.STUNPort)},
		{"agent", "ufw status", "sudo ufw status verbose"},
		{"agent", "nft ruleset", "sudo nft list ruleset"},
		{"agent", "iptables rules", "sudo iptables-save"},
		{"agent", "addresses", "ip -br addr"},
		{"agent", "routes", "ip route"},
		{"agent", "UDP listeners", "sudo ss -H -lunp"},
		{"agent", "reverse path filter", "sysctl net.ipv4.conf.all.rp_filter net.ipv4.conf.default.rp_filter"},
		{"agent", "derper journal", "sudo journalctl -u " + server.UnitName + " --no-pager -n 40"},
		{"scanner", "addresses", "ip -br addr"},
		{"scanner", "route to the agent", "ip route get " + agentIPv4},
		{"scanner", "nft ruleset", "sudo nft list ruleset"},
		{"scanner", "iptables rules", "sudo iptables-save"},
	}
}

// m5dCapDiagnostic keeps at most m5dDiagnosticCap characters of out: its start, or its end when tail is set.
func m5dCapDiagnostic(out string, tail bool) string {
	if len(out) <= m5dDiagnosticCap {
		return out
	}
	if tail {
		return "[...cut]\n" + out[len(out)-m5dDiagnosticCap:]
	}
	return out[:m5dDiagnosticCap] + "\n[cut...]"
}

// m5dParseListeners reads the output of ss -H -ltunp, keeping the order of its lines.
func m5dParseListeners(out string) ([]m5dListener, error) {
	var ls []m5dListener
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 || (fields[0] != "tcp" && fields[0] != "udp") {
			return nil, fmt.Errorf("harness: ss line %q is not a tcp or udp socket", line)
		}
		local := fields[4]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			return nil, fmt.Errorf("harness: ss line %q has no local port", line)
		}
		port, err := strconv.ParseUint(local[i+1:], 10, 16)
		if err != nil {
			return nil, fmt.Errorf("harness: ss line %q has no local port", line)
		}
		l := m5dListener{Proto: fields[0], Port: int(port)}
		if m := m5dUsers.FindStringSubmatch(line); m != nil {
			l.Process = m[1]
		}
		ls = append(ls, l)
	}
	return ls, nil
}

// m5dCheckListeners requires that 80/tcp, 443/tcp and 3478/udp are each listened on, and only by process.
func m5dCheckListeners(ls []m5dListener, process string) error {
	var errs []error
	for _, want := range []struct {
		port  int
		proto string
	}{{server.HTTPPort, "tcp"}, {server.HTTPSPort, "tcp"}, {server.STUNPort, "udp"}} {
		found := false
		for _, l := range ls {
			if l.Port != want.port || l.Proto != want.proto {
				continue
			}
			found = true
			if l.Process != process {
				name := l.Process
				if name == "" {
					name = "an unknown process"
				}
				errs = append(errs, fmt.Errorf("harness: %d/%s is held by %s, want %s", want.port, want.proto, name, process))
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("harness: nothing listens on %d/%s", want.port, want.proto))
		}
	}
	return errors.Join(errs...)
}

// m5dCheckBinary requires that the -derper file is a linux/amd64 ELF executable.
func m5dCheckBinary(p string) error {
	const fix = "build it with: go run ./release/releasetool derper 0.0.0 bin/harness -arch amd64"
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("harness: -derper %s: %w; %s", p, err, fix)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("harness: -derper %s is not a regular file", p)
	}
	f, err := os.Open(p) //nolint:gosec // G304: the path of the -derper flag
	if err != nil {
		return fmt.Errorf("harness: -derper %s: %w; %s", p, err, fix)
	}
	defer func() { _ = f.Close() }()
	h := make([]byte, 20)
	if _, err := io.ReadFull(f, h); err != nil || string(h[:4]) != "\x7fELF" || h[4] != 2 || h[5] != 1 || binary.LittleEndian.Uint16(h[18:]) != 62 {
		return fmt.Errorf("harness: -derper %s is not an amd64 ELF executable; %s", p, fix)
	}
	return nil
}

// m5dCheckPin requires that the leaf derper served is the certificate in certPEM, and that it is still valid at now.
func m5dCheckPin(served, certPEM []byte, now time.Time) error {
	der, notAfter, err := server.ParseCert(certPEM)
	if err != nil {
		return fmt.Errorf("harness: the certificate on the VM: %w", err)
	}
	if got, want := server.Pin(served), server.Pin(der); got != want {
		return fmt.Errorf("harness: derper serves a certificate with pin %s, the VM's file has %s", got, want)
	}
	if !notAfter.After(now) {
		return fmt.Errorf("harness: the certificate expires %s, before %s", notAfter.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	return nil
}

// m5dStunCheck requires that answerHex is the answer to request tx and reflects the scanner's address.
func m5dStunCheck(tx [12]byte, answerHex string, scanner netip.Addr) error {
	answer, err := hex.DecodeString(strings.TrimSpace(answerHex))
	if err != nil {
		return fmt.Errorf("harness: the STUN answer is not hex: %w", err)
	}
	got, mapped, err := stun.Parse(answer)
	if err != nil {
		return fmt.Errorf("harness: the STUN answer: %w", err)
	}
	if got != tx {
		return fmt.Errorf("harness: the STUN answer has transaction id %x, want %x", got, tx)
	}
	if mapped.Addr().Unmap() != scanner {
		return fmt.Errorf("harness: STUN reflects %s, want the scanner's %s", mapped.Addr().Unmap(), scanner)
	}
	return nil
}

// m5dProbe GETs https://host/derp/probe through the network of from and returns the status and the DER of the
// first certificate the server presented.
func m5dProbe(ctx context.Context, from *vm.VM, host string) (status int, leaf []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u := url.URL{Scheme: "https", Host: host, Path: "/derp/probe"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("harness: %w", err)
	}
	tr := &http.Transport{
		DialContext:       from.DialContext,
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // G402: the pin is checked after the request
	}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("harness: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return 0, nil, fmt.Errorf("harness: GET %s answered without a peer certificate", u.String())
	}
	return resp.StatusCode, resp.TLS.PeerCertificates[0].Raw, nil
}

// m5dStun sends a STUN binding request to target from the network of from and checks the answer.
func m5dStun(ctx context.Context, from *vm.VM, target netip.AddrPort) error {
	var tx [12]byte
	if _, err := rand.Read(tx[:]); err != nil {
		return fmt.Errorf("harness: transaction id: %w", err)
	}
	out, err := from.Run(ctx, m5dStunCommand(hex.EncodeToString(stun.Request(tx)), target.Addr().String(), int(target.Port())))
	if err != nil {
		return fmt.Errorf("harness: STUN request from the scanner: %w", err)
	}
	return m5dStunCheck(tx, out, netip.MustParseAddr(scannerIPv4))
}
