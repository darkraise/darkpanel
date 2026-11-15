package dockerapi

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestExecCreateStartInspect(t *testing.T) {
	mux := http.NewServeMux()
	var created map[string]any
	var started map[string]any
	mux.HandleFunc("POST /v1.49/containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "gone" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"No such container: gone"}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"e1"}`)
	})
	mux.HandleFunc("POST /v1.49/exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&started); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /v1.49/exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "running":
			_, _ = io.WriteString(w, `{"ID":"running","Running":true,"ExitCode":null}`)
		case "failed":
			_, _ = io.WriteString(w, `{"ID":"failed","Running":false,"ExitCode":1}`)
		default:
			_, _ = io.WriteString(w, `{"ID":"e1","Running":false,"ExitCode":0}`)
		}
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	id, err := c.ExecCreate(ctx, "darkpanel-npm", ExecCreate{Cmd: []string{"nginx", "-t"}, User: "0:0"})
	if err != nil || id != "e1" {
		t.Fatalf("create = %q, %v", id, err)
	}
	wantCreate := map[string]any{"AttachStdin": false, "AttachStdout": false, "AttachStderr": false, "Cmd": []any{"nginx", "-t"}, "User": "0:0"}
	if !reflect.DeepEqual(created, wantCreate) {
		t.Fatalf("create body = %v", created)
	}
	if err := c.ExecStart(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(started, map[string]any{"Detach": true, "Tty": false}) {
		t.Fatalf("start body = %v", started)
	}
	for id, want := range map[string]ExecInspect{
		"running": {ID: "running", Running: true},
		"e1":      {ID: "e1", ExitCode: new(0)},
		"failed":  {ID: "failed", ExitCode: new(1)},
	} {
		got, err := c.ExecInspect(ctx, id)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("inspect %s = %+v, %v; want %+v", id, got, err, want)
		}
	}
	if _, err := c.ExecCreate(ctx, "gone", ExecCreate{Cmd: []string{"x"}}); !IsNotFound(err) {
		t.Fatalf("create in a missing container err = %v", err)
	}
	if _, err := c.ExecCreate(ctx, "", ExecCreate{}); err == nil {
		t.Fatal("an empty container was accepted")
	}
	if err := c.ExecStart(ctx, ""); err == nil {
		t.Fatal("an empty exec id was accepted")
	}
}

func TestContainerTop(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/containers/{id}/top", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"Titles":["PID","PPID","COMMAND"],"Processes":[["10","1","nginx: master process nginx"],["11","10","nginx: worker process"]]}`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	got, err := c.ContainerTop(t.Context(), "darkpanel-npm", "-eo pid,ppid,args")
	if err != nil {
		t.Fatal(err)
	}
	want := TopResult{Titles: []string{"PID", "PPID", "COMMAND"}, Processes: [][]string{{"10", "1", "nginx: master process nginx"}, {"11", "10", "nginx: worker process"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("top = %+v", got)
	}
	if query != "ps_args=-eo+pid%2Cppid%2Cargs" {
		t.Fatalf("query = %q", query)
	}
	if _, err := c.ContainerTop(t.Context(), "", ""); err == nil {
		t.Fatal("an empty container was accepted")
	}
}
