package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const crTunnel = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

// crWrite is one WriteManaged call.
type crWrite struct {
	Dir, Name string
	Data      []byte
	Mode      fs.FileMode
	UID, GID  int
}

// crFiles wraps the host's FileWriter: it records the managed-file calls and serves the files written to readFile.
type crFiles struct {
	core.FileWriter
	mu        sync.Mutex
	files     map[string][]byte
	writes    []crWrite
	removes   []string
	failWrite error
}

func (f *crFiles) WriteManaged(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	if err := f.FileWriter.WriteManaged(ctx, module, dir, name, data, mode, uid, gid); err != nil {
		return err
	}
	f.files[dir+"/"+name] = bytes.Clone(data)
	f.writes = append(f.writes, crWrite{Dir: dir, Name: name, Data: bytes.Clone(data), Mode: mode, UID: uid, GID: gid})
	return nil
}

func (f *crFiles) RemoveManaged(ctx context.Context, module, dir, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.FileWriter.RemoveManaged(ctx, module, dir, name); err != nil {
		return err
	}
	delete(f.files, dir+"/"+name)
	f.removes = append(f.removes, dir+"/"+name)
	return nil
}

func (f *crFiles) read(name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return bytes.Clone(data), nil
}

// crHost is the agent test host with the recorder of the managed files in place of its FileWriter and in
// agentDeps.readFile.
type crHost struct {
	*agHost
	cfg cf
	rec *crFiles
}

func newCR(t *testing.T) *crHost {
	t.Helper()
	h := newAGHost(t)
	rec := &crFiles{FileWriter: h.env("").Files, files: map[string][]byte{}}
	cfg := h.cf()
	cfg.a.readFile = rec.read
	return &crHost{agHost: h, cfg: cfg, rec: rec}
}

func (h *crHost) env(changeID string) core.Env {
	env := h.agHost.env(changeID)
	env.Files = h.rec
	return env
}

func crInput(tunnel, token string) json.RawMessage {
	raw, err := json.Marshal(ConnectorRunInput{Tunnel: tunnel, Token: token})
	if err != nil {
		panic(err)
	}
	return raw
}

func crApply(t *testing.T, h *crHost, changeID, tunnel string) (Connector, error) {
	t.Helper()
	raw, err := h.cfg.connectorRun().Handler.Apply(context.Background(), h.env(changeID), crInput(tunnel, agToken(tunnel)))
	if err != nil {
		return Connector{}, err
	}
	var out Connector
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

var crMutations = []string{"PullImage", "CreateContainer", "StartContainer", "StopContainer", "RemoveContainer", "ConnectNetwork", "DisconnectNetwork"}

// crMutated are the calls that changed the Engine, in order.
func crMutated(h *crHost) []string {
	var got []string
	for _, c := range h.docker.calls() {
		if slices.Contains(crMutations, c) {
			got = append(got, c)
		}
	}
	return got
}

// crRefused checks err is a Definite refusal with the code and returns it.
func crRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want a Definite %s", err, code)
	}
	return coded
}

func crSeedRow(t *testing.T, h *crHost, tunnel, containerID string) {
	t.Helper()
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(tunnel), []byte(agToken(tunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: tunnel, ContainerID: containerID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func crHasRow(t *testing.T, h *crHost, tunnel string) bool {
	t.Helper()
	_, ok, err := loadConnector(context.Background(), h.env("").DB.Read(), tunnel)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestCrSpec(t *testing.T) {
	def := newCR(t).cfg.connectorRun()
	s := def.Spec
	if s.Name != "cloudflare.connector.run" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || !s.LongRunning || s.ApplyTimeout != 10*time.Minute || s.Summary == "" {
		t.Errorf("spec %+v", s)
	}
	h := def.Handler
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
		t.Errorf("handler lacks a stage: plan %t apply %t verify %t observe %t inverse %t",
			h.Plan != nil, h.Apply != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil)
	}
	if def.InputType != reflect.TypeFor[ConnectorRunInput]() || def.OutputType != reflect.TypeFor[Connector]() {
		t.Errorf("types %v and %v", def.InputType, def.OutputType)
	}
}

func TestCrProductionDepsCanReadFiles(t *testing.T) {
	if productionAgentDeps().readFile == nil {
		t.Error("productionAgentDeps has no readFile")
	}
}

func TestCrPlanShowsTheRun(t *testing.T) {
	ctx := context.Background()
	h := newCR(t)
	token := agToken(crTunnel)
	plan := func(tunnel string) core.PlanOutcome {
		out, err := h.cfg.connectorRun().Handler.Plan(ctx, h.env(""), crInput(tunnel, token))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := plan(crTunnel)
	want := []core.DiffLine{
		{Op: "+", Text: fmt.Sprintf("token file %s/%s.token, mode 0400, owned by uid and gid %d", tokenDir, crTunnel, imageUID)},
		{Op: "+", Text: "container darkpanel-cloudflared-" + crTunnel + " from " + Image},
		{Op: "+", Text: "restart policy unless-stopped; the token file is mounted read-only at /run/darkpanel/token"},
	}
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	for _, l := range out.Diff {
		if strings.Contains(l.Text, token) {
			t.Errorf("the diff line %q carries the token", l.Text)
		}
	}
	other := plan("1b2c3d4e-5f60-4b7c-8d9e-0a1b2c3d4e5f")
	if len(out.StateHash) != 64 || out.StateHash != plan(crTunnel).StateHash || out.StateHash == other.StateHash {
		t.Errorf("state hashes %q and %q: want a stable hash that differs per tunnel", out.StateHash, other.StateHash)
	}
	if got := crMutated(h); len(got) != 0 {
		t.Errorf("the plan changed the Engine: %v", got)
	}
	if len(h.rec.writes) != 0 {
		t.Errorf("the plan wrote files: %+v", h.rec.writes)
	}
}

func TestCrRefuses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		tunnel string
		token  string
		seed   func(t *testing.T, h *crHost)
		path   string // the field the refusal names, "" for none
		detail string // a substring of the refusal's detail
	}{
		{name: "an empty tunnel", tunnel: "", token: "tok", path: "/tunnel"},
		{name: "a name that is no UUID", tunnel: "my-tunnel", token: "tok", path: "/tunnel"},
		{name: "an upper-case UUID", tunnel: strings.ToUpper(crTunnel), token: "tok", path: "/tunnel"},
		{name: "an empty token", tunnel: crTunnel, token: "", path: "/token"},
		{name: "a connector row", tunnel: crTunnel, token: "tok", detail: crTunnel,
			seed: func(t *testing.T, h *crHost) { crSeedRow(t, h, crTunnel, "existing-container") }},
		{name: "a container of the name", tunnel: crTunnel, token: "tok", detail: "darkpanel-cloudflared-" + crTunnel,
			seed: func(_ *testing.T, h *crHost) { h.docker.putContainer(agContainer(connectorName(crTunnel))) }},
	} {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" "+stage, func(t *testing.T) {
				h := newCR(t)
				if tc.seed != nil {
					tc.seed(t, h)
				}
				var err error
				if stage == "plan" {
					_, err = h.cfg.connectorRun().Handler.Plan(ctx, h.env(""), crInput(tc.tunnel, tc.token))
				} else {
					_, err = h.cfg.connectorRun().Handler.Apply(ctx, h.env("c1"), crInput(tc.tunnel, tc.token))
				}
				coded := crRefused(t, err, errcode.InvalidInput)
				if tc.path != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tc.path }) {
					t.Errorf("fields %+v, want one at %s", coded.Fields, tc.path)
				}
				if !strings.Contains(coded.Detail, tc.detail) {
					t.Errorf("detail %q, want it to contain %q", coded.Detail, tc.detail)
				}
				if got := crMutated(h); len(got) != 0 || len(h.rec.writes) != 0 {
					t.Errorf("the refusal changed the host: calls %v, files %+v", got, h.rec.writes)
				}
			})
		}
	}
	t.Run("the Engine down", func(t *testing.T) {
		h := newCR(t)
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := h.cfg.connectorRun().Handler.Plan(ctx, h.env(""), crInput(crTunnel, "tok"))
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
}

func TestCrApply(t *testing.T) {
	ctx := context.Background()
	h := newCR(t)
	token := agToken(crTunnel)
	out, err := crApply(t, h, "c1", crTunnel)
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.docker.InspectContainer(ctx, connectorName(crTunnel))
	if err != nil {
		t.Fatal(err)
	}
	if out.Tunnel != crTunnel || out.Container != connectorName(crTunnel) || out.ContainerID != c.ID || out.State != "running" {
		t.Errorf("output %+v", out)
	}

	t.Run("the container", func(t *testing.T) {
		wantCmd := []string{"tunnel", "--no-autoupdate", "run", "--token-file", "/run/darkpanel/token"}
		wantMounts := []dockerapi.Mount{{Type: "bind", Source: tokenDir + "/" + crTunnel + ".token", Target: "/run/darkpanel/token", ReadOnly: true}}
		if !c.State.Running || c.Config.Image != Image || c.Config.Labels[core.LabelModule] != "cloudflare" ||
			!slices.Equal(c.Config.Cmd, wantCmd) || len(c.Config.Entrypoint) != 0 ||
			c.HostConfig.RestartPolicy.Name != "unless-stopped" || !slices.Equal(c.HostConfig.Mounts, wantMounts) || len(c.HostConfig.Binds) != 0 {
			t.Errorf("container %+v host %+v", c.Config, c.HostConfig)
		}
		if strings.Contains(strings.Join(c.Config.Env, "\n")+strings.Join(c.Config.Cmd, "\n"), token) {
			t.Error("the token is in the container's environment or command line")
		}
		if got, want := crMutated(h), []string{"PullImage", "CreateContainer", "StartContainer"}; !slices.Equal(got, want) {
			t.Errorf("Engine calls %v, want %v", got, want)
		}
	})

	t.Run("the token file", func(t *testing.T) {
		if len(h.rec.writes) != 1 {
			t.Fatalf("%d files written, want 1", len(h.rec.writes))
		}
		w := h.rec.writes[0]
		if w.Dir != tokenDir || w.Name != tokenFileName(crTunnel) || string(w.Data) != token || w.Mode != 0o400 || w.UID != imageUID || w.GID != imageUID {
			t.Errorf("write %+v, want the token in %s/%s with mode 0400 and owner %d:%d", w, tokenDir, tokenFileName(crTunnel), imageUID, imageUID)
		}
	})

	t.Run("the row", func(t *testing.T) {
		env := h.env("")
		row, ok, err := loadConnector(ctx, env.DB.Read(), crTunnel)
		if err != nil || !ok {
			t.Fatalf("row %+v, %v, %v", row, ok, err)
		}
		if row.ContainerID != c.ID || row.ChangeID != "c1" || !row.CreatedAt.Equal(env.Clock.Now()) {
			t.Errorf("row %+v", row)
		}
		if bytes.Contains(row.TokenCT, []byte(token)) {
			t.Error("the stored token is not sealed")
		}
		opened, err := env.Sealer.Open(connectorSealAD(crTunnel), row.TokenCT)
		if err != nil || string(opened) != token {
			t.Errorf("the sealed token opens to %q, %v", opened, err)
		}
	})

	t.Run("the journal", func(t *testing.T) {
		effs, err := h.env("c1").Effects.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		type want struct {
			kind     core.EffectKind
			resource string
			recovery string
			local    bool
			wait     time.Duration
		}
		wants := []want{
			{core.EffectCreate, "cloudflare.tokenfile:" + crTunnel, crTunnel, true, 0},
			{core.EffectCreate, "cloudflare.container:" + c.ID, connectorName(crTunnel), false, 2 * time.Minute},
			{core.EffectCreate, "cloudflare.connector:" + crTunnel, crTunnel, true, 0},
			{core.EffectModify, "cloudflare.container:" + c.ID, "", false, 2 * time.Minute},
		}
		if len(effs) != len(wants) {
			t.Fatalf("%d effects, want %d: %+v", len(effs), len(wants), effs)
		}
		for i, w := range wants {
			e := effs[i]
			if e.Kind != w.kind || e.Resource != w.resource || e.State != core.EffectMade || e.Local != w.local || e.InverseLocal != w.local ||
				e.CommitWait != w.wait || e.ChangeID != "c1" || e.Seq != i+1 || e.Post == nil {
				t.Errorf("effect %d = %+v, want a made %s of %s (local %t, commit wait %s)", i+1, e, w.kind, w.resource, w.local, w.wait)
			}
			switch {
			case w.kind == core.EffectCreate:
				if e.Recovery == nil || e.Recovery.Kind != "name" || e.Recovery.Value != w.recovery || string(e.Before.State) != "null" {
					t.Errorf("effect %d recovery %+v before %s, want name %s and a null before", i+1, e.Recovery, e.Before.State, w.recovery)
				}
			case e.Recovery != nil:
				t.Errorf("effect %d declares a recovery identity", i+1)
			}
		}
		for _, i := range []int{0, 2} {
			if effs[i].After == nil || string(effs[i].After.State) != string(effs[i].Post.State) {
				t.Errorf("local effect %d: after %v, post %s, want them equal", i+1, effs[i].After, effs[i].Post.State)
			}
		}
		var file struct {
			Name string `json:"name"`
			Size int    `json:"size"`
		}
		if err := json.Unmarshal(effs[0].Post.State, &file); err != nil || file.Name != tokenFileName(crTunnel) || file.Size != len(token) {
			t.Errorf("token file state %s (%v), want its name and %d bytes", effs[0].Post.State, err, len(token))
		}
		var before, post map[string]any
		_ = json.Unmarshal(effs[3].Before.State, &before)
		_ = json.Unmarshal(effs[3].Post.State, &post)
		if before["id"] != c.ID || before["running"] != false || post["running"] != true || post["startedAt"] == nil {
			t.Errorf("the start's before %v and post %v, want the container stopped and then running", before, post)
		}
		if _, ok := before["startedAt"]; ok {
			t.Errorf("the start's before %v holds a timestamp no undo can restore", before)
		}
		raw, _ := json.Marshal(effs)
		if bytes.Contains(raw, []byte(token)) {
			t.Error("an effect holds the token")
		}
		for seq := 1; seq <= len(effs); seq++ {
			if s, _ := h.env("c1").Effects.Secret(ctx, seq); len(s) != 0 {
				t.Errorf("effect %d carries a secret", seq)
			}
		}
	})
}

func TestCrApplyPullsOnlyAMissingImage(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(h.docker.calls(), "PullImage") {
			t.Errorf("calls %v: the image was not pulled", h.docker.calls())
		}
		if _, err := h.docker.InspectImage(context.Background(), Image); err != nil {
			t.Errorf("the image is not on the host: %v", err)
		}
	})
	t.Run("present", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(h.docker.calls(), "PullImage") {
			t.Errorf("calls %v: a present image was pulled", h.docker.calls())
		}
	})
}

func TestCrApplyFailures(t *testing.T) {
	ctx := context.Background()
	effects := func(t *testing.T, h *crHost) []core.Effect {
		t.Helper()
		effs, err := h.env("c1").Effects.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return effs
	}
	t.Run("a failed pull changes nothing", func(t *testing.T) {
		h := newCR(t)
		h.docker.Fail("PullImage", errors.New("boom"))
		_, err := crApply(t, h, "c1", crTunnel)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		if len(h.rec.writes) != 0 || len(effects(t, h)) != 0 {
			t.Errorf("files %+v, effects %+v: want none", h.rec.writes, effects(t, h))
		}
	})
	t.Run("a refused create is Definite and keeps the file effect", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.docker.Fail("CreateContainer", &dockerapi.APIError{StatusCode: http.StatusConflict, Message: "name in use"})
		_, err := crApply(t, h, "c1", crTunnel)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		effs := effects(t, h)
		if len(effs) != 2 || effs[0].State != core.EffectMade || effs[1].State != core.EffectSkipped {
			t.Errorf("effects %+v, want the token file made and the container skipped", effs)
		}
		if crHasRow(t, h, crTunnel) {
			t.Error("a row was inserted for a container that was never created")
		}
	})
	t.Run("an unknown create failure stays intended", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.docker.Fail("CreateContainer", errors.New("timeout"))
		_, err := crApply(t, h, "c1", crTunnel)
		effs := effects(t, h)
		if err == nil || core.IsDefinite(err) || len(effs) != 2 || effs[1].State != core.EffectIntended {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite and an intended container effect", err, effs)
		}
	})
	t.Run("a failed file write creates nothing", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.rec.failWrite = errors.New("disk full")
		_, err := crApply(t, h, "c1", crTunnel)
		effs := effects(t, h)
		if err == nil || core.IsDefinite(err) || len(effs) != 1 || effs[0].State != core.EffectIntended {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite and one intended file effect", err, effs)
		}
		if slices.Contains(h.docker.calls(), "CreateContainer") {
			t.Error("a container was created without its token file")
		}
	})
}

// crPullReportsAnError is the host's Engine whose pull streams report msg as an error message.
type crPullReportsAnError struct {
	*agDocker
	msg string
}

func (d crPullReportsAnError) PullImage(_ context.Context, ref string) (pullStream, error) {
	return &agPull{msgs: []dockerapi.PullMessage{{Status: "Pulling from " + ref}, {Error: d.msg}}}, nil
}

// crStates are the journal states of the change's effects, in order.
func crStates(t *testing.T, h *crHost, changeID string) []core.EffectState {
	t.Helper()
	effs, err := h.env(changeID).Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []core.EffectState
	for _, e := range effs {
		got = append(got, e.State)
	}
	return got
}

// crGone fails the test unless the run left neither its container, its token file nor its row.
func crGone(t *testing.T, h *crHost) {
	t.Helper()
	if _, err := h.docker.InspectContainer(context.Background(), connectorName(crTunnel)); !dockerapi.IsNotFound(err) {
		t.Errorf("the container is still there: %v", err)
	}
	if _, err := h.rec.read(tokenDir + "/" + tokenFileName(crTunnel)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the token file is still there: %v", err)
	}
	if crHasRow(t, h, crTunnel) {
		t.Error("the row is still there")
	}
}

// TestCrApplyFailuresAfterTheContainer: a run that fails once its container exists keeps what it made journaled as
// made, never starts a container without its row, and the undo of the made effects removes all of it.
func TestCrApplyFailuresAfterTheContainer(t *testing.T) {
	ctx := context.Background()
	made := core.EffectMade
	t.Run("a refused start is Definite and skipped", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.docker.Fail("StartContainer", &dockerapi.APIError{StatusCode: http.StatusConflict, Message: "port is already allocated"})
		_, err := crApply(t, h, "c1", crTunnel)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		if got, want := crStates(t, h, "c1"), []core.EffectState{made, made, made, core.EffectSkipped}; !slices.Equal(got, want) {
			t.Errorf("effect states %v, want %v", got, want)
		}
		if c, ok := h.docker.container(connectorName(crTunnel)); !ok || c.State.Running || !crHasRow(t, h, crTunnel) {
			t.Errorf("container %+v (%t), row %t: want the created, stopped container and its row", c.State, ok, crHasRow(t, h, crTunnel))
		}
		crUndo(t, h, "c1")
		crGone(t, h)
	})
	t.Run("an unknown start failure stays intended", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.docker.Fail("StartContainer", errors.New("timeout"))
		_, err := crApply(t, h, "c1", crTunnel)
		if err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want a failure that is not Definite", err)
		}
		if got, want := crStates(t, h, "c1"), []core.EffectState{made, made, made, core.EffectIntended}; !slices.Equal(got, want) {
			t.Errorf("effect states %v, want %v", got, want)
		}
		crUndo(t, h, "c1")
		crGone(t, h)
	})
	t.Run("a failed row write starts nothing", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `CREATE TRIGGER cr_fail BEFORE INSERT ON cloudflare_connectors BEGIN SELECT RAISE(ABORT, 'disk I/O error'); END`)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = crApply(t, h, "c1", crTunnel)
		if err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want a failure that is not Definite", err)
		}
		if got, want := crStates(t, h, "c1"), []core.EffectState{made, made, core.EffectIntended}; !slices.Equal(got, want) {
			t.Errorf("effect states %v, want %v", got, want)
		}
		if slices.Contains(h.docker.calls(), "StartContainer") || crHasRow(t, h, crTunnel) {
			t.Errorf("calls %v, row %t: a container was started without its row", h.docker.calls(), crHasRow(t, h, crTunnel))
		}
		crUndo(t, h, "c1")
		crGone(t, h)
	})
	t.Run("a pull stream that reports an error changes nothing", func(t *testing.T) {
		h := newCR(t)
		h.cfg.a.docker = crPullReportsAnError{agDocker: h.docker, msg: "manifest unknown"}
		_, err := crApply(t, h, "c1", crTunnel)
		if err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), "manifest unknown") {
			t.Errorf("error %v, want a Definite failure that carries the Engine's message", err)
		}
		if got := crStates(t, h, "c1"); len(got) != 0 || len(h.rec.writes) != 0 || slices.Contains(h.docker.calls(), "CreateContainer") {
			t.Errorf("effects %v, files %+v, calls %v: want nothing made", got, h.rec.writes, h.docker.calls())
		}
	})
}

func TestCrVerify(t *testing.T) {
	ctx := context.Background()
	token := agToken(crTunnel)
	edit := func(change func(c *dockerapi.ContainerJSON)) func(t *testing.T, h *crHost, id string) {
		return func(t *testing.T, h *crHost, id string) {
			t.Helper()
			c, err := h.docker.InspectContainer(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			change(&c)
			h.docker.putContainer(c)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, h *crHost, id string)
		fails  bool
	}{
		{"as deployed", func(*testing.T, *crHost, string) {}, false},
		{"a restarting container", edit(func(c *dockerapi.ContainerJSON) {
			c.State.Status, c.State.Running, c.State.Restarting = "restarting", false, true
		}), false},
		{"an exited container", edit(func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = "exited", false }), true},
		{"another image", edit(func(c *dockerapi.ContainerJSON) { c.Config.Image = "cloudflare/cloudflared:latest" }), true},
		{"no module label", edit(func(c *dockerapi.ContainerJSON) { c.Config.Labels = map[string]string{} }), true},
		{"another command", edit(func(c *dockerapi.ContainerJSON) { c.Config.Cmd = []string{"tunnel", "run", "--token-file", "/other"} }), true},
		{"the token in the environment", edit(func(c *dockerapi.ContainerJSON) { c.Config.Env = append(c.Config.Env, "TUNNEL_TOKEN="+token) }), true},
		{"the token in the command", edit(func(c *dockerapi.ContainerJSON) { c.Config.Cmd = append(slices.Clone(c.Config.Cmd), token) }), true},
		{"a writable mount", edit(func(c *dockerapi.ContainerJSON) {
			c.HostConfig.Mounts = slices.Clone(c.HostConfig.Mounts)
			c.HostConfig.Mounts[0].ReadOnly = false
		}), true},
		{"no mount", edit(func(c *dockerapi.ContainerJSON) { c.HostConfig.Mounts = nil }), true},
		{"no restart policy", edit(func(c *dockerapi.ContainerJSON) { c.HostConfig.RestartPolicy.Name = "no" }), true},
		{"no row", func(t *testing.T, h *crHost, _ string) {
			t.Helper()
			env := h.env("")
			if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteConnector(ctx, tx, crTunnel) }); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a row of another container", func(t *testing.T, h *crHost, _ string) {
			t.Helper()
			env := h.env("")
			err := env.DB.Write(ctx, func(tx *sql.Tx) error {
				if err := deleteConnector(ctx, tx, crTunnel); err != nil {
					return err
				}
				return insertConnector(ctx, tx, connectorRow{TunnelID: crTunnel, ContainerID: "another", TokenCT: []byte("x"), ChangeID: "c1", CreatedAt: env.Clock.Now()})
			})
			if err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCR(t)
			out, err := crApply(t, h, "c1", crTunnel)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(t, h, out.ContainerID)
			rawOut, _ := json.Marshal(out)
			err = h.cfg.connectorRun().Handler.Verify(ctx, h.env("c1"), crInput(crTunnel, token), rawOut)
			switch {
			case tc.fails && err == nil:
				t.Error("Verify passed")
			case !tc.fails && err != nil:
				t.Errorf("Verify: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), token) {
				t.Error("the error carries the token")
			}
		})
	}
	t.Run("the container is gone", func(t *testing.T) {
		h := newCR(t)
		out, err := crApply(t, h, "c1", crTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.docker.RemoveContainer(ctx, out.ContainerID, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		rawOut, _ := json.Marshal(out)
		if err := h.cfg.connectorRun().Handler.Verify(ctx, h.env("c1"), crInput(crTunnel, token), rawOut); err == nil {
			t.Error("Verify passed")
		}
	})
}

// crUndo undoes the made effects of the change as the engine does: in reverse order, each checked against its
// journaled post-state before and against its undo target after.
func crUndo(t *testing.T, h *crHost, changeID string) {
	t.Helper()
	ctx := context.Background()
	def := h.cfg.connectorRun()
	env := h.env(changeID)
	effs, err := env.Effects.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(effs) - 1; i >= 0; i-- {
		e := effs[i]
		if e.ChangeID != changeID || e.State != core.EffectMade {
			continue
		}
		obs, err := def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe effect %d (%s): %v", e.Seq, e.Resource, err)
		}
		if string(obs.Current.State) != string(e.Post.State) {
			t.Errorf("effect %d (%s): observed %s before its undo, journaled %s", e.Seq, e.Resource, obs.Current.State, e.Post.State)
		}
		e.State = core.EffectUndoing
		if _, err := def.Handler.Inverse(ctx, env, e); err != nil {
			t.Fatalf("inverse of effect %d (%s): %v", e.Seq, e.Resource, err)
		}
		obs, err = def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe effect %d (%s) after its undo: %v", e.Seq, e.Resource, err)
		}
		want := "null"
		if e.Kind != core.EffectCreate {
			want = string(e.Before.State)
		}
		if string(obs.Current.State) != want {
			t.Errorf("effect %d (%s): observed %s after its undo, want %s", e.Seq, e.Resource, obs.Current.State, want)
		}
	}
}

func TestCrUndo(t *testing.T) {
	ctx := context.Background()
	t.Run("everything the change made", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		crUndo(t, h, "c1")
		if _, err := h.docker.InspectContainer(ctx, connectorName(crTunnel)); !dockerapi.IsNotFound(err) {
			t.Errorf("the container is still there: %v", err)
		}
		if _, err := h.rec.read(tokenDir + "/" + tokenFileName(crTunnel)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the token file is still there: %v", err)
		}
		if crHasRow(t, h, crTunnel) {
			t.Error("the row is still there")
		}
		if !slices.Contains(h.docker.calls(), "StopContainer") {
			t.Errorf("calls %v: the started container was not stopped", h.docker.calls())
		}
	})
	t.Run("an undo run again", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		crUndo(t, h, "c1")
		env := h.env("c1")
		effs, _ := env.Effects.List(ctx)
		for i := len(effs) - 1; i >= 0; i-- {
			e := effs[i]
			e.State = core.EffectUndoing
			if _, err := h.cfg.connectorRun().Handler.Inverse(ctx, env, e); err != nil {
				t.Errorf("inverse of effect %d again: %v", e.Seq, err)
			}
		}
	})
	t.Run("a row of another change is kept", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		env := h.env("c1")
		effs, _ := env.Effects.List(ctx)
		e := effs[2]
		e.ChangeID, e.State = "c2", core.EffectUndoing
		_, err := h.cfg.connectorRun().Handler.Inverse(ctx, env, e)
		_ = crRefused(t, err, errcode.InvalidInput)
		if !crHasRow(t, h, crTunnel) {
			t.Error("the row of another change was deleted")
		}
	})
}

func TestCrObserveCreations(t *testing.T) {
	ctx := context.Background()
	creations := []core.Effect{
		{Kind: core.EffectCreate, Resource: "cloudflare.tokenfile:?" + crTunnel},
		{Kind: core.EffectCreate, Resource: "cloudflare.container:?" + connectorName(crTunnel)},
		{Kind: core.EffectCreate, Resource: "cloudflare.connector:?" + crTunnel},
	}
	h := newCR(t)
	def := h.cfg.connectorRun()
	for _, e := range creations {
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
			t.Errorf("%s before the apply: matches %d state %s (%v), want none and null", e.Resource, obs.Matches, obs.Current.State, err)
		}
	}
	if _, err := crApply(t, h, "c1", crTunnel); err != nil {
		t.Fatal(err)
	}
	for _, e := range creations {
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		want := strings.Replace(e.Resource, "?", "", 1)
		if strings.HasPrefix(e.Resource, "cloudflare.container:") {
			c, _ := h.docker.InspectContainer(ctx, connectorName(crTunnel))
			want = "cloudflare.container:" + c.ID
		}
		if err != nil || obs.Matches != 1 || obs.Resource != want || !obs.Exclusive {
			t.Errorf("%s after the apply: matches %d resource %q exclusive %t (%v), want one match of %s", e.Resource, obs.Matches, obs.Resource, obs.Exclusive, err, want)
		}
	}
}

func TestCrRemoveConnector(t *testing.T) {
	ctx := context.Background()
	t.Run("everything", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || !removed {
			t.Fatalf("removed %t, %v", removed, err)
		}
		if _, err := h.docker.InspectContainer(ctx, connectorName(crTunnel)); !dockerapi.IsNotFound(err) {
			t.Errorf("the container is still there: %v", err)
		}
		if !slices.Contains(h.rec.removes, tokenDir+"/"+tokenFileName(crTunnel)) || crHasRow(t, h, crTunnel) {
			t.Errorf("removed files %v, row present %t", h.rec.removes, crHasRow(t, h, crTunnel))
		}
		again, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || again {
			t.Errorf("removing again: %t, %v; want false and no error", again, err)
		}
	})
	t.Run("nothing to remove", func(t *testing.T) {
		h := newCR(t)
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || removed {
			t.Errorf("removed %t, %v; want false and no error", removed, err)
		}
	})
	t.Run("only the row", func(t *testing.T) {
		h := newCR(t)
		crSeedRow(t, h, crTunnel, "gone")
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || !removed || crHasRow(t, h, crTunnel) {
			t.Errorf("removed %t, %v, row present %t", removed, err, crHasRow(t, h, crTunnel))
		}
	})
	t.Run("only the container", func(t *testing.T) {
		h := newCR(t)
		h.docker.putContainer(agContainer(connectorName(crTunnel), agLabelled(map[string]string{core.LabelModule: ID})))
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if _, ierr := h.docker.InspectContainer(ctx, connectorName(crTunnel)); err != nil || !removed || !dockerapi.IsNotFound(ierr) {
			t.Errorf("removed %t, %v, inspect %v", removed, err, ierr)
		}
	})
	for _, foreign := range []struct {
		name   string
		labels map[string]string
	}{
		{"an unlabelled container is refused", nil},
		{"a container of another module is refused", map[string]string{core.LabelModule: "npm"}},
	} {
		t.Run(foreign.name, func(t *testing.T) {
			h := newCR(t)
			crSeedRow(t, h, crTunnel, "existing-container")
			h.docker.putContainer(agContainer(connectorName(crTunnel), agRunning, agLabelled(foreign.labels)))
			removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
			_ = crRefused(t, err, errcode.DockerOwned)
			if removed || slices.Contains(h.docker.calls(), "RemoveContainer") {
				t.Errorf("removed %t, calls %v: want the container left alone", removed, h.docker.calls())
			}
			if _, ierr := h.docker.InspectContainer(ctx, connectorName(crTunnel)); ierr != nil {
				t.Errorf("the container is gone: %v", ierr)
			}
			if len(h.rec.removes) != 0 || !crHasRow(t, h, crTunnel) {
				t.Errorf("removed files %v, row present %t: want the refusal to remove nothing", h.rec.removes, crHasRow(t, h, crTunnel))
			}
		})
	}
}

// TestCrUndoLeavesAForeignContainer: the run's recovery does not adopt a container of the connector's name the module
// did not label, and the undo of the container's creation refuses, not removes, one that has lost the label.
func TestCrUndoLeavesAForeignContainer(t *testing.T) {
	ctx := context.Background()
	for _, foreign := range []struct {
		name   string
		labels map[string]string
	}{
		{"unlabelled", nil},
		{"another module's", map[string]string{core.LabelModule: "npm"}},
	} {
		t.Run(foreign.name+" container at recovery", func(t *testing.T) {
			h := newCR(t)
			h.docker.putContainer(agContainer(connectorName(crTunnel), agLabelled(foreign.labels)))
			e := core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.container:?" + connectorName(crTunnel), State: core.EffectIntended}
			obs, err := h.cfg.connectorRun().Handler.Observe(ctx, h.env("c1"), e)
			if err != nil || obs.Matches != 0 || obs.Resource != "" || string(obs.Current.State) != "null" {
				t.Errorf("matches %d resource %q state %s (%v), want no match and the null state", obs.Matches, obs.Resource, obs.Current.State, err)
			}
		})
		t.Run(foreign.name+" container at undo", func(t *testing.T) {
			h := newCR(t)
			out, err := crApply(t, h, "c1", crTunnel)
			if err != nil {
				t.Fatal(err)
			}
			c, err := h.docker.InspectContainer(ctx, out.ContainerID)
			if err != nil {
				t.Fatal(err)
			}
			c.Config.Labels = foreign.labels
			h.docker.putContainer(c)
			e := core.Effect{ChangeID: "c1", Kind: core.EffectCreate, Resource: "cloudflare.container:" + out.ContainerID, State: core.EffectUndoing}
			_, err = h.cfg.connectorRun().Handler.Inverse(ctx, h.env("c1"), e)
			_ = crRefused(t, err, errcode.DockerOwned)
			if slices.Contains(h.docker.calls(), "RemoveContainer") {
				t.Error("the undo removed a container the module did not label")
			}
			if _, err := h.docker.InspectContainer(ctx, out.ContainerID); err != nil {
				t.Errorf("the container is gone: %v", err)
			}
		})
	}
}
