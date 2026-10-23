package dockerapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSplitImageRef(t *testing.T) {
	cases := []struct{ ref, name, tag string }{
		{"alpine", "alpine", "latest"},
		{"alpine:3", "alpine", "3"},
		{"registry.local:5000/team/app", "registry.local:5000/team/app", "latest"},
		{"registry.local:5000/team/app:1.2", "registry.local:5000/team/app", "1.2"},
		{"jc21/nginx-proxy-manager:2.13@sha256:bbbb", "jc21/nginx-proxy-manager:2.13@sha256:bbbb", ""},
	}
	for _, tc := range cases {
		if name, tag := splitImageRef(tc.ref); name != tc.name || tag != tc.tag {
			t.Errorf("splitImageRef(%q) = %q, %q; want %q, %q", tc.ref, name, tag, tc.name, tc.tag)
		}
	}
}

func TestImageCalls(t *testing.T) {
	mux := http.NewServeMux()
	var seen []string
	mux.HandleFunc("GET /v1.49/images/json", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "list "+r.URL.RawQuery)
		_, _ = io.WriteString(w, `[{"Id":"sha256:aaaa","ParentId":"","RepoTags":["alpine:3"],"RepoDigests":["alpine@sha256:cccc"],`+
			`"Created":1761210902,"Size":8000000,"SharedSize":5000000,"Labels":null,"Containers":-1}]`)
	})
	mux.HandleFunc("GET /v1.49/images/", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "inspect "+r.URL.Path)
		_, _ = io.WriteString(w, `{"Id":"sha256:aaaa","RepoTags":["alpine:3"],"Created":"2026-10-01T00:00:00Z","Size":8000000,`+
			`"Architecture":"amd64","Os":"linux","Config":{"Cmd":["/bin/sh"],"Labels":{"x":"y"}}}`)
	})
	mux.HandleFunc("DELETE /v1.49/images/", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "delete "+r.URL.Path+" "+r.URL.RawQuery)
		_, _ = io.WriteString(w, `[{"Untagged":"alpine:3"},{"Deleted":"sha256:aaaa"}]`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	list, err := c.ListImages(ctx, ListImagesOptions{SharedSize: true, Filters: Filters{"dangling": {"true"}}})
	if err != nil || len(list) != 1 || list[0].RepoDigests[0] != "alpine@sha256:cccc" || list[0].Containers != -1 || list[0].SharedSize != 5000000 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	img, err := c.InspectImage(ctx, "docker.io/library/alpine:3")
	if err != nil || img.Config.Labels["x"] != "y" || img.Os != "linux" {
		t.Fatalf("inspect = %+v, %v", img, err)
	}
	items, err := c.RemoveImage(ctx, "alpine:3", RemoveImageOptions{NoPrune: true})
	if err != nil || !reflect.DeepEqual(items, []ImageDeleteItem{{Untagged: "alpine:3"}, {Deleted: "sha256:aaaa"}}) {
		t.Fatalf("remove = %+v, %v", items, err)
	}
	want := []string{
		"list filters=%7B%22dangling%22%3A%5B%22true%22%5D%7D&shared-size=1",
		"inspect /v1.49/images/docker.io/library/alpine:3/json",
		"delete /v1.49/images/alpine:3 force=false&noprune=true",
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("requests = %q", seen)
	}
	for _, bad := range []string{"", "a?b", "a b", "../../containers/x", "a#b", "a%2Fb"} {
		if _, err := c.InspectImage(ctx, bad); err == nil || !strings.Contains(err.Error(), "invalid image reference") {
			t.Errorf("InspectImage(%q) err = %v", bad, err)
		}
	}
}

func TestPullImageStream(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("POST /v1.49/images/create", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"status":"Pulling from library/alpine","id":"3"}
{"status":"Downloading","progressDetail":{"current":10,"total":100},"progress":"[=>  ]","id":"abc"}
{"errorDetail":{"message":"manifest unknown"},"error":"manifest unknown"}
`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	s, err := c.PullImage(t.Context(), "alpine:3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var msgs []PullMessage
	for {
		m, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	if query != "fromImage=alpine&tag=3" {
		t.Fatalf("query = %s", query)
	}
	if len(msgs) != 3 || msgs[1].ProgressDetail.Total != 100 || msgs[0].Err() != nil {
		t.Fatalf("messages = %+v", msgs)
	}
	if err := msgs[2].Err(); err == nil || err.Error() != "dockerapi: pull: manifest unknown" {
		t.Fatalf("stream failure = %v", err)
	}
	if err := (PullMessage{Error: "denied"}).Err(); err == nil || err.Error() != "dockerapi: pull: denied" {
		t.Fatalf("legacy error field = %v", err)
	}
	query = "unset"
	if _, err := c.PullImage(t.Context(), "alpine:"); err == nil || query != "unset" {
		t.Fatalf("trailing colon: err = %v, query = %s", err, query)
	}
}

func TestVolumeCalls(t *testing.T) {
	mux := http.NewServeMux()
	var created VolumeCreate
	mux.HandleFunc("GET /v1.49/volumes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filters") != `{"label":["darkpanel.module=npm"]}` {
			t.Errorf("filters = %s", r.URL.Query().Get("filters"))
		}
		_, _ = io.WriteString(w, `{"Volumes":[{"Name":"darkpanel-npm-data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/darkpanel-npm-data/_data",`+
			`"CreatedAt":"2026-10-01T00:00:00Z","Scope":"local","Labels":{"darkpanel.module":"npm"},"Options":null}],"Warnings":null}`)
	})
	mux.HandleFunc("GET /v1.49/volumes/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"get `+r.PathValue("name")+`: no such volume"}`) //nolint:gosec // G705: a test fake answering JSON
	})
	mux.HandleFunc("POST /v1.49/volumes/create", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Name":"v1","Driver":"local","Labels":{"darkpanel.module":"npm"},"Scope":"local"}`)
	})
	mux.HandleFunc("DELETE /v1.49/volumes/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "force=false" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"message":"remove v1: volume is in use - [c1]"}`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	vols, err := c.ListVolumes(ctx, Filters{"label": {"darkpanel.module=npm"}})
	if err != nil || len(vols) != 1 || vols[0].Labels["darkpanel.module"] != "npm" {
		t.Fatalf("list = %+v, %v", vols, err)
	}
	if _, err := c.InspectVolume(ctx, "gone"); !IsNotFound(err) {
		t.Fatalf("inspect err = %v", err)
	}
	v, err := c.CreateVolume(ctx, VolumeCreate{Name: "v1", Labels: map[string]string{"darkpanel.module": "npm"}})
	if err != nil || v.Name != "v1" || created.Name != "v1" || created.Labels["darkpanel.module"] != "npm" {
		t.Fatalf("create = %+v, %v (sent %+v)", v, err, created)
	}
	if err := c.RemoveVolume(ctx, "v1", false); !IsConflict(err) {
		t.Fatalf("remove err = %v", err)
	}
}
