//go:build harness

package harness

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	metricsctl "github.com/darkraise/darkpanel/internal/modules/metrics/controller"
	"github.com/darkraise/darkpanel/internal/modules/notify/send"
)

func m10UnitSign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestM10UnitDelivery(t *testing.T) {
	body := []byte(`{"id":"e1","kind":"alert.firing","severity":"warning","hostId":"h1","title":"Disk on agent","body":"","createdAt":"x"}`)
	for _, c := range []struct {
		name, body, sig string
		ok, signed      bool
	}{
		{"signed", string(body), m10UnitSign(body, "k1"), true, true},
		{"wrong key", string(body), m10UnitSign(body, "k2"), true, false},
		{"unsigned", string(body), "", true, false},
		{"probe", `{}`, "", false, false},
		{"not JSON", `kind=alert.firing`, "", false, false},
	} {
		d, ok := m10xDelivery([]byte(c.body), c.sig, "k1")
		if ok != c.ok || d.Signed != c.signed {
			t.Errorf("%s: ok %v signed %v, want %v %v", c.name, ok, d.Signed, c.ok, c.signed)
		}
		if ok && (d.Kind != "alert.firing" || d.HostID != "h1" || d.Title != "Disk on agent" || d.Severity != "warning") {
			t.Errorf("%s: decoded %+v", c.name, d)
		}
	}
}

// TestM10UnitHook posts to a running receiver: a signed delivery is kept, the probe and a wrong path are not.
func TestM10UnitHook(t *testing.T) {
	h := m10StartHook(t, "k1")
	local := "http://127.0.0.1:" + strconv.Itoa(h.port)
	if want := "http://10.0.2.2:" + strconv.Itoa(h.port) + "/m10"; h.url() != want {
		t.Errorf("url = %s, want %s", h.url(), want)
	}
	body := []byte(`{"kind":"alert.firing","hostId":"h1","title":"r on agent"}`)
	for _, c := range []struct {
		path, body, sig string
		status          int
	}{
		{"/m10", `{}`, "", http.StatusNoContent},
		{"/other", string(body), m10UnitSign(body, "k1"), http.StatusBadRequest},
		{"/m10", string(body), m10UnitSign(body, "k1"), http.StatusNoContent},
	} {
		req, err := http.NewRequest(http.MethodPost, local+c.path, bytes.NewReader([]byte(c.body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(send.SignatureHeader, c.sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("POST %s: %d, want %d", c.path, resp.StatusCode, c.status)
		}
	}
	got := h.deliveries()
	if len(got) != 1 || !got[0].Signed || got[0].HostID != "h1" {
		t.Errorf("deliveries = %+v, want the one signed alert", got)
	}
}

func TestM10UnitFirstPoint(t *testing.T) {
	code := "metrics.clock_skew"
	ts := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	point := func(mem uint64, fs ...metricsctl.FSUsage) metricsctl.History {
		return metricsctl.History{Points: []metricsctl.Point{{TS: ts, MemTotal: mem, FS: fs}}}
	}
	for _, c := range []struct {
		name string
		h    metricsctl.History
		want string // "" accepts
	}{
		{"real", point(1<<30, metricsctl.FSUsage{Mount: "/boot", Total: 1}, metricsctl.FSUsage{Mount: "/", Total: 1 << 33}), ""},
		{"none", metricsctl.History{}, "no points yet"},
		{"failing poll", metricsctl.History{Status: &metricsctl.PollStatus{LastErrorCode: &code}}, "metrics.clock_skew"},
		{"no memory", point(0, metricsctl.FSUsage{Mount: "/", Total: 1}), "no memory total"},
		{"no root", point(1<<30, metricsctl.FSUsage{Mount: "/srv", Total: 1}), "no root filesystem"},
		{"empty root", point(1<<30, metricsctl.FSUsage{Mount: "/", Total: 0}), "no root filesystem"},
	} {
		err := m10xFirstPoint(c.h)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestM10UnitFiring(t *testing.T) {
	good := m10Delivery{Kind: core.NotifyAlertFiring, HostID: "h1", Title: "Rule on agent", Signed: true}
	for _, c := range []struct {
		name string
		ds   []m10Delivery
		want string
	}{
		{"found", []m10Delivery{{Kind: "notify.test"}, good}, ""},
		{"none", nil, "0 deliveries"},
		{"other host", []m10Delivery{{Kind: core.NotifyAlertFiring, HostID: "h2", Title: "Rule on c", Signed: true}}, "no alert.firing"},
		{"other rule", []m10Delivery{{Kind: core.NotifyAlertFiring, HostID: "h1", Title: "Rules on agent", Signed: true}}, "no alert.firing"},
		{"resolved", []m10Delivery{{Kind: core.NotifyAlertResolved, HostID: "h1", Title: "Rule on agent", Signed: true}}, "no alert.firing"},
		{"unsigned", []m10Delivery{{Kind: core.NotifyAlertFiring, HostID: "h1", Title: "Rule on agent"}}, "no valid signature"},
	} {
		err := m10xFiring(c.ds, "h1", "Rule")
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestM10UnitListed(t *testing.T) {
	l := metricsctl.AlertList{Alerts: []metricsctl.Alert{
		{RuleID: "r1", HostID: "h1", State: "resolved"}, {RuleID: "r2", HostID: "h1", State: "firing"}, {RuleID: "r1", HostID: "h2", State: "firing"},
	}}
	if err := m10xListed(l, "r2", "h1"); err != nil {
		t.Error(err)
	}
	if err := m10xListed(l, "r1", "h1"); err == nil {
		t.Error("a resolved alert passed as firing")
	}
}

func TestM10UnitMemberHosts(t *testing.T) {
	at := func(id string, s core.Scope) core.Host { return core.Host{ID: id, Scope: s} }
	for _, c := range []struct {
		name  string
		hosts []core.Host
		ok    bool
	}{
		{"only the agent", []core.Host{at("a", core.ScopeRead)}, true},
		{"the controller too", []core.Host{at("c", core.ScopeRead), at("a", core.ScopeRead)}, false},
		{"operate", []core.Host{at("a", core.ScopeOperate)}, false},
		{"another host", []core.Host{at("b", core.ScopeRead)}, false},
		{"none", nil, false},
	} {
		if err := m10xMemberHosts(c.hosts, "a"); (err == nil) != c.ok {
			t.Errorf("%s: %v, want ok %v", c.name, err, c.ok)
		}
	}
}

func TestM10UnitRefused(t *testing.T) {
	notFound := fmt.Errorf("plan firewall.rule.add: %w", &APIError{Status: http.StatusNotFound, Code: "request.not_found"})
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"wrapped answer", notFound, ""},
		{"planned", nil, "was planned"},
		{"forbidden instead", &APIError{Status: http.StatusForbidden, Code: "auth.forbidden"}, "answered 403 auth.forbidden"},
		{"right status, other code", &APIError{Status: http.StatusNotFound, Code: "module.not_found"}, "module.not_found"},
		{"not an answer", errors.New("dial tcp: refused"), "dial tcp"},
	} {
		err := m10xRefused(c.err, http.StatusNotFound, "request.not_found")
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

const (
	m10wJob     = "vm-m10"
	m10wRunStep = "Run the M10 harness"
	// m10wStepTimeout is the -timeout in minutes of the TestM10 step and m10wJobLimit the job's timeout-minutes.
	m10wStepTimeout = 40
	m10wJobLimit    = 60
)

// m10wWorkflow checks the text of harness.yml for TestM10: the vm-m10 job has the vm jobs' setup on the five OS
// images, a timeout-minutes of m10wJobLimit, a step that runs the test with the lab flags, a work directory of
// its own and a -timeout of m10wStepTimeout minutes, and its failure logs; and no other job holds the test.
func m10wWorkflow(yml string) error {
	job, ok := m3dxExpoJob(yml, m10wJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m10wJob)
	}
	for _, name := range fpYmlJobs(yml) {
		if name == m10wJob {
			continue
		}
		other, _ := m3dxExpoJob(yml, name)
		for _, text := range []string{"TestM10", "harness-m10", " -m10"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m10wJob)
			}
		}
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, ubuntu-26.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m10wJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m10wJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m10wJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m10wJob, have, m10wJobLimit)
	}
	run, ok := m4lStep(job, m10wRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m10wJob, m10wRunStep)
	}
	for _, want := range []string{
		"-run '^TestM10$'", " -m10", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m10"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m10wRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m10wRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m10wStepTimeout || time.Duration(n)*time.Minute < m10Deadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm, at least the deadline %s plus 10 minutes", m10wRunStep, m[1], m10wStepTimeout, m10Deadline)
	}
	for _, want := range []string{"name: logs-m10-${{ matrix.os }}", "harness-m10/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m10wJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// TestM10wUnitWorkflowFile checks the committed workflow, which is the only place TestM10 runs, and that the check
// refuses each break of the job.
func TestM10wUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	yml := string(b)
	if err := m10wWorkflow(yml); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, from, to, want string }{
		{"no job", "  vm-m10:\n", "  vm-other:\n", "no vm-m10 job"},
		{"no step", "- name: " + m10wRunStep, "- name: Run M10", "no step named"},
		{"wrong test", "-run '^TestM10$'", "-run '^TestM9$'", "^TestM10$"},
		{"no flag", " -m10\n", "\n", " -m10"},
		{"no workdir", "-workdir \"$RUNNER_TEMP/harness-m10\"", "-workdir \"$RUNNER_TEMP/harness-m9\"", "harness-m10"},
		{"short timeout", "-timeout 40m -v -run '^TestM10$'", "-timeout 30m -v -run '^TestM10$'", "-timeout 30m"},
		{"no failure logs", "name: logs-m10-${{ matrix.os }}", "name: logs-m9x-${{ matrix.os }}", "logs-m10-"},
		{"test in another job", "-run '^TestM9System'", "-run '^TestM9System|^TestM10$'", "m9-system job holds TestM10"},
	} {
		if n := strings.Count(yml, c.from); n != 1 {
			t.Fatalf("%s: the workflow holds %q %d times, want once", c.name, c.from, n)
		}
		if err := m10wWorkflow(strings.Replace(yml, c.from, c.to, 1)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.want)
		}
	}
}
