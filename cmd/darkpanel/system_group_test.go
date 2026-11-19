package main

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/registry"
)

// TestSystemGroupAdoptsSystemModule holds the hand-written system group and
// the system module's generated commands together under darkpanel system.
func TestSystemGroupAdoptsSystemModule(t *testing.T) {
	root, err := newRoot(cli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cmd, _, err := root.Find([]string{"system", "install"}); err != nil || cmd.Annotations[cli.AnnotationAction] != "" ||
		cmd.CommandPath() != "darkpanel system install" {
		t.Fatalf("system install: %v, %v; want the hand-written install command", cmd, err)
	}
	reg, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range reg.Actions() {
		if !a.Spec.Internal && strings.HasPrefix(a.Spec.Name, "system.") {
			names = append(names, a.Spec.Name)
		}
	}
	for _, r := range reg.Resources() {
		if strings.HasPrefix(r.Name, "system.") {
			names = append(names, r.Name)
		}
	}
	for _, name := range names {
		cmd, ok := cli.FindCommand(root, name)
		if !ok || !strings.HasPrefix(cmd.CommandPath(), "darkpanel system ") {
			t.Errorf("FindCommand(%q) = %v, %v; want a command under darkpanel system", name, cmd, ok)
		}
	}
}
