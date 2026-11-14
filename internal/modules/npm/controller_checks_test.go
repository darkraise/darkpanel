package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
)

const ckHost = "h7"

// ckAgent is what one host's agent answers to the controller: the firewall.docker resource and the
// npm.instance.status read. It records what was asked.
type ckAgent struct {
	docker      firewall.DockerStatus
	dockerErr   error
	status      InstanceStatus
	statusErr   error
	fwStatus    firewall.Status // the firewall.status resource
	fwStatusErr error

	mu        sync.Mutex
	reads     []string // "<action>/v<version> <input>"
	resources []string
}

func (a *ckAgent) fake() *coretest.FakeAgent {
	return &coretest.FakeAgent{
		ReadFunc: func(_ context.Context, _ core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			a.mu.Lock()
			a.reads = append(a.reads, ref.String()+" "+string(input))
			a.mu.Unlock()
			if ref.Name != "npm.instance.status" || ref.Version != 1 {
				return nil, fmt.Errorf("ckAgent: no read %s", ref)
			}
			if a.statusErr != nil {
				return nil, a.statusErr
			}
			return json.Marshal(a.status)
		},
		ResourceFunc: func(_ context.Context, _ core.Principal, name string) (json.RawMessage, error) {
			a.mu.Lock()
			a.resources = append(a.resources, name)
			a.mu.Unlock()
			switch name {
			case "firewall.docker":
				if a.dockerErr != nil {
					return nil, a.dockerErr
				}
				return json.Marshal(a.docker)
			case "firewall.status":
				if a.fwStatusErr != nil {
					return nil, a.fwStatusErr
				}
				return json.Marshal(a.fwStatus)
			}
			return nil, fmt.Errorf("ckAgent: no resource %s", name)
		},
	}
}

// ckWorld is a controller whose only host, ckHost, is served by a.
func ckWorld(t *testing.T, a *ckAgent) *thHost {
	t.Helper()
	h := newTHHost(t)
	h.agents = map[string]*coretest.FakeAgent{ckHost: a.fake()}
	return h
}

// ckDockerOK is a supported host with the integration on: Docker 29.9.1 with raw protection.
func ckDockerOK() firewall.DockerStatus {
	return firewall.DockerStatus{
		Available: true, Version: "29.9.1", Driver: "iptables", RawProtection: true, Supported: true, Enabled: true, Health: "ok",
	}
}

// ckBinding is a stored binding of selector with the sources allowed.
func ckBinding(selector string, port uint16, proto string, allowed ...string) firewall.DockerBinding {
	b := dockerfw.Binding{Selector: selector, HostPort: port, Proto: proto}
	return firewall.DockerBinding{Binding: b, ID: b.ID(), Containers: []string{}, Allowed: allowed, Denied: []string{}}
}

// ckPorts is the two bindings npm.instance.deploy-public's ports step stores.
func ckPorts() []firewall.DockerBinding {
	return []firewall.DockerBinding{
		ckBinding("darkpanel-npm", 80, "tcp", "any"),
		ckBinding("darkpanel-npm", 443, "tcp", "any"),
	}
}

// ckRun reads def through the handler the engine uses for an internal read and decodes its answer.
func ckRun[Out any](t *testing.T, def core.ActionDef, env core.Env, in any) (Out, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if def.Handler.Reveal == nil {
		t.Fatalf("%s has no Reveal handler", def.Spec.Name)
	}
	_, out, err := def.Handler.Reveal(context.Background(), env, raw)
	var got Out
	if err != nil {
		return got, err
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output %s: %v", out, err)
	}
	return got, nil
}

func ckCode(err error) errcode.Code {
	var coded *errcode.Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

func ckDetail(err error) string {
	var coded *errcode.Error
	if errors.As(err, &coded) {
		return coded.Detail
	}
	return ""
}

// TestCkDeclarations: both checks are internal controller reads without locks and have a schema.
func TestCkDeclarations(t *testing.T) {
	n := newTHHost(t).nm()
	tests := []struct {
		name    string
		def     core.ActionDef
		in, out reflect.Type
	}{
		{"npm.instance.check", n.instanceCheck(), reflect.TypeFor[CheckInput](), reflect.TypeFor[CheckResult]()},
		{"npm.public.check", n.publicCheck(), reflect.TypeFor[PublicInput](), reflect.TypeFor[PublicResult]()},
	}
	for _, tc := range tests {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Summary == "" || s.Class != core.ClassRead || s.Side != core.SideController ||
			!s.Internal || s.Reveal || len(s.LockKeys) != 0 || tc.def.Handler.Reveal == nil || tc.def.Handler.Read != nil {
			t.Errorf("%s: spec %+v, reveal handler %v, read handler %v; want an internal controller read v1 without locks, served by Reveal only",
				tc.name, s, tc.def.Handler.Reveal != nil, tc.def.Handler.Read != nil)
		}
		if tc.def.InputType != tc.in || tc.def.OutputType != tc.out {
			t.Errorf("%s: types %v and %v, want %v and %v", tc.name, tc.def.InputType, tc.def.OutputType, tc.in, tc.out)
		}
		if _, err := schema.ActionSnapshot(tc.def); err != nil {
			t.Errorf("%s: schema: %v", tc.name, err)
		}
	}
}

// TestCkInstanceCheck: the check refuses a host that cannot run NPM before it compares the integration, and
// refuses an integration that is not as planned with plan.state_changed.
func TestCkInstanceCheck(t *testing.T) {
	tests := []struct {
		name        string
		docker      func(d *firewall.DockerStatus)
		integration bool
		want        errcode.Code // "" for an answer
		detail      string       // a substring of the refusal's detail
	}{
		{"the integration is on and supported, as planned", nil, true, "", ""},
		{"the integration is off, as planned", func(d *firewall.DockerStatus) { d.Enabled = false }, false, "", ""},
		{"the integration is on but unsupported, which counts as off", func(d *firewall.DockerStatus) {
			d.Supported, d.Unsupported = false, []string{"UFW's iptables variant is not known"}
		}, false, "", ""},
		{"Docker 28.0 is enough", func(d *firewall.DockerStatus) { d.Version = "28.0.0" }, true, "", ""},
		{"the integration was turned on since the plan", nil, false, errcode.PlanStateChanged, "integration"},
		{"the integration was turned off since the plan", func(d *firewall.DockerStatus) { d.Enabled = false }, true, errcode.PlanStateChanged, "integration"},
		{"the host stopped supporting the integration since the plan", func(d *firewall.DockerStatus) { d.Supported = false }, true, errcode.PlanStateChanged, "integration"},
		{"Docker is older than 28.0", func(d *firewall.DockerStatus) { d.Version, d.Supported = "27.5.1", false }, false, errcode.ModuleUnavailable, "27.5.1"},
		{"Docker's version is not recognised", func(d *firewall.DockerStatus) { d.Version = "latest" }, false, errcode.ModuleUnavailable, "latest"},
		{"the raw-table protection is not loaded", func(d *firewall.DockerStatus) { d.RawProtection, d.Supported = false, false }, false, errcode.ModuleUnavailable, "raw"},
		{"Docker does not answer", func(d *firewall.DockerStatus) {
			*d = firewall.DockerStatus{Available: false, Reason: "the Docker socket is missing"}
		}, false, errcode.ModuleUnavailable, "the Docker socket is missing"},
		{"an unusable host is reported before a changed integration", func(d *firewall.DockerStatus) { d.Version, d.Supported = "27.5.1", false }, true, errcode.ModuleUnavailable, "28.0"},
		{"a bare major version is recognised", func(d *firewall.DockerStatus) { d.Version = "29" }, true, "", ""},
		{"a bare major below 28 is older than 28.0", func(d *firewall.DockerStatus) { d.Version, d.Supported = "27", false }, false, errcode.ModuleUnavailable, "older than 28.0"},
		{"a major with a suffix instead of a dot is not recognised", func(d *firewall.DockerStatus) { d.Version = "28-ce" }, false, errcode.ModuleUnavailable, "not recognised"},
		{"an empty major is not recognised", func(d *firewall.DockerStatus) { d.Version = ".5" }, false, errcode.ModuleUnavailable, "not recognised"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &ckAgent{docker: ckDockerOK()}
			if tc.docker != nil {
				tc.docker(&a.docker)
			}
			h := ckWorld(t, a)
			got, err := ckRun[CheckResult](t, h.nm().instanceCheck(), h.ctlEnv(""), CheckInput{HostID: ckHost, Integration: tc.integration})
			if tc.want == "" {
				if err != nil || got != (CheckResult{Integration: tc.integration}) {
					t.Fatalf("answer %+v, %v; want integration %v", got, err, tc.integration)
				}
			} else if ckCode(err) != tc.want || !core.IsDefinite(err) || !strings.Contains(ckDetail(err), tc.detail) {
				t.Fatalf("error %#v; want a definite %s refusal whose detail holds %q", err, tc.want, tc.detail)
			}
			if !reflect.DeepEqual(a.resources, []string{"firewall.docker"}) || len(a.reads) != 0 {
				t.Errorf("asked resources %v and reads %v, want firewall.docker once and no read", a.resources, a.reads)
			}
		})
	}
}

// TestCkInstanceCheckReadErrors: a host that cannot be read is an error of its own, not a refusal that claims
// the host is unusable or changed.
func TestCkInstanceCheckReadErrors(t *testing.T) {
	boom := errors.New("the agent does not answer")
	run := func(h *thHost) error {
		_, err := ckRun[CheckResult](t, h.nm().instanceCheck(), h.ctlEnv(""), CheckInput{HostID: ckHost})
		return err
	}
	t.Run("the resource read fails", func(t *testing.T) {
		err := run(ckWorld(t, &ckAgent{dockerErr: boom}))
		if !errors.Is(err, boom) || core.IsDefinite(err) || ckCode(err) != "" {
			t.Errorf("error %#v, want the read's own error, wrapped, uncoded and not definite", err)
		}
	})
	t.Run("the resource is not JSON", func(t *testing.T) {
		h := ckWorld(t, &ckAgent{})
		h.agents[ckHost].ResourceFunc = func(context.Context, core.Principal, string) (json.RawMessage, error) {
			return json.RawMessage(`[`), nil
		}
		if err := run(h); err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want a decode error that is not definite", err)
		}
	})
	t.Run("the host is unknown", func(t *testing.T) {
		h := ckWorld(t, &ckAgent{})
		h.agents = map[string]*coretest.FakeAgent{}
		if err := run(h); ckCode(err) != errcode.NotFound {
			t.Errorf("error %#v, want %s", err, errcode.NotFound)
		}
	})
}

// TestCkPublicCheck: an instance is public through its mark, which is tried first and needs no firewall read, or
// through stored allow bindings of both ports from every source while the integration is on.
func TestCkPublicCheck(t *testing.T) {
	deployed := func(public string) InstanceStatus {
		return InstanceStatus{Deployed: true, Running: true, APIReady: true, Public: public}
	}
	withBindings := func(b ...firewall.DockerBinding) firewall.DockerStatus {
		d := ckDockerOK()
		d.Bindings = b
		return d
	}
	hostAddr := func(b firewall.DockerBinding) firewall.DockerBinding {
		b.Binding.HostAddr = netip.MustParseAddr("203.0.113.5")
		return b
	}
	denying := func(b firewall.DockerBinding, sources ...string) firewall.DockerBinding {
		b.Denied = sources
		return b
	}
	tests := []struct {
		name      string
		status    InstanceStatus
		docker    firewall.DockerStatus
		want      PublicResult
		refused   bool
		resources int // firewall.docker reads
	}{
		{"a confirmed mark", deployed("confirmed"), ckDockerOK(), PublicResult{Public: true, Via: "mark"}, false, 0},
		{"the mark wins over the ports", deployed("confirmed"), withBindings(ckPorts()...), PublicResult{Public: true, Via: "mark"}, false, 0},
		{"both ports allowed from every source", deployed("no"), withBindings(ckPorts()...), PublicResult{Public: true, Via: "ports"}, false, 1},
		{"every source among several", deployed("no"),
			withBindings(ckBinding("darkpanel-npm", 80, "tcp", "10.0.0.0/8", "any"), ckBinding("darkpanel-npm", 443, "tcp", "any")),
			PublicResult{Public: true, Via: "ports"}, false, 1},
		{"only port 80", deployed("no"), withBindings(ckBinding("darkpanel-npm", 80, "tcp", "any")), PublicResult{}, true, 1},
		{"port 443 limited to a source range", deployed("no"),
			withBindings(ckBinding("darkpanel-npm", 80, "tcp", "any"), ckBinding("darkpanel-npm", 443, "tcp", "10.0.0.0/8")), PublicResult{}, true, 1},
		{"bindings of another container", deployed("no"),
			withBindings(ckBinding("web", 80, "tcp", "any"), ckBinding("web", 443, "tcp", "any")), PublicResult{}, true, 1},
		{"udp bindings", deployed("no"),
			withBindings(ckBinding("darkpanel-npm", 80, "udp", "any"), ckBinding("darkpanel-npm", 443, "udp", "any")), PublicResult{}, true, 1},
		{"bindings of one host address", deployed("no"),
			withBindings(hostAddr(ckBinding("darkpanel-npm", 80, "tcp", "any")), hostAddr(ckBinding("darkpanel-npm", 443, "tcp", "any"))), PublicResult{}, true, 1},
		{"stored bindings while the integration is off", deployed("no"),
			func() firewall.DockerStatus { d := withBindings(ckPorts()...); d.Enabled = false; return d }(), PublicResult{}, true, 1},
		{"stored bindings on an unsupported host", deployed("no"),
			func() firewall.DockerStatus { d := withBindings(ckPorts()...); d.Supported = false; return d }(), PublicResult{}, true, 1},
		{"no mark and no bindings", deployed("no"), ckDockerOK(), PublicResult{}, true, 1},
		{"no instance", InstanceStatus{}, withBindings(ckPorts()...), PublicResult{}, true, 0},
		{"port 80 denied to a range while allowed from every source", deployed("no"),
			withBindings(denying(ckBinding("darkpanel-npm", 80, "tcp", "any"), "203.0.113.0/24"), ckBinding("darkpanel-npm", 443, "tcp", "any")),
			PublicResult{}, true, 1},
		{"port 443 denied from every source", deployed("no"),
			withBindings(ckBinding("darkpanel-npm", 80, "tcp", "any"), denying(ckBinding("darkpanel-npm", 443, "tcp", "any"), "any")),
			PublicResult{}, true, 1},
		{"a denial on another container's binding does not matter", deployed("no"),
			withBindings(append(ckPorts(), denying(ckBinding("web", 80, "tcp", "any"), "10.0.0.0/8"))...),
			PublicResult{Public: true, Via: "ports"}, false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &ckAgent{status: tc.status, docker: tc.docker}
			h := ckWorld(t, a)
			got, err := ckRun[PublicResult](t, h.nm().publicCheck(), h.ctlEnv(""), PublicInput{HostID: ckHost})
			if tc.refused {
				if ckCode(err) != errcode.NPMNotPublic || !core.IsDefinite(err) {
					t.Fatalf("answer %+v, error %#v; want a definite %s refusal", got, err, errcode.NPMNotPublic)
				}
				want := "not confirmed public"
				if !tc.status.Deployed {
					want = "not deployed"
				}
				if !strings.Contains(ckDetail(err), want) || !strings.Contains(ckDetail(err), ckHost) {
					t.Errorf("detail %q, want it to say %q and name host %s", ckDetail(err), want, ckHost)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("answer %+v, %v; want %+v", got, err, tc.want)
			}
			if !reflect.DeepEqual(a.reads, []string{"npm.instance.status/v1 {}"}) {
				t.Errorf("reads %v, want npm.instance.status once with an empty input", a.reads)
			}
			if len(a.resources) != tc.resources {
				t.Errorf("firewall.docker read %d times, want %d", len(a.resources), tc.resources)
			}
		})
	}
}

// TestCkPublicCheckReadErrors: a status or firewall read that fails is an error of its own, never an answer that
// the instance is not public.
func TestCkPublicCheckReadErrors(t *testing.T) {
	boom := errors.New("the agent does not answer")
	run := func(a *ckAgent) error {
		h := ckWorld(t, a)
		_, err := ckRun[PublicResult](t, h.nm().publicCheck(), h.ctlEnv(""), PublicInput{HostID: ckHost})
		return err
	}
	unmarked := InstanceStatus{Deployed: true, Public: "no"}
	for name, a := range map[string]*ckAgent{
		"the status read fails":               {statusErr: boom},
		"the firewall read fails when needed": {status: unmarked, dockerErr: boom},
	} {
		if err := run(a); !errors.Is(err, boom) || core.IsDefinite(err) || ckCode(err) != "" {
			t.Errorf("%s: error %#v, want the read's own error, wrapped, uncoded and not definite", name, err)
		}
	}
	if err := run(&ckAgent{status: InstanceStatus{Deployed: true, Public: "confirmed"}, dockerErr: boom}); err != nil {
		t.Errorf("a confirmed mark with a failing firewall read: %v, want an answer", err)
	}
}

func TestW12cCheckComparesFirewallAndIPv6(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name          string
		supported, v6 bool
		statusErr     error
		in            CheckInput
		want          string // "" when the check passes
	}{
		{"no new fields reads no firewall status", true, true, errors.New("boom"), CheckInput{HostID: ckHost, Integration: true}, ""},
		{"both as planned", true, true, nil, CheckInput{HostID: ckHost, Integration: true, Firewall: &yes, IPv6: &yes}, ""},
		{"firewall support changed", false, true, nil, CheckInput{HostID: ckHost, Integration: true, Firewall: &yes, IPv6: &yes},
			"the host's firewall support is now off and was on when the plan was made"},
		{"IPv6 setting changed", true, false, nil, CheckInput{HostID: ckHost, Integration: true, Firewall: &yes, IPv6: &yes},
			"the host's UFW IPv6 setting is now off and was on when the plan was made"},
		{"IPv6 turned on", true, true, nil, CheckInput{HostID: ckHost, Integration: true, Firewall: &yes, IPv6: &no},
			"the host's UFW IPv6 setting is now on and was off when the plan was made"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &ckAgent{docker: ckDockerOK(), fwStatus: firewall.Status{Supported: tc.supported, Configured: firewall.Configured{IPv6: tc.v6}}, fwStatusErr: tc.statusErr}
			h := ckWorld(t, a)
			_, err := ckRun[CheckResult](t, h.nm().instanceCheck(), h.ctlEnv(""), tc.in)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("check = %v, want a pass", err)
			case tc.want != "" && (ckCode(err) != errcode.PlanStateChanged || ckDetail(err) != tc.want):
				t.Fatalf("check = %v, want plan.state_changed %q", err, tc.want)
			}
		})
	}
	t.Run("a failed status read fails the check", func(t *testing.T) {
		a := &ckAgent{docker: ckDockerOK(), fwStatusErr: errors.New("boom")}
		h := ckWorld(t, a)
		_, err := ckRun[CheckResult](t, h.nm().instanceCheck(), h.ctlEnv(""), CheckInput{HostID: ckHost, Integration: true, Firewall: &yes, IPv6: &yes})
		if err == nil || !strings.Contains(err.Error(), "npm: read the firewall status of host "+ckHost) {
			t.Fatalf("check = %v, want the status read error", err)
		}
	})
}
