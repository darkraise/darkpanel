package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type strmParams struct {
	Container string `json:"container"`
	Tail      int    `json:"tail,omitempty"`
}

type strmEvent struct {
	Line string `json:"line"`
}

// strmLogs emits p.Tail lines naming the host and the container.
func strmLogs(_ context.Context, env Env, p strmParams, emit func(strmEvent) error) error {
	for i := range p.Tail {
		if err := emit(strmEvent{Line: fmt.Sprintf("%s %s %d", env.HostID, p.Container, i)}); err != nil {
			return err
		}
	}
	return nil
}

func TestDefineStream(t *testing.T) {
	def := DefineStream("logs", strmLogs)
	if def.Name != "logs" || def.ParamsType != reflect.TypeFor[strmParams]() || def.EventType != reflect.TypeFor[strmEvent]() {
		t.Fatalf("def = %s, %v, %v; want logs, strmParams, strmEvent", def.Name, def.ParamsType, def.EventType)
	}
	ctx := context.Background()
	var got []any
	err := def.Run(ctx, Env{HostID: "h1"}, json.RawMessage(`{"container":"web","tail":2}`), func(e any) error {
		got = append(got, e)
		return nil
	})
	if want := []any{strmEvent{"h1 web 0"}, strmEvent{"h1 web 1"}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Run emitted %v, %v; want %v", got, err, want)
	}
	gone := errors.New("reader gone")
	calls := 0
	err = def.Run(ctx, Env{}, json.RawMessage(`{"container":"web","tail":5}`), func(any) error {
		calls++
		return gone
	})
	if !errors.Is(err, gone) || calls != 1 {
		t.Errorf("Run after a failed emit = %v after %d events, want %v after 1", err, calls, gone)
	}
	var coded *errcode.Error
	err = def.Run(ctx, Env{}, json.RawMessage(`{"container":"web","follow":true}`), func(any) error { return nil })
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Errorf("unknown parameter: err = %v, want %s", err, errcode.InvalidInput)
	}
}
