package api

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

func TestCheckOpenAPI(t *testing.T) {
	demo, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	built, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	for name, reg := range map[string]*core.Registry{"demo": demo, "built": built} {
		if err := CheckOpenAPI(reg); err != nil {
			t.Errorf("%s registry: %v", name, err)
		}
	}
}

func TestCheckOpenAPIFindsGaps(t *testing.T) {
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	routes := (&Server{}).networkRoutes()
	doc, err := schema.OpenAPI(reg, Routes())
	if err != nil {
		t.Fatal(err)
	}

	missing := strings.Replace(string(doc), `"/api/v1/changes/{id}/revert"`, `"/api/v1/changes/{id}/rewind"`, 1)
	err = checkOpenAPI(reg, routes, []byte(missing))
	for _, want := range []string{
		"route POST /api/v1/changes/{id}/revert: the document lacks",
		"document operation POST /api/v1/changes/{id}/rewind: no network route serves it",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("renamed route: error %v, want %q", err, want)
		}
	}

	noAction := strings.Replace(string(doc), `"/api/v1/hosts/{host}/actions/demo.item.add/v1/plan"`, `"/api/v1/hosts/{host}/actions/demo.item.add/v1/draft"`, 1)
	err = checkOpenAPI(reg, routes, []byte(noAction))
	if err == nil || !strings.Contains(err.Error(), "the document lacks POST /api/v1/hosts/{host}/actions/demo.item.add/v1/plan") {
		t.Errorf("missing action operation: %v", err)
	}

	breakGlass := strings.Replace(string(doc), `"/api/v1/actions/demo.item.remove/v1/plan"`, `"/api/v1/actions/demo.secret.reset/v1/plan"`, 1)
	err = checkOpenAPI(reg, routes, []byte(breakGlass))
	if err == nil || !strings.Contains(err.Error(), "break-glass action demo.secret.reset is listed") {
		t.Errorf("listed break-glass action: %v", err)
	}

	if err := checkOpenAPI(reg, routes, []byte("{")); err == nil {
		t.Error("checkOpenAPI accepted a malformed document")
	}
}
