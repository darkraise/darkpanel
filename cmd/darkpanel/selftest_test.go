package main

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

func TestSelftest(t *testing.T) {
	harnessKey := trustedkeys.Key{ID: "BCB10AB3E9AF694E", PublicKey: "RWROaa/pswqxvLcQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvD"}
	tests := []struct {
		name string
		keys []trustedkeys.Key
		code int
		out  string
	}{
		{"passes", []trustedkeys.Key{harnessKey}, cli.ExitOK, "selftest: ok\n"},
		{"no trusted key", nil, cli.ExitError, "darkpanel: selftest failed the trusted keys check: this build trusts no release signing key, so it could never verify an update\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTrustedKeys(t, tt.keys)
			out, code := runArgs("selftest")
			if code != tt.code || out != tt.out {
				t.Errorf("darkpanel selftest: exit %d, output %q; want exit %d and %q", code, out, tt.code, tt.out)
			}
		})
	}
	if out, code := runArgs("selftest", "extra"); code != cli.ExitUsage || !strings.Contains(out, `unknown command "extra"`) {
		t.Errorf("darkpanel selftest extra: exit %d, output %q; want a usage error", code, out)
	}
}
