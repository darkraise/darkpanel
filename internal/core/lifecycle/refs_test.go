package lifecycle

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestResolveInputRefs(t *testing.T) {
	tests := []struct {
		name, tmpl, input, want string
	}{
		{"a whole reference keeps its type",
			`{"port":"${input.port}","name":"${input.name}","tags":"${input.tags}","on":"${input.on}","none":"${input.none}"}`,
			`{"port":8080,"name":"web","tags":["a","b"],"on":true,"none":null}`,
			`{"name":"web","none":null,"on":true,"port":8080,"tags":["a","b"]}`},
		{"embedded references become text",
			`{"url":"http://${input.host}:${input.port}/x"}`, `{"host":"h","port":81}`, `{"url":"http://h:81/x"}`},
		{"a number keeps its text", `{"n":"v${input.n}"}`, `{"n":1.50}`, `{"n":"v1.50"}`},
		{"nested arrays and indexes",
			`[{"a":["${input.list.1}","x-${input.obj.k}"]}]`, `{"list":[1,{"z":2}],"obj":{"k":"v"}}`, `[{"a":[{"z":2},"x-v"]}]`},
		{"step references stay",
			`{"key":"${steps.auth.output.key}","url":"${input.u}/${steps.auth.output.id}"}`, `{"u":"h"}`,
			`{"key":"${steps.auth.output.key}","url":"h/${steps.auth.output.id}"}`},
		{"the escape stays escaped", `{"s":"cost $${input.x} $5"}`, `{}`, `{"s":"cost $${input.x} $5"}`},
		{"a value's ${ is escaped",
			`{"a":"${input.v}","b":"x${input.v}","c":"${input.o}"}`, `{"v":"${steps.s.output.k}","o":{"n":"${input.v}"}}`,
			`{"a":"$${steps.s.output.k}","b":"x$${steps.s.output.k}","c":{"n":"$${input.v}"}}`},
		{"keys are not templates", `{"${input.k}":"${input.k}"}`, `{"k":"v"}`, `{"${input.k}":"v"}`},
		{"no references", `[1,"a",{"b":null}]`, `{}`, `[1,"a",{"b":null}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveInputRefs(json.RawMessage(tt.tmpl), json.RawMessage(tt.input))
			if err != nil || string(got) != tt.want {
				t.Errorf("resolveInputRefs = %s, %v; want %s", got, err, tt.want)
			}
		})
	}
}

func TestResolveStepRefs(t *testing.T) {
	outputs := map[string]json.RawMessage{
		"auth": json.RawMessage(`{"key":{"id":"k1","secret":"s"},"n":3}`),
		"dns":  json.RawMessage(`{"name":"a.example","port":443}`),
		"s":    json.RawMessage(`{"k":"SECRET"}`),
	}
	tests := []struct {
		name, tmpl, want string
	}{
		{"a whole reference keeps its type",
			`{"key":"${steps.auth.output.key}","n":"${steps.auth.output.n}"}`, `{"key":{"id":"k1","secret":"s"},"n":3}`},
		{"embedded references become text",
			`{"url":"https://${steps.dns.output.name}:${steps.dns.output.port}"}`, `{"url":"https://a.example:443"}`},
		{"the escape is decoded", `{"s":"cost $${input.x}"}`, `{"s":"cost ${input.x}"}`},
		{"no references", `[1,"a",{"b":null}]`, `[1,"a",{"b":null}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveStepRefs(json.RawMessage(tt.tmpl), outputs)
			if err != nil || string(got) != tt.want {
				t.Errorf("resolveStepRefs = %s, %v; want %s", got, err, tt.want)
			}
		})
	}
	// Through both passes, an input value never becomes a reference.
	for _, tt := range []struct {
		name, tmpl, input, want string
	}{
		{"a value holding a reference",
			`{"a":"${input.v}","b":"pre-${input.v}"}`, `{"v":"${steps.s.output.k}"}`,
			`{"a":"${steps.s.output.k}","b":"pre-${steps.s.output.k}"}`},
		{"a value ending in $ before a literal {",
			`{"a":"${input.d}{steps.s.output.k}"}`, `{"d":"$"}`, `{"a":"${steps.s.output.k}"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mid, err := resolveInputRefs(json.RawMessage(tt.tmpl), json.RawMessage(tt.input))
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolveStepRefs(mid, outputs)
			if err != nil || string(got) != tt.want {
				t.Errorf("both passes = %s, %v; want %s", got, err, tt.want)
			}
		})
	}
	if got, err := resolveStepRefs(json.RawMessage(`{"a":"x"}`), nil); err != nil || string(got) != `{"a":"x"}` {
		t.Errorf("resolveStepRefs with no outputs = %s, %v", got, err)
	}
}

func TestRefsErrors(t *testing.T) {
	outputs := map[string]json.RawMessage{"s": json.RawMessage(`{"j":1,"o":[1]}`)}
	tests := []struct {
		name string
		run  func() error
		want errcode.Code
	}{
		{"missing input path", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${input.nope}"}`), json.RawMessage(`{}`))
			return err
		}, errcode.InvalidInput},
		{"input index out of range", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${input.l.2}"}`), json.RawMessage(`{"l":[1]}`))
			return err
		}, errcode.InvalidInput},
		{"input index with a leading zero", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${input.l.01}"}`), json.RawMessage(`{"l":[1,2]}`))
			return err
		}, errcode.InvalidInput},
		{"an object inside a longer string", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"x${input.o}"}`), json.RawMessage(`{"o":{}}`))
			return err
		}, errcode.Internal},
		{"an unterminated reference", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${input.x"}`), json.RawMessage(`{"x":1}`))
			return err
		}, errcode.Internal},
		{"an unknown reference", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${env.HOME}"}`), json.RawMessage(`{}`))
			return err
		}, errcode.Internal},
		{"an empty path segment", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${input..x}"}`), json.RawMessage(`{}`))
			return err
		}, errcode.Internal},
		{"a step reference without output", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${steps.s.result.x}"}`), json.RawMessage(`{}`))
			return err
		}, errcode.Internal},
		{"a $ before a step reference", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{"a":"${input.p}${steps.s.output.j}"}`), json.RawMessage(`{"p":"x$"}`))
			return err
		}, errcode.Internal},
		{"a template that is not JSON", func() error {
			_, err := resolveInputRefs(json.RawMessage(`{`), json.RawMessage(`{}`))
			return err
		}, errcode.Internal},
		{"a step without output", func() error {
			_, err := resolveStepRefs(json.RawMessage(`{"a":"${steps.t.output.k}"}`), outputs)
			return err
		}, errcode.ChildFailed},
		{"a missing step path", func() error {
			_, err := resolveStepRefs(json.RawMessage(`{"a":"${steps.s.output.k}"}`), outputs)
			return err
		}, errcode.ChildFailed},
		{"a list inside a longer string", func() error {
			_, err := resolveStepRefs(json.RawMessage(`{"a":"x${steps.s.output.o}"}`), outputs)
			return err
		}, errcode.ChildFailed},
		{"an input reference left for the step pass", func() error {
			_, err := resolveStepRefs(json.RawMessage(`{"a":"${input.x}"}`), outputs)
			return err
		}, errcode.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || errcode.From(err).Code != tt.want {
				t.Errorf("error %v, want code %s", err, tt.want)
			}
		})
	}
}

func TestStepRefs(t *testing.T) {
	tmpl := `{"b":"${steps.db.output.host}","a":["${steps.auth.output.key}","x${steps.db.output.port}"],` +
		`"c":"$${steps.no.output.x}","d":"${input.y}"}`
	ids, err := stepRefs(json.RawMessage(tmpl))
	if err != nil || !slices.Equal(ids, []string{"auth", "db"}) {
		t.Errorf("stepRefs = %q, %v; want auth and db, in key order", ids, err)
	}
	if ids, err := stepRefs(json.RawMessage(`{"a":"${input.x}","b":1}`)); err != nil || len(ids) != 0 {
		t.Errorf("stepRefs without step references = %q, %v", ids, err)
	}
	if _, err := stepRefs(json.RawMessage(`{"a":"${steps.db"}`)); err == nil {
		t.Error("stepRefs accepted an unterminated reference")
	}
}
