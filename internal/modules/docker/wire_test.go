package docker

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestDKWireTypesExist names every wire type, so a missing or renamed one
// fails the build.
func TestDKWireTypesExist(t *testing.T) {
	all := []any{
		Engine{}, Port{}, Usage{}, Container{}, ContainerList{}, Image{}, ImageList{}, Network{}, NetworkList{},
		Volume{}, VolumeList{}, Project{}, ProjectList{}, ContainerInput{}, ContainerState{}, ContainerRemoveInput{},
		Removed{}, EnvVar{}, Mount{}, EndpointView{}, ContainerDetail{}, LogsInput{}, LogLine{}, LogsOutput{},
		RevealedEnv{}, ImagePullInput{}, PulledImage{}, ImageRemoveInput{}, RemovedImage{}, ImagePruneInput{},
		PrunedImage{}, Pruned{}, NetworkCreateInput{}, VolumeCreateInput{}, Created{}, NetworkInput{}, VolumeInput{},
		ConnectInput{}, DisconnectInput{}, ComposeInput{}, ComposeResult{}, LogsParams{}, LogEvent{}, StatsParams{},
		StatsEvent{}, PullParams{}, PullEvent{},
	}
	if len(all) != 47 {
		t.Errorf("%d wire types listed, want 47", len(all))
	}
}

func TestDKWireJSON(t *testing.T) {
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	empty := []string{}
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"engine unavailable", Engine{Reason: "socket missing"}, `{"available":false,"reason":"socket missing","containers":0,"running":0,"images":0}`},
		{"container", Container{ID: "i", Name: "web", Image: "nginx", ImageID: "sha", State: "running", Status: "Up", Created: created,
			Ports: []Port{{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Proto: "tcp"}}, Project: "p", Service: "s", Owner: "npm",
			Usage: &Usage{CPUPercent: 1.5, MemUsage: 2, MemLimit: 3}},
			`{"id":"i","name":"web","image":"nginx","imageId":"sha","state":"running","status":"Up","created":"2026-10-01T00:00:00Z",` +
				`"ports":[{"ip":"0.0.0.0","privatePort":80,"publicPort":8080,"proto":"tcp"}],"project":"p","service":"s","owner":"npm",` +
				`"usage":{"cpuPercent":1.5,"memUsage":2,"memLimit":3}}`},
		{"project without a last run", Project{Name: "p", Directory: "/srv/p", Files: []string{"/srv/p/c.yaml"}, EnvFiles: []string{},
			Profiles: []string{}, Containers: []string{}, FirstSeen: created},
			`{"name":"p","directory":"/srv/p","files":["/srv/p/c.yaml"],"envFiles":[],"profiles":[],"configHash":"","hashChanged":false,` +
				`"containers":[],"running":0,"firstSeen":"2026-10-01T00:00:00Z"}`},
		{"compose input without profiles", ComposeInput{Project: "p"}, `{"project":"p"}`},
		{"compose input with an empty profile list", ComposeInput{Project: "p", Profiles: &empty}, `{"project":"p","profiles":[]}`},
		{"container remove input", ContainerRemoveInput{Container: "c", Force: true}, `{"container":"c","force":true}`},
		{"image prune input", ImagePruneInput{}, `{}`},
		{"logs input", LogsInput{Container: "c", Tail: 10, Timestamps: true}, `{"container":"c","tail":10,"timestamps":true}`},
		{"pull event", PullEvent{Status: "Downloading", ID: "l1", Current: 5, Total: 10}, `{"status":"Downloading","id":"l1","current":5,"total":10}`},
		{"stats event", StatsEvent{Read: "t", CPUPercent: 2, MemUsage: 3, MemLimit: 4, NetRx: 5, NetTx: 6, Pids: 7},
			`{"read":"t","cpuPercent":2,"memUsage":3,"memLimit":4,"netRx":5,"netTx":6,"pids":7}`},
		{"connect input", ConnectInput{Network: "n", Container: "c", Aliases: []string{"a"}, IPv4: "10.0.0.2"},
			`{"network":"n","container":"c","aliases":["a"],"ipv4":"10.0.0.2"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal(b, back.Interface()); err != nil {
				t.Fatal(err)
			}
			if again, _ := json.Marshal(back.Elem().Interface()); string(again) != tc.want {
				t.Errorf("round trip changed the document: %s", again)
			}
		})
	}
}

func TestDKWireSecretTags(t *testing.T) {
	f, _ := reflect.TypeOf(RevealedEnv{}).FieldByName("Env")
	if f.Tag.Get("secret") != "true" || f.Tag.Get("json") != "env" {
		t.Errorf("RevealedEnv.Env tag %q, want json:\"env\" secret:\"true\"", f.Tag)
	}
}
