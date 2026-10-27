package fwstate

import (
	"slices"
	"testing"
)

func TestStateFiles(t *testing.T) {
	want := []string{
		"/etc/ufw/user.rules", "/etc/ufw/user6.rules",
		"/etc/ufw/before.rules", "/etc/ufw/before6.rules",
		"/etc/ufw/after.rules", "/etc/ufw/after6.rules",
		"/etc/ufw/ufw.conf", "/etc/default/ufw",
	}
	if !slices.Equal(StateFiles, want) {
		t.Fatalf("StateFiles = %q, want %q", StateFiles, want)
	}
	if MaxFileSize != 4*1024*1024 {
		t.Fatalf("MaxFileSize = %d, want 4 MiB", MaxFileSize)
	}
}

func TestValues(t *testing.T) {
	got := []string{string(V4), string(V6), string(TransNone), string(TransReload), string(TransEnable),
		string(TransDisable), string(Accept), string(Drop), string(Reject), string(Unknown)}
	want := []string{"v4", "v6", "none", "reload", "enable", "disable", "accept", "drop", "reject", "unknown"}
	if !slices.Equal(got, want) {
		t.Fatalf("values = %q, want %q", got, want)
	}
}
