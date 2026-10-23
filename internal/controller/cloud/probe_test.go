package cloud

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestProbeReads(t *testing.T) {
	probes := []Probe{
		{Name: "devices:read", Read: func(context.Context) error { return nil }},
		{Name: "policy:read", Read: func(context.Context) error {
			return errcode.New(errcode.CloudPermissionMissing, "tailscale answered 403")
		}},
	}
	got := ProbeReads(t.Context(), probes, []string{"devices:write", "auth_keys:write"})
	want := []Capability{
		{Name: "devices:read", State: CapabilityOK},
		{Name: "policy:read", State: CapabilityMissing, Detail: "cloud.permission_missing"},
		{Name: "devices:write", State: CapabilityUnverified},
		{Name: "auth_keys:write", State: CapabilityUnverified},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %+v, want %+v", got, want)
	}
}
