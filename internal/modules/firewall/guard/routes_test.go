package guard

import "github.com/darkraise/darkpanel/internal/platform/netstat"

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
