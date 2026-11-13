package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const a7Marker = "qa-interpolated-7f3c91"

type a7Kind struct {
	name, key, method string
	config            string
	skip              int // calls of method that must pass before the one that fails
}

func a7Kinds() []a7Kind {
	return []a7Kind{
		{name: "image", key: "web", method: "InspectImage",
			config: `{"name":"shop","services":{"web":{"build":{"context":"/srv/shop"},"image":"reg.example/` + a7Marker + `/web"}}}`},
		{name: "container", key: "web", method: "ListContainers", skip: 1,
			config: `{"name":"shop","services":{"web":{"image":"nginx:1.27","container_name":"cn-` + a7Marker + `"}}}`},
		{name: "volume", key: "data", method: "InspectVolume",
			config: `{"name":"shop","volumes":{"data":{"name":"vol-` + a7Marker + `"}},"services":{"web":{"image":"nginx:1.27","volumes":[{"type":"volume","source":"data","target":"/d"}]}}}`},
		{name: "network", key: "edge", method: "ListNetworks",
			config: `{"name":"shop","networks":{"edge":{"name":"net-` + a7Marker + `","external":true}},"services":{"web":{"image":"nginx:1.27","networks":{"edge":null}}}}`},
	}
}

type a7Failure struct {
	name   string
	err    func(path string) error
	code   errcode.Code
	detail func(kind, key string) string
}

func a7Failures() []a7Failure {
	var decode error
	var v struct{}
	decode = json.Unmarshal([]byte("{"), &v)
	reachable := func(string) string { return "the Docker Engine is not reachable" }
	return []a7Failure{
		{"an unreachable engine", func(p string) error { return fmt.Errorf("dockerapi: GET %s: %w", p, dockerapi.ErrUnavailable) },
			errcode.DockerEngineUnavailable, func(_, _ string) string { return reachable("") }},
		{"a transport error", func(p string) error {
			return fmt.Errorf("dockerapi: GET %s: %w", p, &url.Error{Op: "Get", URL: "http://docker" + p, Err: io.ErrUnexpectedEOF})
		}, errcode.DockerEngineUnavailable, func(_, _ string) string { return reachable("") }},
		{"an HTTP error answer", func(p string) error { return &dockerapi.APIError{StatusCode: 503, Message: "failed on " + p} },
			errcode.Unavailable, func(kind, key string) string {
				return "the Docker Engine answered HTTP 503 while checking the " + kind + " of " + key
			}},
		{"an answer that does not decode", func(p string) error { return fmt.Errorf("dockerapi: decode GET %s: %w", p, decode) },
			errcode.Unavailable, func(kind, key string) string {
				return "the Docker Engine gave an answer that did not decode while checking the " + kind + " of " + key
			}},
	}
}

// a7Run runs a Compose verb of project shop on a host whose Engine fails the
// check of one object kind, and returns the error and the host's log.
func a7Run(t *testing.T, k a7Kind, f a7Failure, apply bool) (string, error) {
	t.Helper()
	h := newDKHost(t)
	p := ccProject(t, h, "shop")
	ccConfig(h, p, nil, k.config)
	for range k.skip {
		h.docker.fail(k.method, nil)
	}
	h.docker.fail(k.method, f.err("/"+k.name+"s/"+a7Marker+"/json"))
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	var err error
	if apply {
		env := h.env("chg1")
		env.Log = log
		_, err = h.dk().composeUp().Handler.Apply(context.Background(), env, json.RawMessage(`{"project":"shop"}`))
	} else {
		env := h.env("")
		env.Log = log
		_, err = h.dk().composeUp().Handler.Plan(context.Background(), env, json.RawMessage(`{"project":"shop"}`))
	}
	if err != nil {
		// The agent logs the whole chain of an error that answers 5xx.
		log.Error("api: request failed", "code", errcode.From(err).Code, "err", err)
	}
	return logs.String(), err
}

func TestA7_AFailedObjectCheckWritesNoConfigurationValueAnywhere(t *testing.T) {
	for _, k := range a7Kinds() {
		for _, f := range a7Failures() {
			for _, apply := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s %s apply=%v", k.name, f.name, apply), func(t *testing.T) {
					logs, err := a7Run(t, k, f, apply)
					if err == nil {
						t.Fatal("the verb succeeded, want a refusal")
					}
					for chain := err; chain != nil; chain = errors.Unwrap(chain) {
						if strings.Contains(chain.Error(), a7Marker) {
							t.Errorf("the error chain carries the configuration value: %q", chain.Error())
						}
					}
					if strings.Contains(errcode.From(err).Detail, a7Marker) {
						t.Errorf("the change record would carry the configuration value: %q", errcode.From(err).Detail)
					}
					if strings.Contains(logs, a7Marker) {
						t.Errorf("a log line carries the configuration value: %s", logs)
					}
				})
			}
		}
	}
}

func TestA8_AFailedObjectCheckReportsTheKindTheKeyAndTheClass(t *testing.T) {
	for _, k := range a7Kinds() {
		for _, f := range a7Failures() {
			for _, apply := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s %s apply=%v", k.name, f.name, apply), func(t *testing.T) {
					logs, err := a7Run(t, k, f, apply)
					if err == nil {
						t.Fatal("the verb succeeded, want a refusal")
					}
					coded := errcode.From(err)
					want := f.detail(k.name, k.key)
					if coded.Code != f.code || coded.Detail != want {
						t.Errorf("error %s %q, want %s %q", coded.Code, coded.Detail, f.code, want)
					}
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the error value %q does not carry %q", err.Error(), want)
					}
					if !strings.Contains(logs, want) {
						t.Errorf("the log %q does not carry %q", logs, want)
					}
				})
			}
		}
	}
}
