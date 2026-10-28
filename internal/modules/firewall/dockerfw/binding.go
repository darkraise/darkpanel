package dockerfw

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	bindingNameRe    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	bindingServiceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}/[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
)

// Binding names a published container port: the containers a selector picks,
// the host address and port they are published on, and the protocol.
type Binding struct {
	Selector string     `json:"selector"`          // container name, or <compose project>/<service>
	HostAddr netip.Addr `json:"hostAddr,omitzero"` // zero: every host address
	HostPort uint16     `json:"hostPort"`
	Proto    string     `json:"proto"` // tcp | udp
}

// ID is the first 12 hex digits of the SHA-256 of the binding's identity.
func (b Binding) ID() string {
	addr := "all"
	if b.HostAddr.IsValid() {
		addr = b.HostAddr.String()
	}
	sum := sha256.Sum256([]byte(b.Selector + "|" + addr + "|" + strconv.Itoa(int(b.HostPort)) + "|" + b.Proto))
	return hex.EncodeToString(sum[:])[:12]
}

// Chain is the name of the binding's chain in the Docker block.
func (b Binding) Chain() string {
	return "dp-b-" + b.ID()
}

// problems lists the binding's field errors with paths relative to the
// binding. The unspecified host address is left to ContainerRule.Validate.
func (b Binding) problems() []errcode.FieldError {
	var fields []errcode.FieldError
	add := func(path, msg string) {
		fields = append(fields, errcode.FieldError{Path: path, Message: msg})
	}
	if !bindingNameRe.MatchString(b.Selector) && !bindingServiceRe.MatchString(b.Selector) {
		add("/selector", "must be a container name or <compose project>/<service>")
	}
	if b.HostAddr.IsValid() && (b.HostAddr.Zone() != "" || b.HostAddr.Is4In6()) {
		add("/hostAddr", "must be an IPv4 or IPv6 address without a zone and not IPv4-mapped")
	}
	if b.HostPort == 0 {
		add("/hostPort", "must be a port from 1 to 65535")
	}
	if b.Proto != "tcp" && b.Proto != "udp" {
		add("/proto", "must be tcp or udp")
	}
	return fields
}

// Validate returns nil, or an *errcode.Error with code errcode.InvalidInput
// that holds one field error per problem.
func (b Binding) Validate() error {
	fields := b.problems()
	if len(fields) == 0 {
		return nil
	}
	err := errcode.New(errcode.InvalidInput, "the binding is invalid")
	err.Fields = fields
	return err
}

// Selects reports whether a container with the given name and Compose labels
// is one the binding's selector picks. Docker's leading slash on name is
// ignored; the comparison is exact and case sensitive.
func (b Binding) Selects(name, project, service string) bool {
	if p, s, ok := strings.Cut(b.Selector, "/"); ok {
		return project != "" && project == p && service == s
	}
	return b.Selector == strings.TrimPrefix(name, "/")
}
