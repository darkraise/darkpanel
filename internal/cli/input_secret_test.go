package cli

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type v1r14LabelsInput struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty" secret:"true"`
}

type v1r14RequiredLabelsInput struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels" secret:"true"`
}

// v1r14Reader counts the reads of standard input.
type v1r14Reader struct {
	r     *strings.Reader
	reads int
}

func (r *v1r14Reader) Read(p []byte) (int, error) {
	r.reads++
	return r.r.Read(p)
}

// v1r14Collect parses args into a command with typ's flags and collects the
// input from stdin. It fails the test when a secret is read without echo.
func v1r14Collect(t *testing.T, typ reflect.Type, args []string, stdin string, onTerminal bool) (string, string, int, error) {
	t.Helper()
	fields, err := inputFields(typ)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{Use: "x"}
	addInputFlags(cmd, fields)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	in := &v1r14Reader{r: strings.NewReader(stdin)}
	var prompts bytes.Buffer
	term := &terminal{in: bufio.NewReader(in), out: &prompts, isTerm: onTerminal,
		readSecret: func() (string, error) {
			t.Error("a secret was read without echo")
			return "", errors.New("unexpected secret read")
		}}
	raw, err := collectInput(cmd, fields, term)
	return string(raw), prompts.String(), in.reads, err
}

func TestV1r14NestedSecretField(t *testing.T) {
	fields, err := inputFields(reflect.TypeFor[v1r14LabelsInput]())
	if err != nil {
		t.Fatal(err)
	}
	var labels *inputField
	for i := range fields {
		if fields[i].Name == "labels" {
			labels = &fields[i]
		}
	}
	if labels == nil || !labels.Secret || labels.Type != "" {
		t.Fatalf("labels field %+v, want a secret field with an empty type", labels)
	}
	cmd := &cobra.Command{Use: "x"}
	addInputFlags(cmd, fields)
	if cmd.Flags().Lookup("labels") != nil {
		t.Error("unexpected flag --labels")
	}
	if cmd.Flags().Lookup("input") == nil {
		t.Error("no flag --input")
	}
}

func TestV1r14NestedSecretFromInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(file, []byte(`{"labels":{"team":"web"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, prompts, reads, err := v1r14Collect(t, reflect.TypeFor[v1r14LabelsInput](),
		[]string{"--name", "n1", "--input", file}, "", true)
	want := `{"labels":{"team":"web"},"name":"n1"}`
	if err != nil || got != want || prompts != "" || reads != 0 {
		t.Errorf("got %s, %v, prompts %q, %d reads of standard input\nwant %s", got, err, prompts, reads, want)
	}
}

func TestV1r14NestedSecretAbsent(t *testing.T) {
	got, prompts, reads, err := v1r14Collect(t, reflect.TypeFor[v1r14LabelsInput](), []string{"--name", "n1"}, "", false)
	want := `{"name":"n1"}`
	if err != nil || got != want || prompts != "" || reads != 0 {
		t.Errorf("got %s, %v, prompts %q, %d reads of standard input\nwant %s", got, err, prompts, reads, want)
	}
}

func TestV1r14RequiredNestedSecretMissing(t *testing.T) {
	_, prompts, reads, err := v1r14Collect(t, reflect.TypeFor[v1r14RequiredLabelsInput](), []string{"--name", "n1"}, "", false)
	if ExitCode(ranError(err)) != ExitUsage || !strings.Contains(err.Error(), "--input") || !strings.Contains(err.Error(), `"labels"`) {
		t.Errorf("error %v, want a usage error naming --input and labels", err)
	}
	if prompts != "" || reads != 0 {
		t.Errorf("prompts %q, %d reads of standard input", prompts, reads)
	}
}
