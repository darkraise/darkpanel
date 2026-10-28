package exposure

import (
	"cmp"
	"encoding/binary"
	"math/bits"
	"net/netip"
	"slices"
)

// Range is an inclusive run of addresses of one family.
type Range struct{ From, To netip.Addr }

// paWide is an address as a 128-bit number. An IPv4 address occupies the low
// 32 bits, so one set of arithmetic serves both families.
type paWide struct{ hi, lo uint64 }

// paSpan is an inclusive interval of paWide values.
type paSpan struct{ lo, hi paWide }

func (a paWide) compare(b paWide) int {
	return cmp.Or(cmp.Compare(a.hi, b.hi), cmp.Compare(a.lo, b.lo))
}

func (a paWide) or(b paWide) paWide { return paWide{a.hi | b.hi, a.lo | b.lo} }

func (a paWide) plusOne() paWide {
	if a.lo == ^uint64(0) {
		return paWide{a.hi + 1, 0}
	}
	return paWide{a.hi, a.lo + 1}
}

func (a paWide) minusOne() paWide {
	if a.lo == 0 {
		return paWide{a.hi - 1, ^uint64(0)}
	}
	return paWide{a.hi, a.lo - 1}
}

func (a paWide) trailingZeros() int {
	if a.lo != 0 {
		return bits.TrailingZeros64(a.lo)
	}
	return 64 + bits.TrailingZeros64(a.hi)
}

func (a paWide) addr(is4 bool) netip.Addr {
	var b [16]byte
	if is4 {
		binary.BigEndian.PutUint64(b[:8], a.lo)
		return netip.AddrFrom4([4]byte(b[4:8]))
	}
	binary.BigEndian.PutUint64(b[:8], a.hi)
	binary.BigEndian.PutUint64(b[8:], a.lo)
	return netip.AddrFrom16(b)
}

func paWideOf(a netip.Addr) paWide {
	if a.Is4() {
		b := a.As4()
		return paWide{lo: uint64(binary.BigEndian.Uint32(b[:]))}
	}
	b := a.As16()
	return paWide{binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])}
}

func paWidth(is4 bool) int {
	if is4 {
		return 32
	}
	return 128
}

func paTop(is4 bool) paWide {
	if is4 {
		return paWide{lo: 1<<32 - 1}
	}
	return paWide{^uint64(0), ^uint64(0)}
}

// paHostMask returns a number whose n low bits are set.
func paHostMask(n int) paWide {
	if n >= 64 {
		return paWide{hi: 1<<(n-64) - 1, lo: ^uint64(0)}
	}
	return paWide{lo: 1<<n - 1}
}

// paSpans returns the intervals of the valid prefixes of the given family.
func paSpans(ps []netip.Prefix, is4 bool) []paSpan {
	var out []paSpan
	for _, p := range ps {
		if !p.IsValid() || p.Addr().Is4() != is4 {
			continue
		}
		lo := paWideOf(p.Masked().Addr())
		out = append(out, paSpan{lo, lo.or(paHostMask(paWidth(is4) - p.Bits()))})
	}
	return out
}

// paUnion sorts spans and merges those that overlap or touch.
func paUnion(spans []paSpan, top paWide) []paSpan {
	slices.SortFunc(spans, func(a, b paSpan) int { return a.lo.compare(b.lo) })
	var out []paSpan
	for _, s := range spans {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.hi.compare(top) == 0 || s.lo.compare(last.hi.plusOne()) <= 0 {
				if s.hi.compare(last.hi) > 0 {
					last.hi = s.hi
				}
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// paSubtract removes the sorted, merged holes from the sorted, merged runs.
func paSubtract(runs, holes []paSpan) []paSpan {
	var out []paSpan
	for _, run := range runs {
		cur, done := run.lo, false
		for _, h := range holes {
			if h.hi.compare(cur) < 0 {
				continue
			}
			if h.lo.compare(run.hi) > 0 {
				break
			}
			if h.lo.compare(cur) > 0 {
				out = append(out, paSpan{cur, h.lo.minusOne()})
			}
			if h.hi.compare(run.hi) >= 0 {
				done = true
				break
			}
			cur = h.hi.plusOne()
		}
		if !done {
			out = append(out, paSpan{cur, run.hi})
		}
	}
	return out
}

// Atoms partitions the addresses of include that exclude does not cover into
// ranges that no cut splits: every address of an atom lies inside the same
// cuts. The family is the first valid include prefix's, and a prefix of
// another family or an invalid one is ignored in all three lists. Atoms come
// in ascending order and a run of addresses with no cut inside is one atom.
// No include, or an exclude that covers it all, returns nil.
func Atoms(include, exclude, cuts []netip.Prefix) []Range {
	i := slices.IndexFunc(include, func(p netip.Prefix) bool { return p.IsValid() })
	if i < 0 {
		return nil
	}
	is4 := include[i].Addr().Is4()
	top := paTop(is4)
	runs := paSubtract(paUnion(paSpans(include, is4), top), paUnion(paSpans(exclude, is4), top))

	var bounds []paWide
	for _, c := range paSpans(cuts, is4) {
		bounds = append(bounds, c.lo)
		if c.hi.compare(top) != 0 {
			bounds = append(bounds, c.hi.plusOne())
		}
	}
	slices.SortFunc(bounds, paWide.compare)
	bounds = slices.CompactFunc(bounds, func(a, b paWide) bool { return a.compare(b) == 0 })

	var out []Range
	next := 0
	for _, run := range runs {
		for next < len(bounds) && bounds[next].compare(run.lo) <= 0 {
			next++
		}
		start := run.lo
		for next < len(bounds) && bounds[next].compare(run.hi) <= 0 {
			out = append(out, Range{start.addr(is4), bounds[next].minusOne().addr(is4)})
			start = bounds[next]
			next++
		}
		out = append(out, Range{start.addr(is4), run.hi.addr(is4)})
	}
	return out
}

// Prefixes returns the fewest CIDR prefixes that cover r exactly, in
// ascending order. A zero or invalid address, addresses of two families or a
// From after To returns nil.
func (r Range) Prefixes() []netip.Prefix {
	if !r.From.IsValid() || !r.To.IsValid() || r.From.Is4() != r.To.Is4() {
		return nil
	}
	is4 := r.From.Is4()
	cur, to := paWideOf(r.From), paWideOf(r.To)
	if cur.compare(to) > 0 {
		return nil
	}
	width := paWidth(is4)
	var out []netip.Prefix
	for {
		k := min(cur.trailingZeros(), width)
		for k > 0 && cur.or(paHostMask(k)).compare(to) > 0 {
			k--
		}
		out = append(out, netip.PrefixFrom(cur.addr(is4), width-k))
		end := cur.or(paHostMask(k))
		if end.compare(to) == 0 {
			return out
		}
		cur = end.plusOne()
	}
}

// PortAtoms partitions the ports 1 to 65535 into ranges that no cut splits.
// A cut is clipped to 1 to 65535, since port 0 is no source port, and one
// that is empty after clipping is ignored. Adjacent cuts stay apart.
func PortAtoms(cuts []PortRange) []PortRange {
	bounds := []uint16{1}
	for _, c := range cuts {
		if c.From > c.To || c.To == 0 {
			continue
		}
		bounds = append(bounds, max(c.From, 1))
		if c.To < 65535 {
			bounds = append(bounds, c.To+1)
		}
	}
	slices.Sort(bounds)
	bounds = slices.Compact(bounds)
	out := make([]PortRange, len(bounds))
	for i, from := range bounds {
		to := uint16(65535)
		if i+1 < len(bounds) {
			to = bounds[i+1] - 1
		}
		out[i] = PortRange{From: from, To: to}
	}
	return out
}
