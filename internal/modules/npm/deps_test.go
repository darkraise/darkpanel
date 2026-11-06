package npm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/proxy"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

func TestSdConstants(t *testing.T) {
	got := []any{lockKey, commitWait, certCommitWait, networkName, containerName, dataVolume, certVolume, apiURL, backupDir}
	want := []any{"npm", 2 * time.Minute, 10 * time.Minute, "darkpanel-npm", "darkpanel-npm", "darkpanel-npm-data", "darkpanel-npm-letsencrypt",
		"http://127.0.0.1:81", "/var/lib/darkpanel/agent/npm-backups"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("constants %v, want %v", got, want)
	}
}

func TestSdOwnedPathsAndArchiveNames(t *testing.T) {
	owned := ownedPaths()
	if len(owned) != 1 || owned[0].Path != backupDir || owned[0].Kind != core.OwnedManagedDir || owned[0].Pattern != `^[a-z0-9][a-z0-9._-]{0,63}\.tar$` {
		t.Fatalf("owned paths %+v, want the backup directory as a managed-dir with the .tar pattern", owned)
	}
	pattern := regexp.MustCompile(owned[0].Pattern)
	tests := []struct{ changeID, volume, want string }{
		{"01J9ZABCDEF", dataVolume, "01j9zabcdef-data.tar"},
		{"01J9ZABCDEF", certVolume, "01j9zabcdef-letsencrypt.tar"},
		{"c1", dataVolume, "c1-data.tar"},
	}
	for _, tc := range tests {
		got := archiveName(tc.changeID, tc.volume)
		if got != tc.want || !pattern.MatchString(got) {
			t.Errorf("archiveName(%q, %q) = %q (matches the pattern: %v), want %q", tc.changeID, tc.volume, got, pattern.MatchString(got), tc.want)
		}
	}
	for _, name := range []string{"x.tar.gz", "../x.tar", "X.tar", ".x.tar", "x/y.tar", "x.TAR"} {
		if pattern.MatchString(name) {
			t.Errorf("the pattern admits %q", name)
		}
	}
}

func TestSdInstanceCreate(t *testing.T) {
	c := instanceCreate("img@sha256:abc")
	if c.Name != "darkpanel-npm" || c.Image != "img@sha256:abc" || !reflect.DeepEqual(c.Labels, map[string]string{"darkpanel.module": "npm"}) {
		t.Errorf("name %q, image %q, labels %v", c.Name, c.Image, c.Labels)
	}
	if len(c.Env) != 0 || len(c.Cmd) != 0 || len(c.Entrypoint) != 0 {
		t.Errorf("env %v, cmd %v, entrypoint %v; the container takes none, and no secret goes in its environment", c.Env, c.Cmd, c.Entrypoint)
	}
	hc := c.HostConfig
	if hc == nil || hc.NetworkMode != "darkpanel-npm" || hc.RestartPolicy.Name != "unless-stopped" || hc.Privileged || len(hc.Binds) != 0 {
		t.Fatalf("host config %+v", hc)
	}
	wantMounts := []dockerapi.Mount{{Type: "volume", Source: "darkpanel-npm-data", Target: "/data"}, {Type: "volume", Source: "darkpanel-npm-letsencrypt", Target: "/etc/letsencrypt"}}
	if !reflect.DeepEqual(hc.Mounts, wantMounts) {
		t.Errorf("mounts %+v, want %+v", hc.Mounts, wantMounts)
	}
	wantPorts := map[string][]dockerapi.PortBinding{
		"80/tcp":  {{HostPort: "80"}},
		"81/tcp":  {{HostIP: "127.0.0.1", HostPort: "81"}},
		"443/tcp": {{HostPort: "443"}},
	}
	if !reflect.DeepEqual(hc.PortBindings, wantPorts) {
		t.Errorf("port bindings %+v, want %+v", hc.PortBindings, wantPorts)
	}
	if c.NetworkingConfig == nil || len(c.NetworkingConfig.EndpointsConfig) != 1 || c.NetworkingConfig.EndpointsConfig["darkpanel-npm"] == nil {
		t.Errorf("networking config %+v, want one endpoint on darkpanel-npm", c.NetworkingConfig)
	}
}

// TestSdProductionNMReachesNothing: in a test binary the production wiring dials neither the host's Docker socket nor
// 127.0.0.1:81.
func TestSdProductionNMReachesNothing(t *testing.T) {
	n := productionNM()
	d := n.d
	if d.docker == nil || d.api == nil || d.anon == nil || d.info == nil || d.raw == nil || d.random == nil || d.readFile == nil ||
		d.readDir == nil || d.panel == nil || d.sockets == nil {
		t.Fatalf("a production dependency is nil: %+v", d)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := d.docker.Info(ctx); err == nil || !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("Info error %v, want the Engine unavailable", err)
	}
	s, err := d.docker.PullImage(ctx, "jc21/nginx-proxy-manager:2.13.5")
	if err == nil || s != nil {
		t.Errorf("PullImage = %v, %v; want a nil stream and an error", s, err)
	}
	if _, err := d.anon().Health(ctx); err == nil {
		t.Error("the production NPM client reached something")
	}
	if b, err := d.random(32); err != nil || len(b) != 32 || reflect.DeepEqual(b, make([]byte, 32)) {
		t.Errorf("random(32) = %x, %v", b, err)
	}
}

func TestSdModuleSeam(t *testing.T) {
	m, ok := Module().(module)
	if !ok || m.n.d.docker == nil {
		t.Fatalf("Module() = %#v, want a module over the production wiring", Module())
	}
}

func TestSdAPIVia(t *testing.T) {
	ctx := context.Background()
	t.Run("the stored administrator gets a token", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		c, err := h.nm().d.api(ctx, h.env(""))
		if err != nil {
			t.Fatal(err)
		}
		if hosts, err := c.ProxyHosts(ctx); err != nil || len(hosts) != 0 {
			t.Errorf("ProxyHosts = %v, %v; want none, answered to a client with a token", hosts, err)
		}
		if _, err := h.nm().d.anon().ProxyHosts(ctx); err == nil {
			t.Error("a client without a token listed proxy hosts")
		}
	})
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		_, err := h.nm().d.api(ctx, h.env(""))
		if err == nil || !core.IsDefinite(err) || sdCode(t, err) != errcode.NotFound {
			t.Errorf("error %v, want a Definite not_found", err)
		}
	})
	t.Run("a password NPM refuses", func(t *testing.T) {
		h := newTHHost(t)
		inst := h.deployed()
		h.npm.SeedAdmin(inst.Email, "another-password-entirely")
		_, err := h.nm().d.api(ctx, h.env(""))
		if err == nil || sdCode(t, err) != errcode.NPMUnhealthy {
			t.Fatalf("error %v, want %s", err, errcode.NPMUnhealthy)
		}
		if strings.Contains(err.Error(), inst.Password) || strings.Contains(err.Error(), "another-password-entirely") {
			t.Errorf("error %q carries a password", err)
		}
	})
	t.Run("a sealed password that cannot be opened", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		err := h.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE npm_instances SET password_ct = x'0102'`)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.nm().d.api(ctx, h.env("")); err == nil || sdCode(t, err) != errcode.NPMUnhealthy {
			t.Errorf("error %v, want %s", err, errcode.NPMUnhealthy)
		}
	})
	t.Run("the wording of a failed login follows its status", func(t *testing.T) {
		for _, tc := range []struct {
			status  int
			refused bool
		}{{401, true}, {500, false}} {
			h := newTHHost(t)
			h.deployed()
			h.npm.Fail("POST", "/api/tokens", fake.Fault{Status: tc.status})
			_, err := h.nm().d.api(ctx, h.env(""))
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
				t.Fatalf("status %d: error %v, want %s", tc.status, err, errcode.NPMUnhealthy)
			}
			if want := fmt.Sprintf("(HTTP %d)", tc.status); !strings.Contains(coded.Detail, want) {
				t.Errorf("status %d: detail %q does not name %s", tc.status, coded.Detail, want)
			}
			if got := strings.Contains(coded.Detail, "refused"); got != tc.refused {
				t.Errorf("status %d: detail %q says refused: %t, want %t; only a 4xx is a refused login", tc.status, coded.Detail, got, tc.refused)
			}
		}
	})
}

const (
	sdFilterDocker = "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n:DOCKER-FORWARD - [0:0]\n" +
		"-A FORWARD -j DOCKER-USER\n-A FORWARD -j DOCKER-FORWARD\nCOMMIT\n"
	sdFilterPlain = "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
	sdRawEmpty    = "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
	sdRawDrop     = "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n-A PREROUTING -d 127.0.0.1/32 ! -i lo -p tcp -m tcp --dport 81 -j DROP\nCOMMIT\n"
)

// sdReader answers variant reads from save text: variant -> table -> text. A variant it holds nothing of reads as not loaded, and a table of a variant it holds that has no text is left out, as ReadVariant leaves out a legacy table that does not exist.
type sdReader struct {
	tables map[string]map[string]string
	err    error
	asked  []string
}

func (r *sdReader) read(_ context.Context, _ core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
	r.asked = append(r.asked, variant+":"+strings.Join(tables, ","))
	if f != fwstate.V4 {
		return nil, fmt.Errorf("asked for family %s", f)
	}
	if r.err != nil {
		return nil, r.err
	}
	held, ok := r.tables[variant]
	if !ok {
		return nil, nil
	}
	var text strings.Builder
	for _, name := range tables {
		text.WriteString(held[name])
	}
	if text.Len() == 0 {
		return &netfilter.Ruleset{}, nil
	}
	return netfilter.Parse(strings.NewReader(text.String()))
}

func TestSdRawVia(t *testing.T) {
	publishing := []func(*dockerapi.ContainerJSON){thRunning, thPublish("127.0.0.1:81:81/tcp")}
	boom := errors.New("nft is gone")
	tests := []struct {
		name       string
		tables     map[string]map[string]string
		containers [][]func(*dockerapi.ContainerJSON)
		readErr    error
		want       bool
		wantErr    bool
		asked      []string
	}{
		{"Docker's chains are in neither variant", map[string]map[string]string{"nft": {"filter": sdFilterPlain}, "legacy": {"filter": sdFilterPlain}}, nil, nil, false, false,
			[]string{"nft:filter", "legacy:filter"}},
		{"neither variant is loaded", nil, nil, nil, false, false, []string{"nft:filter", "legacy:filter"}},
		{"Docker's chains are in both variants", map[string]map[string]string{"nft": {"filter": sdFilterDocker}, "legacy": {"filter": sdFilterDocker}}, nil, nil, false, false,
			[]string{"nft:filter", "legacy:filter"}},
		{"an idle Docker has nothing to protect", map[string]map[string]string{"nft": {"filter": sdFilterDocker, "raw": sdRawEmpty}}, nil, nil, true, false,
			[]string{"nft:filter", "legacy:filter", "nft:raw"}},
		{"the raw table is missing", map[string]map[string]string{"nft": {"filter": sdFilterDocker}}, nil, nil, false, false,
			[]string{"nft:filter", "legacy:filter", "nft:raw"}},
		{"a loopback port without its drop rule", map[string]map[string]string{"nft": {"filter": sdFilterDocker, "raw": sdRawEmpty}}, [][]func(*dockerapi.ContainerJSON){publishing}, nil, false, false,
			[]string{"nft:filter", "legacy:filter", "nft:raw"}},
		{"a loopback port with its drop rule", map[string]map[string]string{"nft": {"filter": sdFilterDocker, "raw": sdRawDrop}}, [][]func(*dockerapi.ContainerJSON){publishing}, nil, true, false,
			[]string{"nft:filter", "legacy:filter", "nft:raw"}},
		{"the legacy variant holds the chains", map[string]map[string]string{"legacy": {"filter": sdFilterDocker, "raw": sdRawDrop}}, [][]func(*dockerapi.ContainerJSON){publishing}, nil, true, false,
			[]string{"nft:filter", "legacy:filter", "legacy:raw"}},
		{"the read fails", map[string]map[string]string{"nft": {"filter": sdFilterDocker}}, nil, boom, false, true, []string{"nft:filter"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			for i, opts := range tc.containers {
				h.docker.putContainer(thContainer(fmt.Sprintf("darkpanel-npm%d", i), opts...))
			}
			r := &sdReader{tables: tc.tables, err: tc.readErr}
			got, err := rawVia(r.read, h.docker)(context.Background(), h.env(""))
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("rawVia = %v, %v; want %v (error %v)", got, err, tc.want, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, boom) {
				t.Errorf("error %v lost its cause", err)
			}
			if !reflect.DeepEqual(r.asked, tc.asked) {
				t.Errorf("reads %v, want %v", r.asked, tc.asked)
			}
		})
	}
}

func TestSdRawViaIgnoresInternalNetworks(t *testing.T) {
	// An internal network's bridge gets no raw drop rule from Docker, so a container on one needs none.
	h := newTHHost(t)
	h.docker.putNetwork(thNetwork("private", thInternal, thSubnet("172.30.0.0/24", "172.30.0.1")))
	h.docker.putContainer(thContainer("worker", thRunning, thEndpoint("private", "172.30.0.2")))
	r := &sdReader{tables: map[string]map[string]string{"nft": {"filter": sdFilterDocker, "raw": sdRawEmpty}}}
	if got, err := rawVia(r.read, h.docker)(context.Background(), h.env("")); err != nil || !got {
		t.Errorf("rawVia = %v, %v; want protected, since the only endpoint is on an internal network", got, err)
	}
	h.docker.putNetwork(thNetwork("public", thSubnet("172.31.0.0/24", "172.31.0.1")))
	h.docker.putContainer(thContainer("worker", thRunning, thEndpoint("public", "172.31.0.2")))
	if got, err := rawVia(r.read, h.docker)(context.Background(), h.env("")); err != nil || got {
		t.Errorf("rawVia = %v, %v; want unprotected: a bridge endpoint without a drop rule", got, err)
	}
}

func TestSdReservedVia(t *testing.T) {
	ctx := context.Background()
	t.Run("the panel network and the NPM container", func(t *testing.T) {
		h := newTHHost(t)
		h.docker.putNetwork(thNetwork("darkpanel-panel", thSubnet("172.31.255.0/29", "172.31.255.1")))
		h.docker.putContainer(thContainer("darkpanel-npm", thRunning, thEndpoint("darkpanel-panel", "172.31.255.2"), thEndpoint("darkpanel-npm", "172.20.0.2")))
		got, err := reservedVia(h.docker)(ctx, h.env(""))
		want := proxy.Reserved{
			NPMAddrs:     []netip.Addr{netip.MustParseAddr("172.20.0.2"), netip.MustParseAddr("172.31.255.2")},
			PanelNetwork: netip.MustParsePrefix("172.31.255.0/29"),
			PanelGateway: netip.MustParseAddr("172.31.255.1"),
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("reserved = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("neither exists", func(t *testing.T) {
		h := newTHHost(t)
		got, err := reservedVia(h.docker)(ctx, h.env(""))
		if err != nil || !reflect.DeepEqual(got, proxy.Reserved{}) {
			t.Errorf("reserved = %+v, %v; want the zero value", got, err)
		}
	})
	t.Run("the Engine is not reachable", func(t *testing.T) {
		h := newTHHost(t)
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		if _, err := reservedVia(h.docker)(ctx, h.env("")); err == nil || sdCode(t, err) != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want %s", err, errcode.DockerEngineUnavailable)
		}
	})
}
