package npm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const upAppNet = "app-net"

var (
	upFromImage = "jc21/nginx-proxy-manager:2.13.0@sha256:" + strings.Repeat("a", 64)
	upAppIP     = netip.MustParseAddr("172.30.0.9")
	// upTars is what the fake Engine serves for the archive of each mount, keyed by the copied path.
	upTars = map[string][]byte{"/data": []byte("tar-of-data"), "/etc/letsencrypt": []byte("tar-of-letsencrypt")}
)

func upSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type upConnect struct {
	Network, Container string
	Endpoint           *dockerapi.EndpointSettings
}

type upPut struct {
	Container, Path string
	Data            []byte
}

// upDocker wraps the host's fake Engine. It serves the archives of upTars, records what an upgrade creates,
// connects and copies back, traces the calls that change the host and lets a test fail a call: fail receives the
// call, with its argument where it has one, and how often that call has been made.
type upDocker struct {
	dockerAPI
	trace    func(call string)
	fail     func(call string, n int) error
	counts   map[string]int
	created  []dockerapi.ContainerCreate
	ids      []string
	connects []upConnect
	puts     []upPut
	missing  map[string]bool
}

func (d *upDocker) note(call string) error {
	d.counts[call]++
	if method, _, found := strings.Cut(call, " "); found {
		d.counts[method]++
	}
	if d.trace != nil {
		d.trace(call)
	}
	if d.fail != nil {
		return d.fail(call, d.counts[call])
	}
	return nil
}

func (d *upDocker) InspectImage(ctx context.Context, ref string) (dockerapi.ImageInspect, error) {
	if d.missing[ref] {
		return dockerapi.ImageInspect{}, &dockerapi.APIError{StatusCode: 404, Message: "no such image: " + ref}
	}
	return d.dockerAPI.InspectImage(ctx, ref)
}

func (d *upDocker) PullImage(ctx context.Context, ref string) (pullStream, error) {
	if err := d.note("PullImage"); err != nil {
		return nil, err
	}
	return d.dockerAPI.PullImage(ctx, ref)
}

func (d *upDocker) StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error) {
	if err := d.note("StopContainer"); err != nil {
		return false, err
	}
	return d.dockerAPI.StopContainer(ctx, id, timeout)
}

func (d *upDocker) RemoveContainer(ctx context.Context, id string, opts dockerapi.RemoveContainerOptions) error {
	if err := d.note("RemoveContainer"); err != nil {
		return err
	}
	return d.dockerAPI.RemoveContainer(ctx, id, opts)
}

func (d *upDocker) CreateContainer(ctx context.Context, cfg dockerapi.ContainerCreate) (string, error) {
	if err := d.note("CreateContainer"); err != nil {
		return "", err
	}
	id, err := d.dockerAPI.CreateContainer(ctx, cfg)
	if err == nil {
		d.created = append(d.created, cfg)
		d.ids = append(d.ids, id)
	}
	return id, err
}

func (d *upDocker) StartContainer(ctx context.Context, id string) (bool, error) {
	if err := d.note("StartContainer"); err != nil {
		return false, err
	}
	return d.dockerAPI.StartContainer(ctx, id)
}

func (d *upDocker) ConnectNetwork(ctx context.Context, network, container string, endpoint *dockerapi.EndpointSettings) error {
	if err := d.note("ConnectNetwork " + network); err != nil {
		return err
	}
	d.connects = append(d.connects, upConnect{network, container, endpoint})
	return d.dockerAPI.ConnectNetwork(ctx, network, container, endpoint)
}

func (d *upDocker) RemoveVolume(ctx context.Context, name string, force bool) error {
	if err := d.note("RemoveVolume " + name); err != nil {
		return err
	}
	return d.dockerAPI.RemoveVolume(ctx, name, force)
}

func (d *upDocker) CreateVolume(ctx context.Context, v dockerapi.VolumeCreate) (dockerapi.Volume, error) {
	if err := d.note("CreateVolume " + v.Name); err != nil {
		return dockerapi.Volume{}, err
	}
	return d.dockerAPI.CreateVolume(ctx, v)
}

func (d *upDocker) CopyFromContainer(_ context.Context, _, path string) (io.ReadCloser, error) {
	if err := d.note("CopyFromContainer " + path); err != nil {
		return nil, err
	}
	data, ok := upTars[path]
	if !ok {
		return nil, errors.New("upDocker: no archive is served for " + path)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (d *upDocker) CopyToContainer(_ context.Context, id, path string, archive io.Reader) error {
	if err := d.note("CopyToContainer " + path); err != nil {
		return err
	}
	data, err := io.ReadAll(archive)
	if err != nil {
		return err
	}
	d.puts = append(d.puts, upPut{id, path, data})
	return nil
}

// upFiles wraps the environment's file writer: it traces and can fail a managed write, and records the managed
// removals without requiring the file to exist.
type upFiles struct {
	core.FileWriter
	trace     func(call string)
	failWrite func(name string) error
	removed   []string
}

func (f *upFiles) WriteManaged(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error {
	if f.trace != nil {
		f.trace("WriteManaged " + name)
	}
	if f.failWrite != nil {
		if err := f.failWrite(name); err != nil {
			return err
		}
	}
	return f.FileWriter.WriteManaged(ctx, module, dir, name, data, mode, uid, gid)
}

func (f *upFiles) RemoveManaged(ctx context.Context, module, dir, name string) error {
	f.removed = append(f.removed, name)
	_ = f.FileWriter.RemoveManaged(ctx, module, dir, name)
	return nil
}

// upDown is an NPM client whose API does not answer.
type upDown struct{ npmAPI }

func (upDown) Health(context.Context) (npmapi.Health, error) {
	return npmapi.Health{}, errors.New("npmapi: connection refused")
}

// upHealthTrace calls note on every Health call.
type upHealthTrace struct {
	npmAPI
	note func()
}

func (a upHealthTrace) Health(ctx context.Context) (npmapi.Health, error) {
	a.note()
	return a.npmAPI.Health(ctx)
}

// upSetup is a host whose instance runs from, with an extra network attached at a static address, and change c1's
// environment, whose Engine and file writer are the wrappers above.
type upSetup struct {
	t     *testing.T
	h     *thHost
	inst  thInstance
	orig  dockerapi.ContainerJSON
	n     nm
	dock  *upDocker
	files *upFiles
	env   core.Env
}

// upNewSetup puts the pinned image on the host; unless pinned is set, the Engine answers that it has no such image.
func upNewSetup(t *testing.T, from string, pinned bool) *upSetup {
	t.Helper()
	ctx := context.Background()
	h := newTHHost(t)
	inst := h.deployed()
	h.addChange("c1", "npm.instance.upgrade")
	c, ok := h.docker.container(inst.ContainerID)
	if !ok {
		t.Fatalf("the deployed container %s is not in the fake Engine", inst.ContainerID)
	}
	c.Config.Image = from
	c.Config.Env = append(slices.Clone(c.Config.Env), "HARNESS_ONLY=1")
	if c.NetworkSettings.Networks == nil {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
	}
	c.NetworkSettings.Networks[upAppNet] = &dockerapi.EndpointSettings{
		IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: upAppIP}, Aliases: []string{"web"}, NetworkID: "net-app",
		EndpointID: "ep-app", IPAddress: upAppIP, MacAddress: "02:42:ac:1e:00:09",
	}
	h.docker.putContainer(c)
	h.docker.putNetwork(thNetwork(upAppNet))
	h.docker.putVolume(dockerapi.Volume{Name: dataVolume})
	h.docker.putVolume(dockerapi.Volume{Name: certVolume})
	h.docker.putImage(Image)
	row, ok, err := loadInstance(ctx, h.db.Read())
	if err != nil || !ok {
		t.Fatalf("instance row: found %v, error %v", ok, err)
	}
	row.Image, row.Public = from, "confirmed"
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return saveInstance(ctx, tx, row) }); err != nil {
		t.Fatal(err)
	}

	s := &upSetup{t: t, h: h, inst: inst, orig: c, n: h.nm()}
	s.dock = &upDocker{dockerAPI: s.n.d.docker, counts: map[string]int{}, missing: map[string]bool{Image: !pinned}}
	s.n.d.docker = s.dock
	s.env = h.env("c1")
	s.files = &upFiles{FileWriter: s.env.Files}
	s.env.Files = s.files
	return s
}

// apiDown makes the API of every NPM the module starts unreachable.
func (s *upSetup) apiDown() {
	inner := s.n.d.anon
	s.n.d.anon = func() npmAPI { return upDown{npmAPI: inner()} }
}

func (s *upSetup) phase() string {
	u, ok, err := loadUpgrade(context.Background(), s.h.db.Read(), "c1")
	if err != nil || !ok {
		return "no row"
	}
	return u.Phase
}

func (s *upSetup) upgrade() upgradeRow {
	s.t.Helper()
	u, ok, err := loadUpgrade(context.Background(), s.h.db.Read(), "c1")
	if err != nil || !ok {
		s.t.Fatalf("upgrade row of c1: found %v, error %v", ok, err)
	}
	return u
}

func (s *upSetup) instanceRow() instanceRow {
	s.t.Helper()
	row, ok, err := loadInstance(context.Background(), s.h.db.Read())
	if err != nil || !ok {
		s.t.Fatalf("instance row: found %v, error %v", ok, err)
	}
	return row
}

func (s *upSetup) effects() []core.Effect {
	s.t.Helper()
	effects, err := s.h.journal.List(context.Background())
	if err != nil {
		s.t.Fatal(err)
	}
	return effects
}

// apply runs the action's Apply while advancing the host's clock by one second whenever it waits.
func (s *upSetup) apply() (json.RawMessage, error) {
	s.t.Helper()
	var out json.RawMessage
	var err error
	upDrive(s.t, s.h, func() {
		out, err = s.n.instanceUpgrade().Handler.Apply(context.Background(), s.env, json.RawMessage(`{}`))
	})
	return out, err
}

func upDrive(t *testing.T, h *thHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(30 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(time.Second)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

// wantRestored checks that the original instance runs again, recreated from its journaled configuration with its
// volumes restored from the archives, and that the upgrade ended rolled back with its effect skipped.
func (s *upSetup) wantRestored() {
	s.t.Helper()
	t := s.t
	if got := s.phase(); got != "rolled_back" {
		t.Errorf("phase %q, want rolled_back", got)
	}
	c, ok := s.h.docker.container(containerName)
	row := s.instanceRow()
	if !ok || c.ID == s.inst.ContainerID || c.ID != row.ContainerID || c.Config.Image != upFromImage || !c.State.Running {
		t.Fatalf("container %+v (found %v) with the instance row at %s %s; want a new running container of %s that the row names",
			c, ok, row.ContainerID, row.Image, upFromImage)
	}
	if row.Image != upFromImage || row.Public != "confirmed" {
		t.Errorf("instance row image %q, public %q; want %q and confirmed", row.Image, row.Public, upFromImage)
	}
	if len(s.dock.created) == 0 {
		t.Fatal("no container was recreated")
	}
	created := s.dock.created[len(s.dock.created)-1]
	wantConfig := s.orig.Config
	wantConfig.Hostname = ""
	if created.Name != containerName || !reflect.DeepEqual(created.ContainerConfig, wantConfig) || created.HostConfig == nil ||
		!reflect.DeepEqual(*created.HostConfig, s.orig.HostConfig) {
		t.Errorf("the original was recreated as %+v, want its configuration %+v and host configuration %+v", created, wantConfig, s.orig.HostConfig)
	}
	if created.NetworkingConfig == nil || len(created.NetworkingConfig.EndpointsConfig) != 1 || created.NetworkingConfig.EndpointsConfig[networkName] == nil {
		t.Errorf("networking configuration %+v, want only the %s endpoint", created.NetworkingConfig, networkName)
	}
	var puts []upPut
	for _, p := range s.dock.puts {
		if p.Container == c.ID {
			puts = append(puts, p)
		}
	}
	wantPuts := []upPut{{c.ID, "/", upTars["/data"]}, {c.ID, "/etc", upTars["/etc/letsencrypt"]}}
	if !reflect.DeepEqual(puts, wantPuts) {
		t.Errorf("archives copied back to the recreated container: %+v, want %+v", puts, wantPuts)
	}
	reconnected := false
	for _, cn := range s.dock.connects {
		reconnected = reconnected || (cn.Network == upAppNet && cn.Container == c.ID)
	}
	if !reconnected {
		t.Errorf("connects %+v, want %s connected to the recreated container", s.dock.connects, upAppNet)
	}
	for _, v := range []string{dataVolume, certVolume} {
		if _, ok := s.h.docker.volume(v); !ok || s.dock.counts["RemoveVolume "+v] != 1 || s.dock.counts["CreateVolume "+v] != 1 {
			t.Errorf("volume %s: present %v, removed %d, created %d; want it removed once and created once", v, ok,
				s.dock.counts["RemoveVolume "+v], s.dock.counts["CreateVolume "+v])
		}
	}
	for _, id := range s.dock.ids[:len(s.dock.ids)-1] {
		if _, ok := s.h.docker.container(id); ok {
			t.Errorf("container %s of the failed upgrade is still there", id)
		}
	}
	if effects := s.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
		t.Errorf("journal %+v, want one skipped effect", effects)
	}
}

func upImageOf(t *testing.T, doc core.StateDoc) string {
	t.Helper()
	if string(doc.State) == "null" {
		return ""
	}
	var s struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(doc.State, &s); err != nil {
		t.Fatalf("state %s: %v", doc.State, err)
	}
	return s.Image
}

func TestUpDeclaration(t *testing.T) {
	def := newTHHost(t).nm().instanceUpgrade()
	s, hd := def.Spec, def.Handler
	if s.Name != "npm.instance.upgrade" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"npm"}) || s.ApplyTimeout != 20*time.Minute || !s.LongRunning || s.Task || s.Converges ||
		s.Internal || s.Reveal || s.MinScope != "" || s.Available != nil || len(s.Guards) != 0 || s.Summary == "" {
		t.Errorf("spec %+v, want a long-running compensable agent action locking npm for 20 minutes", s)
	}
	if def.InputType != reflect.TypeFor[UpgradeInput]() || def.OutputType != reflect.TypeFor[Upgraded]() {
		t.Errorf("types %v and %v, want UpgradeInput and Upgraded", def.InputType, def.OutputType)
	}
	if hd.Plan == nil || hd.Apply == nil || hd.Verify == nil || hd.Observe == nil || hd.Read != nil || hd.Task != nil {
		t.Errorf("handler has Plan %v Apply %v Verify %v Observe %v Read %v Task %v", hd.Plan != nil, hd.Apply != nil,
			hd.Verify != nil, hd.Observe != nil, hd.Read != nil, hd.Task != nil)
	}
}

func TestUpPlan(t *testing.T) {
	ctx := context.Background()
	s := upNewSetup(t, upFromImage, true)
	s.dock.fail = func(call string, _ int) error { return errors.New("a plan must not call " + call) }
	h := s.n.instanceUpgrade().Handler

	out, err := h.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	want := []core.DiffLine{
		line("-", "image "+upFromImage),
		line("+", "image "+Image),
		line(" ", "both volumes ("+dataVolume+", "+certVolume+") are archived to "+backupDir+" first"),
		line(" ", "the original container and volumes are restored if the new container's API does not answer within 120 s"),
	}
	if !slices.Equal(out.Diff, want) || out.Confirmation != "" || len(out.Guards) != 0 || len(out.StateHash) != 64 {
		t.Errorf("plan %+v, want diff %+v, no confirmation, no guard and a 64-digit hash", out, want)
	}
	if len(s.effects()) != 0 || s.phase() != "no row" {
		t.Errorf("plan journaled %+v or recorded phase %q, want nothing", s.effects(), s.phase())
	}
	again, err := h.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("a second plan hash %q (error %v), want %q", again.StateHash, err, out.StateHash)
	}
	row := s.instanceRow()
	row.Image = "jc21/nginx-proxy-manager:2.12.0@sha256:" + strings.Repeat("b", 64)
	if err := s.h.db.Write(ctx, func(tx *sql.Tx) error { return saveInstance(ctx, tx, row) }); err != nil {
		t.Fatal(err)
	}
	if other, err := h.Plan(ctx, s.h.env(""), json.RawMessage(`{}`)); err != nil || other.StateHash == out.StateHash {
		t.Errorf("plan over another installed image: hash %q (error %v), want one different from %q", other.StateHash, err, out.StateHash)
	}
}

func TestUpRefusals(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		setup func(t *testing.T) (*thHost, nm)
		code  errcode.Code
	}{
		{"no instance is deployed", func(t *testing.T) (*thHost, nm) {
			h := newTHHost(t)
			return h, h.nm()
		}, errcode.NotFound},
		{"the instance's container is gone", func(t *testing.T) (*thHost, nm) {
			s := upNewSetup(t, upFromImage, true)
			if err := s.h.docker.RemoveContainer(ctx, s.inst.ContainerID, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			return s.h, s.h.nm()
		}, errcode.NPMUnhealthy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, n := tc.setup(t)
			h.addChange("c2", "npm.instance.upgrade")
			def := n.instanceUpgrade()
			var coded *errcode.Error
			_, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
			if !errors.As(err, &coded) || coded.Code != tc.code {
				t.Errorf("plan error %v, want code %s", err, tc.code)
			}
			_, err = def.Handler.Apply(ctx, h.env("c2"), json.RawMessage(`{}`))
			if !errors.As(err, &coded) || coded.Code != tc.code || !core.IsDefinite(err) {
				t.Errorf("apply error %v (definite %v), want a definite %s", err, core.IsDefinite(err), tc.code)
			}
			effects, lerr := h.journal.List(ctx)
			if lerr != nil || len(effects) != 0 {
				t.Errorf("a refusal journaled %+v (%v)", effects, lerr)
			}
			if _, ok, _ := loadUpgrade(ctx, h.db.Read(), "c2"); ok {
				t.Error("a refusal recorded an upgrade")
			}
		})
	}
}

// TestUpApplyUpgradesTheInstance covers the whole successful path: the archives, the recreated container and the
// bookkeeping.
func TestUpApplyUpgradesTheInstance(t *testing.T) {
	ctx := context.Background()
	s := upNewSetup(t, upFromImage, true)
	out, err := s.apply()
	if err != nil {
		t.Fatal(err)
	}
	var got Upgraded
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got != (Upgraded{FromImage: upFromImage, ToImage: Image}) {
		t.Errorf("output %+v, want from %s to %s", got, upFromImage, Image)
	}

	c, ok := s.h.docker.container(containerName)
	if !ok || c.ID == s.inst.ContainerID || c.Config.Image != Image || !c.State.Running {
		t.Fatalf("container %+v (found %v), want a new running container of %s", c, ok, Image)
	}
	if _, ok := s.h.docker.container(s.inst.ContainerID); ok {
		t.Error("the original container still exists")
	}
	if len(s.dock.created) != 1 || !reflect.DeepEqual(s.dock.created[0], instanceCreate(Image)) {
		t.Errorf("created %+v, want one container made from the deploy configuration of %s", s.dock.created, Image)
	}
	wantEndpoint := &dockerapi.EndpointSettings{IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: upAppIP}}
	if len(s.dock.connects) != 1 || s.dock.connects[0].Network != upAppNet || s.dock.connects[0].Container != c.ID ||
		!reflect.DeepEqual(s.dock.connects[0].Endpoint, wantEndpoint) {
		t.Errorf("connects %+v, want %s connected to the new container with only its static address", s.dock.connects, upAppNet)
	}
	if len(s.dock.puts) != 0 || s.dock.counts["RemoveVolume"] != 0 {
		t.Errorf("a successful upgrade restored archives %+v or removed %d volumes", s.dock.puts, s.dock.counts["RemoveVolume"])
	}

	row := s.instanceRow()
	if row.ContainerID != c.ID || row.Image != Image || row.Public != "confirmed" {
		t.Errorf("instance row %+v, want container %s, image %s and the public mark kept", row, c.ID, Image)
	}

	u := s.upgrade()
	wantArchives := []archiveRef{
		{Volume: dataVolume, File: "c1-data.tar", SHA256: upSum(upTars["/data"])},
		{Volume: certVolume, File: "c1-letsencrypt.tar", SHA256: upSum(upTars["/etc/letsencrypt"])},
	}
	if u.Phase != "done" || u.FromImage != upFromImage || u.ToImage != Image || !reflect.DeepEqual(u.Archives, wantArchives) {
		t.Errorf("upgrade row %+v, want phase done from %s to %s with archives %+v", u, upFromImage, Image, wantArchives)
	}
	wantFiles := map[string][]byte{"c1-data.tar": upTars["/data"], "c1-letsencrypt.tar": upTars["/etc/letsencrypt"]}
	for name, data := range wantFiles {
		if !bytes.Equal(s.h.managed[backupDir+"/"+name], data) {
			t.Errorf("managed file %s holds %q, want %q", name, s.h.managed[backupDir+"/"+name], data)
		}
	}
	if u.Config.Name != containerName || u.Config.Create.Image != upFromImage || !slices.Contains(u.Config.Create.Env, "HARNESS_ONLY=1") {
		t.Errorf("journaled configuration %+v, want the original container's", u.Config)
	}
	if len(u.Config.Networks) != 1 || u.Config.Networks[upAppNet] == nil || !reflect.DeepEqual(u.Config.Networks[upAppNet], wantEndpoint) {
		t.Errorf("journaled networks %+v, want only %s with its static address", u.Config.Networks, upAppNet)
	}

	effects := s.effects()
	if len(effects) != 1 {
		t.Fatalf("journal %+v, want one effect", effects)
	}
	e := effects[0]
	if e.Kind != core.EffectModify || e.Resource != "npm.instance:image" || !e.Local || e.InverseLocal || e.State != core.EffectMade ||
		upImageOf(t, e.Before) != upFromImage || e.After == nil || upImageOf(t, *e.After) != Image || e.Post == nil || upImageOf(t, *e.Post) != Image {
		t.Errorf("effect %+v, want a made local modify of npm.instance:image from %s to %s", e, upFromImage, Image)
	}

	def := s.n.instanceUpgrade()
	if err := def.Handler.Verify(ctx, s.env, json.RawMessage(`{}`), out); err != nil {
		t.Errorf("verify after the upgrade: %v", err)
	}
}

func TestUpSameImageStillRecreatesTheInstance(t *testing.T) {
	s := upNewSetup(t, Image, true)
	out, err := s.apply()
	if err != nil {
		t.Fatal(err)
	}
	var got Upgraded
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	c, ok := s.h.docker.container(containerName)
	if got != (Upgraded{FromImage: Image, ToImage: Image}) || !ok || c.ID == s.inst.ContainerID || !c.State.Running || s.phase() != "done" {
		t.Errorf("output %+v, container %+v (found %v), phase %q; want a new running container of the same image and phase done", got, c, ok, s.phase())
	}
}

// TestUpPhasesAreWrittenBeforeTheirChange: at each call that changes the host, the upgrade's row already names the
// phase the call belongs to, so a crash leaves the row naming the last phase that may be half done.
func TestUpPhasesAreWrittenBeforeTheirChange(t *testing.T) {
	s := upNewSetup(t, upFromImage, true)
	var calls []string
	note := func(call string) { calls = append(calls, call+"@"+s.phase()) }
	s.dock.trace = func(call string) {
		if slices.Contains([]string{"StopContainer", "RemoveContainer", "CreateContainer", "StartContainer"}, call) {
			note(call)
		}
	}
	s.files.trace = note
	inner := s.n.d.anon
	s.n.d.anon = func() npmAPI { return upHealthTrace{npmAPI: inner(), note: func() { note("Health") }} }
	if _, err := s.apply(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"StopContainer@stopped",
		"WriteManaged c1-data.tar@archiving",
		"WriteManaged c1-letsencrypt.tar@archiving",
		"RemoveContainer@recreating",
		"CreateContainer@recreating",
		"StartContainer@recreating",
		"Health@waiting",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("calls and the phase recorded at each:\n%q\nwant\n%q", calls, want)
	}
}

func TestUpPullFailureChangesNothing(t *testing.T) {
	s := upNewSetup(t, upFromImage, false)
	s.dock.fail = func(call string, _ int) error {
		if call == "PullImage" {
			return errors.New("pull access denied")
		}
		return nil
	}
	_, err := s.apply()
	if err == nil || !core.IsDefinite(err) {
		t.Fatalf("error %v (definite %v), want a definite refusal", err, core.IsDefinite(err))
	}
	c, ok := s.h.docker.container(s.inst.ContainerID)
	if !ok || !c.State.Running || s.dock.counts["StopContainer"] != 0 || len(s.effects()) != 0 || s.phase() != "no row" {
		t.Errorf("container %+v (found %v), stops %d, effects %+v, phase %q; want the instance untouched and nothing recorded",
			c, ok, s.dock.counts["StopContainer"], s.effects(), s.phase())
	}
}

// TestUpFailureBeforeTheRecreateStartsTheOriginalAgain: until the original container is removed, a failure only has
// to start it again and take back the archives.
func TestUpFailureBeforeTheRecreateStartsTheOriginalAgain(t *testing.T) {
	tests := []struct {
		name    string
		partial bool
		setup   func(s *upSetup)
	}{
		{"the container does not stop", false, func(s *upSetup) {
			s.dock.fail = func(call string, _ int) error {
				if call == "StopContainer" {
					return errors.New("docker: stop timed out")
				}
				return nil
			}
		}},
		{"an archive cannot be written", true, func(s *upSetup) {
			s.files.failWrite = func(name string) error {
				if name == "c1-letsencrypt.tar" {
					return errors.New("disk full")
				}
				return nil
			}
		}},
		{"an archive reads back differently", true, func(s *upSetup) {
			inner := s.n.d.readFile
			s.n.d.readFile = func(name string) ([]byte, error) {
				b, err := inner(name)
				if err != nil {
					return nil, err
				}
				return append(slices.Clone(b), 'x'), nil
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := upNewSetup(t, upFromImage, true)
			tc.setup(s)
			_, err := s.apply()
			if err == nil || core.IsDefinite(err) {
				t.Fatalf("error %v (definite %v), want an error that is not definite: the host changed", err, core.IsDefinite(err))
			}
			c, ok := s.h.docker.container(s.inst.ContainerID)
			if !ok || !c.State.Running || s.dock.counts["CreateContainer"] != 0 || s.dock.counts["RemoveContainer"] != 0 {
				t.Errorf("original %+v (found %v), creates %d, removals %d; want it running again and nothing recreated",
					c, ok, s.dock.counts["CreateContainer"], s.dock.counts["RemoveContainer"])
			}
			for _, name := range []string{"c1-data.tar", "c1-letsencrypt.tar"} {
				if slices.Contains(s.files.removed, name) != tc.partial {
					t.Errorf("removed archives %v, want %s removed: %v (a partial archive is deleted)", s.files.removed, name, tc.partial)
				}
			}
			if row := s.instanceRow(); row.ContainerID != s.inst.ContainerID || row.Image != upFromImage {
				t.Errorf("instance row %+v, want it unchanged", row)
			}
			if s.phase() != "rolled_back" {
				t.Errorf("phase %q, want rolled_back", s.phase())
			}
			if effects := s.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
				t.Errorf("journal %+v, want one skipped effect", effects)
			}
		})
	}
}

// TestUpRollbackRestoresTheOriginal covers a failure after the original container is gone: the new container is
// removed, the volumes are recreated from the verified archives and the original is recreated and started.
func TestUpRollbackRestoresTheOriginal(t *testing.T) {
	t.Run("the new container cannot be created", func(t *testing.T) {
		s := upNewSetup(t, upFromImage, true)
		s.dock.fail = func(call string, n int) error {
			if call == "CreateContainer" && n == 1 {
				return errors.New("docker: no space left on device")
			}
			return nil
		}
		_, err := s.apply()
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v (definite %v), want an error that is not definite", err, core.IsDefinite(err))
		}
		s.wantRestored()
	})

	t.Run("the new container's API does not answer within 120 seconds", func(t *testing.T) {
		s := upNewSetup(t, upFromImage, true)
		s.apiDown()
		start := s.h.clock.Now()
		_, err := s.apply()
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy || core.IsDefinite(err) {
			t.Fatalf("error %v (definite %v), want an npm.unhealthy error that is not definite", err, core.IsDefinite(err))
		}
		if waited := s.h.clock.Now().Sub(start); waited < 120*time.Second || waited >= 126*time.Second {
			t.Errorf("the upgrade waited %v for the API, want 120 s", waited)
		}
		if len(s.dock.ids) != 2 {
			t.Fatalf("created containers %v, want the new one and the recreated original", s.dock.ids)
		}
		s.wantRestored()
	})
}

func TestUpRollbackFailureIsRecorded(t *testing.T) {
	s := upNewSetup(t, upFromImage, true)
	s.apiDown()
	s.dock.fail = func(call string, _ int) error {
		if call == "CopyToContainer /etc" {
			return errors.New("docker: no space left on device")
		}
		return nil
	}
	_, err := s.apply()
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("error %v (definite %v), want an error that is not definite", err, core.IsDefinite(err))
	}
	if u := s.upgrade(); u.Phase != "failed" || u.Result == "" {
		t.Errorf("upgrade row %+v, want phase failed with the reason", u)
	}
	if effects := s.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("journal %+v, want the effect left intended for the engine to settle", effects)
	}
}

// TestUpRollbackVerifiesTheArchivesBeforeRemovingAnything: an archive that no longer matches its checksum stops the
// restore before the new container or the volumes are touched.
func TestUpRollbackVerifiesTheArchivesBeforeRemovingAnything(t *testing.T) {
	s := upNewSetup(t, upFromImage, true)
	s.apiDown()
	inner := s.n.d.readFile
	s.n.d.readFile = func(name string) ([]byte, error) {
		b, err := inner(name)
		if err != nil || s.phase() != "waiting" {
			return b, err
		}
		return append(slices.Clone(b), 'x'), nil
	}
	if _, err := s.apply(); err == nil || core.IsDefinite(err) {
		t.Fatalf("error %v (definite %v), want an error that is not definite", err, core.IsDefinite(err))
	}
	if _, ok := s.h.docker.container(s.dock.ids[0]); !ok || s.dock.counts["RemoveContainer"] != 1 || s.dock.counts["RemoveVolume"] != 0 {
		t.Errorf("new container present %v, container removals %d, volume removals %d; want only the original's removal",
			ok, s.dock.counts["RemoveContainer"], s.dock.counts["RemoveVolume"])
	}
	if u := s.upgrade(); u.Phase != "failed" || u.Result == "" {
		t.Errorf("upgrade row %+v, want phase failed with the reason", u)
	}
}

// TestUpPrunesOldArchiveSets: after a completed upgrade the archive sets of the three newest changes are kept, and so
// is any set a transaction that is not finished refers to.
func TestUpPrunesOldArchiveSets(t *testing.T) {
	ctx := context.Background()
	s := upNewSetup(t, upFromImage, true)
	listing := fstest.MapFS{"readme.txt": {}}
	for _, id := range []string{"a1", "a2", "a3", "a4", "c1"} {
		listing[id+"-data.tar"] = &fstest.MapFile{}
		listing[id+"-letsencrypt.tar"] = &fstest.MapFile{}
	}
	s.n.d.readDir = func(name string) ([]os.DirEntry, error) {
		if name != backupDir {
			return nil, fs.ErrNotExist
		}
		return fs.ReadDir(listing, ".")
	}
	s.h.addChange("a1", "npm.instance.upgrade")
	err := insertUpgrade(ctx, s.h.db, upgradeRow{
		ChangeID: "a1", FromImage: upFromImage, ToImage: Image, UpdatedAt: s.h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	a1Archives := []archiveRef{{Volume: dataVolume, File: "a1-data.tar", SHA256: "x"}, {Volume: certVolume, File: "a1-letsencrypt.tar", SHA256: "x"}}
	if err := setUpgradePhase(ctx, s.h.db, "a1", "waiting", "", a1Archives, s.h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(); err != nil {
		t.Fatal(err)
	}
	removed := slices.Clone(s.files.removed)
	slices.Sort(removed)
	if want := []string{"a2-data.tar", "a2-letsencrypt.tar"}; !slices.Equal(removed, want) {
		t.Errorf("removed %v, want %v: the oldest set that no unfinished transaction refers to", removed, want)
	}
}

func TestUpObserveReadsTheRunningImage(t *testing.T) {
	ctx := context.Background()
	s := upNewSetup(t, upFromImage, true)
	h := s.n.instanceUpgrade().Handler
	e := core.Effect{ChangeID: "c1", Seq: 1, Kind: core.EffectModify, Resource: "npm.instance:image", Local: true}

	obs, err := h.Observe(ctx, s.env, e)
	if err != nil || obs.Matches != 1 || !obs.Exclusive || upImageOf(t, obs.Current) != upFromImage {
		t.Errorf("observation %+v (%v), want one exclusive match in image %s", obs, err, upFromImage)
	}
	if err := s.h.docker.RemoveContainer(ctx, s.inst.ContainerID, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	obs, err = h.Observe(ctx, s.env, e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) != "null" {
		t.Errorf("observation without a container %+v (%v), want state null", obs, err)
	}
	e.Resource = "npm.instance:public"
	if _, err := h.Observe(ctx, s.env, e); err == nil {
		t.Error("observing a resource this action does not change succeeded, want an error")
	}
}

func TestUpVerifyChecksThePinnedImageRuns(t *testing.T) {
	ctx := context.Background()
	s := upNewSetup(t, upFromImage, true)
	h := s.n.instanceUpgrade().Handler
	if err := h.Verify(ctx, s.env, json.RawMessage(`{}`), json.RawMessage("null")); err == nil {
		t.Error("verify before the upgrade succeeded, want an error: the instance runs the older image")
	}
	if _, err := s.apply(); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(ctx, s.env, json.RawMessage(`{}`), json.RawMessage("null")); err != nil {
		t.Errorf("verify without an output, as recovery calls it: %v", err)
	}
	if err := s.h.docker.RemoveContainer(ctx, s.instanceRow().ContainerID, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(ctx, s.env, json.RawMessage(`{}`), json.RawMessage("null")); err == nil {
		t.Error("verify without a container succeeded, want an error")
	}
}
