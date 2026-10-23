package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// avrModule has an internal read, which the availability route leaves out.
type avrModule struct{}

func (avrModule) Manifest() core.Manifest {
	return core.Manifest{ID: "avr", Version: 1, Actions: []core.ActionDef{
		core.DefineInternalRead(core.ActionSpec{Name: "avr.cert.get", Version: 1, Side: core.SideAgent},
			func(context.Context, core.Env, demoInput) (core.Revealed[demoOutput], error) {
				return core.Revealed[demoOutput]{}, nil
			}),
		core.DefineRead(core.ActionSpec{Name: "avr.cert.list", Version: 1, Side: core.SideAgent}, demoHandler[demoInput, demoOutput]{}),
	}}
}

// avrServe serves h's configuration with source as its Availability.
func avrServe(t *testing.T, h *harness, source func(context.Context, string, string) (core.ModuleAvailability, error)) {
	t.Helper()
	cfg := h.cfg
	cfg.Availability = source
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
}

func TestAvailabilityAPIRoute(t *testing.T) {
	h := newHarnessWith(t, avrModule{})
	var asked []string
	avrServe(t, h, func(_ context.Context, hostID, moduleID string) (core.ModuleAvailability, error) {
		asked = append(asked, hostID+"/"+moduleID)
		switch moduleID {
		case "demo":
			return core.ModuleAvailability{Module: "demo", Available: true,
				Actions: []core.ActionAvailability{{Name: "demo.x.y", Reason: "down"}}}, nil
		case "avr":
			return core.ModuleAvailability{Module: "avr", Available: true, Actions: []core.ActionAvailability{
				{Name: "avr.cert.get", Available: true}, {Name: "avr.cert.list", Available: true}}}, nil
		}
		return core.ModuleAvailability{}, errcode.New(errcode.HostOffline, "host h1 is offline")
	})
	read := h.token(core.ScopeRead)
	rec := h.serve(withToken(request("GET", "/api/v1/hosts/h1/modules/demo/availability", ""), read))
	want := `{"module":"demo","available":true,"reason":null,"actions":[{"name":"demo.x.y","available":false,"reason":"down"}]}`
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != want {
		t.Errorf("answer %d %s %s\nwant 200 %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body, want)
	}
	rec = h.serve(withToken(request("GET", "/api/v1/hosts/h1/modules/avr/availability", ""), read))
	var got struct {
		Actions []struct {
			Name string `json:"name"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Actions) != 1 || got.Actions[0].Name != "avr.cert.list" {
		t.Errorf("answer %s, want the internal action left out", rec.Body)
	}
	decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/hosts/h1/modules/other/availability", ""), read)),
		503, errcode.HostOffline)
	decodeProblem(t, h.serve(request("GET", "/api/v1/hosts/h1/modules/demo/availability", "")), 401, errcode.AuthRequired)
	if len(asked) != 3 || asked[0] != "h1/demo" {
		t.Errorf("the source was asked %v", asked)
	}

	avrServe(t, h, nil)
	decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/hosts/h1/modules/demo/availability", ""), read)),
		404, errcode.NotFound)
}
