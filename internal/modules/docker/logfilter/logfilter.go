// Package logfilter masks secrets that a module registered for a container in
// the container's log frames.
package logfilter

import "bytes"

// Mask replaces every secret in the filtered output.
const Mask = "[redacted]"

// Filter masks secrets in the byte streams of one log read. It holds back,
// per stream, the bytes that may still grow into a secret. A Filter is not
// safe for concurrent use.
type Filter struct {
	secrets [][]byte
	held    map[string][]byte
}

// New builds a filter for secrets; empty secrets are ignored, and with none
// the filter passes data through unchanged. The secrets are copied.
func New(secrets [][]byte) *Filter {
	f := &Filter{held: map[string][]byte{}}
	for _, s := range secrets {
		if len(s) > 0 {
			f.secrets = append(f.secrets, bytes.Clone(s))
		}
	}
	return f
}

// Push takes the next bytes of one stream ("stdout" or "stderr") and returns
// the bytes now safe to emit, every secret replaced by Mask, even when a
// secret spans calls. It holds back only the longest suffix that is a proper
// prefix of some secret.
func (f *Filter) Push(stream string, data []byte) []byte {
	if len(f.secrets) == 0 {
		return data
	}
	held := f.held[stream]
	buf := make([]byte, 0, len(held)+len(data))
	buf = append(buf, held...)
	buf = append(buf, data...)
	out, rest := f.scan(buf, false)
	if len(rest) == 0 {
		delete(f.held, stream)
	} else {
		f.held[stream] = rest
	}
	return out
}

// Flush returns the bytes held for stream, masked, and forgets them.
func (f *Filter) Flush(stream string) []byte {
	held := f.held[stream]
	delete(f.held, stream)
	out, _ := f.scan(held, true)
	return out
}

func (f *Filter) scan(buf []byte, final bool) (out, held []byte) {
	for i := 0; i < len(buf); {
		rest := buf[i:]
		// A shorter secret that already matches here does not end the hold:
		// a longer secret starting at the same byte may still complete.
		if !final && f.prefixOfLonger(rest) {
			return out, bytes.Clone(rest)
		}
		if n := f.longestMatch(rest); n > 0 {
			out = append(out, Mask...)
			i += n
			continue
		}
		out = append(out, buf[i])
		i++
	}
	return out, nil
}

func (f *Filter) prefixOfLonger(rest []byte) bool {
	for _, s := range f.secrets {
		if len(s) > len(rest) && bytes.HasPrefix(s, rest) {
			return true
		}
	}
	return false
}

func (f *Filter) longestMatch(rest []byte) int {
	n := 0
	for _, s := range f.secrets {
		if len(s) > n && bytes.HasPrefix(rest, s) {
			n = len(s)
		}
	}
	return n
}
