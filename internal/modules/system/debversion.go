package system

import (
	"strconv"
	"strings"
)

// debVersionCompare compares two Debian versions as Debian Policy 5.6.12
// orders them: epoch, then upstream version, then revision. It returns -1, 0
// or 1.
func debVersionCompare(a, b string) int {
	ea, ua, ra := splitDebVersion(a)
	eb, ub, rb := splitDebVersion(b)
	if ea != eb {
		if ea < eb {
			return -1
		}
		return 1
	}
	if c := debPartCompare(ua, ub); c != 0 {
		return c
	}
	return debPartCompare(ra, rb)
}

// splitDebVersion splits [epoch:]upstream[-revision]; a missing epoch is 0
// and a missing revision is "".
func splitDebVersion(v string) (epoch int, upstream, revision string) {
	if e, rest, ok := strings.Cut(v, ":"); ok {
		epoch, _ = strconv.Atoi(e)
		v = rest
	}
	if i := strings.LastIndex(v, "-"); i >= 0 {
		return epoch, v[:i], v[i+1:]
	}
	return epoch, v, ""
}

// debPartCompare compares an upstream version or a revision: alternating
// non-digit parts, compared by debCharOrder, and digit parts, compared as
// numbers.
func debPartCompare(a, b string) int {
	for a != "" || b != "" {
		var na, nb string
		na, a = cutWhile(a, func(c byte) bool { return !isDigit(c) })
		nb, b = cutWhile(b, func(c byte) bool { return !isDigit(c) })
		if c := debLexCompare(na, nb); c != 0 {
			return c
		}
		var da, db string
		da, a = cutWhile(a, isDigit)
		db, b = cutWhile(b, isDigit)
		if c := debNumCompare(da, db); c != 0 {
			return c
		}
	}
	return 0
}

// debLexCompare compares non-digit parts character by character, with ~
// before the end of the part, the end before letters, and letters before
// every other character.
func debLexCompare(a, b string) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var ca, cb int
		if i < len(a) {
			ca = debCharOrder(a[i])
		}
		if i < len(b) {
			cb = debCharOrder(b[i])
		}
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
	}
	return 0
}

func debCharOrder(c byte) int {
	switch {
	case c == '~':
		return -1
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		return int(c)
	default:
		return int(c) + 256
	}
}

func debNumCompare(a, b string) int {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func cutWhile(s string, f func(byte) bool) (head, rest string) {
	i := 0
	for i < len(s) && f(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
