package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type sampleLimits struct {
	Max int `json:"max"`
}

type sampleInput struct {
	Name       string       `json:"name" jsonschema_description:"Rule name."`
	Port       int          `json:"port" jsonschema_description:"Port number."`
	Weight     float64      `json:"weight,omitempty"`
	Force      bool         `json:"force"`
	Mode       string       `json:"mode,omitempty" jsonschema:"enum=allow,enum=deny"`
	Tags       []string     `json:"tags,omitempty"`
	Ports      []int        `json:"ports,omitempty"`
	HostID     string       `json:"hostId,omitempty"`
	Limits     sampleLimits `json:"limits"`
	APIKey     string       `json:"apiKey" secret:"true"`
	Passphrase string       `json:"passphrase" secret:"true"`
}

func TestInputFields(t *testing.T) {
	fields, err := inputFields(reflect.TypeFor[sampleInput]())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fields {
		s := f.Name + ":" + f.Flag + ":" + f.Type + ":" + f.Items
		if f.Required {
			s += ":required"
		}
		if f.Secret {
			s += ":secret"
		}
		if len(f.Enum) > 0 {
			s += ":" + strings.Join(f.Enum, "|")
		}
		got = append(got, s)
	}
	want := []string{
		"name:name:string::required", "port:port:integer::required", "weight:weight:number:", "force:force:boolean::required",
		"mode:mode:string::allow|deny", "tags:tags:array:string", "ports:ports:array:integer", "hostId:host-id:string:",
		"limits:limits:::required", "apiKey:api-key:string::required:secret", "passphrase:passphrase:string::required:secret",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("fields\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	cmd := &cobra.Command{Use: "x"}
	addInputFlags(cmd, fields)
	for _, name := range []string{"name", "port", "weight", "force", "mode", "tags", "ports", "host-id", "input"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("no flag --%s", name)
		}
	}
	for _, name := range []string{"limits", "api-key", "apiKey", "passphrase"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("unexpected flag --%s", name)
		}
	}
	if u := cmd.Flags().Lookup("mode").Usage; !strings.Contains(u, "allow, deny") {
		t.Errorf("--mode usage %q does not list the values", u)
	}
	if u := cmd.Flags().Lookup("name").Usage; !strings.Contains(u, "Rule name.") || !strings.Contains(u, "required") {
		t.Errorf("--name usage %q", u)
	}
}

func TestInputFieldsRefuseReservedFlags(t *testing.T) {
	type hostInput struct {
		Host string `json:"host"`
	}
	type yesInput struct {
		Yes bool `json:"yes,omitempty"`
	}
	type twiceInput struct {
		HostID  string `json:"hostId"`
		HostID2 string `json:"host-id"`
	}
	type overrideInput struct {
		Override []string `json:"override,omitempty"`
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[hostInput](), reflect.TypeFor[yesInput](), reflect.TypeFor[twiceInput](),
		reflect.TypeFor[overrideInput]()} {
		if _, err := inputFields(typ); err == nil || !strings.Contains(err.Error(), "flag --") {
			t.Errorf("%s: error %v", typ, err)
		}
	}
}

// collect parses args into a command with sampleInput's flags and collects
// the input. secrets answers the no-echo prompts on a terminal.
func collect(t *testing.T, args []string, stdin string, onTerminal bool, secrets ...string) (string, string, error) {
	t.Helper()
	fields, err := inputFields(reflect.TypeFor[sampleInput]())
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{Use: "x"}
	addInputFlags(cmd, fields)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	var prompts bytes.Buffer
	term := &terminal{in: bufio.NewReader(strings.NewReader(stdin)), out: &prompts, isTerm: onTerminal,
		readSecret: func() (string, error) {
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		}}
	raw, err := collectInput(cmd, fields, term)
	return string(raw), prompts.String(), err
}

func TestCollectInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(file, []byte(`{"limits":{"max":5},"port":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, prompts, err := collect(t, []string{"--name", "web", "--port", "443", "--tags", "a", "--tags", "b",
		"--ports", "80,8080", "--mode", "allow", "--weight", "0.5", "--input", file}, "k-123\npass phrase\n", false)
	want := `{"apiKey":"k-123","force":false,"limits":{"max":5},"mode":"allow","name":"web","passphrase":"pass phrase",` +
		`"port":443,"ports":[80,8080],"tags":["a","b"],"weight":0.5}`
	if err != nil || got != want || prompts != "" {
		t.Errorf("flags and file: %s, %v, prompts %q\nwant %s", got, err, prompts, want)
	}

	got, prompts, err = collect(t, []string{"--name", "web", "--port", "1", "--force", "--input", "-"},
		`{"limits":{"max":1},"apiKey":"from-file","passphrase":"pp"}`, false)
	want = `{"apiKey":"from-file","force":true,"limits":{"max":1},"name":"web","passphrase":"pp","port":1}`
	if err != nil || got != want || prompts != "" {
		t.Errorf("input from stdin: %s, %v, prompts %q\nwant %s", got, err, prompts, want)
	}

	got, prompts, err = collect(t, []string{"--name", "web", "--port", "1", "--input", file}, "", true, "tty-key", "tty-pass")
	want = `{"apiKey":"tty-key","force":false,"limits":{"max":5},"name":"web","passphrase":"tty-pass","port":1}`
	if err != nil || got != want || prompts != "apiKey: \npassphrase: \n" {
		t.Errorf("terminal: %s, %v, prompts %q\nwant %s", got, err, prompts, want)
	}
}

func TestCollectInputUsageErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(file, []byte(`{"limits":{"max":5}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"missing flag", []string{"--port", "1", "--input", file}, "k\np\n", "--name"},
		{"missing nested field", []string{"--name", "a", "--port", "1"}, "k\np\n", `"limits"`},
		{"missing secret", []string{"--name", "a", "--port", "1", "--input", file}, "k\n", "passphrase"},
		{"bad JSON", []string{"--name", "a", "--port", "1", "--input", "-"}, `{"limits":`, "--input"},
		{"not an object", []string{"--name", "a", "--port", "1", "--input", "-"}, `[1]`, "--input"},
		{"no file", []string{"--name", "a", "--port", "1", "--input", filepath.Join(t.TempDir(), "none")}, "", "--input"},
	}
	for _, c := range cases {
		_, _, err := collect(t, c.args, c.stdin, false)
		if ExitCode(ranError(err)) != ExitUsage || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want a usage error naming %s", c.name, err, c.want)
		}
	}
}

func TestKebab(t *testing.T) {
	for in, want := range map[string]string{"name": "name", "hostId": "host-id", "expiresInDays": "expires-in-days",
		"apiKey": "api-key", "ipv6Addr": "ipv6-addr", "snake_case": "snake-case", "URL": "url"} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want)
		}
	}
}
