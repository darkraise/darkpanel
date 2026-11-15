package errcode

import (
	"net/http"
	"testing"
)

func TestW12gForwardCodes(t *testing.T) {
	for code, title := range map[Code]string{
		NPMForwardUnanswered: "Forward host has no answer",
		NPMForwardUnresolved: "Forward host could not be resolved",
	} {
		if !Known(code) || code.Status() != http.StatusConflict || code.Title() != title {
			t.Errorf("%s: known %v status %d title %q, want known, 409 and %q", code, Known(code), code.Status(), code.Title(), title)
		}
	}
	if NPMForwardUnanswered != "npm.forward_unanswered" || NPMForwardUnresolved != "npm.forward_unresolved" {
		t.Error("a code's wire name changed")
	}
}
