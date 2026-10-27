package guard

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// gxDumpHost is a gHost that also lists routes: dump answers by family, err
// fails every dump, and dumps records the families asked.
type gxDumpHost struct {
	*gHost
	dump  map[netstat.Family][]netstat.Route
	err   error
	dumps []netstat.Family
}

func (h *gxDumpHost) Routes(family netstat.Family) ([]netstat.Route, error) {
	h.dumps = append(h.dumps, family)
	return h.dump[family], h.err
}

var (
	_ Host        = (*gxDumpHost)(nil)
	_ routeDumper = (*gxDumpHost)(nil)
	_ routeDumper = LiveHost{}
)

func TestGxLiveHostRoutes(t *testing.T) {
	routes, err := LiveHost{}.Routes(netstat.IPv4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if !r.Dst.IsValid() || !r.Dst.Addr().Is4() {
			t.Errorf("route %+v is not an IPv4 route", r)
		}
	}
}
