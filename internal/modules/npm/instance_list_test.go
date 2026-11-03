package npm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// ckAddHost adds a core_hosts row to the controller database.
func ckAddHost(t *testing.T, h *thHost, id, name string) {
	t.Helper()
	err := h.ctlDB.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, ?)`,
			id, name, h.clock.Now().UTC().Format(time.RFC3339))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ckList reads npm.instance.list as the controller's read handler does.
func ckList(ctx context.Context, h *thHost) (InstancesOutput, json.RawMessage, error) {
	raw, err := h.nm().instanceList().Handler.Read(ctx, h.ctlEnv(""), json.RawMessage(`{}`))
	if err != nil {
		return InstancesOutput{}, nil, err
	}
	var out InstancesOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return InstancesOutput{}, raw, err
	}
	return out, raw, nil
}

// ckExposed is the status of a host with a deployed, publicly confirmed instance whose admin UI is exposed.
func ckExposed() InstanceStatus {
	return InstanceStatus{
		Deployed: true, ContainerID: "abc123", Image: "jc21/nginx-proxy-manager:2.13.5@sha256:aa", PinnedImage: "jc21/nginx-proxy-manager:2.13.5@sha256:aa",
		Running: true, APIReady: true, Public: "confirmed", AdminEmail: "ops@example.com",
		AdminExposure: &AdminExposure{Hostname: "npm.example.com", ProxyHostID: 4, CertificateID: 2}, UpgradeAvailable: true,
	}
}

func TestCkInstanceListDeclaration(t *testing.T) {
	def := newTHHost(t).nm().instanceList()
	s := def.Spec
	if s.Name != "npm.instance.list" || s.Version != 1 || s.Summary == "" || s.Class != core.ClassRead || s.Side != core.SideController ||
		s.Internal || s.Reveal || len(s.LockKeys) != 0 || def.Handler.Read == nil || def.Handler.Reveal != nil {
		t.Errorf("spec %+v, read handler %v; want a public controller read v1 without locks", s, def.Handler.Read != nil)
	}
	if def.InputType != reflect.TypeFor[InstanceListInput]() || def.OutputType != reflect.TypeFor[InstancesOutput]() {
		t.Errorf("types %v and %v, want InstanceListInput and InstancesOutput", def.InputType, def.OutputType)
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
}

// TestCkInstanceList: every host is listed by id with the status its agent answers, a host whose read fails or
// that has no agent is left out, and a host without an instance is listed with Deployed false.
func TestCkInstanceList(t *testing.T) {
	boom := errors.New("the agent does not answer")
	h := newTHHost(t)
	ckAddHost(t, h, "h3", "charlie")
	ckAddHost(t, h, "h1", "alpha")
	ckAddHost(t, h, "h2", "bravo")
	ckAddHost(t, h, "h4", "delta")
	h.agents = map[string]*coretest.FakeAgent{
		"h1": (&ckAgent{status: ckExposed()}).fake(),
		"h2": (&ckAgent{statusErr: boom}).fake(),
		"h3": (&ckAgent{status: InstanceStatus{}}).fake(),
	}
	got, _, err := ckList(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	want := []HostInstance{
		{HostID: "h1", HostName: "alpha", Status: ckExposed()},
		{HostID: "h3", HostName: "charlie", Status: InstanceStatus{}},
	}
	if !reflect.DeepEqual(got.Instances, want) {
		t.Errorf("instances %+v, want %+v: by host id, without the failing host h2 and the host h4 that has no agent", got.Instances, want)
	}
}

// TestCkInstanceListWithoutHosts: no host gives an empty list, never null.
func TestCkInstanceListWithoutHosts(t *testing.T) {
	h := newTHHost(t)
	_, raw, err := ckList(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || string(fields["instances"]) != "[]" {
		t.Errorf("output %s (%v), want an instances member of []", raw, err)
	}
}

// TestCkInstanceListReadsOverlap: the agents are read concurrently, so one slow host does not delay the others
// one after another. Each read waits until all three have started.
func TestCkInstanceListReadsOverlap(t *testing.T) {
	h := newTHHost(t)
	h.agents = map[string]*coretest.FakeAgent{}
	var started atomic.Int32
	release := make(chan struct{})
	for _, id := range []string{"h1", "h2", "h3"} {
		ckAddHost(t, h, id, "host "+id)
		fa := (&ckAgent{status: ckExposed()}).fake()
		inner := fa.ReadFunc
		fa.ReadFunc = func(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			if started.Add(1) == 3 {
				close(release)
			}
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				return nil, errors.New("the reads did not overlap")
			}
			return inner(ctx, p, ref, input)
		}
		h.agents[id] = fa
	}
	got, _, err := ckList(context.Background(), h)
	if err != nil || len(got.Instances) != 3 {
		t.Fatalf("instances %+v, %v; want all three hosts", got.Instances, err)
	}
}

// TestCkInstanceListCancelled: a read cut off by a cancelled context fails the list rather than leaving the host
// out, since an empty banner would claim that nothing is exposed.
func TestCkInstanceListCancelled(t *testing.T) {
	h := newTHHost(t)
	ckAddHost(t, h, "h1", "alpha")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.agents = map[string]*coretest.FakeAgent{"h1": {
		ReadFunc: func(ctx context.Context, _ core.Principal, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
			cancel()
			return nil, ctx.Err()
		},
	}}
	if _, _, err := ckList(ctx, h); !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want context.Canceled", err)
	}
}
