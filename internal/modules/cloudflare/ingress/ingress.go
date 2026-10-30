// Package ingress reads and edits the ingress rules of a Cloudflare tunnel
// configuration. Members it does not model are kept byte for byte, so an edit
// never alters settings an operator made elsewhere.
package ingress

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// CatchAll is the service of the rule that answers every request no other rule
// matches.
const CatchAll = "http_status:404"

// Rule is one ingress rule. Members of the rule and of its originRequest that
// are not named here are kept as read.
type Rule struct {
	Hostname    string
	Path        string
	Service     string
	NoTLSVerify bool // originRequest.noTLSVerify

	extra       map[string]json.RawMessage
	originExtra map[string]json.RawMessage
}

// Config is the config object of a tunnel configuration. Members other than
// ingress are kept as read.
type Config struct {
	Ingress []Rule

	extra map[string]json.RawMessage
}

// encode marshals v without HTML escaping and without the trailing newline. A
// Marshaler's output passes through json.Marshal's escaping of <, > and &,
// which would alter the raw members kept as read. Maps are written with sorted
// keys.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func isNull(data []byte) bool { return bytes.Equal(bytes.TrimSpace(data), []byte("null")) }

// copyMembers returns a new map holding the members of m.
func copyMembers(m map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// MarshalJSON writes the rule with sorted keys. A false NoTLSVerify is not
// written.
func (r Rule) MarshalJSON() ([]byte, error) {
	m := copyMembers(r.extra)
	set := func(key string, v any) error {
		b, err := encode(v)
		if err != nil {
			return err
		}
		m[key] = b
		return nil
	}
	if r.Hostname != "" {
		if err := set("hostname", r.Hostname); err != nil {
			return nil, err
		}
	}
	if r.Path != "" {
		if err := set("path", r.Path); err != nil {
			return nil, err
		}
	}
	if err := set("service", r.Service); err != nil {
		return nil, err
	}
	if r.NoTLSVerify || len(r.originExtra) > 0 {
		origin := copyMembers(r.originExtra)
		if r.NoTLSVerify {
			origin["noTLSVerify"] = json.RawMessage("true")
		}
		if err := set("originRequest", origin); err != nil {
			return nil, err
		}
	}
	return encode(m)
}

// UnmarshalJSON reads one rule.
func (r *Rule) UnmarshalJSON(data []byte) error {
	*r = Rule{}
	if isNull(data) {
		return fmt.Errorf("ingress: a rule must be an object")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"hostname", &r.Hostname}, {"path", &r.Path}, {"service", &r.Service}} {
		if raw, ok := m[f.key]; ok {
			if err := json.Unmarshal(raw, f.dst); err != nil {
				return fmt.Errorf("ingress: %s: %w", f.key, err)
			}
			delete(m, f.key)
		}
	}
	if raw, ok := m["originRequest"]; ok {
		var origin map[string]json.RawMessage
		if err := json.Unmarshal(raw, &origin); err != nil {
			return fmt.Errorf("ingress: originRequest: %w", err)
		}
		if v, ok := origin["noTLSVerify"]; ok {
			if err := json.Unmarshal(v, &r.NoTLSVerify); err != nil {
				return fmt.Errorf("ingress: originRequest.noTLSVerify: %w", err)
			}
			delete(origin, "noTLSVerify")
		}
		if len(origin) > 0 {
			r.originExtra = origin
		}
		delete(m, "originRequest")
	}
	if len(m) > 0 {
		r.extra = m
	}
	return nil
}

// MarshalJSON writes the configuration with sorted keys. An empty rule list is
// not written, so an empty configuration is {}.
func (c Config) MarshalJSON() ([]byte, error) {
	m := copyMembers(c.extra)
	if len(c.Ingress) > 0 {
		b, err := encode(c.Ingress)
		if err != nil {
			return nil, err
		}
		m["ingress"] = b
	}
	return encode(m)
}

// UnmarshalJSON reads a configuration; null is an empty configuration.
func (c *Config) UnmarshalJSON(data []byte) error {
	*c = Config{}
	if isNull(data) {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("ingress: %w", err)
	}
	if raw, ok := m["ingress"]; ok {
		var rules []json.RawMessage
		if err := json.Unmarshal(raw, &rules); err != nil {
			return fmt.Errorf("ingress: ingress list: %w", err)
		}
		for _, rr := range rules {
			var r Rule
			if err := r.UnmarshalJSON(rr); err != nil {
				return err
			}
			c.Ingress = append(c.Ingress, r)
		}
		delete(m, "ingress")
	}
	if len(m) > 0 {
		c.extra = m
	}
	return nil
}

// Lookup returns the first rule for hostname, compared without case. An empty
// hostname matches nothing, so the catch-all is not found by it.
func Lookup(c Config, hostname string) (Rule, bool) {
	if hostname == "" {
		return Rule{}, false
	}
	for _, r := range c.Ingress {
		if strings.EqualFold(r.Hostname, hostname) {
			return r, true
		}
	}
	return Rule{}, false
}

// Add inserts r before the catch-all, which is the last rule when it has
// neither hostname nor path; without one, r and the CatchAll rule are appended.
// It reports changed false when an equal rule for the hostname exists, and
// refuses a hostname that already has a different rule. The argument is never
// modified.
func Add(c Config, r Rule) (out Config, changed bool, err error) {
	if r.Hostname == "" {
		return Config{}, false, fmt.Errorf("ingress: a rule needs a hostname")
	}
	if r.Service == "" {
		return Config{}, false, fmt.Errorf("ingress: a rule needs a service")
	}
	if have, ok := Lookup(c, r.Hostname); ok {
		if have.Path == r.Path && have.Service == r.Service && have.NoTLSVerify == r.NoTLSVerify {
			return c, false, nil
		}
		return Config{}, false, fmt.Errorf("ingress: hostname %q already has a different rule", r.Hostname)
	}
	rules := make([]Rule, 0, len(c.Ingress)+2)
	if n := len(c.Ingress); n > 0 && c.Ingress[n-1].Hostname == "" && c.Ingress[n-1].Path == "" {
		rules = append(rules, c.Ingress[:n-1]...)
		rules = append(rules, r, c.Ingress[n-1])
	} else {
		rules = append(rules, c.Ingress...)
		rules = append(rules, r, Rule{Service: CatchAll})
	}
	return Config{Ingress: rules, extra: c.extra}, true, nil
}

// Remove drops every rule for hostname, compared without case, and carries the
// other rules over unchanged. An empty or absent hostname returns c and false;
// the catch-all is therefore never dropped.
func Remove(c Config, hostname string) (out Config, changed bool) {
	if _, ok := Lookup(c, hostname); !ok {
		return c, false
	}
	rules := make([]Rule, 0, len(c.Ingress))
	for _, r := range c.Ingress {
		if !strings.EqualFold(r.Hostname, hostname) {
			rules = append(rules, r)
		}
	}
	return Config{Ingress: rules, extra: c.extra}, true
}

// Hash is the lowercase hex SHA-256 of the configuration's canonical JSON, in
// which the keys of every object are sorted. Configurations that differ only
// in member order or whitespace hash alike.
func Hash(c Config) string {
	b, err := c.MarshalJSON()
	if err != nil {
		panic(fmt.Sprintf("ingress: marshal configuration: %v", err))
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		panic(fmt.Sprintf("ingress: canonicalize configuration: %v", err))
	}
	canon, err := encode(v)
	if err != nil {
		panic(fmt.Sprintf("ingress: canonicalize configuration: %v", err))
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}
