package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// authorize refuses p with auth.forbidden unless its scope includes need.
func authorize(p core.Principal, need core.Scope) error {
	if !p.Scope.Allows(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	return nil
}

// authorizeHost refuses p on hostID: request.not_found when the host is
// outside p's access, so host ids do not leak, and auth.forbidden when p's
// scope there is below need.
func authorizeHost(p core.Principal, hostID string, need core.Scope) error {
	have := p.HostAccess().On(hostID)
	if have == "" {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	if !have.Allows(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope on host %s", need, hostID))
	}
	return nil
}

// authorizeController refuses p a controller-side read by the read's
// HostData class (ADR 0018). An input read's hosts are checked once its
// input is decoded (authorizeInputHosts).
func authorizeController(p core.Principal, def core.ActionDef) error {
	need := core.RequiredScope(def.Spec)
	// A MinScope read needs that scope everywhere: the class rules alone would
	// serve it to an admin's read token.
	access := p.HostAccess()
	if !core.ScopeRead.Allows(need) && !access.AllHosts(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	switch def.Spec.HostData {
	case core.HostDataNone, core.HostDataInput, core.HostDataFiltered:
		if access.Fleet || readsAnyHost(access) {
			return nil
		}
	default:
		if access.Fleet {
			return nil
		}
	}
	return errcode.New(errcode.Forbidden, "this request needs access to the whole fleet")
}

// readsAnyHost reports whether a grants read on at least one host.
func readsAnyHost(a core.Access) bool {
	if a.Admin || a.All.Allows(core.ScopeRead) {
		return true
	}
	for _, s := range a.Hosts {
		if s.Allows(core.ScopeRead) {
			return true
		}
	}
	return false
}

// authorizeInputHosts checks read on every host an input read's
// HostPointers name. A pointer to an array names each of its elements. A
// pointer the input leaves out names no host; one holding anything but a
// string or an array of strings refuses a principal without fleet access
// as not found.
func authorizeInputHosts(p core.Principal, def core.ActionDef, input json.RawMessage) error {
	if def.Spec.HostData != core.HostDataInput {
		return nil
	}
	var doc any
	if err := json.Unmarshal(input, &doc); err != nil {
		return invalidField("/input", "must be a JSON value")
	}
	for _, ptr := range def.Spec.HostPointers {
		v, present := jsonPointer(doc, ptr)
		if !present {
			continue
		}
		var hosts []string
		named := true
		switch v := v.(type) {
		case string:
			hosts = []string{v}
		case []any:
			for _, e := range v {
				s, isString := e.(string)
				if !isString {
					named = false
					continue
				}
				hosts = append(hosts, s)
			}
		default:
			named = false
		}
		if !named && !p.HostAccess().Fleet {
			return errcode.New(errcode.NotFound, "no host named by "+ptr)
		}
		for _, h := range hosts {
			if err := authorizeHost(p, h, core.ScopeRead); err != nil {
				return err
			}
		}
	}
	return nil
}

// jsonPointer resolves an RFC 6901 pointer of object members in doc.
func jsonPointer(doc any, ptr string) (any, bool) {
	if ptr == "" {
		return doc, true
	}
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		obj, ok := doc.(map[string]any)
		if !ok {
			return nil, false
		}
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		if doc, ok = obj[tok]; !ok {
			return nil, false
		}
	}
	return doc, true
}
