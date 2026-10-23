package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
)

type crvIn struct {
	Name string `json:"name" jsonschema_description:"Key name."`
}

type crvOut struct {
	Key string `json:"key" secret:"true"`
}

func crvValue(context.Context, core.Env, crvIn) (core.Revealed[crvOut], error) {
	return core.Revealed[crvOut]{}, nil
}

// crvModule has an agent and a controller reveal action and an agent
// Internal read.
type crvModule struct{}

func (crvModule) Manifest() core.Manifest {
	return core.Manifest{ID: "crv", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineReveal(core.ActionSpec{Name: "crv.key.reveal", Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin,
			Summary: "Reveal a key."}, crvValue),
		core.DefineReveal(core.ActionSpec{Name: "crv.vault.reveal", Version: 1, Side: core.SideController, MinScope: core.ScopeAdmin,
			Summary: "Reveal the vault key."}, crvValue),
		core.DefineInternalRead(core.ActionSpec{Name: "crv.cert.get", Version: 1, Side: core.SideAgent}, crvValue),
	}}
}

// crvRegistry is the core module, as testRegistry builds it, plus
// crvModule. It is kept apart from testRegistry, whose tests expect a
// command for every action.
func crvRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(coreactions.Module(coreactions.Deps{}), crvModule{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestRevealCommand(t *testing.T) {
	const hostPath = "/api/v1/hosts/h1/actions/crv.key.reveal/v1/reveal"
	forbidden := `{"type":"about:blank","title":"Forbidden","status":403,"detail":"this request needs the admin scope","code":"auth.forbidden"}`
	unknown := `{"type":"about:blank","title":"Unknown action","status":404,"detail":"no action crv.key.reveal/v1","code":"action.unknown"}`
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: hostPath, body: `{"input":{"name":"x"}}`, reply: `{"value":{"key":"k-x"}}`},
		hostList("h1:web"),
		exchange{method: "POST", path: hostPath, body: `{"input":{"name":"x"}}`, reply: `{"value":{"key":"k-x"}}`},
		exchange{method: "POST", path: "/api/v1/actions/crv.vault.reveal/v1/reveal", body: `{"input":{"name":"y"}}`,
			reply: `{"value":"v-y"}`},
		hostList("h1:web"),
		exchange{method: "POST", path: hostPath, body: `{"input":{"name":"x"}}`, status: 403, problem: true, reply: forbidden},
		hostList("h1:web"),
		exchange{method: "POST", path: hostPath, body: `{"input":{"name":"x"}}`, status: 404, problem: true, reply: unknown},
	)
	cli := cliEnv{reg: crvRegistry(t), env: tokenEnv}
	for _, tc := range []struct {
		args []string
		code int
		out  string
		err  string
	}{
		{[]string{"crv", "key", "reveal", "--host", "h1", "--name", "x"}, ExitOK, `{"key":"k-x"}` + "\n", ""},
		{[]string{"crv", "key", "reveal", "--name", "x", "--json"}, ExitOK, `{"key":"k-x"}` + "\n", ""},
		{[]string{"crv", "vault", "reveal", "--name", "y"}, ExitOK, `"v-y"` + "\n", ""},
		{[]string{"crv", "key", "reveal", "--name", "x"}, ExitAuth, "", "this request needs the admin scope"},
		{[]string{"crv", "key", "reveal", "--name", "x"}, ExitError, "", "no action crv.key.reveal/v1"},
	} {
		out, errOut, code := cli.run(t, append(tc.args, "--url", base)...)
		if code != tc.code || out != tc.out || !strings.Contains(errOut, tc.err) {
			t.Errorf("%q: exit %d, out %q, err %q; want exit %d, out %q and %q", tc.args, code, out, errOut, tc.code, tc.out, tc.err)
		}
	}
}

func TestRevealHasNoLocal(t *testing.T) {
	root, err := NewRoot(Options{Registry: crvRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	cmd, ok := FindCommand(root, "crv.key.reveal")
	if !ok || cmd.Annotations[annotationLocal] != "" {
		t.Fatalf("crv key reveal: found %v, annotations %v; want a command without --local", ok, cmd.Annotations)
	}
	_, errOut, code := cliEnv{reg: crvRegistry(t)}.run(t, "crv", "key", "reveal", "--local", "--name", "x")
	if code != ExitUsage || !strings.Contains(errOut, "--local cannot run darkpanel crv key reveal: a reveal runs only through the controller") {
		t.Errorf("--local: exit %d, err %q", code, errOut)
	}
}

func TestNoInternalCommand(t *testing.T) {
	root, err := NewRoot(Options{Registry: crvRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := FindCommand(root, "crv.cert.get"); ok {
		t.Error("the internal action crv.cert.get has a command")
	}
	crv, _, err := root.Find([]string{"crv"})
	if err != nil {
		t.Fatal(err)
	}
	var groups []string
	for _, c := range crv.Commands() {
		groups = append(groups, c.Name())
	}
	if strings.Join(groups, " ") != "key vault" {
		t.Errorf("crv has the commands %q, want key and vault only", groups)
	}
}
