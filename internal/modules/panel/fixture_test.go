package panel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var (
	_ panelDocker    = (*fdDocker)(nil)
	_ panelDocker    = (*dockerapi.Client)(nil)
	_ core.Listeners = (*fdListeners)(nil)
)

// fdNow is the time the controller fixture's clock reads.
var fdNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// fdStamp is a time column as the module's migrations store it.
func fdStamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// fdAPIError is the Engine's answer with the status code.
func fdAPIError(code int, format string, args ...any) error {
	return &dockerapi.APIError{StatusCode: code, Message: fmt.Sprintf(format, args...)}
}

// fdDocker is an in-memory Docker Engine for the agent actions. Created holds every NetworkCreate request, Removed the
// name of every network a RemoveNetwork call removed.
type fdDocker struct {
	mu         sync.Mutex
	networks   map[string]dockerapi.Network       // by id
	containers map[string]dockerapi.ContainerJSON // by id
	faults     map[string][]error
	next       int

	Created []dockerapi.NetworkCreate
	Removed []string
}

func fdNewDocker() *fdDocker {
	return &fdDocker{
		networks:   map[string]dockerapi.Network{},
		containers: map[string]dockerapi.ContainerJSON{},
		faults:     map[string][]error{},
	}
}

// AddNetwork stores a network; an empty ID becomes "fdnet-<name>".
func (d *fdDocker) AddNetwork(n dockerapi.Network) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n.ID == "" {
		n.ID = "fdnet-" + n.Name
	}
	d.networks[n.ID] = fdCloneNetwork(n)
}

// AddContainer stores a container; an empty ID becomes "fdctr-<name>".
func (d *fdDocker) AddContainer(c dockerapi.ContainerJSON) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c.ID == "" {
		c.ID = "fdctr-" + strings.TrimPrefix(c.Name, "/")
	}
	d.containers[c.ID] = c
}

// Fail queues err as the answer of the next call of the method op; that call changes nothing.
func (d *fdDocker) Fail(op string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.faults[op] = append(d.faults[op], err)
}

func (d *fdDocker) enter(op string) error {
	if q := d.faults[op]; len(q) > 0 {
		d.faults[op] = q[1:]
		return q[0]
	}
	return nil
}

func fdCloneNetwork(n dockerapi.Network) dockerapi.Network {
	n.IPAM.Config = slices.Clone(n.IPAM.Config)
	n.Options = maps.Clone(n.Options)
	n.Labels = maps.Clone(n.Labels)
	n.Containers = nil
	return n
}

func (d *fdDocker) networkID(idOrName string) (string, bool) {
	if _, ok := d.networks[idOrName]; ok {
		return idOrName, true
	}
	for id, n := range d.networks {
		if n.Name == idOrName {
			return id, true
		}
	}
	return "", false
}

// attached returns the endpoints of the containers on the network, keyed by container id.
func (d *fdDocker) attached(n dockerapi.Network) map[string]dockerapi.NetworkContainer {
	out := map[string]dockerapi.NetworkContainer{}
	for _, c := range d.containers {
		if ep := c.NetworkSettings.Networks[n.Name]; ep != nil {
			out[c.ID] = dockerapi.NetworkContainer{Name: strings.TrimPrefix(c.Name, "/"), EndpointID: ep.EndpointID}
		}
	}
	return out
}

// fdMatch applies the label and name filters of the Engine API: every label must match, the name is a substring.
func fdMatch(f dockerapi.Filters, n dockerapi.Network) bool {
	for _, want := range f["label"] {
		k, v, hasValue := strings.Cut(want, "=")
		got, ok := n.Labels[k]
		if !ok || (hasValue && got != v) {
			return false
		}
	}
	names := f["name"]
	return len(names) == 0 || slices.ContainsFunc(names, func(s string) bool { return strings.Contains(n.Name, s) })
}

func (d *fdDocker) ListNetworks(_ context.Context, f dockerapi.Filters) ([]dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ListNetworks"); err != nil {
		return nil, err
	}
	var out []dockerapi.Network
	for _, n := range d.networks {
		if fdMatch(f, n) {
			out = append(out, fdCloneNetwork(n))
		}
	}
	slices.SortFunc(out, func(a, b dockerapi.Network) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (d *fdDocker) InspectNetwork(_ context.Context, idOrName string) (dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectNetwork"); err != nil {
		return dockerapi.Network{}, err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, fdAPIError(http.StatusNotFound, "network %s not found", idOrName)
	}
	n := fdCloneNetwork(d.networks[id])
	n.Containers = d.attached(n)
	return n, nil
}

// CreateNetwork records the request and stores the network it describes. Like the Engine it gives a subnet without a
// gateway the subnet's first host address.
func (d *fdDocker) CreateNetwork(_ context.Context, req dockerapi.NetworkCreate) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CreateNetwork"); err != nil {
		return "", err
	}
	if _, taken := d.networkID(req.Name); taken {
		return "", fdAPIError(http.StatusConflict, "network with name %s already exists", req.Name)
	}
	rec := req
	rec.Options, rec.Labels = maps.Clone(req.Options), maps.Clone(req.Labels)
	if req.EnableIPv6 != nil {
		v := *req.EnableIPv6
		rec.EnableIPv6 = &v
	}
	if req.IPAM != nil {
		ipam := *req.IPAM
		ipam.Config = slices.Clone(req.IPAM.Config)
		rec.IPAM = &ipam
	}
	d.Created = append(d.Created, rec)
	d.next++
	n := dockerapi.Network{
		ID: fmt.Sprintf("fdnet%02d", d.next), Name: req.Name, Created: "2026-11-04T12:00:00Z", Scope: "local", Driver: req.Driver,
		Internal: req.Internal, Attachable: req.Attachable, Options: maps.Clone(req.Options), Labels: maps.Clone(req.Labels),
	}
	if n.Driver == "" {
		n.Driver = "bridge"
	}
	if req.EnableIPv6 != nil {
		n.EnableIPv6 = *req.EnableIPv6
	}
	if req.IPAM != nil {
		n.IPAM = dockerapi.IPAM{Driver: req.IPAM.Driver, Config: slices.Clone(req.IPAM.Config)}
		for i, c := range n.IPAM.Config {
			if c.Subnet.IsValid() && !c.Gateway.IsValid() {
				n.IPAM.Config[i].Gateway = c.Subnet.Masked().Addr().Next()
			}
		}
	}
	d.networks[n.ID] = n
	return n.ID, nil
}

func (d *fdDocker) RemoveNetwork(_ context.Context, idOrName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("RemoveNetwork"); err != nil {
		return err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return fdAPIError(http.StatusNotFound, "network %s not found", idOrName)
	}
	if len(d.attached(d.networks[id])) > 0 {
		return fdAPIError(http.StatusForbidden, "error while removing network: network %s has active endpoints", idOrName)
	}
	d.Removed = append(d.Removed, d.networks[id].Name)
	delete(d.networks, id)
	return nil
}

func (d *fdDocker) InspectContainer(_ context.Context, idOrName string) (dockerapi.ContainerJSON, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectContainer"); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	if c, ok := d.containers[idOrName]; ok {
		return c, nil
	}
	for _, c := range d.containers {
		if strings.TrimPrefix(c.Name, "/") == idOrName {
			return c, nil
		}
	}
	return dockerapi.ContainerJSON{}, fdAPIError(http.StatusNotFound, "No such container: %s", idOrName)
}

// fdDB opens a controller database in t.TempDir() migrated with store.CoreMigrations(store.SideController) and
// controllerMigrations(), closed at cleanup.
func fdDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: fdNow,
	}, set, []store.MigrationSet{{Module: ID, FS: controllerMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("panel migrations failed: %v", res.Failed[0].Err)
	}
	return db
}

// fdListeners is an in-memory core.Listeners: Open records the spec under ids "l1", "l2", ..., Revoke moves open to
// revoked (and succeeds on revoked or closed), Close moves revoked to closed (and succeeds on closed; an open listener is
// refused), Get answers the row; FailOpen, when set, is returned by Open.
type fdListeners struct {
	Rows     map[string]core.ListenerInfo
	FailOpen error
	next     int
}

func fdNewListeners() *fdListeners { return &fdListeners{Rows: map[string]core.ListenerInfo{}} }

func (l *fdListeners) Open(_ context.Context, spec core.ListenerSpec) (string, error) {
	if l.FailOpen != nil {
		return "", l.FailOpen
	}
	l.next++
	id := fmt.Sprintf("l%d", l.next)
	l.Rows[id] = core.ListenerInfo{ID: id, Spec: spec, State: core.ListenerOpen}
	return id, nil
}

func (l *fdListeners) Revoke(_ context.Context, id string) error {
	row, ok := l.Rows[id]
	if !ok {
		return fmt.Errorf("fake: no listener %s", id)
	}
	if row.State == core.ListenerOpen {
		row.State = core.ListenerRevoked
		l.Rows[id] = row
	}
	return nil
}

func (l *fdListeners) Close(_ context.Context, id string) error {
	row, ok := l.Rows[id]
	if !ok {
		return fmt.Errorf("fake: no listener %s", id)
	}
	if row.State == core.ListenerOpen {
		return fmt.Errorf("fake: listener %s is open; revoke it first", id)
	}
	row.State = core.ListenerClosed
	l.Rows[id] = row
	return nil
}

func (l *fdListeners) Get(_ context.Context, id string) (core.ListenerInfo, error) {
	row, ok := l.Rows[id]
	if !ok {
		return core.ListenerInfo{}, fmt.Errorf("fake: no listener %s", id)
	}
	return row, nil
}

// fdEnv is a controller-side env with DB, Listeners, a fake clock at fdNow and Call set to call.
func fdEnv(t *testing.T, db *store.DB, l core.Listeners, call core.CallContext) core.Env {
	t.Helper()
	return core.Env{
		Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin},
		Call:      call,
		DB:        db,
		Sealer:    coretest.Sealer{},
		Clock:     coretest.NewFakeClock(fdNow),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Listeners: l,
	}
}

func fdExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// fdInsertUser adds a core_users row of role admin in the state (pending or active).
func fdInsertUser(t *testing.T, db *store.DB, name, state string) {
	t.Helper()
	fdExec(t, db, `INSERT INTO core_users (id, username, role, state, password_hash, totp_secret_ct, created_at, updated_at)
		VALUES (?, ?, 'admin', ?, 'x', x'00', ?, ?)`, "u-"+name, name, state, fdStamp(fdNow), fdStamp(fdNow))
}

// fdInsertHost adds a core_hosts row in state enrolled; local marks the controller's own host.
func fdInsertHost(t *testing.T, db *store.DB, id string, local bool) {
	t.Helper()
	fdExec(t, db, `INSERT INTO core_hosts (id, name, is_local, created_at, state) VALUES (?, ?, ?, ?, 'enrolled')`,
		id, id, local, fdStamp(fdNow))
}

// fdInsertChange adds the core_plans and core_changes rows of a controller-side change of action in state.
func fdInsertChange(t *testing.T, db *store.DB, id, action, state string) {
	t.Helper()
	at := fdStamp(fdNow)
	fdExec(t, db, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		confirmation, created_at, expires_at) VALUES (?, '', ?, 1, 'user:u1', '', '', '[]', '[]', '', ?, ?)`, "p-"+id, action, at, at)
	fdExec(t, db, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json,
		created_at, updated_at) VALUES (?, ?, '', ?, 1, 'compensable', '', 'user:u1', ?, '{}', ?, ?)`, id, "p-"+id, action, state, at, at)
}

// fdInsertExposure stores the exposure row.
func fdInsertExposure(t *testing.T, db *store.DB, r exposureRow) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return insertExposure(ctx, tx, r) }); err != nil {
		t.Fatal(err)
	}
}

// TestFdDockerFake: the fake Engine answers as the Engine does, because the agent actions' tests rely on it: name and
// label filters, a 409 for a taken name, the first host address as the gateway, attached containers in the inspect
// answer, a 403 for a removal with active endpoints, Engine-style not-found answers and queued faults.
func TestFdDockerFake(t *testing.T) {
	ctx := context.Background()
	d := fdNewDocker()
	ipam := &dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{Subnet: netip.MustParsePrefix("172.31.255.248/29")}}}
	v6 := false
	id, err := d.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: NetworkName, IPAM: ipam, EnableIPv6: &v6, Labels: map[string]string{core.LabelModule: ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Created) != 1 || d.Created[0].Name != NetworkName || d.Created[0].IPAM == nil || *d.Created[0].EnableIPv6 {
		t.Errorf("Created = %+v", d.Created)
	}
	if _, err := d.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: NetworkName}); !dockerapi.IsConflict(err) {
		t.Errorf("creating a taken name: %v, want a 409", err)
	}
	d.AddNetwork(dockerapi.Network{Name: "other", Labels: map[string]string{"app": "x"}})
	byLabel, err := d.ListNetworks(ctx, dockerapi.Filters{"label": {core.LabelModule + "=" + ID}})
	if err != nil || len(byLabel) != 1 || byLabel[0].Name != NetworkName {
		t.Errorf("ListNetworks by label = %v, %v", byLabel, err)
	}
	byName, err := d.ListNetworks(ctx, dockerapi.Filters{"name": {"oth"}})
	if err != nil || len(byName) != 1 || byName[0].Name != "other" {
		t.Errorf("ListNetworks by name = %v, %v", byName, err)
	}
	got, err := d.InspectNetwork(ctx, NetworkName)
	if err != nil || got.ID != id || got.IPAM.Config[0].Gateway != netip.MustParseAddr("172.31.255.249") || got.EnableIPv6 {
		t.Errorf("InspectNetwork = %+v, %v", got, err)
	}
	d.AddContainer(dockerapi.ContainerJSON{Name: "/web", NetworkSettings: dockerapi.NetworkSettings{
		Networks: map[string]*dockerapi.EndpointSettings{NetworkName: {NetworkID: id, EndpointID: "e1"}}}})
	if got, _ = d.InspectNetwork(ctx, id); len(got.Containers) != 1 {
		t.Errorf("InspectNetwork lists %d containers, want 1", len(got.Containers))
	}
	if c, err := d.InspectContainer(ctx, "web"); err != nil || c.ID != "fdctr-web" {
		t.Errorf("InspectContainer(web) = %+v, %v", c, err)
	}
	var refusal *dockerapi.APIError
	if err := d.RemoveNetwork(ctx, id); !errors.As(err, &refusal) || refusal.StatusCode != http.StatusForbidden {
		t.Errorf("removing a network with an endpoint = %v, want a 403", err)
	}
	if len(d.Removed) != 0 {
		t.Errorf("Removed = %v after a refused removal", d.Removed)
	}
	delete(d.containers, "fdctr-web")
	if err := d.RemoveNetwork(ctx, id); err != nil || len(d.Removed) != 1 || d.Removed[0] != NetworkName {
		t.Errorf("RemoveNetwork = %v, Removed = %v", err, d.Removed)
	}
	if _, err := d.InspectNetwork(ctx, NetworkName); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectNetwork of a removed network = %v, want not found", err)
	}
	if err := d.RemoveNetwork(ctx, NetworkName); !dockerapi.IsNotFound(err) {
		t.Errorf("RemoveNetwork of a removed network = %v, want not found", err)
	}
	if _, err := d.InspectContainer(ctx, "ghost"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectContainer of a missing container = %v, want not found", err)
	}
	d.Fail("InspectContainer", dockerapi.ErrUnavailable)
	if _, err := d.InspectContainer(ctx, "ghost"); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("queued fault: %v", err)
	}
	if _, err := d.InspectContainer(ctx, "ghost"); !dockerapi.IsNotFound(err) {
		t.Errorf("the fault was not consumed: %v", err)
	}
}

// TestFdListenersFake: the fake runtime opens listeners as l1, l2, revokes and closes them in order, treats a repeated
// revoke or close as done, refuses to close an open listener and returns FailOpen.
func TestFdListenersFake(t *testing.T) {
	ctx := context.Background()
	l := fdNewListeners()
	spec := core.ListenerSpec{Addr: netip.MustParseAddrPort("172.31.255.249:9443"), AllowedPeer: netip.MustParseAddr("172.31.255.250"),
		ClientHeader: core.ClientHeaderNPM, PublicHost: "panel.example.org", ChangeID: "c1"}
	a, err := l.Open(ctx, spec)
	if err != nil || a != "l1" {
		t.Fatalf("Open = %q, %v", a, err)
	}
	if b, _ := l.Open(ctx, spec); b != "l2" {
		t.Errorf("second Open = %q, want l2", b)
	}
	if info, err := l.Get(ctx, "l1"); err != nil || info.State != core.ListenerOpen || info.Spec != spec {
		t.Errorf("Get(l1) = %+v, %v", info, err)
	}
	if err := l.Close(ctx, "l1"); err == nil {
		t.Error("closing an open listener succeeded")
	}
	for range 2 {
		if err := l.Revoke(ctx, "l1"); err != nil {
			t.Fatal(err)
		}
	}
	if info, _ := l.Get(ctx, "l1"); info.State != core.ListenerRevoked {
		t.Errorf("state %s after revoke, want revoked", info.State)
	}
	for range 2 {
		if err := l.Close(ctx, "l1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Revoke(ctx, "l1"); err != nil {
		t.Errorf("revoking a closed listener: %v", err)
	}
	if info, _ := l.Get(ctx, "l1"); info.State != core.ListenerClosed {
		t.Errorf("state %s after close and revoke, want closed", info.State)
	}
	if _, err := l.Get(ctx, "nope"); err == nil {
		t.Error("Get of an unknown listener succeeded")
	}
	l.FailOpen = errors.New("bind failed")
	if _, err := l.Open(ctx, spec); err == nil || err.Error() != "bind failed" {
		t.Errorf("Open with FailOpen = %v", err)
	}
}

// TestFdControllerFixtures: the database carries the module's table, fdEnv reaches it with the listeners and the call,
// and the insert helpers write rows the core tables accept, one local host at most.
func TestFdControllerFixtures(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	l := fdNewListeners()
	call := core.CallContext{ParentChangeID: "c1", ParentAction: ActionAdd}
	env := fdEnv(t, db, l, call)
	if env.DB != db || env.Listeners != l || !reflect.DeepEqual(env.Call, call) || !env.Clock.Now().Equal(fdNow) || env.Principal.Scope != core.ScopeAdmin || env.HostID != "" {
		t.Errorf("fdEnv = %+v", env)
	}
	fdInsertUser(t, db, "alice", "active")
	fdInsertUser(t, db, "bob", "pending")
	fdInsertHost(t, db, "h1", true)
	fdInsertHost(t, db, "h2", false)
	fdInsertChange(t, db, "c1", ActionAdd, "completed")
	fdInsertExposure(t, db, fdRow("c1"))
	count := func(query string) (n int) {
		t.Helper()
		if err := db.Read().QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM core_users WHERE role = 'admin'`:                                       2,
		`SELECT count(*) FROM core_users WHERE state = 'pending'`:                                    1,
		`SELECT count(*) FROM core_hosts WHERE is_local = 1 AND state = 'enrolled'`:                  1,
		`SELECT count(*) FROM core_hosts WHERE is_local = 0 AND state = 'enrolled'`:                  1,
		`SELECT count(*) FROM core_changes WHERE id = 'c1' AND state = 'completed' AND host_id = ''`: 1,
		`SELECT count(*) FROM panel_exposures WHERE id = 'c1'`:                                       1,
	} {
		if got := count(query); got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}
}
