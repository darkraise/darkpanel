//go:build harness

package harness

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// TestM6cUnitSpikeValues: the constants taken from the spike note are filled,
// the line that shows the token file was read cannot be mistaken for the line
// that shows it was not, and the states allowed after an unknown token are
// states of a container that docker keeps alive.
func TestM6cUnitSpikeValues(t *testing.T) {
	read := m6cLogRead(m6cTunnel)
	if read == "" || m6cLogUnreadable == "" {
		t.Fatal("harness: m6cLogReadTemplate or m6cLogUnreadable is empty; fill them from the spike note's ## Log lines")
	}
	if strings.Contains(read, "{tunnel}") {
		t.Errorf("harness: the read line %q keeps its placeholder", read)
	}
	if strings.Contains(m6cLogUnreadable, read) || strings.Contains(read, m6cLogUnreadable) {
		t.Errorf("harness: the read line %q and the unreadable line %q contain one another", read, m6cLogUnreadable)
	}
	if len(m6cRefusalStates) == 0 {
		t.Fatal("harness: m6cRefusalStates is empty; fill it from the spike note's ## Unauthorized token")
	}
	for _, s := range m6cRefusalStates {
		if !slices.Contains(m6cLiveStates, s) {
			t.Errorf("harness: refusal state %q is not one of %v", s, m6cLiveStates)
		}
	}
}

func TestM6cUnitExpand(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Tunnel ID: {tunnel}", "Tunnel ID: abc"},
		{"{tunnel} and {tunnel}", "abc and abc"},
		{"no placeholder", "no placeholder"},
	} {
		if got := m6cExpand(tc.in, "abc"); got != tc.want {
			t.Errorf("m6cExpand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestM6cUnitConstants pins the values the spec names and the cloudflare
// package keeps to itself.
func TestM6cUnitConstants(t *testing.T) {
	if m6cTokenDir != "/var/lib/darkpanel/agent/cloudflare-tokens" || m6cTokenMount != "/run/darkpanel/token" || m6cImageUID != 65532 { //nolint:gosec // G101: paths, not credentials
		t.Errorf("token directory %q, mount %q, uid %d, want the values of spec 7.2", m6cTokenDir, m6cTokenMount, m6cImageUID)
	}
	if m6cConnector != "darkpanel-cloudflared-"+m6cTunnel {
		t.Errorf("connector name %q, want darkpanel-cloudflared-<tunnel>", m6cConnector)
	}
	want := []string{"tunnel", "--no-autoupdate", "run", "--token-file", "/run/darkpanel/token"}
	if got := m6cArgs(m6cTokenMount); !slices.Equal(got, want) {
		t.Errorf("m6cArgs = %q, want %q", got, want)
	}
}

func TestM6cUnitToken(t *testing.T) {
	tok := m6cToken(m6cTunnel)
	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("harness: the token is not standard base64: %v", err)
	}
	var parts map[string]string
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("harness: the token does not decode to a JSON object: %v", err)
	}
	if parts["a"] != m6cAccountTag || parts["t"] != m6cTunnel || parts["s"] != m6cSecret || len(parts) != 3 {
		t.Errorf("the token has %d members with a=%q t=%q and the secret matching: %t, want the account tag, the tunnel and the secret in a, t and s",
			len(parts), parts["a"], parts["t"], parts["s"] == m6cSecret)
	}
	if secret, err := base64.StdEncoding.DecodeString(m6cSecret); err != nil || len(secret) != 32 {
		t.Errorf("the secret decodes to %d bytes (error %v), want 32", len(secret), err)
	}
	if m6cToken("another-tunnel") == tok {
		t.Error("the token does not depend on the tunnel")
	}
}

func TestM6cUnitLeaks(t *testing.T) {
	tok := m6cToken(m6cTunnel)
	decoded, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"clean", "INF Starting tunnel tunnelID=" + m6cTunnel, nil},
		{"the token", "cloudflared --token " + tok, []string{"token"}},
		{"the secret alone", "secret=" + m6cSecret, []string{"secret"}},
		{"the decoded token", string(decoded), []string{"decoded token", "secret"}},
	} {
		if got := m6cLeaks(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("%s: m6cLeaks = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// m6cUnitInspect is what docker inspect prints for a connector that is as it
// should be.
func m6cUnitInspect(t *testing.T) string {
	t.Helper()
	doc := []map[string]any{{
		"Name": "/" + m6cConnector,
		"Config": map[string]any{
			"Image":  cloudflare.Image,
			"Cmd":    m6cArgs(m6cTokenMount),
			"Env":    []string{"PATH=/usr/local/sbin:/usr/local/bin", "TUNNEL_ORIGIN_CERT=/etc/cloudflared/cert.pem"},
			"Labels": map[string]string{"darkpanel.module": "cloudflare"},
		},
		"HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}},
		"Mounts": []map[string]any{{
			"Type": "bind", "Source": m6cTokenDir + "/" + m6cTunnel + ".token", "Destination": m6cTokenMount, "Mode": "ro", "RW": false,
		}},
		"State": map[string]any{"Status": "running"},
	}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestM6cUnitInspectCheck(t *testing.T) {
	good := m6cUnitInspect(t)
	if err := m6cInspectCheck([]byte(good), m6cLiveStates); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	restarting := strings.Replace(good, `"Status":"running"`, `"Status":"restarting"`, 1)
	if err := m6cInspectCheck([]byte(restarting), m6cLiveStates); err != nil {
		t.Errorf("a restarting connector: %v, want it accepted", err)
	}
	if err := m6cInspectCheck([]byte(restarting), []string{"running"}); err == nil {
		t.Error("a restarting connector was accepted where only running is allowed")
	}
	tok := m6cToken(m6cTunnel)
	for _, tc := range []struct{ name, from, to, want string }{
		{"wrong name", `"/` + m6cConnector + `"`, `"/other"`, "named"},
		{"wrong image", cloudflare.Image, "cloudflare/cloudflared:latest", "image"},
		{"wrong label", `"darkpanel.module":"cloudflare"`, `"darkpanel.module":"npm"`, "label"},
		{"restart policy", `"unless-stopped"`, `"always"`, "restart policy"},
		{"token on the command line", `"--token-file","` + m6cTokenMount + `"`, `"--token","` + tok + `"`, "command"},
		{"token in the environment", "/etc/cloudflared/cert.pem", tok, "environment holds"},
		{"token variable", "TUNNEL_ORIGIN_CERT=/etc/cloudflared/cert.pem", "TUNNEL_TOKEN=x", "environment sets TUNNEL_TOKEN"},
		{"second mount", `"Mounts":[`, `"Mounts":[{"Type":"volume","Source":"v","Destination":"/x","RW":true},`, "mounts"},
		{"wrong source", "cloudflare-tokens/" + m6cTunnel, "cloudflare-tokens/other", "mount is"},
		{"writable mount", `"RW":false`, `"RW":true`, "writable"},
		{"exited", `"Status":"running"`, `"Status":"exited"`, "want one of"},
	} {
		if n := strings.Count(good, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		err := m6cInspectCheck([]byte(strings.Replace(good, tc.from, tc.to, 1)), m6cLiveStates)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
	for _, tc := range []struct{ name, in, want string }{
		{"no container", `[]`, "want 1"},
		{"two containers", `[{},{}]`, "want 1"},
		{"not JSON", `{`, "docker inspect"},
	} {
		if err := m6cInspectCheck([]byte(tc.in), m6cLiveStates); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM6cUnitParseStat(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want m6cStat
		bad  bool
	}{
		{out: "65532 65532 400\n", want: m6cStat{UID: 65532, GID: 65532, Mode: "400"}},
		{out: "0 0 700", want: m6cStat{UID: 0, GID: 0, Mode: "700"}},
		{out: "", bad: true},
		{out: "65532 65532", bad: true},
		{out: "a b c", bad: true},
		{out: "1 2 3 4", bad: true},
	} {
		got, err := m6cParseStat(tc.out)
		switch {
		case tc.bad && err == nil:
			t.Errorf("m6cParseStat(%q) was accepted", tc.out)
		case !tc.bad && (err != nil || got != tc.want):
			t.Errorf("m6cParseStat(%q) = %+v, %v, want %+v", tc.out, got, err, tc.want)
		}
	}
}

func TestM6cUnitSealed(t *testing.T) {
	plain := hex.EncodeToString([]byte(m6cToken(m6cTunnel)))
	for _, tc := range []struct{ name, out, want string }{
		{"sealed", "A1B2C3D4E5F60718\n", ""},
		{"empty", "\n", "no sealed token"},
		{"not hexadecimal", "sealed!", "not hexadecimal"},
		{"plain, lower case", plain + "\n", "in the clear"},
		{"plain, upper case", strings.ToUpper(plain), "in the clear"},
		{"plain, inside", "AA" + strings.ToUpper(plain) + "BB", "in the clear"},
	} {
		err := m6cSealedOK(tc.out, m6cToken(m6cTunnel))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

// m6cFlag selects TestM6Cloudflare.
var m6cFlag = flag.Bool("m6c", false, "run TestM6Cloudflare; needs -os and -m2, and is a go test run of its own")

// m6cDeadline bounds TestM6Cloudflare. The workflow step that runs it must keep
// its -timeout at least m6cDeadline plus 10 minutes, for the cleanups that stop
// the VMs.
const m6cDeadline = 30 * time.Minute

const (
	// m6cTunnel is a lower-case UUID that the Cloudflare edge does not know.
	m6cTunnel = "0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"
	// m6cAccountTag is the account member of the token.
	m6cAccountTag = "9d4e6a2b1c8f4e3a9b7d5c3e1f2a4b6c"
	// m6cTokenDir is the agent's directory of token files.
	m6cTokenDir = "/var/lib/darkpanel/agent/cloudflare-tokens" //nolint:gosec // G101: a directory path, not a credential
	// m6cTokenMount is where the container sees its token file.
	m6cTokenMount = "/run/darkpanel/token" //nolint:gosec // G101: a mount path, not a credential
	// m6cAbsentToken is a path nothing is mounted at.
	m6cAbsentToken = "/run/darkpanel/absent" //nolint:gosec // G101: a path, not a credential
	// m6cImageUID is the user of the cloudflared image, which owns the token file.
	m6cImageUID = 65532
	// m6cConnector is the connector's container name.
	m6cConnector = "darkpanel-cloudflared-" + m6cTunnel

	// m6cAppName is the target container on the user-defined network.
	m6cAppName = "m6c-app"
	// m6cBridgeName is the target container on the default bridge only.
	m6cBridgeName = "m6c-bridge"
	// m6cControlName is the container of the negative control for the log constants.
	m6cControlName = "m6c-control"
	// m6cNet is the user-defined network the claims attach the connector to.
	m6cNet = "m6c-net"

	// m6cClaimantA, m6cClaimantB and m6cClaimantC are the claimants of the attachment steps.
	m6cClaimantA = "expose:m6c-a.example.test"
	m6cClaimantB = "expose:m6c-b.example.test"
	m6cClaimantC = "expose:m6c-c.example.test"

	// m6cLogReadTemplate is what cloudflared logs after it has read and decoded
	// the token file; {tunnel} stands for the tunnel id.
	m6cLogReadTemplate = "Starting tunnel tunnelID={tunnel}"
	// m6cLogUnreadable is what cloudflared logs when the token file cannot be
	// read, cut before the path.
	m6cLogUnreadable = "Failed to read token file: open"

	// m6cDiagnosticTimeout bounds each command of logDiagnostics.
	m6cDiagnosticTimeout = 15 * time.Second
)

// m6cSecret is the tunnel secret of the token: 32 bytes, in standard base64.
var m6cSecret = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x6d}, 32))

// m6cLiveStates are the container states connector.run's own verification accepts.
var m6cLiveStates = []string{"running", "restarting"}

// m6cRefusalStates are the states of the connector after the edge refuses its
// token. The spike note leaves open whether the container keeps running or
// restarts, and the restart policy relaunches it, so both live states pass.
var m6cRefusalStates = []string{"running", "restarting"}

// m6cToken is the tunnel token for tunnel: the standard base64 of the JSON
// object cloudflared decodes.
func m6cToken(tunnel string) string {
	doc := fmt.Sprintf(`{"a":%q,"t":%q,"s":%q}`, m6cAccountTag, tunnel, m6cSecret)
	return base64.StdEncoding.EncodeToString([]byte(doc))
}

// m6cExpand replaces every {tunnel} in template by tunnel.
func m6cExpand(template, tunnel string) string {
	return strings.ReplaceAll(template, "{tunnel}", tunnel)
}

// m6cLogRead is the log line that shows cloudflared read the token file of tunnel.
func m6cLogRead(tunnel string) string {
	return m6cExpand(m6cLogReadTemplate, tunnel)
}

// m6cArgs is the command of the connector container.
func m6cArgs(tokenFile string) []string {
	return []string{"tunnel", "--no-autoupdate", "run", "--token-file", tokenFile}
}

// m6cLeaks names the secrets text shows, never their values: the token, the
// JSON it decodes to and the tunnel secret.
func m6cLeaks(text string) []string {
	var found []string
	token := m6cToken(m6cTunnel)
	if strings.Contains(text, token) {
		found = append(found, "token")
	}
	if decoded, err := base64.StdEncoding.DecodeString(token); err == nil && strings.Contains(text, string(decoded)) {
		found = append(found, "decoded token")
	}
	if strings.Contains(text, m6cSecret) {
		found = append(found, "secret")
	}
	return found
}

// m6cStat is the owner and mode of a file as stat -c '%u %g %a' prints them.
type m6cStat struct {
	UID, GID int
	Mode     string
}

// m6cParseStat parses the output of stat -c '%u %g %a'.
func m6cParseStat(out string) (m6cStat, error) {
	f := strings.Fields(out)
	if len(f) != 3 {
		return m6cStat{}, fmt.Errorf("harness: stat printed %d fields, want 3", len(f))
	}
	uid, err := strconv.Atoi(f[0])
	if err != nil {
		return m6cStat{}, fmt.Errorf("harness: stat printed the owner %q: %w", f[0], err)
	}
	gid, err := strconv.Atoi(f[1])
	if err != nil {
		return m6cStat{}, fmt.Errorf("harness: stat printed the group %q: %w", f[1], err)
	}
	return m6cStat{UID: uid, GID: gid, Mode: f[2]}, nil
}

// m6cSealedOK checks the output of SELECT hex(token_ct): a sealed value that
// does not hold the hexadecimal form of token.
func m6cSealedOK(hexOut, token string) error {
	h := strings.TrimSpace(hexOut)
	if h == "" {
		return errors.New("harness: no sealed token")
	}
	if _, err := hex.DecodeString(h); err != nil {
		return errors.New("harness: the sealed token is not hexadecimal")
	}
	if strings.Contains(strings.ToLower(h), hex.EncodeToString([]byte(token))) {
		return errors.New("harness: the stored token is in the clear")
	}
	return nil
}

// m6cInspectCheck checks the one container in the JSON array that docker
// inspect prints against the connector connector.run must create for m6cTunnel.
// No error prints an environment value or the actual command.
func m6cInspectCheck(inspect []byte, allowed []string) error {
	var cs []struct {
		Name   string `json:"Name"`
		Config struct {
			Image  string            `json:"Image"`
			Cmd    []string          `json:"Cmd"`
			Env    []string          `json:"Env"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		HostConfig struct {
			RestartPolicy struct {
				Name string `json:"Name"`
			} `json:"RestartPolicy"`
		} `json:"HostConfig"`
		Mounts []struct {
			Type        string `json:"Type"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
	}
	if err := json.Unmarshal(inspect, &cs); err != nil {
		return fmt.Errorf("harness: docker inspect: %w", err)
	}
	if len(cs) != 1 {
		return fmt.Errorf("harness: docker inspect printed %d containers, want 1", len(cs))
	}
	c := cs[0]
	if c.Name != "/"+m6cConnector {
		return fmt.Errorf("harness: the container is named %q, want %q", c.Name, "/"+m6cConnector)
	}
	if c.Config.Image != cloudflare.Image {
		return fmt.Errorf("harness: the container runs image %q, want %q", c.Config.Image, cloudflare.Image)
	}
	if got := c.Config.Labels["darkpanel.module"]; got != "cloudflare" {
		return fmt.Errorf("harness: the container label darkpanel.module is %q, want cloudflare", got)
	}
	if got := c.HostConfig.RestartPolicy.Name; got != "unless-stopped" {
		return fmt.Errorf("harness: the container's restart policy is %q, want unless-stopped", got)
	}
	if want := m6cArgs(m6cTokenMount); !slices.Equal(c.Config.Cmd, want) {
		msg := fmt.Sprintf("harness: the container's command differs from %q", want)
		if leaks := m6cLeaks(strings.Join(c.Config.Cmd, " ")); len(leaks) > 0 {
			msg += ", and it holds the " + strings.Join(leaks, " and the ")
		}
		return errors.New(msg)
	}
	for _, e := range c.Config.Env {
		if strings.HasPrefix(e, "TUNNEL_TOKEN") {
			return errors.New("harness: the container's environment sets TUNNEL_TOKEN")
		}
	}
	if leaks := m6cLeaks(strings.Join(c.Config.Env, "\n")); len(leaks) > 0 {
		return fmt.Errorf("harness: the container's environment holds the %s", strings.Join(leaks, " and the "))
	}
	if len(c.Mounts) != 1 {
		return fmt.Errorf("harness: the container has %d mounts, want the one token mount", len(c.Mounts))
	}
	m, source := c.Mounts[0], m6cTokenDir+"/"+m6cTunnel+".token"
	if m.Type != "bind" || m.Source != source || m.Destination != m6cTokenMount {
		return fmt.Errorf("harness: the container's mount is %s %s:%s, want a bind of %s at %s", m.Type, m.Source, m.Destination, source, m6cTokenMount)
	}
	if m.RW {
		return errors.New("harness: the token mount is writable")
	}
	if !slices.Contains(allowed, c.State.Status) {
		return fmt.Errorf("harness: the container is %s, want one of %v", c.State.Status, allowed)
	}
	return nil
}

// m6cStaysUp reads the state of container name five times, two seconds apart,
// and reports each state that is not in allowed.
func m6cStaysUp(ctx context.Context, t *testing.T, v *vm.VM, name string, allowed []string) {
	t.Helper()
	for i := range 5 {
		if i > 0 {
			select {
			case <-ctx.Done():
				t.Errorf("container %s: %v", name, ctx.Err())
				return
			case <-time.After(2 * time.Second):
			}
		}
		out, err := v.Run(ctx, "sudo docker inspect -f "+m3Quote("{{.State.Status}}")+" "+m3dxShell(name))
		if err != nil {
			t.Errorf("container %s: %v", name, err)
			continue
		}
		if state := strings.TrimSpace(out); !slices.Contains(allowed, state) {
			t.Errorf("container %s is %s, want one of %v", name, state, allowed)
		}
	}
}

// m6cDiagnostics are what a failed step logs from the agent: the containers,
// the networks, the token directory's listing and the end of each container's
// log. None reads the token file or inspects a container.
func m6cDiagnostics() []m6nDiagnostic {
	ds := []m6nDiagnostic{
		{"containers", `sudo docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'`},
		{"networks", "sudo docker network ls"},
		{"token directory", "sudo ls -la " + m6cTokenDir},
	}
	for _, name := range []string{m6cConnector, m6cControlName, m6cAppName} {
		ds = append(ds, m6nDiagnostic{name + " log", "sudo docker logs --tail 40 " + name + " 2>&1"})
	}
	return ds
}

// m6cRun is what the steps of TestM6Cloudflare share.
type m6cRun struct {
	ctx         context.Context
	f           *fleet
	token       string
	changeID    string // the change of connector.run, set by run
	containerID string // the connector's container id, set by run
}

// TestM6Cloudflare is the host half of M6's Cloudflare items (spec 7.2, 7.3 and
// 6, Verification): connector.run, applied on the agent with a well-formed
// token that the edge does not know, starts the pinned cloudflared in a
// container whose token file the real binary reads; the token is on the host
// only in the 0400 file and the sealed row, and in no inspect, log or process
// listing; connector.list shows the connector; the attachment claims keep the
// connector on a network until the last claim is released, a default-bridge-only
// target is refused in its preview, and connector.stop and the undos clean up.
// It never reaches the Cloudflare API: the controller's base URL is a
// constant, and nothing here calls a tunnel or hostname action. The workflow
// selects it with -run, -os, -m2 and -m6c.
func TestM6Cloudflare(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m6cFlag {
		t.Skip("no -os, -m2 or -m6c given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m6cDeadline)
	t.Cleanup(cancel)
	r := &m6cRun{ctx: ctx, f: m5dLab(ctx, t), token: m6cToken(m6cTunnel)}
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"prepare", true, r.prepare},
		{"control", false, r.control},
		{"run", true, r.run},
		{"host-state", false, r.hostState},
		{"reads-token", false, r.readsToken},
		{"no-leak", false, r.noLeak},
		{"list", false, r.list},
		{"rerun-refused", false, r.rerunRefused},
		{"unsupported-target", false, r.unsupportedTarget},
		{"claims", true, r.claims},
		{"attach-undo", false, r.attachUndo},
		{"stop", true, r.stop},
		{"run-undo", false, r.runUndo},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			r.logDiagnostics(t)
			if s.gate {
				return
			}
		}
	}
}

// target returns the pinned reference of the image of the target containers.
func (r *m6cRun) target(t *testing.T) string {
	t.Helper()
	ref, err := m3dxImage("pebble-challtestsrv")
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// m6cTargetArgs are the arguments that make the target image serve only its
// management port.
func m6cTargetArgs() []string {
	return []string{"-dnsserver", "", "-http01", "", "-https01", "", "-tlsalpn01", ""}
}

// prepare pulls the cloudflared image and the target image on the agent.
func (r *m6cRun) prepare(t *testing.T) {
	for _, ref := range []string{cloudflare.Image, r.target(t)} {
		m6nPull(r.ctx, t, r.f.agent, ref)
	}
}

// control is the negative control for the log constants: cloudflared, given a
// token file that does not exist, logs m6cLogUnreadable and not the line that
// shows the file was read, so a step that waits for that line cannot be
// satisfied by the other.
func (r *m6cRun) control(t *testing.T) {
	v := r.f.agent
	m3Run(r.ctx, t, v, "sudo docker rm -f "+m6cControlName+" >/dev/null 2>&1 || true")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = v.Run(ctx, "sudo docker rm -f "+m6cControlName)
	})
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{Name: m6cControlName, Image: cloudflare.Image, Args: m6cArgs(m6cAbsentToken)}))
	var logs string
	err := eventually(r.ctx, 2*time.Minute, func(ctx context.Context) error {
		out, err := v.Run(ctx, "sudo docker logs "+m6cControlName+" 2>&1")
		if err != nil {
			return err
		}
		if !strings.Contains(out, m6cLogUnreadable) {
			return fmt.Errorf("harness: the log of %s does not hold %q yet", m6cControlName, m6cLogUnreadable)
		}
		logs = out
		return nil
	})
	if err != nil {
		t.Fatalf("the control container: %v", err)
	}
	if strings.Contains(logs, m6cLogRead(m6cTunnel)) {
		t.Errorf("the log of %s holds %q, which only a readable token file produces", m6cControlName, m6cLogRead(m6cTunnel))
	}
}

// run applies connector.run on the agent, checks its output and keeps the ids.
func (r *m6cRun) run(t *testing.T) {
	ch := m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "cloudflare.connector.run", cloudflare.ConnectorRunInput{Tunnel: m6cTunnel, Token: r.token})
	var out cloudflare.Connector
	if err := json.Unmarshal(ch.Output, &out); err != nil {
		t.Fatalf("output of cloudflare.connector.run is not a connector: %v", err)
	}
	if out.Tunnel != m6cTunnel || out.Container != m6cConnector {
		t.Errorf("connector.run answered tunnel %q and container %q, want %q and %q", out.Tunnel, out.Container, m6cTunnel, m6cConnector)
	}
	if len(out.ContainerID) != 64 || strings.Trim(out.ContainerID, "0123456789abcdef") != "" {
		t.Errorf("connector.run answered container id %q, want 64 lower-case hexadecimal digits", out.ContainerID)
	}
	if !slices.Contains(m6cLiveStates, out.State) {
		t.Errorf("connector.run answered state %q, want one of %v", out.State, m6cLiveStates)
	}
	r.changeID, r.containerID = ch.ID, out.ContainerID
	if leaks := m6cLeaks(string(ch.Output)); len(leaks) > 0 {
		t.Errorf("the output of connector.run shows the %s", strings.Join(leaks, " and the "))
	}
	effects, err := m4Effects(r.ctx, r.f.api, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) == 0 {
		t.Error("connector.run journaled no effect")
	}
	b, err := json.Marshal(effects)
	if err != nil {
		t.Fatal(err)
	}
	if leaks := m6cLeaks(string(b)); len(leaks) > 0 {
		t.Errorf("the effects of connector.run show the %s", strings.Join(leaks, " and the "))
	}
}

// m6cFileStat returns the owner and mode of path on v.
func m6cFileStat(ctx context.Context, t *testing.T, v *vm.VM, path string) m6cStat {
	t.Helper()
	out := m3Run(ctx, t, v, "sudo stat -c "+m3Quote("%u %g %a")+" "+m3dxShell(path))
	st, err := m6cParseStat(out)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st
}

// hostState checks what connector.run left on the host: the container as
// specified, the token file with its owner, mode and content, the directory
// that holds only that file, and the sealed row of the agent database.
func (r *m6cRun) hostState(t *testing.T) {
	v := r.f.agent
	inspect := m3dDocker(r.ctx, t, v, "inspect", m6cConnector)
	if err := m6cInspectCheck([]byte(inspect), m6cLiveStates); err != nil {
		t.Error(err)
	}

	file := m6cTokenDir + "/" + m6cTunnel + ".token"
	if got, want := m6cFileStat(r.ctx, t, v, file), (m6cStat{UID: m6cImageUID, GID: m6cImageUID, Mode: "400"}); got != want {
		t.Errorf("the token file is %+v, want %+v", got, want)
	}
	content, err := v.Run(r.ctx, "sudo cat "+m3dxShell(file))
	if err != nil {
		t.Errorf("reading the token file: %v", err)
	} else if strings.TrimSpace(content) != r.token {
		t.Error("the content of the token file differs from the token")
	}
	if got, want := m6cFileStat(r.ctx, t, v, m6cTokenDir), (m6cStat{UID: 0, GID: 0, Mode: "700"}); got != want {
		t.Errorf("the token directory is %+v, want %+v", got, want)
	}
	if names := strings.Fields(m3Run(r.ctx, t, v, "sudo ls -A "+m6cTokenDir)); !slices.Equal(names, []string{m6cTunnel + ".token"}) {
		t.Errorf("the token directory holds %v, want only %s.token", names, m6cTunnel)
	}

	query := "SELECT hex(token_ct) FROM cloudflare_connectors WHERE tunnel_id = '" + m6cTunnel + "' AND container_id = '" + r.containerID + "'"
	out, err := v.Run(r.ctx, "sudo sqlite3 -cmd '.timeout 10000' "+paths.Production().AgentDB()+" \""+query+"\"")
	if err != nil {
		t.Errorf("reading the sealed token: %v", err)
		return
	}
	if err := m6cSealedOK(out, r.token); err != nil {
		t.Error(err)
	}
}

// readsToken: the real cloudflared logs that it read and decoded the token
// file, and the connector stays in a state docker keeps alive after the edge
// refuses the unknown tunnel.
func (r *m6cRun) readsToken(t *testing.T) {
	v := r.f.agent
	err := eventually(r.ctx, 3*time.Minute, func(ctx context.Context) error {
		out, err := v.Run(ctx, "sudo docker logs "+m6cConnector+" 2>&1")
		if err != nil {
			return err
		}
		if strings.Contains(out, m6cLogUnreadable) {
			return fmt.Errorf("harness: the log of %s holds %q", m6cConnector, m6cLogUnreadable)
		}
		if !strings.Contains(out, m6cLogRead(m6cTunnel)) {
			return fmt.Errorf("harness: the log of %s does not hold %q yet", m6cConnector, m6cLogRead(m6cTunnel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cloudflared reading its token file: %v", err)
	}
	m6cStaysUp(r.ctx, t, v, m6cConnector, m6cRefusalStates)
}

// noLeak: neither docker, nor the process list, nor the module's own reads of
// the container show the token, its decoded JSON or its secret.
func (r *m6cRun) noLeak(t *testing.T) {
	v := r.f.agent
	sources := map[string]string{}
	for name, cmd := range map[string]string{
		"docker inspect": "sudo docker inspect " + m6cConnector,
		"docker logs":    "sudo docker logs " + m6cConnector + " 2>&1",
		"ps":             "sudo ps -eo args",
	} {
		out, err := v.Run(r.ctx, cmd)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		sources[name] = out
	}
	for name, c := range map[string]struct {
		action string
		input  map[string]any
	}{
		"docker.container.inspect": {"docker.container.inspect/v1", map[string]any{"container": m6cConnector}},
		"docker.container.logs":    {"docker.container.logs/v1", map[string]any{"container": m6cConnector, "tail": 500}},
	} {
		var raw json.RawMessage
		if err := r.f.api.Read(r.ctx, r.f.agentID, c.action, c.input, &raw); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		sources[name] = string(raw)
	}
	for name, text := range sources {
		if leaks := m6cLeaks(text); len(leaks) > 0 {
			t.Errorf("%s shows the %s", name, strings.Join(leaks, " and the "))
		}
	}
}

// listConnectors reads cloudflare.connector.list on the agent.
func (r *m6cRun) listConnectors(t *testing.T) []cloudflare.Connector {
	t.Helper()
	var l cloudflare.ConnectorList
	if err := r.f.api.Read(r.ctx, r.f.agentID, "cloudflare.connector.list/v1", cloudflare.ConnectorListInput{}, &l); err != nil {
		t.Fatal(err)
	}
	return l.Connectors
}

// list: connector.list shows the one connector with its container and state.
func (r *m6cRun) list(t *testing.T) {
	cs := r.listConnectors(t)
	if len(cs) != 1 {
		t.Fatalf("connector.list answered %+v, want the one connector", cs)
	}
	c := cs[0]
	if c.Tunnel != m6cTunnel || c.Container != m6cConnector || c.ContainerID != r.containerID || !slices.Contains(m6cLiveStates, c.State) {
		t.Errorf("connector.list answered %+v, want tunnel %s, container %s (%s) in one of %v", c, m6cTunnel, m6cConnector, r.containerID, m6cLiveStates)
	}
}

// rerunRefused: a second connector.run for the same tunnel is refused in its plan.
func (r *m6cRun) rerunRefused(t *testing.T) {
	_, err := m4Plan(r.ctx, r.f.api, r.f.agentID, "cloudflare.connector.run", cloudflare.ConnectorRunInput{Tunnel: m6cTunnel, Token: r.token})
	m6nRefused(t, "a second connector.run for the tunnel", err, errcode.InvalidInput, "")
}

// unsupportedTarget: a container on the default bridge only cannot be reached
// by name, and the attachment is refused in its preview.
func (r *m6cRun) unsupportedTarget(t *testing.T) {
	v := r.f.agent
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{Name: m6cBridgeName, Image: r.target(t), Args: m6cTargetArgs()}))
	m3dWaitRunning(r.ctx, t, v, m6cBridgeName)
	_, err := m4Plan(r.ctx, r.f.api, r.f.agentID, "cloudflare.network.attach", cloudflare.NetworkAttachInput{
		Tunnel: m6cTunnel, Network: "bridge", Claimant: m6cClaimantA, Target: m6cBridgeName,
	})
	m6nRefused(t, "the attachment to a default-bridge-only container", err, errcode.CloudflareUnsupportedTarget, m6nUnsupportedReason)
}

// attach applies network.attach for claimant on m6cNet and returns the change
// and its output.
func (r *m6cRun) attach(t *testing.T, claimant string) (Change, cloudflare.NetworkAttachment) {
	t.Helper()
	ch := m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "cloudflare.network.attach", cloudflare.NetworkAttachInput{
		Tunnel: m6cTunnel, Network: m6cNet, Claimant: claimant, Target: m6cAppName,
	})
	var out cloudflare.NetworkAttachment
	if err := json.Unmarshal(ch.Output, &out); err != nil {
		t.Fatalf("output of cloudflare.network.attach: %v", err)
	}
	return ch, out
}

// detach applies network.detach for claimant on network and returns its output.
func (r *m6cRun) detach(t *testing.T, network, claimant string) cloudflare.NetworkAttachment {
	t.Helper()
	ch := m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "cloudflare.network.detach", cloudflare.NetworkDetachInput{
		Tunnel: m6cTunnel, Network: network, Claimant: claimant,
	})
	var out cloudflare.NetworkAttachment
	if err := json.Unmarshal(ch.Output, &out); err != nil {
		t.Fatalf("output of cloudflare.network.detach: %v", err)
	}
	return out
}

// onNet reports whether the connector is attached to m6cNet.
func (r *m6cRun) onNet(t *testing.T) bool {
	t.Helper()
	return slices.Contains(m6nNetworkMembers(r.ctx, t, r.f.agent, m6cNet), m6cConnector)
}

// planEmpty requires that the plan of action with input is accepted and holds
// no diff line: the action converges.
func (r *m6cRun) planEmpty(t *testing.T, what, action string, input any) {
	t.Helper()
	p, err := m4Plan(r.ctx, r.f.api, r.f.agentID, action, input)
	if err != nil {
		t.Errorf("%s: %v, want it planned", what, err)
		return
	}
	if len(p.Diff) != 0 {
		t.Errorf("%s: the diff is %+v, want it empty", what, p.Diff)
	}
}

// claims: the connector joins a user-defined network for the first claimant,
// stays while any claim is left, blocks connector.stop while one is held, and
// leaves with the last one; releasing a claim that is gone converges.
func (r *m6cRun) claims(t *testing.T) {
	v := r.f.agent
	m3dDocker(r.ctx, t, v, "network", "create", m6cNet)
	m3Run(r.ctx, t, v, m6nRunCmd(m6nRunSpec{Name: m6cAppName, Image: r.target(t), Network: m6cNet, Args: m6cTargetArgs()}))
	m3dWaitRunning(r.ctx, t, v, m6cAppName)

	_, first := r.attach(t, m6cClaimantA)
	if !first.Attached || first.Preexisting {
		t.Errorf("the first network.attach answered %+v, want attached and not pre-existing", first)
	}
	if !r.onNet(t) {
		t.Errorf("network %s does not hold %s after the first claim", m6cNet, m6cConnector)
	}
	r.attach(t, m6cClaimantB)

	r.detach(t, m6cNet, m6cClaimantA)
	if !r.onNet(t) {
		t.Errorf("network %s lost %s while the claim of %s is held", m6cNet, m6cConnector, m6cClaimantB)
	}
	_, err := m4Plan(r.ctx, r.f.api, r.f.agentID, "cloudflare.connector.stop", cloudflare.ConnectorStopInput{Tunnel: m6cTunnel})
	m6nRefused(t, "connector.stop while a claim is held", err, errcode.DockerInUse, m6cClaimantB)

	if last := r.detach(t, "", m6cClaimantB); last.Attached {
		t.Errorf("the last network.detach answered %+v, want the connector detached", last)
	}
	if r.onNet(t) {
		t.Errorf("network %s still holds %s after the last claim is released", m6cNet, m6cConnector)
	}
	r.planEmpty(t, "a second network.detach of the same claim", "cloudflare.network.detach", cloudflare.NetworkDetachInput{
		Tunnel: m6cTunnel, Network: "", Claimant: m6cClaimantB,
	})
}

// attachUndo: undoing an attachment drops its claim and, as it was the only
// one, takes the connector off the network again.
func (r *m6cRun) attachUndo(t *testing.T) {
	ch, _ := r.attach(t, m6cClaimantC)
	if !r.onNet(t) {
		t.Fatalf("network %s does not hold %s after the attachment", m6cNet, m6cConnector)
	}
	if err := m4Undo(r.ctx, r.f.api, ch.ID); err != nil {
		t.Errorf("undo of the attachment: %v", err)
	}
	if r.onNet(t) {
		t.Errorf("network %s still holds %s after the undo", m6cNet, m6cConnector)
	}
	if _, err := m4Plan(r.ctx, r.f.api, r.f.agentID, "cloudflare.connector.stop", cloudflare.ConnectorStopInput{Tunnel: m6cTunnel}); err != nil {
		t.Errorf("connector.stop after the undo: %v, want it planned: the undo should have dropped the claim", err)
	}
}

// containerNames lists the names of all containers on the agent.
func (r *m6cRun) containerNames(t *testing.T) []string {
	t.Helper()
	return strings.Fields(m3Run(r.ctx, t, r.f.agent, "sudo docker ps -a --format "+m3Quote("{{.Names}}")))
}

// requireClean requires that the connector, its token file and its row are gone.
func (r *m6cRun) requireClean(t *testing.T, when string) {
	t.Helper()
	if slices.Contains(r.containerNames(t), m6cConnector) {
		t.Errorf("container %s still exists %s", m6cConnector, when)
	}
	out, err := r.f.agent.Run(r.ctx, "sudo ls -A "+m6cTokenDir)
	if err != nil {
		t.Errorf("listing the token directory %s: %v", when, err)
	} else if strings.TrimSpace(out) != "" {
		t.Errorf("the token directory holds %q %s, want it empty", strings.Fields(out), when)
	}
	if cs := r.listConnectors(t); len(cs) != 0 {
		t.Errorf("connector.list answered %+v %s, want none", cs, when)
	}
}

// stop: connector.stop removes the container and the token file, and a second
// stop converges.
func (r *m6cRun) stop(t *testing.T) {
	input := cloudflare.ConnectorStopInput{Tunnel: m6cTunnel}
	ch := m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "cloudflare.connector.stop", input)
	var out cloudflare.ConnectorStopped
	if err := json.Unmarshal(ch.Output, &out); err != nil {
		t.Fatalf("output of cloudflare.connector.stop: %v", err)
	}
	if !out.Removed {
		t.Errorf("connector.stop answered %+v, want removed", out)
	}
	r.requireClean(t, "after connector.stop")
	r.planEmpty(t, "a second connector.stop", "cloudflare.connector.stop", input)
}

// runUndo: undoing a connector.run removes what it made. The connector may be
// restarting, so only its existence is checked before the undo.
func (r *m6cRun) runUndo(t *testing.T) {
	ch := m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "cloudflare.connector.run", cloudflare.ConnectorRunInput{Tunnel: m6cTunnel, Token: r.token})
	if _, err := r.f.agent.Run(r.ctx, "sudo docker inspect -f "+m3Quote("{{.Id}}")+" "+m6cConnector); err != nil {
		t.Fatalf("the connector after the second connector.run: %v", err)
	}
	if err := m4Undo(r.ctx, r.f.api, ch.ID); err != nil {
		t.Errorf("undo of connector.run: %v", err)
	}
	r.requireClean(t, "after the undo of connector.run")
}

// logDiagnostics logs what m6cDiagnostics collects. A command that fails is
// logged with its error and never fails the test.
func (r *m6cRun) logDiagnostics(t *testing.T) {
	for _, d := range m6cDiagnostics() {
		ctx, cancel := context.WithTimeout(r.ctx, m6cDiagnosticTimeout)
		out, err := r.f.agent.Run(ctx, d.Command)
		cancel()
		out = m5dCapDiagnostic(out, strings.Contains(d.Command, "logs"))
		if err != nil {
			t.Logf("diagnostic %s\n$ %s\n%s\nerror: %v", d.Label, d.Command, out, err)
			continue
		}
		t.Logf("diagnostic %s\n$ %s\n%s", d.Label, d.Command, out)
	}
}
