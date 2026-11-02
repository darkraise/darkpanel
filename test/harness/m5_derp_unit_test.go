//go:build harness

package harness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

const m5dUnitSS = `tcp   LISTEN 0      4096               *:443              *:*    users:(("derper",pid=1058,fd=7))
tcp   LISTEN 0      4096               *:80               *:*    users:(("derper",pid=1058,fd=6))
udp   UNCONN 0      0                  *:3478             *:*    users:(("derper",pid=1058,fd=5))
tcp   LISTEN 0      128          0.0.0.0:22               0.0.0.0:*    users:(("sshd",pid=701,fd=3))
tcp   LISTEN 0      128             [::]:22                  [::]:*    users:(("sshd",pid=701,fd=4))
udp   UNCONN 0      0               [::]:3478                [::]:*    users:(("derper",pid=1058,fd=8))
udp   UNCONN 0      0            0.0.0.0:68           0.0.0.0:*
`

func TestM5dUnitParseListeners(t *testing.T) {
	got, err := m5dParseListeners(m5dUnitSS)
	if err != nil {
		t.Fatal(err)
	}
	want := []m5dListener{
		{"tcp", 443, "derper"}, {"tcp", 80, "derper"}, {"udp", 3478, "derper"},
		{"tcp", 22, "sshd"}, {"tcp", 22, "sshd"}, {"udp", 3478, "derper"}, {"udp", 68, ""},
	}
	if !slices.Equal(got, want) {
		t.Errorf("listeners %+v, want %+v", got, want)
	}
	if got, err := m5dParseListeners("\n"); err != nil || len(got) != 0 {
		t.Errorf("an empty table = %+v, %v, want no listener and no error", got, err)
	}
	for _, bad := range []string{"tcp LISTEN 0 4096 *:443", "raw UNCONN 0 0 *:1 *:*", "tcp LISTEN 0 4096 *:http *:*"} {
		if _, err := m5dParseListeners(bad); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("table %q: error %v, want one quoting the line", bad, err)
		}
	}
}

func TestM5dUnitCheckListeners(t *testing.T) {
	all, err := m5dParseListeners(m5dUnitSS)
	if err != nil {
		t.Fatal(err)
	}
	without := func(port int) []m5dListener {
		return slices.DeleteFunc(slices.Clone(all), func(l m5dListener) bool { return l.Port == port })
	}
	tests := []struct {
		name      string
		listeners []m5dListener
		want      string // "" when accepted, else a substring of the error
	}{
		{"every port held by derper", all, ""},
		{"no UDP 3478", without(3478), "nothing listens on 3478/udp"},
		{"no port 80", without(80), "nothing listens on 80/tcp"},
		{"port 80 shared with nginx", append(slices.Clone(all), m5dListener{"tcp", 80, "nginx"}), "80/tcp is held by nginx, want derper"},
		{"port 443 held by an unknown process", append(without(443), m5dListener{"tcp", 443, ""}), "443/tcp is held by an unknown process, want derper"},
		{"a TCP 3478 does not stand in for the UDP one", append(without(3478), m5dListener{"tcp", 3478, "derper"}), "nothing listens on 3478/udp"},
	}
	for _, tc := range tests {
		err := m5dCheckListeners(tc.listeners, "derper")
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM5dUnitConfig(t *testing.T) {
	cfg := m5dConfig(agentIPv4)
	if want := (server.Config{Mode: server.ModeIP, HostName: agentIPv4, VerifyClients: false}); cfg != want {
		t.Fatalf("config %+v, want %+v", cfg, want)
	}
	if err := server.CheckConfig(cfg); err != nil {
		t.Errorf("CheckConfig refuses the harness address %s: %v", agentIPv4, err)
	}
	unit := string(server.UnitFile(cfg))
	for _, want := range []string{"User=darkpanel-derp", "-certmode manual", "-hostname " + agentIPv4} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit file lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "-verify-clients") {
		t.Errorf("unit file passes -verify-clients, which a logged-out tailscaled cannot answer:\n%s", unit)
	}
}

func TestM5dUnitCommands(t *testing.T) {
	prepare := []string{
		"sudo /usr/sbin/useradd --system --no-create-home --shell /usr/sbin/nologin darkpanel-derp",
		"sudo install -d -m 0755 /usr/local/lib/darkpanel",
		"sudo install -d -m 0700 -o darkpanel-derp -g darkpanel-derp /var/lib/darkpanel/derp /var/lib/darkpanel/derp/certs",
	}
	if got := m5dPrepareCommands(); !slices.Equal(got, prepare) {
		t.Errorf("prepare commands %q, want %q", got, prepare)
	}
	start := []string{"sudo systemctl daemon-reload", "sudo systemctl enable --now darkpanel-derper.service"}
	if got := m5dStartCommands(); !slices.Equal(got, start) {
		t.Errorf("start commands %q, want %q", got, start)
	}
	fw := []string{"sudo ufw allow 80/tcp", "sudo ufw allow 443/tcp", "sudo ufw allow 3478/udp"}
	if got := m5dFirewallCommands(); !slices.Equal(got, fw) {
		t.Errorf("firewall commands %q, want %q", got, fw)
	}
	cmd := m5dStunCommand("00112233", "198.18.0.11", 3478)
	if !strings.HasPrefix(cmd, "python3 -c ") || !strings.HasSuffix(cmd, " 00112233 198.18.0.11 3478") {
		t.Errorf("STUN command %q, want python3 -c <script> <request hex> <address> <port>", cmd)
	}
}

func TestM5dUnitStunDiagnostics(t *testing.T) {
	diags := m5dStunDiagnostics()
	byLabel := map[string]m5dDiagnostic{}
	counts := map[string]int{}
	for _, d := range diags {
		if d.Role != "agent" && d.Role != "scanner" {
			t.Errorf("diagnostic %q has role %q, want agent or scanner", d.Label, d.Role)
		}
		if d.Label == "" || d.Command == "" {
			t.Errorf("diagnostic %+v has an empty label or command", d)
		}
		if _, dup := byLabel[d.Role+"/"+d.Label]; dup {
			t.Errorf("label %q is used twice on %s", d.Label, d.Role)
		}
		byLabel[d.Role+"/"+d.Label] = d
		counts[d.Role]++
	}
	if counts["agent"] != 10 || counts["scanner"] != 4 {
		t.Errorf("%d agent and %d scanner diagnostics, want 10 and 4", counts["agent"], counts["scanner"])
	}
	const request = "000100002112a4420102030405060708090a0b0c"
	selfStun := 0
	for _, d := range diags {
		if !strings.HasPrefix(d.Command, "python3 -c ") {
			continue
		}
		selfStun++
		if d.Role != "agent" {
			t.Errorf("STUN diagnostic %q runs on %q, want agent", d.Label, d.Role)
		}
		if !strings.HasSuffix(d.Command, " "+request+" 127.0.0.1 3478") && !strings.HasSuffix(d.Command, " "+request+" 198.18.0.11 3478") {
			t.Errorf("STUN diagnostic %q is %q, want it to end with the request, 127.0.0.1 or 198.18.0.11, and 3478", d.Label, d.Command)
		}
	}
	if selfStun != 2 {
		t.Errorf("%d STUN diagnostics, want 2 (loopback and the agent's own address)", selfStun)
	}
	for _, want := range []struct{ role, command string }{
		{"agent", "sudo ufw status verbose"},
		{"agent", "sudo nft list ruleset"},
		{"agent", "sudo iptables-save"},
		{"agent", "ip -br addr"},
		{"agent", "ip route"},
		{"agent", "sudo ss -H -lunp"},
		{"agent", "sysctl net.ipv4.conf.all.rp_filter net.ipv4.conf.default.rp_filter"},
		{"agent", "sudo journalctl -u darkpanel-derper.service --no-pager -n 40"},
		{"scanner", "ip -br addr"},
		{"scanner", "ip route get 198.18.0.11"},
		{"scanner", "sudo nft list ruleset"},
		{"scanner", "sudo iptables-save"},
	} {
		found := false
		for _, d := range diags {
			found = found || (d.Role == want.role && d.Command == want.command)
		}
		if !found {
			t.Errorf("no %s diagnostic runs %q", want.role, want.command)
		}
	}
	for _, d := range diags {
		if strings.Contains(d.Command, "tcpdump") {
			t.Errorf("diagnostic %q uses tcpdump, which the images do not have", d.Label)
		}
	}
}

func TestM5dUnitCapDiagnostic(t *testing.T) {
	if got := m5dCapDiagnostic("short", false); got != "short" {
		t.Errorf("short output became %q", got)
	}
	long := strings.Repeat("a", 5000) + strings.Repeat("b", 5000)
	head := m5dCapDiagnostic(long, false)
	if len(head) > m5dDiagnosticCap+64 || !strings.HasPrefix(head, "a") || strings.Contains(head, "b") {
		t.Errorf("head-capped output has %d bytes and does not keep the start", len(head))
	}
	tail := m5dCapDiagnostic(long, true)
	if len(tail) > m5dDiagnosticCap+64 || !strings.HasSuffix(tail, "b") || strings.Contains(tail, "a") {
		t.Errorf("tail-capped output has %d bytes and does not keep the end", len(tail))
	}
}

func TestM5dUnitFlags(t *testing.T) {
	on := flag.CommandLine.Lookup("m5d")
	if on == nil || on.DefValue != "false" {
		t.Errorf("flag -m5d = %+v, want a flag that defaults to false", on)
	}
	bin := flag.CommandLine.Lookup("derper")
	want := filepath.Join("..", "..", "bin", "harness", artifact.BinaryName(artifact.DerperVersion, "amd64"))
	if bin == nil || bin.DefValue != want {
		t.Errorf("flag -derper = %+v, want the default %s", bin, want)
	}
}

func TestM5dUnitCheckBinary(t *testing.T) {
	dir := t.TempDir()
	header := func(class, data byte, machine uint16) []byte {
		h := make([]byte, 64)
		copy(h, "\x7fELF")
		h[4], h[5] = class, data
		binary.LittleEndian.PutUint16(h[18:], machine)
		return h
	}
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		{"an amd64 ELF", write("amd64", header(2, 1, 62)), ""},
		{"an arm64 ELF", write("arm64", header(2, 1, 183)), "not an amd64 ELF"},
		{"a 32-bit ELF", write("elf32", header(1, 1, 62)), "not an amd64 ELF"},
		{"a script", write("script", []byte("#!/bin/sh\necho hi\nand some padding to be long enough\n")), "not an amd64 ELF"},
		{"an empty file", write("empty", nil), "not an amd64 ELF"},
		{"a directory", dir, "is not a regular file"},
		{"a missing file", filepath.Join(dir, "absent"), "go run ./release/releasetool derper 0.0.0 bin/harness -arch amd64"},
	}
	for _, tc := range tests {
		err := m5dCheckBinary(tc.path)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.path)):
			t.Errorf("%s: error %v, want one naming %s and containing %q", tc.name, err, tc.path, tc.want)
		}
	}
}

// m5dTestCert returns a self-signed certificate for ip valid until notAfter, as DER and as PEM.
func m5dTestCert(t *testing.T, ip string, notAfter time.Time) (der, pemBytes []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: ip},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, IPAddresses: []net.IP{net.ParseIP(ip)},
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestM5dUnitCheckPin(t *testing.T) {
	now := time.Now()
	served, file := m5dTestCert(t, agentIPv4, now.Add(365*24*time.Hour))
	other, _ := m5dTestCert(t, agentIPv4, now.Add(365*24*time.Hour))
	expiredDER, expiredPEM := m5dTestCert(t, agentIPv4, now.Add(-time.Minute))
	sum := sha256.Sum256(served)
	if got, want := server.Pin(served), "sha256-raw:"+hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("server.Pin = %s, want %s", got, want)
	}
	tests := []struct {
		name         string
		served, file []byte
		want         string
	}{
		{"the served leaf is the file's", served, file, ""},
		{"another certificate is served", other, file, "serves a certificate with pin " + server.Pin(other)},
		{"a file that is no certificate", served, []byte("not pem"), "the certificate on the VM"},
		{"an expired certificate", expiredDER, expiredPEM, "expires"},
	}
	for _, tc := range tests {
		err := m5dCheckPin(tc.served, tc.file, now)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
	if err := m5dCheckPin(other, file, now); err == nil || !strings.Contains(err.Error(), server.Pin(served)) {
		t.Errorf("a pin mismatch error %v, want it to name the file's pin %s", err, server.Pin(served))
	}
}

// m5dTestStunResponse encodes a Binding success response with one XOR-MAPPED-ADDRESS.
func m5dTestStunResponse(tx [12]byte, mapped netip.AddrPort) []byte {
	const cookie = 0x2112a442
	mask := binary.BigEndian.AppendUint32(nil, cookie)
	value := []byte{0, 1}
	value = binary.BigEndian.AppendUint16(value, mapped.Port()^(cookie>>16))
	for i, b := range mapped.Addr().As4() {
		value = append(value, b^mask[i])
	}
	attr := binary.BigEndian.AppendUint16(nil, 0x0020)
	attr = binary.BigEndian.AppendUint16(attr, 8) // the attribute value: family, port and an IPv4 address
	attr = append(attr, value...)
	msg := binary.BigEndian.AppendUint16(nil, 0x0101)
	msg = binary.BigEndian.AppendUint16(msg, 12) // one attribute: 4 bytes of header and 8 of value
	msg = binary.BigEndian.AppendUint32(msg, cookie)
	msg = append(msg, tx[:]...)
	return append(msg, attr...)
}

func TestM5dUnitStunCheck(t *testing.T) {
	tx := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	otherTx := [12]byte{12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	scanner := netip.MustParseAddr(scannerIPv4)
	good := m5dTestStunResponse(tx, netip.AddrPortFrom(scanner, 41000))
	tests := []struct {
		name   string
		answer string
		want   string
	}{
		{"the scanner's own address is reflected", hex.EncodeToString(good) + "\n", ""},
		{"the answer of another request", hex.EncodeToString(m5dTestStunResponse(otherTx, netip.AddrPortFrom(scanner, 41000))), "transaction id"},
		{"another address is reflected", hex.EncodeToString(m5dTestStunResponse(tx, netip.AddrPortFrom(netip.MustParseAddr("192.0.2.1"), 41000))), "reflects 192.0.2.1"},
		{"an answer that is not hex", "zz", "not hex"},
		{"an answer that is no STUN message", "0011", "the STUN answer"},
		{"no answer", "", "the STUN answer"},
	}
	for _, tc := range tests {
		err := m5dStunCheck(tx, tc.answer, scanner)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}
