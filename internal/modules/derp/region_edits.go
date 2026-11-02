package derp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

const (
	editAckPrefix  = "nodes will relay only through "
	editMaxNameLen = 64
)

var (
	editCodePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	editPinPattern  = regexp.MustCompile(`^` + regexp.QuoteMeta(server.PinPrefix) + `[0-9a-f]{64}$`)
	// editTagPattern accepts the tags the fleet and tailnet modules accept, and no wildcard, autogroup or address.
	editTagPattern = regexp.MustCompile(`^tag:[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)
)

// regionEdits declares derp.region.edits: the tailnet policy edits that publish, withdraw or re-pin a DERP region,
// which the region composites hand to tailnet.policy.patch.
func (d dp) regionEdits() core.ActionDef {
	return core.DefineInternalRead[RegionEditsInput, RegionEdits](core.ActionSpec{
		Name:    ID + ".region.edits",
		Summary: "Compute the tailnet policy edits that set, remove or re-pin a DERP region; nothing is written.",
		Version: 1,
		Side:    core.SideController,
	}, d.regionEditsRead)
}

// regionEditsRead computes the edits of one op; it validates the input before reading anything.
func (d dp) regionEditsRead(ctx context.Context, env core.Env, in RegionEditsInput) (core.Revealed[RegionEdits], error) {
	if err := editValidate(in); err != nil {
		return core.Revealed[RegionEdits]{}, err
	}
	var out RegionEdits
	var err error
	switch in.Op {
	case "set":
		out, err = d.editSet(ctx, env, in)
	case "remove":
		out, err = d.editRemove(ctx, env, in)
	default:
		out, err = d.editRotate(ctx, env, in)
	}
	if err != nil {
		return core.Revealed[RegionEdits]{}, err
	}
	if out.Edits == nil {
		out.Edits = []policy.Edit{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return core.Revealed[RegionEdits]{Identity: fmt.Sprintf("DERP region %s edits for region %d", in.Op, in.RegionID), Value: out}, nil
}

// visibilityGrant is the grant that lets every node reach the DERP server's node, addressed to its tag.
func visibilityGrant(tag string) json.RawMessage {
	raw, err := json.Marshal(struct {
		Src []string `json:"src"`
		Dst []string `json:"dst"`
		IP  []string `json:"ip"`
	}{Src: []string{"*"}, Dst: []string{tag}, IP: []string{"tcp:443"}})
	if err != nil {
		panic(err) // a struct of string slices always encodes
	}
	return raw
}

// editRegion is the region object the set op publishes for server s.
func editRegion(s ServerStatus, in RegionEditsInput) map[string]any {
	node := map[string]any{
		"Name": strconv.Itoa(in.RegionID) + "a", "RegionID": in.RegionID, "HostName": s.HostName,
		"DERPPort": server.HTTPSPort, "STUNPort": server.STUNPort,
	}
	if s.Mode == server.ModeIP {
		if a, err := netip.ParseAddr(s.HostName); err == nil && a.Is4() {
			node["IPv4"] = s.HostName
		} else {
			node["IPv6"] = s.HostName
		}
		node["CertName"] = s.CertName
	}
	return map[string]any{"RegionID": in.RegionID, "RegionCode": in.RegionCode, "RegionName": in.RegionName, "Nodes": []any{node}}
}

// editValidate refuses an input the op cannot use, naming the first wrong field.
func editValidate(in RegionEditsInput) error {
	switch in.Op {
	case "set", "remove", "rotate":
	default:
		return editInvalid("/op", "op must be set, remove or rotate")
	}
	if in.RegionID < 900 || in.RegionID > 999 {
		return editInvalid("/regionId", "the region id must be between 900 and 999")
	}
	if in.Op != "remove" && in.HostID == "" {
		return editInvalid("/hostId", "a host id is required")
	}
	switch in.Op {
	case "set":
		if !editCodePattern.MatchString(in.RegionCode) {
			return editInvalid("/regionCode", "the region code must be a lower-case letter followed by at most 31 lower-case letters, digits or hyphens")
		}
		if in.RegionName == "" || len(in.RegionName) > editMaxNameLen {
			return editInvalid("/regionName", "the region name must have 1 to "+strconv.Itoa(editMaxNameLen)+" bytes")
		}
	case "rotate":
		if !editPinPattern.MatchString(in.CertName) {
			return editInvalid("/certName", "the certificate pin must be "+server.PinPrefix+" followed by 64 lower-case hex digits")
		}
	}
	return nil
}

// editInvalid is a definite request.invalid_input with one field error.
func editInvalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
		Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// editNotReady is a definite derp.not_ready.
func editNotReady(detail string) error {
	return core.Definite(errcode.New(errcode.DERPNotReady, detail))
}

// editSet computes the edits that publish the server of in.HostID as region in.RegionID with its visibility grant.
func (d dp) editSet(ctx context.Context, env core.Env, in RegionEditsInput) (RegionEdits, error) {
	deploy, err := editDeployConfirmed(ctx, env, in.HostID)
	if err != nil {
		return RegionEdits{}, err
	}
	probe, err := d.probeServer(ctx, env, ServerProbeInput{HostID: in.HostID})
	if err != nil {
		return RegionEdits{}, err
	}
	if err := editServerCheck(probe.Server, deploy); err != nil {
		return RegionEdits{}, err
	}
	ready, reason, warnings := probeReady(probe)
	if !ready {
		return RegionEdits{}, editNotReady("the DERP server on " + in.HostID + " is not reachable yet: " + reason)
	}
	m, doc, err := d.editPolicy(ctx, env)
	if err != nil {
		return RegionEdits{}, err
	}
	if r, ok := m.Regions[in.RegionID]; ok {
		for _, n := range r.Nodes {
			if !strings.EqualFold(n.HostName, probe.Server.HostName) {
				return RegionEdits{}, core.Definite(errcode.New(errcode.DERPRegionTaken,
					fmt.Sprintf("region %d already belongs to %s", in.RegionID, n.HostName)))
			}
		}
	}
	if in.OmitDefaultRegions {
		if err := d.editOmitAllowed(ctx, env, m, in); err != nil {
			return RegionEdits{}, err
		}
	}
	tag, err := d.editServerTag(ctx, env, probe.Server)
	if err != nil {
		return RegionEdits{}, err
	}
	edits, err := editSetEdits(m, doc, editRegion(probe.Server, in), visibilityGrant(tag), in)
	if err != nil {
		return RegionEdits{}, err
	}
	return RegionEdits{Edits: edits, Warnings: warnings}, nil
}

// editServerCheck refuses a server status the set op must not publish. The status is the DERP host agent's report,
// so its mode and host name must be the confirmed deploy's and pass server.CheckConfig, and an ip-mode pin must be
// a pin.
func editServerCheck(s ServerStatus, deploy DeployInput) error {
	if err := server.CheckConfig(server.Config{Mode: s.Mode, HostName: s.HostName}); err != nil {
		return editNotReady(fmt.Sprintf("the DERP host reports the %s-mode host name %q: %s", s.Mode, s.HostName, errcode.From(err).Fields[0].Message))
	}
	if s.Mode != deploy.Mode || s.HostName != deploy.HostName {
		return editNotReady(fmt.Sprintf("the DERP host reports a %s-mode server for %s, but its confirmed deploy installed a %s-mode server for %s",
			s.Mode, s.HostName, deploy.Mode, deploy.HostName))
	}
	if s.Mode == server.ModeIP && !editPinPattern.MatchString(s.CertName) {
		return editNotReady(fmt.Sprintf("the DERP host reports the certificate pin %q, which is not %s followed by 64 lower-case hex digits",
			s.CertName, server.PinPrefix))
	}
	return nil
}

// editServerTag is the tag the visibility grant addresses: the lexically smallest tag of the DERP host's node. The
// tags come from the DERP host's agent, so each must be a tag name, and the tailnet's device that holds one of the
// node's addresses must carry the chosen tag.
func (d dp) editServerTag(ctx context.Context, env core.Env, s ServerStatus) (string, error) {
	if len(s.Tags) == 0 {
		return "", editNotReady("the DERP host's node has no tag; the visibility grant needs one")
	}
	for _, t := range s.Tags {
		if !editTagPattern.MatchString(t) {
			return "", editNotReady(fmt.Sprintf("the DERP host reports the node tag %q, which is not a tag name", t))
		}
	}
	tag := slices.Min(s.Tags)
	devices, err := d.d.devices(ctx, env)
	if err != nil {
		return "", err
	}
	for _, dev := range devices {
		if !slices.ContainsFunc(s.Addresses, func(a string) bool { return slices.Contains(dev.Addresses, a) }) {
			continue
		}
		if !slices.Contains(dev.Tags, tag) {
			return "", editNotReady(fmt.Sprintf("the tailnet's device %s does not carry the tag %s that the DERP host reports", dev.Name, tag))
		}
		return tag, nil
	}
	return "", editNotReady(fmt.Sprintf("no device of the tailnet holds the DERP host's node addresses (%s)", strings.Join(s.Addresses, ", ")))
}

// editDeployConfirmed returns the input of the newest derp.server.deploy change of hostID, and refuses unless that
// change is completed.
func editDeployConfirmed(ctx context.Context, env core.Env, hostID string) (DeployInput, error) {
	rows, err := env.DB.Read().QueryContext(ctx,
		`SELECT state, input_json FROM core_changes WHERE action = ? ORDER BY created_at DESC, id DESC`, ID+".server.deploy")
	if err != nil {
		return DeployInput{}, fmt.Errorf("derp: read the deploy changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	state := "no deploy found"
	var found DeployInput
	for rows.Next() {
		var st, input string
		if err := rows.Scan(&st, &input); err != nil {
			return DeployInput{}, fmt.Errorf("derp: read the deploy changes: %w", err)
		}
		var deploy DeployInput
		if json.Unmarshal([]byte(input), &deploy) == nil && deploy.HostID == hostID {
			state, found = st, deploy
			break
		}
	}
	if err := rows.Err(); err != nil {
		return DeployInput{}, fmt.Errorf("derp: read the deploy changes: %w", err)
	}
	if state != core.StateCompleted {
		return DeployInput{}, editNotReady("the deploy of the DERP server on " + hostID + " is not confirmed (" + state + ")")
	}
	return found, nil
}

// editOmitAllowed refuses dropping the default regions unless another custom region's server passes the probe or
// the operator typed the acknowledgement.
func (d dp) editOmitAllowed(ctx context.Context, env core.Env, m derpMap, in RegionEditsInput) error {
	records, err := loadServerRecords(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	for _, id := range m.customRegions() {
		if id == in.RegionID {
			continue
		}
		i := slices.IndexFunc(records, func(r serverRecord) bool { return r.RegionID == id })
		if i < 0 {
			continue
		}
		p, err := d.probeServer(ctx, env, ServerProbeInput{HostID: records[i].HostID})
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			continue
		}
		if ready, _, _ := probeReady(p); ready {
			return nil
		}
	}
	ack := editAckPrefix + in.RegionCode
	if in.Acknowledge == ack {
		return nil
	}
	return editInvalid("/acknowledge", `omitting the default regions needs the acknowledgement "`+ack+`"`)
}

// editSetEdits are the set op's edits: the region, the omit flag and the grant, each left out when the policy
// already holds it.
func editSetEdits(m derpMap, doc map[string]any, region map[string]any, grant json.RawMessage, in RegionEditsInput) ([]policy.Edit, error) {
	key := strconv.Itoa(in.RegionID)
	var edits []policy.Edit
	add := func(op, path string, value any) error {
		e, err := editOp(op, path, value)
		if err == nil {
			edits = append(edits, e)
		}
		return err
	}
	dm, _ := doc["derpMap"].(map[string]any)
	regions, hasRegions := dm["Regions"].(map[string]any)
	switch {
	case !m.Present:
		value := map[string]any{"Regions": map[string]any{key: region}}
		if in.OmitDefaultRegions {
			value["OmitDefaultRegions"] = true
		}
		if err := add("set", "/derpMap", value); err != nil {
			return nil, err
		}
	case !hasRegions:
		if err := add("set", "/derpMap/Regions", map[string]any{key: region}); err != nil {
			return nil, err
		}
	default:
		same, err := editEqual(regions[key], region)
		if err != nil {
			return nil, err
		}
		if !same {
			if err := add("set", "/derpMap/Regions/"+key, region); err != nil {
				return nil, err
			}
		}
	}
	if m.Present && in.OmitDefaultRegions && !m.OmitDefaultRegions {
		if err := add("set", "/derpMap/OmitDefaultRegions", true); err != nil {
			return nil, err
		}
	}
	switch items := doc["grants"].(type) {
	case nil:
		if err := add("set", "/grants", []json.RawMessage{grant}); err != nil {
			return nil, err
		}
	case []any:
		present := false
		for _, item := range items {
			same, err := editEqual(item, grant)
			if err != nil {
				return nil, err
			}
			present = present || same
		}
		if !present {
			edits = append(edits, policy.Edit{Op: "addItem", Path: "/grants", Value: grant})
		}
	default:
		return nil, errors.New("derp: the policy's grants member is not a list")
	}
	return edits, nil
}

// editRemove computes the edits that withdraw region in.RegionID; the last custom region also takes the omit flag
// and its own server's visibility grant with it.
func (d dp) editRemove(ctx context.Context, env core.Env, in RegionEditsInput) (RegionEdits, error) {
	m, doc, err := d.editPolicy(ctx, env)
	if err != nil {
		return RegionEdits{}, err
	}
	region, ok := m.Regions[in.RegionID]
	if !ok {
		return RegionEdits{}, nil
	}
	edits := []policy.Edit{{Op: "delete", Path: "/derpMap/Regions/" + strconv.Itoa(in.RegionID)}}
	others := slices.DeleteFunc(m.customRegions(), func(id int) bool { return id == in.RegionID })
	if len(others) > 0 {
		return RegionEdits{Edits: edits}, nil
	}
	if m.OmitDefaultRegions {
		e, err := editOp("set", "/derpMap/OmitDefaultRegions", false)
		if err != nil {
			return RegionEdits{}, err
		}
		edits = append(edits, e)
	}
	items, _ := doc["grants"].([]any)
	if len(items) == 0 {
		return RegionEdits{Edits: edits}, nil
	}
	tag, err := d.editRemovedTag(ctx, env, region)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return RegionEdits{}, cerr
		}
		// An uncoded error's text may carry provider internals, so only a coded refusal's detail is shown.
		why := "its tag could not be read"
		if coded := errcode.From(err); coded.Code != errcode.Internal && coded.Detail != "" {
			why = coded.Detail
		}
		return RegionEdits{Edits: edits, Warnings: []string{"the visibility grant of region " + strconv.Itoa(in.RegionID) +
			"'s server stays in the policy: " + why}}, nil
	}
	grant := visibilityGrant(tag)
	for _, item := range items {
		same, err := editEqual(item, grant)
		if err != nil {
			return RegionEdits{}, err
		}
		if same {
			edits = append(edits, policy.Edit{Op: "removeItem", Path: "/grants", Value: grant})
			break
		}
	}
	return RegionEdits{Edits: edits}, nil
}

// editRemovedTag is the tag of the visibility grant the set op added for region r's server: the server whose record
// carries the host name of one of r's nodes, and the tag its status reports, checked as the set op checks it. A grant
// of the same shape for another tag may be the operator's, so only this one is removed; when the server cannot be
// found or read, none is.
func (d dp) editRemovedTag(ctx context.Context, env core.Env, r derpRegion) (string, error) {
	records, err := loadServerRecords(ctx, env.DB.Read())
	if err != nil {
		return "", err
	}
	var names []string
	for _, n := range r.Nodes {
		names = append(names, n.HostName)
		i := slices.IndexFunc(records, func(rec serverRecord) bool { return strings.EqualFold(rec.HostName, n.HostName) })
		if i < 0 {
			continue
		}
		status, err := readStatus(ctx, env, records[i].HostID, nil)
		if err != nil {
			return "", editNotReady("the DERP status of host " + records[i].HostID + " cannot be read")
		}
		return d.editServerTag(ctx, env, status)
	}
	return "", editNotReady("no DERP server known to the controller serves " + strings.Join(names, ", "))
}

// editRotate computes the edit that puts the pin in.CertName on the server's nodes of region in.RegionID, keeping
// every other part of the region as the policy holds it.
func (d dp) editRotate(ctx context.Context, env core.Env, in RegionEditsInput) (RegionEdits, error) {
	rec, ok, err := loadServerRecord(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return RegionEdits{}, err
	}
	if !ok {
		return RegionEdits{}, editNotReady("no DERP server is known on " + in.HostID)
	}
	m, doc, err := d.editPolicy(ctx, env)
	if err != nil {
		return RegionEdits{}, err
	}
	// This edit is the only way the new pin reaches the policy, so finding nothing to edit refuses: the rotation's
	// unwind then puts the old certificate back, which the policy still pins.
	if _, ok := m.Regions[in.RegionID]; !ok {
		return RegionEdits{}, editNotReady(fmt.Sprintf("the tailnet policy has no region %d to publish the new pin of %s in",
			in.RegionID, rec.HostName))
	}
	key := strconv.Itoa(in.RegionID)
	dm, _ := doc["derpMap"].(map[string]any)
	regions, _ := dm["Regions"].(map[string]any)
	region, _ := regions[key].(map[string]any)
	nodes, _ := region["Nodes"].([]any)
	found, changed := false, false
	for _, n := range nodes {
		node, _ := n.(map[string]any)
		host, _ := node["HostName"].(string)
		if node == nil || !strings.EqualFold(host, rec.HostName) {
			continue
		}
		found = true
		if cur, _ := node["CertName"].(string); cur != in.CertName {
			node["CertName"] = in.CertName
			changed = true
		}
	}
	if !found {
		return RegionEdits{}, editNotReady(fmt.Sprintf("region %d holds no node for %s to publish the new pin in", in.RegionID, rec.HostName))
	}
	if !changed {
		return RegionEdits{}, nil
	}
	e, err := editOp("set", "/derpMap/Regions/"+key, region)
	if err != nil {
		return RegionEdits{}, err
	}
	return RegionEdits{Edits: []policy.Edit{e}}, nil
}

// editPolicy reads the tailnet policy: its derpMap, and the whole document decoded with every number kept as written.
func (d dp) editPolicy(ctx context.Context, env core.Env) (derpMap, map[string]any, error) {
	p, err := d.d.policy(ctx, env)
	if err != nil {
		return derpMap{}, nil, err
	}
	m, err := parseDERPMap(p.Text)
	if err != nil {
		return derpMap{}, nil, err
	}
	canonical, err := policy.Canonical([]byte(p.Text))
	if err != nil {
		return derpMap{}, nil, fmt.Errorf("derp: read the policy: %w", err)
	}
	v, err := editDecode(canonical)
	if err != nil {
		return derpMap{}, nil, fmt.Errorf("derp: read the policy: %w", err)
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return derpMap{}, nil, errors.New("derp: the policy is not an object")
	}
	return m, doc, nil
}

// editDecode decodes JSON to any, keeping numbers as json.Number so a region rewritten from the policy keeps them.
func editDecode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// editEqual reports whether the decoded policy value cur equals value as JSON.
func editEqual(cur any, value any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("derp: encode a policy value: %w", err)
	}
	v, err := editDecode(raw)
	if err != nil {
		return false, fmt.Errorf("derp: encode a policy value: %w", err)
	}
	return reflect.DeepEqual(cur, v), nil
}

// editOp is one policy edit carrying value as JSON.
func editOp(op, path string, value any) (policy.Edit, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return policy.Edit{}, fmt.Errorf("derp: encode a policy value: %w", err)
	}
	return policy.Edit{Op: op, Path: path, Value: raw}, nil
}
