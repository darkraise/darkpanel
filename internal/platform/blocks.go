// Package platform is the only code that runs programs or writes host files
// (ADR 0009): an allowlisted exec runner, a file writer limited to the paths
// and marked blocks each module declares, and the ownership manifest that
// darkpanel uninstall reverses.
package platform

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// span is a block's BEGIN and END marker line indexes.
type span struct{ begin, end int }

func markersInvalid(format string, args ...any) error {
	return errcode.New(errcode.FilesMarkersBad, fmt.Sprintf(format, args...))
}

// splitLines splits content after each newline, so joining the lines gives
// content back; a last line without a newline is kept as it is.
func splitLines(content []byte) [][]byte {
	lines := bytes.SplitAfter(content, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// scanMarkers checks every DARKPANEL marker with prefix in lines and returns
// each block's span. A file with duplicated, nested or unterminated markers,
// or with a marker line ending in whitespace (a carriage return included,
// which would otherwise read as a different block id), is refused, never
// repaired by guessing (ADR 0009).
func scanMarkers(lines [][]byte, prefix string) (map[string]span, error) {
	begin, end := prefix+" BEGIN DARKPANEL ", prefix+" END DARKPANEL "
	spans := map[string]span{}
	open, openAt := "", -1
	for i, raw := range lines {
		line := strings.TrimSuffix(string(raw), "\n")
		marker := strings.HasPrefix(line, begin) || strings.HasPrefix(line, end)
		if marker && strings.TrimRight(line, " \t\r") != line {
			return nil, markersInvalid("line %d: marker line ends in whitespace", i+1)
		}
		switch {
		case strings.HasPrefix(line, begin):
			id := strings.TrimPrefix(line, begin)
			if open != "" {
				return nil, markersInvalid("line %d: block %s begins inside block %s", i+1, id, open)
			}
			if _, dup := spans[id]; dup {
				return nil, markersInvalid("line %d: block %s appears twice", i+1, id)
			}
			open, openAt = id, i
		case strings.HasPrefix(line, end):
			id := strings.TrimPrefix(line, end)
			if id != open {
				return nil, markersInvalid("line %d: end of block %s without its beginning", i+1, id)
			}
			spans[id] = span{openAt, i}
			open = ""
		}
	}
	if open != "" {
		return nil, markersInvalid("block %s has no end marker", open)
	}
	return spans, nil
}

// replaceBlock returns content with the body of block <module>/<blockID>
// replaced, or with the block appended when it is absent.
func replaceBlock(content []byte, prefix, module, blockID string, body []byte) ([]byte, error) {
	lines := splitLines(content)
	spans, err := scanMarkers(lines, prefix)
	if err != nil {
		return nil, err
	}
	id := module + "/" + blockID
	var block bytes.Buffer
	fmt.Fprintf(&block, "%s BEGIN DARKPANEL %s\n", prefix, id)
	block.Write(body)
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		block.WriteByte('\n')
	}
	fmt.Fprintf(&block, "%s END DARKPANEL %s\n", prefix, id)

	var out bytes.Buffer
	if s, ok := spans[id]; ok {
		out.Write(bytes.Join(lines[:s.begin], nil))
		out.Write(block.Bytes())
		out.Write(bytes.Join(lines[s.end+1:], nil))
	} else {
		out.Write(content)
		if len(content) > 0 && !bytes.HasSuffix(content, []byte("\n")) {
			out.WriteByte('\n')
		}
		out.Write(block.Bytes())
	}
	// A body holding marker lines would leave a file this package refuses.
	if _, err := scanMarkers(splitLines(out.Bytes()), prefix); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// removeBlock returns content without block <module>/<blockID>; content
// without the block is returned unchanged.
func removeBlock(content []byte, prefix, module, blockID string) ([]byte, error) {
	lines := splitLines(content)
	spans, err := scanMarkers(lines, prefix)
	if err != nil {
		return nil, err
	}
	s, ok := spans[module+"/"+blockID]
	if !ok {
		return content, nil
	}
	return append(bytes.Join(lines[:s.begin], nil), bytes.Join(lines[s.end+1:], nil)...), nil
}
