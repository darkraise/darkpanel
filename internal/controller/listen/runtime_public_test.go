package listen

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestV1r18StoreKeepsThePublicBinding(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	npm := rsSpec
	npm.PublicBinding = netip.MustParseAddrPort("0.0.0.0:443")
	rsInsert(t, db, "L1", npm, testStart)
	rsInsert(t, db, "L2", rsSpec, testStart.Add(time.Second))
	got, err := getListener(ctx, db, "L1")
	if err != nil || got.Spec != npm {
		t.Fatalf("getListener(L1) = %+v, %v; want spec %+v", got, err, npm)
	}
	if got, err := getListener(ctx, db, "L2"); err != nil || got.Spec.PublicBinding.IsValid() {
		t.Fatalf("getListener(L2) = %+v, %v; want no public binding", got, err)
	}
	open, err := listListeners(ctx, db, core.ListenerOpen)
	if err != nil || len(open) != 2 || open[0].Spec.PublicBinding != npm.PublicBinding || open[1].Spec.PublicBinding.IsValid() {
		t.Fatalf("listListeners = %+v, %v; want L1 with the binding and L2 without", open, err)
	}
}

func TestV1r18ValidListenerSpecChecksThePublicBinding(t *testing.T) {
	for _, tt := range []struct {
		name    string
		binding string
		want    string // the recorded binding, "" for none
		refused bool
	}{
		{"none", "", "", false},
		{"every address", "0.0.0.0:443", "0.0.0.0:443", false},
		{"IPv4-mapped", "[::ffff:192.0.2.1]:443", "192.0.2.1:443", false},
		{"port 0", "0.0.0.0:0", "", true},
		{"zone", "[fe80::1%eth0]:443", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := rsSpec
			if tt.binding != "" {
				spec.PublicBinding = netip.MustParseAddrPort(tt.binding)
			}
			got, err := validListenerSpec(spec)
			if tt.refused {
				if err == nil || errcode.From(err).Code != errcode.InvalidInput {
					t.Fatalf("validListenerSpec = %+v, %v; want %s", got, err, errcode.InvalidInput)
				}
				return
			}
			text := ""
			if got.PublicBinding.IsValid() {
				text = got.PublicBinding.String()
			}
			if err != nil || text != tt.want {
				t.Fatalf("validListenerSpec binding = %q, %v; want %q", text, err, tt.want)
			}
		})
	}
}
