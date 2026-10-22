package runtime

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

type smParams struct {
	Count int    `json:"count,omitempty" jsonschema:"minimum=0"`
	Fail  string `json:"fail,omitempty"`
	Gone  bool   `json:"gone,omitempty"`
	Big   bool   `json:"big,omitempty"`
	Block bool   `json:"block,omitempty"`
	// Forever emits until emit fails.
	Forever bool `json:"forever,omitempty"`
}

type smEvent struct {
	N    int    `json:"n"`
	Text string `json:"text,omitempty"`
}

// smModule streams count events, then fails as its parameters say, or
// blocks until its context ends and reports that on stopped.
type smModule struct{ stopped chan error }

func (smModule) Manifest() core.Manifest {
	return core.Manifest{ID: "sm", Version: 1, DefaultEnabled: true}
}

func (m smModule) Streams() []core.StreamDef {
	return []core.StreamDef{core.DefineStream("logs", m.logs)}
}

func (m smModule) logs(ctx context.Context, _ core.Env, p smParams, emit func(smEvent) error) error {
	for i := 1; i <= p.Count; i++ {
		if err := emit(smEvent{N: i}); err != nil {
			return err
		}
	}
	switch {
	case p.Big:
		if err := emit(smEvent{Text: strings.Repeat("x", 70<<10)}); err != nil {
			return err
		}
		return emit(smEvent{N: 99})
	case p.Gone:
		return errcode.New(errcode.NotFound, "no container c1")
	case p.Fail != "":
		return errors.New(p.Fail)
	case p.Block:
		<-ctx.Done()
		m.stopped <- ctx.Err()
		return ctx.Err()
	case p.Forever:
		for {
			if err := emit(smEvent{N: 0}); err != nil {
				return err
			}
		}
	}
	return nil
}

// smBadModule has a stream and a migration that fails.
type smBadModule struct{}

func (smBadModule) Manifest() core.Manifest {
	return core.Manifest{ID: "smbad", Version: 1, DefaultEnabled: true,
		Migrations: map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{"0001_smbad.sql": {Data: []byte("CREATE TABLE smbad (")}}}}
}

func (smBadModule) Streams() []core.StreamDef {
	return []core.StreamDef{core.DefineStream("tail", func(context.Context, core.Env, smParams, func(smEvent) error) error {
		return nil
	})}
}

func smFixture(t *testing.T) (*fixture, chan error) {
	t.Helper()
	stopped := make(chan error, 1)
	return newFixtureWith(t, smModule{stopped: stopped}, smBadModule{}), stopped
}

func TestRuntimeStreams(t *testing.T) {
	f, _ := smFixture(t)
	got, err := f.rt.Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var want []core.StreamInfo
	for _, s := range f.reg.Streams() {
		hash, err := schemas.StreamHash(s.Def)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, core.StreamInfo{Module: s.Module, Name: s.Def.Name, SchemaHash: hash})
	}
	if len(want) != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("Streams = %+v, want %+v", got, want)
	}
}

// smOpen opens sm.logs with query as alice.
func smOpen(t *testing.T, f *fixture, query string) io.ReadCloser {
	t.Helper()
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := f.rt.OpenStream(context.Background(), alice, "sm", "logs", q)
	if err != nil {
		t.Fatalf("OpenStream(%s): %v", query, err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return rc
}

func TestOpenStreamLines(t *testing.T) {
	tests := []struct {
		name, query string
		want        []string
	}{
		{"done", "count=2", []string{`{"data":{"n":1}}`, `{"data":{"n":2}}`, `{"end":{"reason":"done"}}`}},
		{"run error", "count=1&fail=disk%20on%20fire", []string{`{"data":{"n":1}}`,
			`{"end":{"reason":"error","error":{"code":"internal","detail":"Internal error"}}}`}},
		{"coded run error", "gone=true", []string{
			`{"end":{"reason":"error","error":{"code":"not_found","detail":"no container c1"}}}`}},
		{"event too large", "count=1&big=true", []string{`{"data":{"n":1}}`,
			`{"end":{"reason":"error","error":{"code":"request.too_large","detail":"a stream event of 71697 bytes exceeds 65536 bytes"}}}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := smFixture(t)
			body, err := io.ReadAll(smOpen(t, f, tt.query))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("lines\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestOpenStreamClose(t *testing.T) {
	f, stopped := smFixture(t)
	rc := smOpen(t, f, "count=1&block=true")
	line := make([]byte, len(`{"data":{"n":1}}`+"\n"))
	if _, err := io.ReadFull(rc, line); err != nil || string(line) != `{"data":{"n":1}}`+"\n" {
		t.Fatalf("first line %q, %v", line, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run's context ended with %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the reader did not end Run's context")
	}
}

// TestOpenStreamAcrossClose closes the runtime under open streams: Close
// returns once every Run returned, also when nobody reads the stream, and a
// stream that is read ends with unavailable.
func TestOpenStreamAcrossClose(t *testing.T) {
	f, stopped := smFixture(t)
	read := smOpen(t, f, "count=1&block=true")
	line := make([]byte, len(`{"data":{"n":1}}`+"\n"))
	if _, err := io.ReadFull(read, line); err != nil {
		t.Fatal(err)
	}
	_ = smOpen(t, f, "forever=true") // never read
	closed := make(chan struct{})
	go func() {
		f.rt.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while streams were open")
	}
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Errorf("Run's context ended with %v, want context.Canceled", err)
	}
	rest, err := io.ReadAll(read)
	if want := `{"end":{"reason":"error","error":{"code":"unavailable","detail":"Unavailable"}}}` + "\n"; err != nil || string(rest) != want {
		t.Errorf("after Close: %q, %v; want %q", rest, err, want)
	}
	_, err = f.rt.OpenStream(context.Background(), alice, "sm", "logs", nil)
	_ = wantCode(t, err, errcode.Unavailable)
}

func TestOpenStreamRefusals(t *testing.T) {
	f, _ := smFixture(t)
	tests := []struct {
		name, module, stream, query string
		code                        errcode.Code
	}{
		{"unknown stream", "sm", "nope", "", errcode.NotFound},
		{"unknown module", "zz", "logs", "", errcode.NotFound},
		{"parameter of the wrong type", "sm", "logs", "count=x", errcode.InvalidInput},
		{"unknown parameter", "sm", "logs", "tail=1", errcode.InvalidInput},
		{"parameter the schema refuses", "sm", "logs", "count=-1", errcode.InvalidInput},
		{"broken module", "smbad", "tail", "", errcode.ModuleUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			rc, err := f.rt.OpenStream(context.Background(), alice, tt.module, tt.stream, q)
			if rc != nil {
				_ = rc.Close()
			}
			_ = wantCode(t, err, tt.code)
		})
	}
}
