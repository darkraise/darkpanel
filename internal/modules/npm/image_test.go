package npm

import (
	"regexp"
	"testing"
)

var imPinnedForm = regexp.MustCompile(`^jc21/nginx-proxy-manager:2\.(1[3-9]|[2-9][0-9])\.[0-9]+@sha256:[0-9a-f]{64}$`)

// TestImPinned: the image the module deploys is a 2.13-or-later release
// tag pinned by the digest of its multi-arch index, never a moving tag.
func TestImPinned(t *testing.T) {
	if !imPinnedForm.MatchString(Image) {
		t.Fatalf("Image = %q, want jc21/nginx-proxy-manager:2.<13 or later>.<patch>@sha256:<64 hex digits>", Image)
	}
}
