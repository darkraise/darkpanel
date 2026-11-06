package derp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// w10rgDrive runs call and advances the host's clock by one second whenever a timer waits on it.
func w10rgDrive(t *testing.T, h *dpHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
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
			t.Fatal("the pass did not finish")
		}
	}
}

// w10rgSeedRegion records the server on cgHost with its region and the record's error text.
func w10rgSeedRegion(t *testing.T, h *dpHost, regionID int, recordError string) {
	t.Helper()
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: cgHost, Mode: "ip", HostName: "203.0.113.9", RegionID: regionID, RegionCode: "dp-eu",
		DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now(), Error: recordError,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestW10RgOmitRefusalNamesTheRegionsItCouldNotProbe: when no other region is known to be ready because a probe
// failed, the refusal that asks for the acknowledgement says which region and why.
func TestW10RgOmitRefusalNamesTheRegionsItCouldNotProbe(t *testing.T) {
	const base = `omitting the default regions needs the acknowledgement "` + reAck + `"`
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"an agent that did not answer", errors.New("simulated agent failure"), base + "; the probes of other regions failed: region 901: its server could not be probed"},
		{"a refusal with a detail", errcode.New(errcode.HostOffline, "host h3 is offline"), base + "; the probes of other regions failed: region 901: host h3 is offline"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := reReady(t)
			h.addHost("h3", "charlie", false)
			reAgent(h, "h3").Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return nil, tc.err }
			err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
				HostID: "h3", Mode: "ip", HostName: "203.0.113.20", RegionID: 901, RegionCode: "us-derp", DerperVersion: "v1.104.1",
				CertName: rePin, UnitActive: true, CheckedAt: h.clock.Now(),
			})
			if err != nil {
				t.Fatal(err)
			}
			h.policy.Text = `{"derpMap": {"Regions": {"901": {"RegionID": 901, "RegionCode": "us-derp", "RegionName": "US",
				"Nodes": [{"Name": "901a", "RegionID": 901, "HostName": "203.0.113.20"}]}}}}`
			_, err = reRun(t, h, reOmitInput(""))
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.InvalidInput || coded.Detail != tc.want ||
				len(coded.Fields) != 1 || coded.Fields[0].Path != "/acknowledge" || coded.Fields[0].Message != tc.want {
				t.Errorf("err = %#v, want invalid input at /acknowledge with the detail %q", err, tc.want)
			}
		})
	}
}

// TestW10RgRemoveRefusesAGrantsMemberThatIsNotAList: the remove op refuses what the set op refuses, instead of
// reading a grants member that is not a list as an empty one.
func TestW10RgRemoveRefusesAGrantsMemberThatIsNotAList(t *testing.T) {
	h := reReady(t)
	h.policy.Text = `{"derpMap": {"Regions": {"900": {"RegionID": 900, "RegionCode": "eu-derp", "RegionName": "EU",
		"Nodes": [{"Name": "900a", "RegionID": 900, "HostName": "203.0.113.10"}]}}}, "grants": {"src": ["*"]}}`
	_, err := reRun(t, h, RegionEditsInput{Op: "remove", RegionID: 900})
	if err == nil || !strings.Contains(err.Error(), "the policy's grants member is not a list") {
		t.Errorf("remove over a grants member that is an object: err = %v, want the error that it is not a list", err)
	}
}

// TestW10RgUpgradeAndRotateRefuseARegionTheLastCheckCouldNotRead: a record whose last pass could not read the
// policy keeps a region that may be out of date, so neither composite plans from it.
func TestW10RgUpgradeAndRotateRefuseARegionTheLastCheckCouldNotRead(t *testing.T) {
	const detail = "the controller could not read the tailnet policy at its last check, so whether the server on " + cgHost +
		" publishes a region is not known; try again after the next check"
	tests := []struct {
		name string
		def  func(dp) core.ActionDef
		in   any
	}{
		{"upgrade", dp.serverUpgrade, UpgradeInput{HostID: cgHost}},
		{"rotate", dp.certRotate, CertRotateInput{HostID: cgHost}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			w10rgSeedRegion(t, h, 905, serversRegionUnknown)
			_, err := cgSteps(t, h, tc.def(h.dp()), tc.in)
			coded := errcode.From(err)
			if err == nil || coded.Code != errcode.DERPNotReady || coded.Detail != detail {
				t.Errorf("err = %#v, want derp.not_ready with the detail %q", err, detail)
			}
			w10rgSeedRegion(t, h, 905, "")
			if _, err := cgSteps(t, h, tc.def(h.dp()), tc.in); err != nil {
				t.Errorf("with the region read at the last check: %v, want the steps", err)
			}
		})
	}
}

// TestW10RgPassWritesNothingForAHostWithoutAServer: with the controller database's one writer held by someone
// else, a pass over a host that has no server and no record has nothing to write and so does not wait for it.
func TestW10RgPassWritesNothingForAHostWithoutAServer(t *testing.T) {
	h := newDPHost(t)
	h.addHost("h3", "charlie", false)
	srServe(h, "h3", ServerStatus{})
	conn, err := h.ctlDB.Writer().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	held := true
	release := func() {
		if held {
			held = false
			_ = conn.Close()
		}
	}
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := h.dp().checkServers(ctx, srDeps(h), h.clock.Now()); err != nil {
		t.Errorf("a pass over a host with no server and no record: %v, want no write and no error", err)
	}
	release()
	srPut(t, h, serverRecord{HostID: "h3", Mode: "ip", HostName: srIP, DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now()})
	srPass(t, h)
	if _, ok := srLoad(t, h, "h3"); ok {
		t.Error("the record of a host that lost its server is still stored")
	}
}

// TestW10RgCancelledRegionLookupEndsThePassBeforeItsWrites: a pass cancelled while it looks the regions up
// answers the context's own error and stores nothing.
func TestW10RgCancelledRegionLookupEndsThePassBeforeItsWrites(t *testing.T) {
	h := srWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := h.dp()
	connected := d.d.connected
	d.d.connected = func(ctx context.Context, db *store.DB) (bool, error) {
		cancel()
		return connected(ctx, db)
	}
	err := d.checkServers(ctx, srDeps(h), h.clock.Now())
	if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
		t.Errorf("a pass cancelled during its region lookup returned %v, want the context's own error", err)
	}
	for _, id := range []string{"h1", "h2"} {
		if _, ok := srLoad(t, h, id); ok {
			t.Errorf("the pass stored a record for %s after it was cancelled", id)
		}
	}
}

// TestW10RgPassReadsHostsTogetherAndBoundsEachRead: the read of one silent agent does not hold back the other
// hosts' reads, ends at its own bound, and is stored as a host that could not be read.
func TestW10RgPassReadsHostsTogetherAndBoundsEachRead(t *testing.T) {
	h := srWorld(t)
	srPut(t, h, serverRecord{HostID: "h1", Mode: "ip", HostName: srIP, DerperVersion: "v1.104.1", CertName: srPin, UnitActive: true, CheckedAt: h.clock.Now()})
	answers := map[string]ServerStatus{
		"h2": {Installed: true, Mode: "letsencrypt", HostName: "derp.example.com", DerperVersion: "v1.104.1", UnitActive: true},
		"h3": {},
	}
	var mu sync.Mutex
	started := map[string]time.Time{}
	hosts := coretest.MapHosts{}
	for _, id := range []string{"h1", "h2", "h3"} {
		hosts[id] = &coretest.FakeAgent{ReadFunc: func(ctx context.Context, _ core.Principal, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
			mu.Lock()
			started[id] = h.clock.Now()
			mu.Unlock()
			if id == "h1" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return json.Marshal(answers[id])
		}}
	}
	deps := srDeps(h)
	deps.Hosts = hosts
	begin := h.clock.Now()
	var err error
	w10rgDrive(t, h, func() { err = h.dp().checkServers(context.Background(), deps, h.clock.Now()) })
	if err != nil {
		t.Fatalf("checkServers: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	limit := begin.Add(serversReadWait)
	for _, id := range []string{"h2", "h3"} {
		if at, ok := started[id]; !ok || !at.Before(limit) {
			t.Errorf("the read of %s started at %v, want it started before the read of h1 ended at %v", id, at, limit)
		}
	}
	if r, ok := srLoad(t, h, "h1"); !ok || r.Error != probeAgentNoAnswer {
		t.Errorf("record of h1 = %+v, %v; want its error to be %q", r, ok, probeAgentNoAnswer)
	}
	if _, ok := srLoad(t, h, "h2"); !ok {
		t.Error("no record of h2, whose read answered at once")
	}
	if _, ok := srLoad(t, h, "h3"); ok {
		t.Error("a record of h3, which has no server")
	}
}
