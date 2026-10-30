package policy

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/tailscale/hujson"
)

// Canonical is the standardized JSON of HuJSON text with object keys sorted and
// no insignificant whitespace. Numbers keep their digits.
func Canonical(text []byte) ([]byte, error) {
	std, err := hujson.Standardize(bytes.Clone(text))
	if err != nil {
		return nil, fmt.Errorf("policy: parse: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(std))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("policy: parse: %w", err)
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("policy: encode: %w", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}
