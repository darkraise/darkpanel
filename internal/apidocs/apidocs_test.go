package apidocs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/apidocs"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

type adAddress struct {
	Host string `json:"host" jsonschema_description:"The host name."`
	Port int    `json:"port" jsonschema:"minimum=1,maximum=65535"`
}

type adInput struct {
	Name     string      `json:"name" jsonschema_description:"The item name, such as web-<n>."`
	Mode     string      `json:"mode,omitempty" jsonschema:"enum=fast,enum=safe" jsonschema_description:"How to apply it: fast | safe."`
	Password string      `json:"password" secret:"true"`
	Target   adAddress   `json:"target"`
	Backups  []adAddress `json:"backups,omitempty"`
	Tags     []string    `json:"tags,omitempty"`
}

type adOutput struct {
	ID     string    `json:"id"`
	Token  string    `json:"token" secret:"true"`
	Target adAddress `json:"target"`
}

type adEmpty struct{}

type adStatus struct {
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty" jsonschema_description:"Why it is not healthy."`
}

// adHandler implements every handler interface and does nothing; Generate
// never calls handlers.
type adHandler[In, Out any] struct{}

func (adHandler[In, Out]) Read(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (adHandler[In, Out]) Plan(context.Context, core.Env, In) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (adHandler[In, Out]) Apply(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (adHandler[In, Out]) Verify(context.Context, core.Env, In, Out) error { return nil }

func (adHandler[In, Out]) Snapshot(context.Context, core.Env, In) ([]byte, error) { return nil, nil }

func (adHandler[In, Out]) Revert(context.Context, core.Env, []byte) (core.RevertOutcome, error) {
	return core.RevertOutcome{}, nil
}

// adDemo has one action of each kind that gets a section, a break-glass
// action that gets none, and a resource.
type adDemo struct{}

func (adDemo) Manifest() core.Manifest {
	h := adHandler[adInput, adOutput]{}
	return core.Manifest{
		ID: "demo", Version: 1,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: "demo.item.get", Summary: "Show one item", Version: 1, Side: core.SideAgent}, h),
			core.DefineRevertible(core.ActionSpec{Name: "demo.item.add", Summary: "Add an item", Version: 1, Side: core.SideAgent,
				LockKeys: []string{"demo"}, RepairedBy: "demo.item.repair"}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.repair", Summary: "Repair the items", Version: 1, Class: core.ClassIrreversible,
				Side: core.SideAgent, LockKeys: []string{"demo"}}, adHandler[adEmpty, adOutput]{}),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.remove", Summary: "Remove an item", Version: 1, Class: core.ClassCompensable,
				Side: core.SideController, CompensatedBy: "demo.item.add"}, h),
			core.DefineReveal[adEmpty, adOutput](core.ActionSpec{Name: "demo.item.reveal", Summary: "Reveal the item secret", Version: 1,
				Side: core.SideAgent, MinScope: core.ScopeAdmin},
				func(context.Context, core.Env, adEmpty) (core.Revealed[adOutput], error) {
					return core.Revealed[adOutput]{}, nil
				}),
			core.DefineMutating(core.ActionSpec{Name: "demo.secret.reset", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
				BreakGlass: true}, adHandler[adEmpty, adOutput]{}),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.status", func(context.Context, core.Env) (adStatus, error) { return adStatus{}, nil }),
		},
	}
}

// adQuiet has only a break-glass action, so it has no operation and no page.
type adQuiet struct{}

func (adQuiet) Manifest() core.Manifest {
	return core.Manifest{
		ID: "quiet", Version: 1,
		Actions: []core.ActionDef{
			core.DefineMutating(core.ActionSpec{Name: "quiet.secret.reset", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
				BreakGlass: true}, adHandler[adEmpty, adOutput]{}),
		},
	}
}

var adRoutes = []schema.Route{
	{Method: "GET", Path: "/healthz", Summary: "Health", Public: true},
	{Method: "POST", Path: "/api/v1/auth/login", Summary: "Log in", Public: true},
	{Method: "GET", Path: "/api/v1/changes/{id}", Summary: "Show a change"},
}

func adRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(adDemo{}, adQuiet{})
	if err != nil {
		t.Fatalf("core.NewRegistry: %v", err)
	}
	return reg
}

func adGenerate(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := apidocs.Generate(adRegistry(t), adRoutes)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return files
}

// adSection returns the text of the ## section of page titled heading, up to
// the next ## heading.
func adSection(t *testing.T, page, heading string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, "\n## "+heading+"\n")
	if !ok {
		t.Fatalf("the page has no section %q:\n%s", heading, page)
	}
	body, _, _ := strings.Cut(rest, "\n## ")
	return "## " + heading + "\n" + body
}

func adLines(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestAdGenerateKeys(t *testing.T) {
	files := adGenerate(t)
	if got, want := slices.Sorted(maps.Keys(files)), []string{"README.md", "demo.md", "openapi.json"}; !slices.Equal(got, want) {
		t.Errorf("Generate keys = %v, want %v: one page per module with an operation, and none for a module with only a break-glass action", got, want)
	}
}

func TestAdOpenAPIDocumentIsTheIndentedSchemaDocument(t *testing.T) {
	raw, err := schema.OpenAPI(adRegistry(t), adRoutes)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.Indent(&want, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	want.WriteByte('\n')
	if got := adGenerate(t)["openapi.json"]; !bytes.Equal(got, want.Bytes()) {
		t.Errorf("openapi.json is not schema.OpenAPI's document indented with two spaces and a final newline; it starts %.60q", got)
	}
}

func TestAdModulePageSections(t *testing.T) {
	page := string(adGenerate(t)["demo.md"])
	if !strings.HasPrefix(page, "# demo API\n\nBack to the [API reference](README.md).\n\n## ") {
		t.Errorf("the page starts %.100q, want the title, the link to the index and the first section", page)
	}
	var headings []string
	for _, line := range strings.Split(page, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			headings = append(headings, h)
		}
	}
	want := []string{"demo.item.add v1", "demo.item.get v1", "demo.item.remove v1", "demo.item.repair v1", "demo.item.reveal v1", "demo.status"}
	if !slices.Equal(headings, want) {
		t.Errorf("sections = %q, want %q (sorted by name and version, no break-glass action)", headings, want)
	}

	add := adSection(t, page, "demo.item.add v1")
	wantAdd := adLines(
		"## demo.item.add v1",
		"",
		"Add an item",
		"",
		"| Operation | Request |",
		"|---|---|",
		"| plan | `POST /api/v1/hosts/{host}/actions/demo.item.add/v1/plan` |",
		"| apply | `POST /api/v1/hosts/{host}/actions/demo.item.add/v1/apply` |",
		"",
		"Minimum scope: `operate`.",
		"",
		"### Input",
		"",
		"Sent in the `input` field of the request body.",
		"",
		"| Field | Type | Required | Description |",
		"|---|---|---|---|",
		"| `backups` | array of object | no |  |",
		"| `backups[].host` | string | yes | The host name. |",
		"| `backups[].port` | integer | yes |  |",
		"| `mode` | string | no | How to apply it: fast \\| safe. Values: fast, safe. |",
		"| `name` | string | yes | The item name, such as web-\\<n\\>. |",
		"| `password` | string | yes | Secret. |",
		"| `tags` | array of string | no |  |",
		"| `target` | object | yes |  |",
		"| `target.host` | string | yes | The host name. |",
		"| `target.port` | integer | yes |  |",
		"",
		"### Output",
		"",
		"Returned in the `output` field of the change that apply returns.",
		"",
		"| Field | Type | Required | Description |",
		"|---|---|---|---|",
		"| `id` | string | yes |  |",
		"| `target` | object | yes |  |",
		"| `target.host` | string | yes | The host name. |",
		"| `target.port` | integer | yes |  |",
		"| `token` | string | yes | Secret. |",
		"",
	)
	if strings.TrimRight(add, "\n") != strings.TrimRight(wantAdd, "\n") {
		t.Errorf("section demo.item.add v1 =\n%s\nwant\n%s", add, wantAdd)
	}

	for _, tt := range []struct{ heading, want string }{
		{"demo.item.get v1", "| read | `POST /api/v1/hosts/{host}/actions/demo.item.get/v1/read` |"},
		{"demo.item.get v1", "Minimum scope: `read`."},
		{"demo.item.remove v1", "| plan | `POST /api/v1/actions/demo.item.remove/v1/plan` |"},
		{"demo.item.remove v1", "Minimum scope: `admin`."},
		{"demo.item.repair v1", "This action takes no input fields."},
		{"demo.item.reveal v1", "| reveal | `POST /api/v1/hosts/{host}/actions/demo.item.reveal/v1/reveal` |"},
		{"demo.item.reveal v1", "Returned in the `value` field of the response."},
		{"demo.item.reveal v1", "Minimum scope: `admin`."},
	} {
		if sec := adSection(t, page, tt.heading); !strings.Contains(sec, tt.want+"\n") {
			t.Errorf("section %s lacks the line %q:\n%s", tt.heading, tt.want, sec)
		}
	}

	status := adSection(t, page, "demo.status")
	wantStatus := adLines(
		"## demo.status",
		"",
		"Read demo.status",
		"",
		"| Operation | Request |",
		"|---|---|",
		"| read | `GET /api/v1/hosts/{host}/resources/demo/status` |",
		"",
		"Minimum scope: `read`.",
		"",
		"### Output",
		"",
		"Returned in the `output` field of the response.",
		"",
		"| Field | Type | Required | Description |",
		"|---|---|---|---|",
		"| `detail` | string | no | Why it is not healthy. |",
		"| `healthy` | boolean | yes |  |",
	)
	if strings.TrimRight(status, "\n") != strings.TrimRight(wantStatus, "\n") {
		t.Errorf("section demo.status =\n%s\nwant\n%s", status, wantStatus)
	}
}

func TestAdIndexPage(t *testing.T) {
	index := string(adGenerate(t)["README.md"])
	if !strings.HasPrefix(index, "# Darkpanel API reference\n") {
		t.Errorf("the index starts %.60q, want the title", index)
	}
	for _, want := range []string{
		"## Authentication",
		"| `session` | apiKey | cookie `__Host-darkpanel_session` | Session cookie; darkpanel_session on the plain-HTTP loopback listener. |",
		"| `token` | http | `Authorization: Bearer` header | API token dpt_\\<id\\>_\\<secret\\>. |",
		"## Common components",
		"### Plan",
		"### Change",
		"### ApplyRequest",
		"### Problem",
		"| `planId` | string | yes |  |",
		"| `expiresAt` | string (date-time) | yes |  |",
		"## Routes",
		"| GET | `/api/v1/changes/{id}` | session or token | Show a change |",
		"| GET | `/healthz` | none | Health |",
		"| POST | `/api/v1/auth/login` | none | Log in |",
		"## Modules",
		"- [demo](demo.md)",
	} {
		if !strings.Contains(index, want+"\n") {
			t.Errorf("the index lacks the line %q", want)
		}
	}
	if strings.Contains(index, "quiet") {
		t.Error("the index links a module without an operation")
	}
	if strings.Contains(index, "demo.item.add") {
		t.Error("the index lists an action; the routes table holds only the hand-written routes")
	}
	order := []string{"## Authentication", "## Common components", "## Routes", "## Modules"}
	last := -1
	for _, h := range order {
		at := strings.Index(index, "\n"+h+"\n")
		if at < last {
			t.Errorf("section %q is out of order in the index", h)
		}
		last = at
	}
}

func TestAdGenerateIsDeterministic(t *testing.T) {
	first, second := adGenerate(t), adGenerate(t)
	for name, b := range first {
		if !bytes.Equal(b, second[name]) {
			t.Errorf("two Generate calls differ in %s", name)
		}
	}
}

func TestAdWriteReplacesTheGeneratedFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"stale.md": "old", "stale.json": "{}", "notes.txt": "keep", "sub/inner.md": "keep",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{"README.md": []byte("# index\n"), "openapi.json": []byte("{}\n")}
	for range 2 {
		if err := apidocs.Write(dir, files); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a path under the test's temporary directory
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, gone := range []string{"stale.md", "stale.json"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Write left %s behind: %v", gone, err)
		}
	}
	for _, kept := range []string{"notes.txt", "sub/inner.md"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("Write removed %s, which is not a .md or .json file directly in the directory: %v", kept, err)
		}
	}
}

// TestAdAPIDocsCurrent regenerates the reference in memory and fails when a
// committed file under docs/api/ differs, is missing, or belongs to no key.
// make api-docs fixes all three for the .md and .json files it manages.
func TestAdAPIDocsCurrent(t *testing.T) {
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	files, err := apidocs.Generate(reg, api.Routes())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := filepath.Join("..", "..", "docs", "api")
	for _, name := range slices.Sorted(maps.Keys(files)) {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))) //nolint:gosec // G304: a path under the committed docs/api directory
		switch {
		case errors.Is(err, fs.ErrNotExist):
			t.Errorf("docs/api/%s is missing; run make api-docs", name)
		case err != nil:
			t.Errorf("docs/api/%s: %v", name, err)
		case !bytes.Equal(got, files[name]):
			t.Errorf("docs/api/%s is out of date; run make api-docs", name)
		}
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			t.Errorf("docs/api/%s belongs to no generated file; delete it (make api-docs removes a stale .md or .json file)", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("walk docs/api: %v", err)
	}
}
