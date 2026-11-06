package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
)

// ctlMinDockerMajor is the oldest Docker major version Nginx Proxy Manager runs on.
const ctlMinDockerMajor = 28

// instanceCheck declares npm.instance.check: the controller's own check that a host can run NPM and that its Docker
// firewall integration is still as the plan found it.
func (n nm) instanceCheck() core.ActionDef {
	return core.DefineInternalRead[CheckInput, CheckResult](core.ActionSpec{
		Name:    ID + ".instance.check",
		Summary: "Check, for the controller's own use, that a host can run Nginx Proxy Manager and that its Docker firewall integration is as planned.",
		Version: 1,
		Side:    core.SideController,
	}, n.instanceCheckRead)
}

// instanceCheckRead refuses a host that cannot run NPM before it compares the integration, so a host that is both
// unusable and changed is reported as unusable.
func (n nm) instanceCheckRead(ctx context.Context, env core.Env, in CheckInput) (core.Revealed[CheckResult], error) {
	d, err := ctlDockerStatus(ctx, env, in.HostID)
	if err != nil {
		return core.Revealed[CheckResult]{}, err
	}
	refuse := func(code errcode.Code, detail string) (core.Revealed[CheckResult], error) {
		return core.Revealed[CheckResult]{}, core.Definite(errcode.New(code, detail))
	}
	if !d.Available {
		detail := "Docker is not available on this host"
		if d.Reason != "" {
			detail += ": " + d.Reason
		}
		return refuse(errcode.ModuleUnavailable, detail)
	}
	major, ok := ctlDockerMajor(d.Version)
	if !ok {
		return refuse(errcode.ModuleUnavailable, fmt.Sprintf(
			"Docker version %q is not recognised; Nginx Proxy Manager needs Docker 28.0 or later", d.Version))
	}
	if major < ctlMinDockerMajor {
		return refuse(errcode.ModuleUnavailable, fmt.Sprintf(
			"Docker %s is older than 28.0, which Nginx Proxy Manager needs", d.Version))
	}
	if !d.RawProtection {
		return refuse(errcode.ModuleUnavailable,
			"Docker's raw-table protection of published ports is not loaded, which Nginx Proxy Manager needs because it publishes port 81 on 127.0.0.1")
	}
	if on := d.Enabled && d.Supported; on != in.Integration {
		return refuse(errcode.PlanStateChanged, fmt.Sprintf(
			"the host's Docker firewall integration is now %s and was %s when the plan was made", ctlOnOff(on), ctlOnOff(in.Integration)))
	}
	return core.Revealed[CheckResult]{
		Identity: "NPM instance check for host " + in.HostID,
		Value:    CheckResult{Integration: in.Integration},
	}, nil
}

// publicCheck declares npm.public.check: the controller's own read of whether a host's NPM instance is public.
func (n nm) publicCheck() core.ActionDef {
	return core.DefineInternalRead[PublicInput, PublicResult](core.ActionSpec{
		Name:    ID + ".public.check",
		Summary: "Read, for the controller's own use, whether a host's Nginx Proxy Manager instance is confirmed public.",
		Version: 1,
		Side:    core.SideController,
	}, n.publicCheckRead)
}

// publicCheckRead tries the public mark before the firewall, so a confirmed mark answers even when the firewall
// cannot be read. A failed read is returned as such and never as npm.not_public.
func (n nm) publicCheckRead(ctx context.Context, env core.Env, in PublicInput) (core.Revealed[PublicResult], error) {
	answer := func(via string) (core.Revealed[PublicResult], error) {
		return core.Revealed[PublicResult]{
			Identity: "NPM public check for host " + in.HostID,
			Value:    PublicResult{Public: true, Via: via},
		}, nil
	}
	st, err := ctlInstanceStatus(ctx, env, in.HostID)
	if err != nil {
		return core.Revealed[PublicResult]{}, err
	}
	if !st.Deployed {
		return core.Revealed[PublicResult]{}, core.Definite(errcode.New(errcode.NPMNotPublic,
			"Nginx Proxy Manager is not deployed on host "+in.HostID))
	}
	if st.Public == "confirmed" {
		return answer("mark")
	}
	d, err := ctlDockerStatus(ctx, env, in.HostID)
	if err != nil {
		return core.Revealed[PublicResult]{}, err
	}
	if d.Enabled && d.Supported && ctlPortOpen(d.Bindings, 80) && ctlPortOpen(d.Bindings, 443) {
		return answer("ports")
	}
	return core.Revealed[PublicResult]{}, core.Definite(errcode.New(errcode.NPMNotPublic, fmt.Sprintf(
		"the Nginx Proxy Manager instance on host %s is not confirmed public: confirm that ports 80 and 443 are reachable from outside, or open them through the firewall integration",
		in.HostID)))
}

// ctlPortOpen reports whether a stored binding publishes the NPM container's TCP port on every host address,
// allows it from every source and denies it to none.
func ctlPortOpen(bindings []firewall.DockerBinding, port uint16) bool {
	for _, b := range bindings {
		addr := b.Binding.HostAddr
		if b.Binding.Selector == containerName && b.Binding.HostPort == port && b.Binding.Proto == "tcp" &&
			(!addr.IsValid() || addr.IsUnspecified()) && slices.Contains(b.Allowed, "any") && len(b.Denied) == 0 {
			return true
		}
	}
	return false
}

// ctlOnOff names an integration state.
func ctlOnOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// ctlDockerMajor returns the major version of a Docker version string: the digits before its first dot, or the whole
// string when it has none.
func ctlDockerMajor(version string) (int, bool) {
	digits, _, _ := strings.Cut(version, ".")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	major, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return major, true
}

// ctlDockerStatus reads the host's firewall.docker resource. Its errors are neither definite nor re-coded, so a
// transport error is never taken for a refusal and the directory's not_found stays reachable.
func ctlDockerStatus(ctx context.Context, env core.Env, hostID string) (firewall.DockerStatus, error) {
	fail := func(err error) (firewall.DockerStatus, error) {
		return firewall.DockerStatus{}, fmt.Errorf("npm: read the Docker firewall status of host %s: %w", hostID, err)
	}
	agent, err := env.Agents.Agent(ctx, hostID)
	if err != nil {
		return fail(err)
	}
	raw, err := agent.Resource(ctx, env.Principal, "firewall.docker")
	if err != nil {
		return fail(err)
	}
	var d firewall.DockerStatus
	if err := json.Unmarshal(raw, &d); err != nil {
		return fail(err)
	}
	return d, nil
}

// ctlInstanceStatus reads the host's npm.instance.status, with the errors of ctlDockerStatus.
func ctlInstanceStatus(ctx context.Context, env core.Env, hostID string) (InstanceStatus, error) {
	fail := func(err error) (InstanceStatus, error) {
		return InstanceStatus{}, fmt.Errorf("npm: read the NPM instance status of host %s: %w", hostID, err)
	}
	agent, err := env.Agents.Agent(ctx, hostID)
	if err != nil {
		return fail(err)
	}
	raw, err := agent.Read(ctx, env.Principal, core.ActionRef{Name: ID + ".instance.status", Version: 1}, json.RawMessage("{}"))
	if err != nil {
		return fail(err)
	}
	var st InstanceStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return fail(err)
	}
	return st, nil
}
