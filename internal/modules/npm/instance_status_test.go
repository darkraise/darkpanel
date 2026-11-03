package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const stOldImage = "jc21/nginx-proxy-manager:2.12.0@sha256:1111111111111111111111111111111111111111111111111111111111111111"

// stHealth is the NPM client with Health replaced; it records the context's deadline.
type stHealth struct {
	npmAPI
	mu       sync.Mutex
	err      error
	deadline time.Time
	hasLimit bool
}

func (a *stHealth) Health(ctx context.Context) (npmapi.Health, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deadline, a.hasLimit = ctx.Deadline()
	if a.err != nil {
		return npmapi.Health{}, a.err
	}
	return npmapi.Health{Status: "OK", Setup: true}, nil
}

func stRead(t *testing.T, h *thHost, n nm) InstanceStatus {
	t.Helper()
	raw, err := n.instanceStatus().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var st InstanceStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return st
}

// stReserve records a reservation of the exposure.
func stReserve(t *testing.T, h *thHost, kind string, id int, owner, exposure string) {
	t.Helper()
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return addReserved(context.Background(), tx, reservedRow{Kind: kind, ObjectID: id, Owner: owner, Exposure: exposure, ChangeID: "c0"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStStatusSpec(t *testing.T) {
	def := newTHHost(t).nm().instanceStatus()
	s := def.Spec
	if s.Name != "npm.instance.status" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent || len(s.LockKeys) != 0 ||
		s.Summary == "" || s.Available != nil {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Read == nil || def.Handler.Plan != nil || def.Handler.Apply != nil {
		t.Error("the status is a plain read")
	}
}

func TestStStatus(t *testing.T) {
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		raw, err := h.nm().instanceStatus().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"deployed": false, "containerId": "", "image": "", "pinnedImage": Image, "running": false, "apiReady": false,
			"public": "no", "adminEmail": "", "adminExposure": nil, "upgradeAvailable": false,
		}
		if !reflect.DeepEqual(doc, want) {
			t.Errorf("status %v, want %v", doc, want)
		}
		if calls := h.docker.calls(); len(calls) != 0 {
			t.Errorf("Engine calls %v for a host with no instance", calls)
		}
	})
	t.Run("a running instance", func(t *testing.T) {
		h := newTHHost(t)
		inst := h.deployed()
		got := stRead(t, h, h.nm())
		want := InstanceStatus{
			Deployed: true, ContainerID: inst.ContainerID, Image: Image, PinnedImage: Image, Running: true, APIReady: true,
			Public: "no", AdminEmail: inst.Email,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("status %+v, want %+v", got, want)
		}
		if calls := h.docker.calls(); !slices.Equal(calls, []string{"InspectContainer"}) {
			t.Errorf("Engine calls %v, want one inspect of the recorded container", calls)
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), inst.Password) {
			t.Error("the status holds the password")
		}
	})
	t.Run("the output has exactly the contract's keys", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		raw, err := h.nm().instanceStatus().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		var keys []string
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := []string{"adminEmail", "adminExposure", "apiReady", "containerId", "deployed", "image", "pinnedImage", "public", "running", "upgradeAvailable"}
		if !slices.Equal(keys, want) {
			t.Errorf("keys %v, want %v", keys, want)
		}
	})
	t.Run("an older image", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		stSetRow(t, h, func(r *instanceRow) { r.Image = stOldImage })
		got := stRead(t, h, h.nm())
		if got.Image != stOldImage || got.PinnedImage != Image || !got.UpgradeAvailable {
			t.Errorf("status %+v, want an upgrade from %s to %s", got, stOldImage, Image)
		}
	})
	t.Run("a confirmed host", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		stSetRow(t, h, func(r *instanceRow) { r.Public = "confirmed" })
		if got := stRead(t, h, h.nm()); got.Public != "confirmed" {
			t.Errorf("public %q, want confirmed", got.Public)
		}
	})
	t.Run("a stopped container", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		if _, err := h.docker.StopContainer(context.Background(), containerName, time.Second); err != nil {
			t.Fatal(err)
		}
		got := stRead(t, h, h.nm())
		if !got.Deployed || got.Running || got.APIReady {
			t.Errorf("status %+v, want deployed and neither running nor ready", got)
		}
		if reqs := h.npm.Requests(); len(reqs) != 0 {
			t.Errorf("NPM was asked %v about a stopped container", reqs)
		}
	})
	t.Run("a container the Engine does not know", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		if err := h.docker.RemoveContainer(context.Background(), containerName, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		if got := stRead(t, h, h.nm()); !got.Deployed || got.Running || got.APIReady {
			t.Errorf("status %+v, want deployed and neither running nor ready", got)
		}
	})
	t.Run("the Engine down", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		if got := stRead(t, h, h.nm()); !got.Deployed || got.Running || got.APIReady {
			t.Errorf("status %+v, want the read to answer with deployed only", got)
		}
	})
	t.Run("NPM does not answer", func(t *testing.T) {
		for name, prepare := range map[string]func(h *thHost) nm{
			"a failing client": func(h *thHost) nm {
				n := h.nm()
				n.d.anon = func() npmAPI { return &stHealth{npmAPI: h.nm().d.anon(), err: errors.New("npm: connection refused")} }
				return n
			},
			"a 503": func(h *thHost) nm {
				h.npm.Fail(http.MethodGet, "/api/", fake.Fault{Status: http.StatusServiceUnavailable})
				return h.nm()
			},
		} {
			t.Run(name, func(t *testing.T) {
				h := newTHHost(t)
				h.deployed()
				got := stRead(t, h, prepare(h))
				if !got.Running || got.APIReady {
					t.Errorf("status %+v, want a running container whose API is not ready", got)
				}
			})
		}
	})
	t.Run("the probe is bounded", func(t *testing.T) {
		h := newTHHost(t)
		h.deployed()
		n := h.nm()
		probe := &stHealth{npmAPI: n.d.anon()}
		n.d.anon = func() npmAPI { return probe }
		before := time.Now()
		stRead(t, h, n)
		if !probe.hasLimit || probe.deadline.After(before.Add(statusProbeTimeout+time.Second)) || !probe.deadline.After(before) {
			t.Errorf("the probe's deadline %v (set %t), want one within %s", probe.deadline, probe.hasLimit, statusProbeTimeout)
		}
	})
}

func TestStStatusAdminExposure(t *testing.T) {
	const admin, panel = "npm.admin.expose", "panel.exposure.add"
	for _, c := range []struct {
		name  string
		rows  func(t *testing.T, h *thHost)
		want  *AdminExposure
		other bool
	}{
		{"none", func(*testing.T, *thHost) {}, nil, false},
		{"an exposure of the admin interface", func(t *testing.T, h *thHost) {
			stReserve(t, h, "proxy_host", 5, admin, "npm.darkpanel.dev")
			stReserve(t, h, "certificate", 9, admin, "npm.darkpanel.dev")
		}, &AdminExposure{Hostname: "npm.darkpanel.dev", ProxyHostID: 5, CertificateID: 9}, false},
		{"a proxy host without its certificate", func(t *testing.T, h *thHost) {
			stReserve(t, h, "proxy_host", 5, admin, "npm.darkpanel.dev")
		}, &AdminExposure{Hostname: "npm.darkpanel.dev", ProxyHostID: 5}, false},
		{"only the panel's exposure", func(t *testing.T, h *thHost) {
			stReserve(t, h, "proxy_host", 6, panel, "panel.darkpanel.dev")
			stReserve(t, h, "certificate", 7, panel, "panel.darkpanel.dev")
		}, nil, false},
		{"both owners", func(t *testing.T, h *thHost) {
			stReserve(t, h, "proxy_host", 6, panel, "panel.darkpanel.dev")
			stReserve(t, h, "certificate", 7, panel, "panel.darkpanel.dev")
			stReserve(t, h, "proxy_host", 5, admin, "npm.darkpanel.dev")
			stReserve(t, h, "certificate", 9, admin, "npm.darkpanel.dev")
		}, &AdminExposure{Hostname: "npm.darkpanel.dev", ProxyHostID: 5, CertificateID: 9}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newTHHost(t)
			h.deployed()
			c.rows(t, h)
			got := stRead(t, h, h.nm())
			if !reflect.DeepEqual(got.AdminExposure, c.want) {
				t.Errorf("admin exposure %+v, want %+v", got.AdminExposure, c.want)
			}
		})
	}
}

// stSetRow changes the instance row.
func stSetRow(t *testing.T, h *thHost, change func(r *instanceRow)) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		row, ok, err := loadInstance(ctx, tx)
		if err != nil || !ok {
			return errors.New("no instance row")
		}
		change(&row)
		return saveInstance(ctx, tx, row)
	})
	if err != nil {
		t.Fatal(err)
	}
}
