package cloud

import (
	"context"
	"errors"
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

// TestW10PlProbeReadsUnexpectedFailures shows that a probe whose read fails
// with an error that is not an errcode error, and a probe with no read at all,
// are reported as missing with the internal code and never panic or leak the
// error text.
func TestW10PlProbeReadsUnexpectedFailures(t *testing.T) {
	probes := []Probe{
		{Name: "devices:read", Read: func(context.Context) error { return errors.New("dial tcp 203.0.113.10:443: connection refused") }},
		{Name: "policy:read"},
		{Name: "keys:read", Read: func(context.Context) error { return nil }},
	}
	var got []Capability
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ProbeReads panicked: %v", r)
			}
		}()
		got = ProbeReads(t.Context(), probes, []string{"devices:write"})
	}()
	want := []Capability{
		{Name: "devices:read", State: CapabilityMissing, Detail: "internal"},
		{Name: "policy:read", State: CapabilityMissing, Detail: "internal"},
		{Name: "keys:read", State: CapabilityOK},
		{Name: "devices:write", State: CapabilityUnverified},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %+v, want %+v", got, want)
	}
}
