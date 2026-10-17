package web

import (
	"io/fs"
	"os"
	"testing"
)

func TestDistContainsIndex(t *testing.T) {
	if os.Getenv("DARKPANEL_REQUIRE_UI") != "1" {
		t.Skip("set DARKPANEL_REQUIRE_UI=1 to require a built web UI")
	}
	if _, err := fs.Stat(Dist, "dist/index.html"); err != nil {
		t.Fatalf("embedded UI lacks dist/index.html: %v", err)
	}
}
