package cloudflare

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const agTunnel = "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f"

func TestAgNamesAndConstants(t *testing.T) {
	if got := []string{tokenDir, tokenMount}; !slices.Equal(got, []string{"/var/lib/darkpanel/agent/cloudflare-tokens", "/run/darkpanel/token"}) {
		t.Errorf("tokenDir and tokenMount %q, want the spec's paths", got)
	}
	if got := connectorName(agTunnel); got != "darkpanel-cloudflared-"+agTunnel {
		t.Errorf("connectorName = %q", got)
	}
	if got := tokenFileName(agTunnel); got != agTunnel+".token" {
		t.Errorf("tokenFileName = %q", got)
	}
}

func TestAgTunnelIDProblem(t *testing.T) {
	tests := []struct {
		name, id string
		bad      bool
	}{
		{"a lower-case uuid", agTunnel, false},
		{"all zeros", "00000000-0000-0000-0000-000000000000", false},
		{"upper case", strings.ToUpper(agTunnel), true},
		{"no dashes", strings.ReplaceAll(agTunnel, "-", ""), true},
		{"empty", "", true},
		{"a name", "darkpanel-c1", true},
		{"a trailing newline", agTunnel + "\n", true},
		{"a path", "../" + agTunnel, true},
		{"too long", agTunnel + "0", true},
		{"a non-hex digit", "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6g", true},
	}
	for _, tc := range tests {
		if got := tunnelIDProblem(tc.id); (got != "") != tc.bad {
			t.Errorf("%s: tunnelIDProblem(%q) = %q, want a problem: %t", tc.name, tc.id, got, tc.bad)
		}
	}
}

func TestAgClaimantProblem(t *testing.T) {
	tests := []struct {
		name, claimant string
		bad            bool
	}{
		{"an expose claimant", "expose:app.example.com", false},
		{"one byte", "x", false},
		{"the longest", strings.Repeat("a", 300), false},
		{"one byte too long", strings.Repeat("a", 301), true},
		{"empty", "", true},
		{"a space", "expose: app", true},
		{"a tab", "expose:\tapp", true},
		{"a newline", "expose:app\n", true},
		{"a control character", "expose:\x00app", true},
	}
	for _, tc := range tests {
		if got := claimantProblem(tc.claimant); (got != "") != tc.bad {
			t.Errorf("%s: claimantProblem = %q, want a problem: %t", tc.name, got, tc.bad)
		}
	}
}

func TestAgNetworkNamePattern(t *testing.T) {
	for name, ok := range map[string]bool{
		"web": true, "my_net-1.x": true, "A": true, strings.Repeat("a", 128): true,
		"": false, "-web": false, ".web": false, "web net": false, "web|x": false, "a/b": false, strings.Repeat("a", 129): false,
	} {
		if got := networkNamePattern.MatchString(name); got != ok {
			t.Errorf("networkNamePattern.MatchString(%q) = %t, want %t", name, got, ok)
		}
	}
}

// TestAgManifestDeclaresTheTokenDirectoryAndTheAgentTables: the token directory is a managed directory whose names are
// "<tunnel id>.token", the agent migrations are the module's, and the registry accepts the manifest.
func TestAgManifestDeclaresTheTokenDirectoryAndTheAgentTables(t *testing.T) {
	m := Module().Manifest()
	if len(m.OwnedPaths) != 1 {
		t.Fatalf("owned paths %+v, want the token directory only", m.OwnedPaths)
	}
	op := m.OwnedPaths[0]
	if op.Path != tokenDir || op.Kind != core.OwnedManagedDir {
		t.Errorf("owned path %+v, want %s as a managed-dir", op, tokenDir)
	}
	for name, ok := range map[string]bool{
		agTunnel + ".token": true, strings.ToUpper(agTunnel) + ".token": false, agTunnel: false, agTunnel + ".tokens": false,
		"x.token": false, "../" + agTunnel + ".token": false, agTunnel + ".token\n": false,
	} {
		if got := agManagedName.MatchString(name); got != ok {
			t.Errorf("name pattern matches %q: %t, want %t", name, got, ok)
		}
	}
	if m.Migrations[core.SideAgent] == nil || m.Migrations[core.SideController] == nil || len(m.Migrations) != 2 {
		t.Errorf("migrations %v, want the controller's and the agent's", m.Migrations)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

// TestAgProductionDepsReachNothing: in a test binary the production wiring dials a socket that never exists, never the
// host's Docker daemon.
func TestAgProductionDepsReachNothing(t *testing.T) {
	m, ok := Module().(module)
	if !ok || m.c.a.docker == nil {
		t.Fatalf("Module() = %#v, want a module over the production agent wiring", Module())
	}
	if productionCF().a.docker == nil {
		t.Fatal("productionCF has no agent Engine")
	}
	d := productionAgentDeps().docker
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := d.InspectContainer(ctx, "darkpanel-cloudflared-x"); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("InspectContainer error %v, want the Engine unavailable", err)
	}
	s, err := d.PullImage(ctx, "cloudflare/cloudflared:2025.4.0")
	if err == nil || s != nil {
		t.Errorf("PullImage = %v, %v; want a nil stream and an error", s, err)
	}
}

func TestAgEngineCoded(t *testing.T) {
	down := errors.Join(errors.New("dial unix"), dockerapi.ErrUnavailable)
	coded := engineCoded(down)
	if errcode.From(coded).Code != errcode.DockerEngineUnavailable || !errors.Is(coded, dockerapi.ErrUnavailable) {
		t.Errorf("engineCoded(unreachable) = %v, want docker.engine_unavailable wrapping the cause", coded)
	}
	other := &dockerapi.APIError{StatusCode: 409, Message: "conflict"}
	if got := engineCoded(other); !errors.Is(got, other) || errors.Is(got, dockerapi.ErrUnavailable) || errcode.From(got).Code == errcode.DockerEngineUnavailable {
		t.Errorf("engineCoded(other) = %v, want it unchanged", got)
	}
	if engineCoded(nil) != nil {
		t.Error("engineCoded(nil) is not nil")
	}
}
