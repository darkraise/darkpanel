package npm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
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

// TestW10NlUnreadableHostsAreListed: a host whose status read fails, whose agent answers something that is not a
// status, or that has no agent is named in Unreadable, in id order, and is not among the instances.
func TestW10NlUnreadableHostsAreListed(t *testing.T) {
	h := newTHHost(t)
	for _, id := range []string{"h5", "h1", "h3", "h2", "h4"} {
		ckAddHost(t, h, id, "host "+id)
	}
	h.agents = map[string]*coretest.FakeAgent{
		"h1": (&ckAgent{status: ckExposed()}).fake(),
		"h2": (&ckAgent{statusErr: errors.New("the agent does not answer")}).fake(),
		"h3": (&ckAgent{status: InstanceStatus{}}).fake(),
		"h4": {ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`[`), nil
		}},
	}
	got, _, err := ckList(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	wantInstances := []HostInstance{
		{HostID: "h1", HostName: "host h1", Status: ckExposed()},
		{HostID: "h3", HostName: "host h3", Status: InstanceStatus{}},
	}
	if !reflect.DeepEqual(got.Instances, wantInstances) {
		t.Errorf("instances %+v, want %+v", got.Instances, wantInstances)
	}
	if want := []string{"h2", "h4", "h5"}; !reflect.DeepEqual(got.Unreadable, want) {
		t.Errorf("unreadable %v, want %v: the failing agent, the agent that answers garbage and the host without an agent", got.Unreadable, want)
	}
}

// TestW10NlUnreadableIsNeverNull: with no unreadable host, the answer carries an empty array, whether there is no
// host at all or every host reads.
func TestW10NlUnreadableIsNeverNull(t *testing.T) {
	empty := newTHHost(t)
	readable := newTHHost(t)
	ckAddHost(t, readable, "h1", "alpha")
	readable.agents = map[string]*coretest.FakeAgent{"h1": (&ckAgent{status: ckExposed()}).fake()}
	for name, h := range map[string]*thHost{"no host": empty, "every host readable": readable} {
		_, raw, err := ckList(context.Background(), h)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || string(fields["unreadable"]) != "[]" {
			t.Errorf("%s: output %s (%v), want an unreadable member of []", name, raw, err)
		}
	}
}

// TestW10NlUnreadableHostLogsAtDebug: a host that cannot be read is in the answer, so reading it again every minute
// writes nothing at info level; the debug line names the host.
func TestW10NlUnreadableHostLogsAtDebug(t *testing.T) {
	h := newTHHost(t)
	ckAddHost(t, h, "h1", "alpha")
	h.agents = map[string]*coretest.FakeAgent{"h1": (&ckAgent{statusErr: errors.New("the agent does not answer")}).fake()}
	for _, tc := range []struct {
		level    slog.Level
		wantLine bool
	}{{slog.LevelInfo, false}, {slog.LevelDebug, true}} {
		var buf bytes.Buffer
		env := h.ctlEnv("")
		env.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: tc.level}))
		if _, err := h.nm().instanceList().Handler.Read(context.Background(), env, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if logged := buf.String(); tc.wantLine != strings.Contains(logged, "host=h1") || (!tc.wantLine && logged != "") {
			t.Errorf("at level %v the log is %q, want a line naming host h1: %v", tc.level, logged, tc.wantLine)
		}
	}
}

// TestW10NlCancelledListStartsNoMoreReads: once the context is cancelled, no further host read is started. Every read
// waits for the cancellation, which the ctlListParallel-th read causes, so all the slots are busy when it happens.
func TestW10NlCancelledListStartsNoMoreReads(t *testing.T) {
	h := newTHHost(t)
	h.agents = map[string]*coretest.FakeAgent{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reads atomic.Int32
	for i := range ctlListParallel + 4 {
		id := fmt.Sprintf("h%02d", i)
		ckAddHost(t, h, id, "host "+id)
		h.agents[id] = &coretest.FakeAgent{ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			if reads.Add(1) == ctlListParallel {
				cancel()
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	if _, _, err := ckList(ctx, h); !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want context.Canceled", err)
	}
	if n := reads.Load(); n != ctlListParallel {
		t.Errorf("%d host reads started, want %d: none after the cancellation", n, ctlListParallel)
	}
}
