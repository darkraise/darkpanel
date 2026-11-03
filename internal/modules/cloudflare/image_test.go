package cloudflare

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var imPinnedForm = regexp.MustCompile(`^cloudflare/cloudflared:([0-9]{4})\.([0-9]{1,2})\.([0-9]+)@sha256:([0-9a-f]{64})$`)

// TestImPinned: the image the connector runs is a release of 2025.4.0 or later, pinned by the digest of its multi-arch
// index and never by a moving tag; the user it runs as is 65532.
func TestImPinned(t *testing.T) {
	m := imPinnedForm.FindStringSubmatch(Image)
	if m == nil {
		t.Fatalf("Image = %q, want cloudflare/cloudflared:<year>.<month>.<patch>@sha256:<64 hex digits>", Image)
	}
	if strings.Trim(m[4], "0") == "" {
		t.Fatalf("Image = %q, want the digest of the release's index, not a placeholder of zeros", Image)
	}
	var got [3]int
	for i := range got {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			t.Fatal(err)
		}
		got[i] = n
	}
	for i, floor := range [3]int{2025, 4, 0} {
		if got[i] > floor {
			break
		}
		if got[i] < floor {
			t.Fatalf("Image = %q, want version 2025.4.0 or later", Image)
		}
	}
	if imageUID != 65532 {
		t.Errorf("imageUID = %d, want 65532", imageUID)
	}
}
