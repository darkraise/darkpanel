package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// xuContributor contributes one route and one service, both named name, or
// fails with err.
type xuContributor struct {
	name string
	err  error
	got  *ext.Deps
}

func (c *xuContributor) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	c.got = &deps
	if c.err != nil {
		return nil, nil, c.err
	}
	route := ext.Route{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/test/" + c.name,
		Handler: http.NotFoundHandler()}
	svc := ext.Service{Name: "test." + c.name, Run: func(ctx context.Context) error { <-ctx.Done(); return nil }}
	return []ext.Route{route}, []ext.Service{svc}, nil
}

func TestContributionsCollectInOrder(t *testing.T) {
	a, b := &xuContributor{name: "a"}, &xuContributor{name: "b"}
	s := &Server{opts: Options{Contributors: []ext.Controller{a, b}}, localHostID: "01LOCAL"}
	routes, services, err := s.contributions()
	if err != nil {
		t.Fatal(err)
	}
	var paths, names []string
	for _, r := range routes {
		if strings.HasPrefix(r.Path, "/api/v1/test/") {
			paths = append(paths, r.Path)
		}
	}
	for _, svc := range services {
		if strings.HasPrefix(svc.Name, "test.") {
			names = append(names, svc.Name)
		}
	}
	if want := []string{"/api/v1/test/a", "/api/v1/test/b"}; !slices.Equal(paths, want) {
		t.Errorf("routes %v, want %v", paths, want)
	}
	if want := []string{"test.a", "test.b"}; !slices.Equal(names, want) {
		t.Errorf("services %v, want %v", names, want)
	}
	if a.got == nil || b.got == nil || a.got.LocalHostID != "01LOCAL" || b.got.LocalHostID != "01LOCAL" {
		t.Errorf("deps given = %+v and %+v, want the server's", a.got, b.got)
	}
}

func TestContributionErrorNamesTheContributor(t *testing.T) {
	ok, broken := &xuContributor{name: "ok"}, &xuContributor{name: "broken", err: errors.New("no credentials table")}
	s := &Server{opts: Options{Contributors: []ext.Controller{ok, broken}}}
	_, _, err := s.contributions()
	if err == nil || !strings.Contains(err.Error(), "contributor 1") || !strings.Contains(err.Error(), "no credentials table") {
		t.Fatalf("contributions = %v, want the error of contributor 1", err)
	}
}

func TestControllerFuncIsAController(t *testing.T) {
	var called bool
	f := controllerFunc(func(ext.Deps) ([]ext.Route, []ext.Service, error) {
		called = true
		return nil, nil, nil
	})
	if _, _, err := f.Controller(ext.Deps{}); err != nil || !called {
		t.Errorf("controllerFunc.Controller = %v, called %v", err, called)
	}
}
