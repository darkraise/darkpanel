package cli

import (
	"bytes"
	"reflect"
	"slices"
	"testing"
)

func TestRender(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"a single list is a table",
			`{"hosts":[{"id":"h1","name":"web","isLocal":true,"modules":[{"id":"hostinfo"}]},` +
				`{"id":"h2","name":"db-primary","isLocal":false,"modules":[]}]}`,
			"id  name        isLocal  modules\n" +
				"h1  web         true     [{\"id\":\"hostinfo\"}]\n" +
				"h2  db-primary  false    []\n"},
		{"an empty list", `{"tokens":[]}`, "(none)\n"},
		{"rows with different keys", `[{"a":1},{"b":"x\ty","a":null}]`, "a  b\n1  \n-  x\\x09y\n"},
		{"nested objects flatten",
			`{"user":{"id":"u1","username":"ann","createdAt":"2026-10-18T00:00:00Z"},"totpSecret":"ABC","n":null}`,
			"user.id: u1\nuser.username: ann\nuser.createdAt: 2026-10-18T00:00:00Z\ntotpSecret: ABC\nn: -\n"},
		{"a list among other fields",
			`{"entries":[{"id":"a1","actor":"user:u1"}],"nextBefore":"a1","tags":["x","y"],"empty":{}}`,
			"entries:\nid  actor\na1  user:u1\nnextBefore: a1\ntags: x, y\nempty: {}\n"},
		{"scalars in a list", `["a",2,true]`, "a\n2\ntrue\n"},
		{"a scalar", `"done"`, "done\n"},
		{"null", `null`, ""},
		{"an empty object", `{}`, ""},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := render(&b, []byte(c.raw)); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if b.String() != c.want {
			t.Errorf("%s: rendered\n%s\nwant\n%s", c.name, b.String(), c.want)
		}
	}
	if err := render(new(bytes.Buffer), []byte(`{"a":`)); err == nil {
		t.Error("malformed JSON rendered without an error")
	}
}

func TestRenderSanitizes(t *testing.T) {
	// esc is an ESC sequence and a bidi override in JSON, as a hostile
	// server would send them; shown is how they must reach the terminal.
	const esc, shown = `\u001b[2J\u202e`, `\x1b[2J\u202e`
	cases := []struct {
		name, raw, want string
	}{
		{"table cell", `[{"name":"a` + esc + `"}]`, "name\na" + shown + "\n"},
		{"table header", `[{"n` + esc + `":1}]`, "n" + shown + "\n1\n"},
		{"single-list table", `{"hosts":[{"id":"h` + esc + `"}]}`, "id\nh" + shown + "\n"},
		{"key: value value", `{"prettyName":"Ubuntu` + esc + `"}`, "prettyName: Ubuntu" + shown + "\n"},
		{"key: value key", `{"k` + esc + `":1}`, "k" + shown + ": 1\n"},
		{"nested value", `{"os":{"name":"x` + esc + `"}}`, "os.name: x" + shown + "\n"},
		{"list of scalars in a value", `{"tags":["t` + esc + `"],"n":1}`, "tags: t" + shown + "\nn: 1\n"},
		{"section key", `{"e` + esc + `":[{"a":1}],"b":2}`, "e" + shown + ":\na\n1\nb: 2\n"},
		{"list scalar", `["s` + esc + `"]`, "s" + shown + "\n"},
		{"top-level scalar", `"v` + esc + `"`, "v" + shown + "\n"},
		{"newline in a value", `{"m":"one\ntwo"}`, `m: one\x0atwo` + "\n"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := render(&b, []byte(c.raw)); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if b.String() != c.want {
			t.Errorf("%s: rendered %q, want %q", c.name, b.String(), c.want)
		}
	}
}

type tokenInfoOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type newTokenOut struct {
	tokenInfoOut
	Token string `json:"token" secret:"true"`
}

type enrollmentOut struct {
	User       tokenInfoOut `json:"user"`
	TOTPSecret string       `json:"totpSecret" secret:"true"`
	Recovery   string       `json:"recovery" secret:"true"`
}

func TestSecretOutputs(t *testing.T) {
	cases := map[reflect.Type][]string{
		reflect.TypeFor[newTokenOut]():   {"token"},
		reflect.TypeFor[enrollmentOut](): {"totpSecret", "recovery"},
		reflect.TypeFor[tokenInfoOut]():  nil,
	}
	for typ, want := range cases {
		got, err := secretOutputs(typ)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %q, %v; want %q", typ, got, err, want)
		}
	}
}

func TestNoteSecrets(t *testing.T) {
	cases := []struct {
		names     []string
		raw, want string
	}{
		{[]string{"token"}, `{"id":"t1","token":"dpt_x"}`, "\nThe token above will not be shown again; store it now.\n"},
		{[]string{"totpSecret", "recovery"}, `{"totpSecret":"A","recovery":"B"}`,
			"\nThe totpSecret and recovery above will not be shown again; store them now.\n"},
		{[]string{"token"}, `{"id":"t1","token":null}`, ""},
		{nil, `{"id":"t1"}`, ""},
	}
	for _, c := range cases {
		var b bytes.Buffer
		noteSecrets(&b, c.names, []byte(c.raw))
		if b.String() != c.want {
			t.Errorf("%q in %s: %q, want %q", c.names, c.raw, b.String(), c.want)
		}
	}
}
