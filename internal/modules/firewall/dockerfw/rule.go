package dockerfw

import (
	"net/netip"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Kind says whether a container rule allows or denies a source.
type Kind string

// The kinds of container rule.
const (
	Allow Kind = "allow"
	Deny  Kind = "deny"
)

// LabelPrefix starts the label of a realized UFW rule: "ctr <ContainerRule.ID>".
const LabelPrefix = "ctr "

var labelIDRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// ContainerRule is a stored allowance or denial for one binding.
type ContainerRule struct {
	ID      string       `json:"id"` // a ULID
	Binding Binding      `json:"binding"`
	Kind    Kind         `json:"kind"`
	From    netip.Prefix `json:"from,omitzero"` // zero: any source (allow only)
}

// RuleOf returns the container rule id a UFW rule label carries, and false
// when the label is not exactly LabelPrefix followed by a ULID.
func RuleOf(label string) (id string, ok bool) {
	id, found := strings.CutPrefix(label, LabelPrefix)
	if !found || !labelIDRe.MatchString(id) {
		return "", false
	}
	return id, true
}

// Validate returns nil, or an *errcode.Error with code errcode.InvalidInput
// that holds one field error per problem. The rule's ID is not checked.
func (r ContainerRule) Validate(s Settings) error {
	var fields []errcode.FieldError
	add := func(path, msg string) {
		fields = append(fields, errcode.FieldError{Path: path, Message: msg})
	}

	for _, f := range r.Binding.problems() {
		add("/binding"+f.Path, f.Message)
	}
	if r.Binding.HostAddr.IsValid() && r.Binding.HostAddr.IsUnspecified() {
		add("/binding/hostAddr", "omit it for every address")
	}
	if r.Kind != Allow && r.Kind != Deny {
		add("/kind", "must be allow or deny")
	}

	set := r.From != netip.Prefix{}
	switch {
	case r.Kind == Deny && !set:
		add("/from", "a deny needs the source range to refuse")
	case set && !r.From.IsValid():
		add("/from", "must be a valid CIDR range")
	case set && r.From.Addr().Is4In6():
		add("/from", "is an IPv4-mapped IPv6 range; write it as an IPv4 range")
	case set && r.From != r.From.Masked():
		add("/from", "has host bits set; use "+r.From.Masked().String())
	case r.Kind == Deny:
		for _, p := range s.PrivateSources {
			if p.Bits() <= r.From.Bits() && p.Contains(r.From.Addr()) {
				add("/from", "lies inside the private range "+p.String()+", so the deny could never match")
				break
			}
		}
	}

	if len(fields) == 0 {
		return nil
	}
	err := errcode.New(errcode.InvalidInput, "the container rule is invalid")
	err.Fields = fields
	return err
}
