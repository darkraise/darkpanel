package derp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	// installPoll is how often an install looks at derper while it waits for the unit and its certificate.
	installPoll = time.Second
	// installUseradd and installUserdel are the programs that add and delete the module's system user.
	installUseradd = "/usr/sbin/useradd"
	installUserdel = "/usr/sbin/userdel"
	// installWantsLink is the link Enable makes for the unit's WantedBy=multi-user.target and Disable removes.
	// The unit API reports no enablement, so Observe reads this link instead.
	installWantsLink = "/etc/systemd/system/multi-user.target.wants/" + server.UnitName
)

// serverInstall is derp.server.install: it fetches the derper of this agent's release and installs it as a
// systemd service run by its own system user. Its undo removes what it made.
func (d dp) serverInstall() core.ActionDef {
	return core.DefineTask[ServerInstallInput, ServerInstalled](core.ActionSpec{
		Name: ID + ".server.install", Summary: "Install derper on this host as a systemd service run by its own system user.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		ApplyTimeout: 15 * time.Minute, Available: d.available,
	}, installHandler{d: d})
}

// installHandler is derp.server.install's handler. It has no Recover: the engine's task sequence settles an
// interrupted install through Observe.
type installHandler struct{ d dp }

// config is the server configuration the input asks for; an absent VerifyClients means true.
func (installHandler) config(in ServerInstallInput) server.Config {
	return server.Config{Mode: in.Mode, HostName: in.HostName, VerifyClients: in.VerifyClients == nil || *in.VerifyClients}
}

// precheck refuses an input or a host the install cannot proceed on, before anything changes.
func (h installHandler) precheck(ctx context.Context, env core.Env, in ServerInstallInput) (server.Config, error) {
	cfg := h.config(in)
	if err := server.CheckConfig(cfg); err != nil {
		return server.Config{}, core.Definite(err)
	}
	_, installed, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil {
		return server.Config{}, err
	}
	if installed {
		return server.Config{}, core.Definite(errcode.New(errcode.InvalidInput, "a DERP server is already installed on this host"))
	}
	if err := h.checkPorts(); err != nil {
		return server.Config{}, err
	}
	if err := h.checkForeign(ctx, env); err != nil {
		return server.Config{}, err
	}
	return cfg, nil
}

// checkPorts refuses a host where another program listens on a port derper serves.
func (h installHandler) checkPorts() error {
	sockets, err := h.d.d.sockets()
	if err != nil {
		return err
	}
	var busy []string
	for _, s := range sockets {
		port := s.Local.Port()
		derper := (s.Proto == netstat.ProtoTCP && (port == server.HTTPPort || port == server.HTTPSPort)) ||
			(s.Proto == netstat.ProtoUDP && port == server.STUNPort)
		if !derper || !s.Listening() {
			continue
		}
		who := "an unknown process"
		if len(s.Processes) > 0 {
			who = s.Processes[0].Comm
		}
		busy = append(busy, fmt.Sprintf("%d/%s is in use by %s", port, s.Proto, who))
	}
	if len(busy) > 0 {
		return core.Definite(errcode.New(errcode.DERPPortInUse, strings.Join(busy, "; ")))
	}
	return nil
}

// checkForeign refuses a user, binary, unit file or service directory that exists and is not the module's. One the
// module owns is the leftover of an interrupted install, which the install takes over.
func (h installHandler) checkForeign(ctx context.Context, env core.Env) error {
	foreign := func(path, detail string) error {
		owned, err := env.Files.Owns(ctx, ID, path)
		if err != nil {
			return err
		}
		if owned {
			return nil
		}
		return core.Definite(errcode.New(errcode.InvalidInput, detail))
	}
	_, _, exists, err := h.d.d.lookupUser(server.ServiceUser)
	if err != nil {
		return err
	}
	if exists {
		if err := foreign(server.ServiceUser, "the system user "+server.ServiceUser+" already exists and is not Darkpanel's"); err != nil {
			return err
		}
	}
	for _, p := range []struct{ path, what string }{
		{server.BinaryPath, "file"}, {server.UnitPath, "file"}, {server.StateDir, "directory"},
	} {
		if !h.exists(p.path) {
			continue
		}
		if err := foreign(p.path, "the "+p.what+" "+p.path+" already exists and is not Darkpanel's"); err != nil {
			return err
		}
	}
	return nil
}

// exists reports whether path exists. Reading a directory fails with an error other than fs.ErrNotExist, so a
// directory exists too.
func (h installHandler) exists(path string) bool {
	_, err := h.d.d.readFile(path)
	return !errors.Is(err, fs.ErrNotExist)
}

func (h installHandler) Plan(ctx context.Context, env core.Env, in ServerInstallInput) (core.PlanOutcome, error) {
	cfg, err := h.precheck(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	add := func(text string) core.DiffLine { return core.DiffLine{Op: "+", Text: text} }
	diff := []core.DiffLine{
		add("user " + server.ServiceUser),
		add("file " + server.BinaryPath + " (derper " + artifact.DerperVersion + " for " + h.d.d.arch + ")"),
		add("file " + server.UnitPath),
		add("directory " + server.StateDir + " (owned by " + server.ServiceUser + ")"),
		add("unit " + server.UnitName + ", enabled and started"),
	}
	if cfg.Mode == server.ModeLetsEncrypt {
		diff = append(diff, add("certificate for "+cfg.HostName+" from Let's Encrypt; derper requests and renews it, and port 80 serves the challenge"))
	} else {
		diff = append(diff, add("certificate for "+cfg.HostName+", self-signed by derper at its first start; clients pin it"))
	}
	if !cfg.VerifyClients {
		diff = append(diff, core.DiffLine{Op: " ", Text: "! client verification is off: any node that knows the region can relay through it"})
	}
	state := []string{
		"mode=" + cfg.Mode, "host=" + cfg.HostName, "verify=" + strconv.FormatBool(cfg.VerifyClients),
		"version=" + artifact.DerperVersion, "arch=" + h.d.d.arch,
	}
	sum := sha256.Sum256([]byte(strings.Join(state, "\n")))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Task runs in the transient unit. It fetches derper before it journals anything, so a failed fetch changes
// nothing, then journals each creation before its host change.
func (h installHandler) Task(ctx context.Context, env core.Env, in ServerInstallInput) (ServerInstalled, error) {
	cfg, err := h.precheck(ctx, env, in)
	if err != nil {
		return ServerInstalled{}, err
	}
	bin, sha, err := h.d.d.fetch(ctx, h.d.d.version, artifact.DerperVersion, h.d.d.arch)
	if err != nil {
		return ServerInstalled{}, core.Definite(err)
	}
	// The precheck admits an existing user only when the module owns it; that user stays as it is.
	_, _, userExists, err := h.d.d.lookupUser(server.ServiceUser)
	if err != nil {
		return ServerInstalled{}, err
	}
	if !userExists {
		err := h.create(ctx, env, "user", server.ServiceUser, h.existsDoc(), func() error {
			entry := core.OwnedEntry{Path: server.ServiceUser, Module: ID, Kind: core.OwnedUser, CreatedAt: env.Clock.Now()}
			if err := platform.NewOwnership(env.DB).Record(ctx, entry); err != nil {
				return err
			}
			return installRun(ctx, env, installUseradd, "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", server.ServiceUser)
		})
		if err != nil {
			return ServerInstalled{}, err
		}
	}
	if err := h.create(ctx, env, "file", server.BinaryPath, h.fileDoc(bin), func() error {
		return env.Files.WriteFile(ctx, ID, server.BinaryPath, bin, 0o755)
	}); err != nil {
		return ServerInstalled{}, err
	}
	unit := server.UnitFile(cfg)
	if err := h.create(ctx, env, "file", server.UnitPath, h.fileDoc(unit), func() error {
		return env.Files.WriteFile(ctx, ID, server.UnitPath, unit, 0o644)
	}); err != nil {
		return ServerInstalled{}, err
	}
	if err := h.create(ctx, env, "dir", server.StateDir, h.existsDoc(), func() error {
		return env.Files.EnsureServiceDir(ctx, ID, server.StateDir)
	}); err != nil {
		return ServerInstalled{}, err
	}
	if err := h.create(ctx, env, "unit", server.UnitName, h.unitDoc(), func() error {
		if err := env.Units.Reload(ctx); err != nil {
			return err
		}
		if err := env.Units.Enable(ctx, server.UnitName); err != nil {
			return err
		}
		return env.Units.Start(ctx, server.UnitName)
	}); err != nil {
		return ServerInstalled{}, err
	}
	pin, notAfter, err := h.await(ctx, env, cfg)
	if err != nil {
		return ServerInstalled{}, err
	}
	now := env.Clock.Now()
	row := localServer{
		ChangeID: env.ChangeID, Mode: cfg.Mode, HostName: cfg.HostName, VerifyClients: cfg.VerifyClients,
		DerperVersion: artifact.DerperVersion, BinarySHA256: sha, CertName: pin, CertNotAfter: notAfter,
		InstalledAt: now, UpdatedAt: now,
	}
	if err := h.create(ctx, env, "server", "this", h.serverDoc(cfg.Mode, cfg.HostName, cfg.VerifyClients), func() error {
		return insertLocalServer(ctx, env.DB, row)
	}); err != nil {
		return ServerInstalled{}, err
	}
	out := ServerInstalled{DerperVersion: artifact.DerperVersion, CertName: pin}
	if cfg.Mode == server.ModeIP {
		out.CertNotAfter = &notAfter
	}
	return out, nil
}

// create journals the creation of the derp.<noun> named identity, makes it with change, and records it made in
// doc. A failed change leaves the effect intended: the engine settles it through Observe.
func (installHandler) create(ctx context.Context, env core.Env, noun, identity string, doc core.StateDoc, change func() error) error {
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: ID + "." + noun + ":?" + identity,
		Recovery: &core.RecoveryID{Kind: "name", Value: identity}, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage("null")}, After: &doc,
	}, nil)
	if err != nil {
		return err
	}
	if err := change(); err != nil {
		return err
	}
	return env.Effects.Made(ctx, seq, ID+"."+noun+":"+identity, doc)
}

// await polls derper at once and then every installPoll until its unit is active and, in ip mode, its certificate
// parses, for at most certWait. It returns the certificate's pin and expiry; both are zero in letsencrypt mode.
func (h installHandler) await(ctx context.Context, env core.Env, cfg server.Config) (pin string, notAfter time.Time, err error) {
	crt, _, ipMode := server.CertFiles(cfg)
	deadline := env.Clock.Now().Add(certWait)
	for {
		st, err := env.Units.State(ctx, server.UnitName)
		if err != nil {
			return "", time.Time{}, err
		}
		active := st.ActiveState == "active"
		if active && !ipMode {
			return "", time.Time{}, nil
		}
		if active {
			der, at, ok, err := h.readCert(crt)
			if err != nil {
				return "", time.Time{}, err
			}
			if ok {
				return server.Pin(der), at, nil
			}
		}
		if !env.Clock.Now().Before(deadline) {
			detail := fmt.Sprintf("derper did not create its certificate within %s", certWait)
			if !active {
				detail = fmt.Sprintf("%s did not become active within %s", server.UnitName, certWait)
			}
			return "", time.Time{}, errcode.New(errcode.DERPVerifyFailed, detail)
		}
		tick := make(chan struct{})
		timer := env.Clock.AfterFunc(installPoll, func() { close(tick) })
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return "", time.Time{}, fmt.Errorf("derp: wait for derper: %w", ctx.Err())
		}
	}
}

// readCert reads the ip certificate derper keeps as name in CertDir; ok is false while it is absent or does not
// parse, as derper may not have finished writing it.
func (h installHandler) readCert(name string) (der []byte, notAfter time.Time, ok bool, err error) {
	path := server.CertDir + "/" + name
	b, err := h.d.d.readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, fmt.Errorf("derp: read %s: %w", path, err)
	}
	der, notAfter, err = server.ParseCert(b)
	if err != nil {
		return nil, time.Time{}, false, nil
	}
	return der, notAfter, true, nil
}

// Verify checks the host from the input alone, as recovery calls it without an output: the server row of the
// input's mode and host name exists and derper's unit is active.
func (h installHandler) Verify(ctx context.Context, env core.Env, in ServerInstallInput, _ ServerInstalled) error {
	row, ok, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("derp: no DERP server is recorded on this host")
	}
	if row.Mode != in.Mode || row.HostName != in.HostName {
		return fmt.Errorf("derp: the recorded server is %s in %s mode, not %s in %s mode", row.HostName, row.Mode, in.HostName, in.Mode)
	}
	st, err := env.Units.State(ctx, server.UnitName)
	if err != nil {
		return err
	}
	if st.ActiveState != "active" {
		return fmt.Errorf("derp: %s is %s, not active", server.UnitName, st.ActiveState)
	}
	return nil
}

// target is the noun and identity of the object effect e names. A creation not yet made names its object by
// recovery identity.
func (installHandler) target(e core.Effect) (noun, identity string, err error) {
	noun, identity, _ = strings.Cut(e.Resource, ":")
	identity = strings.TrimPrefix(identity, "?")
	switch {
	case noun == ID+".user" && identity == server.ServiceUser,
		noun == ID+".file" && (identity == server.BinaryPath || identity == server.UnitPath),
		noun == ID+".dir" && identity == server.StateDir,
		noun == ID+".unit" && identity == server.UnitName,
		noun == ID+".server" && identity == "this":
		return strings.TrimPrefix(noun, ID+"."), identity, nil
	}
	return "", "", fmt.Errorf("derp: %q is not a resource derp.server.install makes", e.Resource)
}

func (h installHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, identity, err := h.target(e)
	if err != nil {
		return core.Observation{}, err
	}
	doc, present, err := h.current(ctx, env, noun, identity)
	if err != nil {
		return core.Observation{}, err
	}
	if !present {
		return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Matches: 0, Exclusive: true}, nil
	}
	return core.Observation{Current: doc, Resource: ID + "." + noun + ":" + identity, Matches: 1, Exclusive: true}, nil
}

// current reads the object of noun and identity: its state document, and whether it is present.
func (h installHandler) current(ctx context.Context, env core.Env, noun, identity string) (core.StateDoc, bool, error) {
	switch noun {
	case "user":
		_, _, ok, err := h.d.d.lookupUser(identity)
		return h.existsDoc(), ok, err
	case "file":
		b, err := h.d.d.readFile(identity)
		if errors.Is(err, fs.ErrNotExist) {
			return core.StateDoc{}, false, nil
		}
		if err != nil {
			return core.StateDoc{}, false, fmt.Errorf("derp: read %s: %w", identity, err)
		}
		return h.fileDoc(b), true, nil
	case "dir":
		return h.existsDoc(), h.exists(identity), nil
	case "unit":
		// The undo compares this with Post before the inverse and with null after it, so it must not depend on
		// whether derper runs: an enabled unit that failed to start still matches, a disabled one is absent.
		return h.unitDoc(), h.exists(installWantsLink), nil
	default:
		row, ok, err := loadLocalServer(ctx, env.DB.Read())
		return h.serverDoc(row.Mode, row.HostName, row.VerifyClients), ok, err
	}
}

// Inverse undoes one made effect and tolerates an object that is already gone.
func (h installHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	noun, identity, err := h.target(e)
	if err != nil {
		return core.InverseResult{}, err
	}
	switch noun {
	case "server":
		err = deleteLocalServer(ctx, env.DB)
	case "unit":
		err = h.removeUnit(ctx, env)
	case "file":
		err = env.Files.Remove(ctx, ID, identity)
		if err == nil && identity == server.UnitPath {
			err = env.Units.Reload(ctx)
		}
	case "dir":
		err = env.Files.RemoveServiceDir(ctx, ID, identity)
	default:
		err = h.removeUser(ctx, env)
	}
	return core.InverseResult{}, err
}

// removeUnit stops derper's unit when it runs and disables it; a unit systemd no longer loads is left alone.
func (installHandler) removeUnit(ctx context.Context, env core.Env) error {
	st, err := env.Units.State(ctx, server.UnitName)
	if err != nil || st.LoadState != "loaded" {
		return err
	}
	if st.ActiveState == "active" || st.ActiveState == "activating" {
		if err := env.Units.Stop(ctx, server.UnitName); err != nil {
			return err
		}
	}
	return env.Units.Disable(ctx, server.UnitName)
}

// removeUser deletes the module's user when it exists and forgets it in every case.
func (h installHandler) removeUser(ctx context.Context, env core.Env) error {
	_, _, exists, err := h.d.d.lookupUser(server.ServiceUser)
	if err != nil {
		return err
	}
	if exists {
		if err := installRun(ctx, env, installUserdel, server.ServiceUser); err != nil {
			return err
		}
	}
	return platform.NewOwnership(env.DB).Forget(ctx, ID, server.ServiceUser, "")
}

// doc is the state document of v, a struct of strings and booleans, which always encodes.
func (installHandler) doc(v any) core.StateDoc {
	b, _ := json.Marshal(v)
	return core.StateDoc{State: b}
}

// existsDoc is the state of the user and of the service directory.
func (h installHandler) existsDoc() core.StateDoc {
	return h.doc(struct {
		Exists bool `json:"exists"`
	}{true})
}

// fileDoc is the state of a file holding b.
func (h installHandler) fileDoc(b []byte) core.StateDoc {
	sum := sha256.Sum256(b)
	return h.doc(struct {
		SHA256 string `json:"sha256"`
	}{hex.EncodeToString(sum[:])})
}

// unitDoc is the state of derper's enabled unit.
func (h installHandler) unitDoc() core.StateDoc {
	return h.doc(struct {
		Enabled bool `json:"enabled"`
	}{true})
}

// serverDoc is the state of the derp_server row.
func (h installHandler) serverDoc(mode, hostName string, verifyClients bool) core.StateDoc {
	return h.doc(struct {
		Mode          string `json:"mode"`
		HostName      string `json:"hostName"`
		VerifyClients bool   `json:"verifyClients"`
	}{mode, hostName, verifyClients})
}

// installRun runs one allowlisted vector through env.Exec and turns a failure to run it, or a non-zero exit, into
// an error naming path and the first line of its stderr.
func installRun(ctx context.Context, env core.Env, path string, args ...string) error {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: path, Args: args})
	if err != nil {
		return fmt.Errorf("derp: run %s: %w", path, err)
	}
	if res.ExitCode == 0 {
		return nil
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(res.Stderr)), "\n")
	if first = strings.TrimSpace(first); first != "" {
		return fmt.Errorf("derp: %s exited %d: %s", path, res.ExitCode, first)
	}
	return fmt.Errorf("derp: %s exited %d", path, res.ExitCode)
}
