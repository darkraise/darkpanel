package npm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/mail"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	// deployPoll is the pause between two probes of NPM while it starts.
	deployPoll = 2 * time.Second
	// deployReady is how long NPM may take to answer after its container starts.
	deployReady = 120 * time.Second
	// deployStop is the stop grace of the undo of the container's start.
	deployStop = 30 * time.Second
	// deployMinMajor is the oldest Docker major version that loads the raw-table protection.
	deployMinMajor = 28
	// deployDERPUnit is the derp module's unit file. An installed but stopped DERP server holds no listener, yet it
	// takes ports 80 and 443 when it starts.
	deployDERPUnit = "/etc/systemd/system/darkpanel-derper.service"
)

// deployReservedDomains are the names that are no public registrable domain (RFC 2606, RFC 6761, RFC 6762, RFC 8375 and
// the common private suffixes). The standard library has no public-suffix list, so refusing these is the module's
// approximation of an address Let's Encrypt can register.
var deployReservedDomains = []string{"invalid", "localhost", "local", "internal", "lan", "home", "corp", "test", "example",
	"home.arpa", "example.com", "example.net", "example.org"}

// instanceDeploy is npm.instance.deploy: it creates the network, the volumes and the container of the module's NPM
// instance, starts it and creates NPM's administrator with a generated password kept sealed on the host.
func (n nm) instanceDeploy() core.ActionDef {
	return core.DefineMutating[DeployInput, Deployed](core.ActionSpec{
		Name:         "npm.instance.deploy",
		Summary:      "Deploy Nginx Proxy Manager in a Darkpanel-owned container and create its administrator.",
		Version:      1,
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{lockKey},
		ApplyTimeout: 20 * time.Minute,
		LongRunning:  true,
		Available:    n.deployAvailable,
	}, deployHandler{n: n})
}

// deployHandler is the handler of npm.instance.deploy.
type deployHandler struct{ n nm }

// deployAvailable requires Docker 28.0 or later with its raw-table protection loaded.
func (n nm) deployAvailable(ctx context.Context, env core.Env) error {
	info, err := n.d.info(ctx)
	if err != nil {
		return engineCoded(err)
	}
	major, _, _ := strings.Cut(info.ServerVersion, ".")
	if v, err := strconv.ParseUint(major, 10, 32); err != nil || v < deployMinMajor {
		found := "an unknown Docker version"
		if info.ServerVersion != "" {
			found = "Docker " + info.ServerVersion
		}
		return errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("Nginx Proxy Manager needs Docker %d.0 or later; this host runs %s", deployMinMajor, found))
	}
	protected, err := n.d.raw(ctx, env)
	if err != nil {
		return err
	}
	if !protected {
		return errcode.New(errcode.ModuleUnavailable, "Docker's raw-table protection is not loaded on this host")
	}
	return nil
}

// checkAcmeEmail refuses an acmeEmail Let's Encrypt cannot register: anything but a bare address, and an address whose
// domain is an IP literal, has no dot, has a last label that does not start with a letter, or is a reserved name or
// under one.
func checkAcmeEmail(email string) error {
	invalid := func(msg string) error {
		return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/acmeEmail", Message: msg}}})
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return invalid("acmeEmail must be one bare email address, without a display name")
	}
	domain := strings.ToLower(addr.Address[strings.LastIndexByte(addr.Address, '@')+1:])
	if _, err := netip.ParseAddr(domain); err == nil || strings.HasPrefix(domain, "[") {
		return invalid("acmeEmail's domain must be a domain name, not an IP address")
	}
	if !strings.Contains(domain, ".") {
		return invalid(fmt.Sprintf("acmeEmail's domain %s is not a public domain: it has no dot", domain))
	}
	if last := domain[strings.LastIndexByte(domain, '.')+1:]; last == "" || last[0] < 'a' || last[0] > 'z' {
		return invalid(fmt.Sprintf("acmeEmail's domain %s is not a public domain: its last label must start with a letter", domain))
	}
	for _, r := range deployReservedDomains {
		if domain == r || strings.HasSuffix(domain, "."+r) {
			return invalid(fmt.Sprintf("acmeEmail's domain %s is not a public domain: %s is a reserved name", domain, r))
		}
	}
	return nil
}

// precheck refuses a deploy the host cannot take: a bad email, an instance already recorded, an Engine object of the
// module's names, a listener on 80/tcp or 443/tcp, or an installed DERP server.
func (h deployHandler) precheck(ctx context.Context, env core.Env, in DeployInput) error {
	if err := checkAcmeEmail(in.AcmeEmail); err != nil {
		return err
	}
	_, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if ok {
		return core.Definite(errcode.New(errcode.InvalidInput, "Nginx Proxy Manager is already deployed on this host"))
	}
	if err := h.checkEngine(ctx); err != nil {
		return err
	}
	if err := h.checkPorts(); err != nil {
		return err
	}
	return h.checkDERP()
}

// checkEngine refuses when a network, volume or container of the module's names exists, whoever made it.
func (h deployHandler) checkEngine(ctx context.Context) error {
	docker := h.n.d.docker
	inUse := func(what string) error {
		return core.Definite(errcode.New(errcode.DockerInUse, fmt.Sprintf("a Docker %s already exists", what)))
	}
	nets, err := docker.ListNetworks(ctx, dockerapi.Filters{"name": {networkName}})
	if err != nil {
		return fmt.Errorf("npm: list networks named %s: %w", networkName, engineCoded(err))
	}
	for _, nw := range nets {
		// Docker's name filter also matches networks whose name only contains networkName.
		if nw.Name == networkName {
			return inUse("network named " + networkName)
		}
	}
	for _, v := range []string{dataVolume, certVolume} {
		_, err := docker.InspectVolume(ctx, v)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return fmt.Errorf("npm: inspect volume %s: %w", v, engineCoded(err))
		default:
			return inUse("volume named " + v)
		}
	}
	_, err = docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	default:
		return inUse("container named " + containerName)
	}
}

// checkPorts refuses a host where another program listens on 80/tcp or 443/tcp, as the derp install does. Each port is
// reported once, in the order it first appears, with the process of its first listener that names one.
func (h deployHandler) checkPorts() error {
	sockets, err := h.n.d.sockets()
	if err != nil {
		return err
	}
	var ports []uint16
	who := map[uint16]string{}
	for _, s := range sockets {
		port := s.Local.Port()
		if s.Proto != netstat.ProtoTCP || !s.Listening() || (port != 80 && port != 443) {
			continue
		}
		if _, seen := who[port]; !seen {
			ports = append(ports, port)
			who[port] = ""
		}
		if who[port] == "" && len(s.Processes) > 0 {
			who[port] = s.Processes[0].Comm
		}
	}
	var busy []string
	for _, port := range ports {
		name := who[port]
		if name == "" {
			name = "an unknown process"
		}
		busy = append(busy, fmt.Sprintf("%d/%s is in use by %s", port, netstat.ProtoTCP, name))
	}
	if len(busy) > 0 {
		return core.Definite(errcode.New(errcode.DERPPortInUse, strings.Join(busy, "; ")))
	}
	return nil
}

// checkDERP refuses a host with an installed DERP server.
func (h deployHandler) checkDERP() error {
	_, err := h.n.d.readFile(deployDERPUnit)
	switch {
	case err == nil:
		return core.Definite(errcode.New(errcode.DERPPortInUse, fmt.Sprintf("a DERP server is installed (%s)", path.Base(deployDERPUnit))))
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return err
	}
}

// Plan checks the host and lists what the deploy creates. It changes nothing and pulls no image.
func (h deployHandler) Plan(ctx context.Context, env core.Env, in DeployInput) (core.PlanOutcome, error) {
	if err := h.precheck(ctx, env, in); err != nil {
		return core.PlanOutcome{}, err
	}
	email := strings.ToLower(in.AcmeEmail)
	sum := sha256.Sum256([]byte(Image + "\n" + email))
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: "Docker network " + networkName},
			{Op: "+", Text: "volume " + dataVolume},
			{Op: "+", Text: "volume " + certVolume},
			{Op: "+", Text: "container " + containerName + " from " + Image},
			{Op: "+", Text: "ports 80/tcp and 443/tcp on every address, 81/tcp on 127.0.0.1 only"},
			{Op: "+", Text: "administrator " + email + " with a generated password kept sealed on this host"},
		},
		StateHash: hex.EncodeToString(sum[:]),
	}, nil
}

// Apply deploys the instance. The email is lower-cased because NPM's login lower-cases the identity and compares it
// case-sensitively with the stored email, so an administrator created with upper-case letters could never log in.
func (h deployHandler) Apply(ctx context.Context, env core.Env, in DeployInput) (Deployed, error) {
	if err := h.precheck(ctx, env, in); err != nil {
		return Deployed{}, err
	}
	email := strings.ToLower(in.AcmeEmail)
	if err := h.ensureImage(ctx); err != nil {
		return Deployed{}, err
	}
	// Another client may have created an object of the module's names while the image downloaded; adopting it would
	// let the undo delete it.
	if err := h.checkEngine(ctx); err != nil {
		return Deployed{}, err
	}
	raw, err := h.n.d.random(32)
	if err != nil {
		return Deployed{}, fmt.Errorf("npm: generate the administrator password: %w", err)
	}
	if len(raw) != 32 {
		return Deployed{}, fmt.Errorf("npm: the random source returned %d bytes, want 32", len(raw))
	}
	password := base64.RawURLEncoding.EncodeToString(raw)

	containerID, err := h.createEngineObjects(ctx, env)
	if err != nil {
		return Deployed{}, err
	}

	// The password is journaled before NPM is asked to create the administrator, so a deploy cut off after NPM
	// created it keeps the password the operator needs to undo or repair.
	e := npmEffect(core.EffectCreate, resourceOf(nounInstance, "?"+containerName), nil, 0)
	e.Local, e.InverseLocal = true, true
	seq, err := env.Effects.Intend(ctx, e, []byte(password))
	if err != nil {
		return Deployed{}, fmt.Errorf("npm: journal %s: %w", e.Resource, err)
	}
	conflict, err := h.bootstrap(ctx, env, email, password)
	if conflict {
		return Deployed{}, h.discardDeploy(ctx, containerID, err)
	}
	if err != nil {
		return Deployed{}, err
	}
	sealed, err := env.Sealer.Seal("npm_instances.password_ct.1", []byte(password))
	if err != nil {
		return Deployed{}, fmt.Errorf("npm: seal the administrator password: %w", err)
	}
	now := env.Clock.Now()
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return saveInstance(ctx, tx, instanceRow{
			ContainerID: containerID, Image: Image, AdminEmail: email, PasswordCT: sealed, Public: "no",
			CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		return Deployed{}, err
	}
	resource := resourceOf(nounInstance, containerName)
	obs, err := h.observeInstance(ctx, env, containerName, false)
	if err != nil {
		return Deployed{}, fmt.Errorf("npm: %s is unsettled: %w", resource, err)
	}
	if err := env.Effects.Made(ctx, seq, resource, obs.Current); err != nil {
		return Deployed{}, fmt.Errorf("npm: journal %s: %w", resource, err)
	}
	return Deployed{ContainerID: containerID, Image: Image, AdminEmail: email}, nil
}

// ensureImage pulls Image when the Engine does not have it. A failed pull changed nothing the module journals.
func (h deployHandler) ensureImage(ctx context.Context) error {
	docker := h.n.d.docker
	_, err := docker.InspectImage(ctx, Image)
	switch {
	case err == nil:
		return nil
	case !dockerapi.IsNotFound(err):
		return fmt.Errorf("npm: inspect image %s: %w", Image, engineCoded(err))
	}
	s, err := docker.PullImage(ctx, Image)
	if err != nil {
		return core.Definite(fmt.Errorf("npm: pull %s: %w", Image, engineCoded(err)))
	}
	defer func() { _ = s.Close() }()
	for {
		m, err := s.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err == nil {
			err = m.Err()
		}
		if err != nil {
			return core.Definite(fmt.Errorf("npm: pull %s: %w", Image, engineCoded(err)))
		}
	}
}

// createEngineObjects creates the network, the two volumes and the container, and starts the container, each its
// own effect. It returns the container's id.
func (h deployHandler) createEngineObjects(ctx context.Context, env core.Env) (string, error) {
	docker := h.n.d.docker
	labels := map[string]string{core.LabelModule: ID}

	err := h.n.run(ctx, env, npmEffect(core.EffectCreate, resourceOf(nounNetwork, "?"+networkName), nil, commitWait), h.n.observeEngine,
		func(ctx context.Context) (string, error) {
			id, err := docker.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: networkName, Driver: "bridge", Labels: labels})
			if err != nil {
				return "", fmt.Errorf("npm: create network %s: %w", networkName, engineCoded(err))
			}
			return resourceOf(nounNetwork, id), nil
		})
	if err != nil {
		return "", err
	}
	for _, name := range []string{dataVolume, certVolume} {
		err := h.n.run(ctx, env, npmEffect(core.EffectCreate, resourceOf(nounVolume, "?"+name), nil, commitWait), h.n.observeEngine,
			func(ctx context.Context) (string, error) {
				v, err := docker.CreateVolume(ctx, dockerapi.VolumeCreate{Name: name, Labels: labels})
				if err != nil {
					return "", fmt.Errorf("npm: create volume %s: %w", name, engineCoded(err))
				}
				return resourceOf(nounVolume, v.Name), nil
			})
		if err != nil {
			return "", err
		}
	}
	var id string
	err = h.n.run(ctx, env, npmEffect(core.EffectCreate, resourceOf(nounContainer, "?"+containerName), nil, commitWait), h.n.observeEngine,
		func(ctx context.Context) (string, error) {
			var err error
			if id, err = docker.CreateContainer(ctx, instanceCreate(Image)); err != nil {
				return "", fmt.Errorf("npm: create container %s: %w", containerName, engineCoded(err))
			}
			return resourceOf(nounContainer, id), nil
		})
	if err != nil {
		return "", err
	}

	c, err := docker.InspectContainer(ctx, id)
	if err != nil {
		return "", fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	}
	before, err := dstate.ContainerRunning(id, c.Created, false)
	if err != nil {
		return "", fmt.Errorf("npm: render container %s: %w", containerName, err)
	}
	err = h.n.run(ctx, env, npmEffect(core.EffectModify, resourceOf(nounContainer, id), before, commitWait), h.n.observeEngine,
		func(ctx context.Context) (string, error) {
			if _, err := docker.StartContainer(ctx, id); err != nil {
				return "", fmt.Errorf("npm: start container %s: %w", containerName, engineCoded(err))
			}
			return "", nil
		})
	if err != nil {
		return "", err
	}
	return id, nil
}

// bootstrap waits for NPM to answer and creates its administrator, then proves the login works. A failure is
// npm.setup_failed and not Definite: the container runs and the administrator may exist. conflict is true only when
// an administrator the deploy did not create exists: NPM reported one before the deploy created its own, or refused
// the creation with a 4xx, which commits no user. A poll that times out, a 5xx or a lost answer to the creation, a
// missing setup flag after it and a refused login are no conflict, since the administrator may hold the sealed
// password.
func (h deployHandler) bootstrap(ctx context.Context, env core.Env, email, password string) (conflict bool, err error) {
	api := h.n.d.anon()
	deadline := env.Clock.Now().Add(deployReady)
	for {
		health, err := api.Health(ctx)
		if err == nil {
			if health.Setup {
				return true, errcode.New(errcode.NPMSetupFailed, "Nginx Proxy Manager already has an administrator")
			}
			break
		}
		if cerr := ctx.Err(); cerr != nil {
			return false, cerr
		}
		if !env.Clock.Now().Before(deadline) {
			return false, deploySetupFailed(ctx, err, fmt.Sprintf("did not answer within %s", deployReady))
		}
		if err := deploySleep(ctx, env.Clock, deployPoll); err != nil {
			return false, err
		}
	}
	if err := api.CreateAdmin(ctx, email, password); err != nil {
		return npmapi.IsClientError(err) && ctx.Err() == nil, deploySetupFailed(ctx, err, "did not create the administrator")
	}
	health, err := api.Health(ctx)
	if err != nil {
		return false, deploySetupFailed(ctx, err, "did not answer after creating the administrator")
	}
	if !health.Setup {
		return false, errcode.New(errcode.NPMSetupFailed, "Nginx Proxy Manager does not report the administrator it created")
	}
	if _, err := api.Token(ctx, email, password); err != nil {
		return false, deploySetupFailed(ctx, err, "refused the new administrator's login")
	}
	return false, nil
}

// discardDeploy stops and removes the container, both volumes and the network the deploy created, so that no
// instance runs under an administrator another client created, and returns cause as npm.setup_failed saying whether
// the removal succeeded. It runs past the apply's cancellation. The journal is left as it is.
func (h deployHandler) discardDeploy(ctx context.Context, containerID string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*deployStop)
	defer cancel()
	docker := h.n.d.docker
	var failed []error
	attempt := func(what string, err error) {
		if err != nil && !dockerapi.IsNotFound(err) {
			failed = append(failed, fmt.Errorf("npm: %s: %w", what, err))
		}
	}
	_, err := docker.StopContainer(ctx, containerID, deployStop)
	attempt("stop container "+containerID, err)
	attempt("remove container "+containerID, docker.RemoveContainer(ctx, containerID, dockerapi.RemoveContainerOptions{Force: true}))
	attempt("remove volume "+dataVolume, docker.RemoveVolume(ctx, dataVolume, false))
	attempt("remove volume "+certVolume, docker.RemoveVolume(ctx, certVolume, false))
	attempt("remove network "+networkName, docker.RemoveNetwork(ctx, networkName))

	var detail string
	var coded *errcode.Error
	if errors.As(cause, &coded) {
		detail = coded.Detail
	}
	if len(failed) == 0 {
		return errcode.Wrap(errcode.NPMSetupFailed, cause, detail+"; the deploy removed what it created")
	}
	return errcode.Wrap(errcode.NPMSetupFailed, errors.Join(append([]error{cause}, failed...)...),
		detail+"; the deploy could not remove what it created: remove the Docker container, volumes and network whose names start with darkpanel-npm")
}

// deploySetupFailed is the npm.setup_failed of an NPM call that failed doing what, with NPM's HTTP status when it
// answered; a cancelled context is returned as it is. The detail never holds NPM's response body.
func deploySetupFailed(ctx context.Context, err error, what string) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	detail := "Nginx Proxy Manager " + what
	var answer *npmapi.APIError
	if errors.As(err, &answer) {
		detail += fmt.Sprintf(" (HTTP %d)", answer.Status)
	}
	return errcode.Wrap(errcode.NPMSetupFailed, err, detail)
}

// deploySleep waits d on clock, or returns the context's error as soon as it ends, stopping the timer.
func deploySleep(ctx context.Context, clock core.Clock, d time.Duration) error {
	done := make(chan struct{})
	t := clock.AfterFunc(d, func() { close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}

// Verify checks the instance from the host alone: its row, its running container and an API that answers for the
// stored administrator.
func (h deployHandler) Verify(ctx context.Context, env core.Env, _ DeployInput, _ Deployed) error {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok {
		return errcode.New(errcode.NPMUnhealthy, "no Nginx Proxy Manager instance is recorded on this host")
	}
	c, err := h.n.d.docker.InspectContainer(ctx, row.ContainerID)
	switch {
	case dockerapi.IsNotFound(err):
		return errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager container does not exist")
	case err != nil:
		return fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	case !c.State.Running:
		return errcode.New(errcode.NPMUnhealthy, "the Nginx Proxy Manager container is not running")
	}
	api, err := h.n.d.api(ctx, env)
	if err != nil {
		return err
	}
	if _, err := api.ProxyHosts(ctx); err != nil {
		detail := "the Nginx Proxy Manager API did not list its proxy hosts"
		var answer *npmapi.APIError
		if errors.As(err, &answer) {
			detail += fmt.Sprintf(" (HTTP %d)", answer.Status)
		}
		return errcode.Wrap(errcode.NPMUnhealthy, err, detail)
	}
	return nil
}

// Observe reads the instance row for the bookkeeping effect and the Engine for every other effect of the deploy.
func (h deployHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	if noun != nounInstance {
		return h.n.observeEngine(ctx, env, e)
	}
	return h.observeInstance(ctx, env, name, creation)
}

// observeInstance is the state of the instance row: its container, image and administrator email, never the password.
// The creation finds the row by the instance's name.
func (h deployHandler) observeInstance(ctx context.Context, env core.Env, name string, creation bool) (core.Observation, error) {
	if name != containerName {
		return core.Observation{}, fmt.Errorf("npm: %q is not the deploy's instance", resourceOf(nounInstance, name))
	}
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return core.Observation{}, err
	}
	if !ok {
		if creation {
			return found(nil), nil
		}
		return named(nullState().State), nil
	}
	doc, err := json.Marshal(struct {
		ContainerID string `json:"containerId"`
		Image       string `json:"image"`
		AdminEmail  string `json:"adminEmail"`
	}{row.ContainerID, row.Image, row.AdminEmail})
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: render the instance: %w", err)
	}
	if creation {
		return found([]core.Candidate{{Resource: resourceOf(nounInstance, containerName), State: core.StateDoc{State: doc}}}), nil
	}
	return named(doc), nil
}

// Inverse undoes one effect of the deploy. An object already gone is undone; an Engine refusal changed nothing.
func (h deployHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.InverseResult{}, err
	}
	if creation {
		return core.InverseResult{}, fmt.Errorf("npm: %s names no made object", e.Resource)
	}
	docker := h.n.d.docker
	var what string
	switch {
	case noun == nounInstance && name == containerName:
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteInstance(ctx, tx) }); err != nil {
			return core.InverseResult{}, err
		}
		return core.InverseResult{}, nil
	case noun == nounContainer && e.Kind == core.EffectModify:
		what = "stop container " + name
		_, err = docker.StopContainer(ctx, name, deployStop)
	case noun == nounContainer && e.Kind == core.EffectCreate:
		what = "remove container " + name
		err = docker.RemoveContainer(ctx, name, dockerapi.RemoveContainerOptions{Force: true})
	case noun == nounVolume:
		what = "remove volume " + name
		err = docker.RemoveVolume(ctx, name, false)
	case noun == nounNetwork:
		what = "remove network " + name
		err = docker.RemoveNetwork(ctx, name)
	default:
		return core.InverseResult{}, fmt.Errorf("npm: %s %s is not an effect of the deploy", e.Kind, e.Resource)
	}
	switch {
	case err == nil, dockerapi.IsNotFound(err):
		return core.InverseResult{}, nil
	case refused(err):
		return core.InverseResult{}, core.Definite(fmt.Errorf("npm: %s: %w", what, err))
	case errors.Is(err, dockerapi.ErrUnavailable):
		return core.InverseResult{}, fmt.Errorf("npm: %s: %w", what, engineCoded(err))
	default:
		return core.InverseResult{}, err
	}
}
