package eval

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// markState is a packet mark or a connection mark. val is the value on the
// assumption that every bit in ext is zero; ext holds the bits a source
// outside netfilter may have set (a socket option, or an earlier packet of
// the connection) and that no rule on the path has assigned since.
type markState struct{ val, ext uint32 }

// startMarks returns the packet mark and the connection mark the walk
// starts with. A packet that arrives from the network carries the mark the
// caller gives (zero for the guard's flows); a locally generated packet
// carries its socket's mark, unknown unless the caller gives a non-zero
// one. A new connection's mark is zero; an earlier packet may have set a
// later packet's.
func startMarks(p fwstate.Packet) (mark, conn markState) {
	mark = markState{val: p.Mark}
	if p.Path == "output" && p.Mark == 0 {
		mark.ext = ^uint32(0)
	}
	if p.CtState != "NEW" {
		conn.ext = ^uint32(0)
	}
	return mark, conn
}

// parseMark reads "value" or "value/mask" in hexadecimal or decimal; the
// mask defaults to every bit.
func parseMark(s string) (val, mask uint32, ok bool) {
	v, m, hasMask := strings.Cut(s, "/")
	val64, err := strconv.ParseUint(v, 0, 32)
	if err != nil {
		return 0, 0, false
	}
	mask64 := uint64(^uint32(0))
	if hasMask {
		if mask64, err = strconv.ParseUint(m, 0, 32); err != nil {
			return 0, 0, false
		}
	}
	return uint32(val64), uint32(mask64), true
}

// match applies [!] --mark val/mask. A match that holds on the assumption
// is taken as holding; one that fails on it but could hold through bits
// outside netfilter is undecidable.
func (s markState) match(val, mask uint32, neg bool) tri {
	if (s.val&mask == val) != neg {
		return yes
	}
	if s.ext&mask == 0 {
		return no
	}
	if neg {
		return maybe
	}
	known := mask &^ s.ext
	if val&^mask == 0 && s.val&known == val&known {
		return maybe
	}
	return no
}

// noConn reports whether the packet has no connection: the kernel gives
// INVALID and UNTRACKED packets none, so connmark never matches them,
// negated or not, and CONNMARK leaves them alone.
func (w *walker) noConn() bool {
	return w.pkt.CtState == "INVALID" || w.pkt.CtState == "UNTRACKED"
}

// markMatch applies -m mark or -m connmark. It handles "!" itself, because
// negating an assumed value is not the same as negating a decided one.
func (w *walker) markMatch(m netfilter.Match) (tri, string) {
	st := w.mark
	if m.Module == "connmark" {
		if w.noConn() {
			return no, ""
		}
		st = w.conn
	}
	res, why := yes, ""
	for _, o := range m.Options {
		val, mask, ok := parseMark(o.Values[0])
		if !ok {
			return maybe, "mark " + strconv.Quote(o.Values[0]) + " is not a number"
		}
		switch st.match(val, mask, o.Neg) {
		case no:
			return no, ""
		case maybe:
			if res == yes {
				res = maybe
				why = fmt.Sprintf("the match on mark %#x/%#x needs bits only a non-netfilter source could set", val, mask)
			}
		}
	}
	return res, why
}

// markSpec is a MARK or CONNMARK target's options.
type markSpec struct {
	setVal, setMask    uint32 // --set-xmark
	nfmask, ctmask     uint32 // every bit when not given
	set, save, restore bool
}

func parseMarkTarget(tg netfilter.Target) (markSpec, bool) {
	sp := markSpec{nfmask: ^uint32(0), ctmask: ^uint32(0)}
	for _, o := range tg.Options {
		var good bool
		switch o.Name {
		case "set-xmark":
			sp.setVal, sp.setMask, good = parseMark(o.Values[0])
			sp.set = true
		case "nfmask":
			_, sp.nfmask, good = parseMark("0/" + o.Values[0])
		case "ctmask":
			_, sp.ctmask, good = parseMark("0/" + o.Values[0])
		case "save-mark":
			sp.save, good = true, true
		case "restore-mark":
			sp.restore, good = true, true
		}
		if !good {
			return markSpec{}, false
		}
	}
	modes := 0
	for _, b := range []bool{sp.set, sp.save, sp.restore} {
		if b {
			modes++
		}
	}
	return sp, modes == 1 && (tg.Name == "CONNMARK" || sp.set)
}

// markTarget applies MARK --set-xmark and CONNMARK --set-xmark,
// --save-mark and --restore-mark as the kernel's xt_MARK and xt_CONNMARK
// do.
func (w *walker) markTarget(tg netfilter.Target, ref string) outcome {
	sp, ok := parseMarkTarget(tg)
	if !ok {
		return w.stop(fwstate.Unknown, ref+": target "+tg.Name+" with these options is not modelled")
	}
	if tg.Name == "CONNMARK" && w.noConn() {
		return next
	}
	switch {
	case tg.Name == "MARK":
		w.mark = markState{val: w.mark.val&^sp.setMask ^ sp.setVal, ext: w.mark.ext &^ sp.setMask}
	case sp.set:
		w.conn = markState{val: w.conn.val&^sp.setMask ^ sp.setVal, ext: w.conn.ext &^ sp.setMask}
	case sp.save:
		w.conn = w.saved(sp)
	default:
		w.mark = w.restored(sp)
	}
	return next
}

// uncertainCopy handles a rule whose match the walk cannot decide and
// whose target only copies one mark into the other, and reports whether it
// did. It absorbs the rule only when running it leaves the known value of
// the mark it writes unchanged; the walk then marks the bits the copy may
// bring in as unknown, so any later match that reads them is undecidable
// in turn. A copy that could change a known bit is left to decides, which
// makes the verdict unknown.
func (w *walker) uncertainCopy(tg netfilter.Target) bool {
	if tg.Name != "CONNMARK" || tg.Chain {
		return false
	}
	sp, ok := parseMarkTarget(tg)
	switch {
	case !ok || sp.set:
		return false
	case w.noConn():
		return true
	case sp.save:
		ran := w.saved(sp)
		if ran.val != w.conn.val {
			return false
		}
		w.conn.ext |= ran.ext
	default:
		ran := w.restored(sp)
		if ran.val != w.mark.val {
			return false
		}
		w.mark.ext |= ran.ext
	}
	return true
}

// saved is the connection mark after CONNMARK --save-mark.
func (w *walker) saved(sp markSpec) markState {
	return markState{
		val: w.conn.val&^sp.ctmask ^ w.mark.val&sp.nfmask,
		ext: w.conn.ext&^sp.ctmask | w.mark.ext&sp.nfmask,
	}
}

// restored is the packet mark after CONNMARK --restore-mark.
func (w *walker) restored(sp markSpec) markState {
	return markState{
		val: w.mark.val&^sp.nfmask ^ w.conn.val&sp.ctmask,
		ext: w.mark.ext&^sp.nfmask | w.conn.ext&sp.ctmask,
	}
}
