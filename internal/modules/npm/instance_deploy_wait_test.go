package npm

import (
	"context"
	"fmt"
	"testing"
)

func TestW12bDeployWaitDetail(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"cancelled", context.Canceled, "the deploy was cancelled while it waited for Nginx Proxy Manager to answer"},
		{"cancelled, wrapped", fmt.Errorf("status: %w", context.Canceled), "the deploy was cancelled while it waited for Nginx Proxy Manager to answer"},
		{"deadline passed", context.DeadlineExceeded, "the deploy ran out of time while it waited for Nginx Proxy Manager to start"},
		{"deadline passed, wrapped", fmt.Errorf("status: %w", context.DeadlineExceeded), "the deploy ran out of time while it waited for Nginx Proxy Manager to start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deployWaitDetail(tc.err); got != tc.want {
				t.Errorf("deployWaitDetail(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
