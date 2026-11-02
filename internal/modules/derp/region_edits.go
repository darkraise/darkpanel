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
	if err := editDeployConfirmed(ctx, env, in.HostID); err != nil {
		return RegionEdits{}, err
	}
	probe, err := d.probeServer(ctx, env, ServerProbeInput{HostID: in.HostID})
	if err != nil {
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
	if len(probe.Server.Tags) == 0 {
		return RegionEdits{}, editNotReady("the DERP host's node has no tag; the visibility grant needs one")
	}
	edits, err := editSetEdits(m, doc, editRegion(probe.Server, in), visibilityGrant(slices.Min(probe.Server.Tags)), in)
	if err != nil {
		return RegionEdits{}, err
	}
	return RegionEdits{Edits: edits, Warnings: warnings}, nil
}

// editDeployConfirmed refuses unless the newest derp.server.deploy change of hostID is completed.
func editDeployConfirmed(ctx context.Context, env core.Env, hostID string) error {
	rows, err := env.DB.Read().QueryContext(ctx,
		`SELECT state, input_json FROM core_changes WHERE action = ? ORDER BY created_at DESC, id DESC`, ID+".server.deploy")
	if err != nil {
		return fmt.Errorf("derp: read the deploy changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	state := "no deploy found"
	for rows.Next() {
		var st, input string
		if err := rows.Scan(&st, &input); err != nil {
			return fmt.Errorf("derp: read the deploy changes: %w", err)
		}
		var deploy DeployInput
		if json.Unmarshal([]byte(input), &deploy) == nil && deploy.HostID == hostID {
			state = st
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("derp: read the deploy changes: %w", err)
	}
	if state != core.StateCompleted {
		return editNotReady("the deploy of the DERP server on " + hostID + " is not confirmed (" + state + ")")
	}
	return nil
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
// and the module's visibility grants with it.
func (d dp) editRemove(ctx context.Context, env core.Env, in RegionEditsInput) (RegionEdits, error) {
	m, doc, err := d.editPolicy(ctx, env)
	if err != nil {
		return RegionEdits{}, err
	}
	if _, ok := m.Regions[in.RegionID]; !ok {
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
	for _, item := range items {
		tag, ok := editGrantTag(item)
		if !ok {
			continue
		}
		grant := visibilityGrant(tag)
		same, err := editEqual(item, grant)
		if err != nil {
			return RegionEdits{}, err
		}
		if same {
			edits = append(edits, policy.Edit{Op: "removeItem", Path: "/grants", Value: grant})
		}
	}
	return RegionEdits{Edits: edits}, nil
}

// editGrantTag is the single string of a grant's dst; ok is false for any other shape.
func editGrantTag(item any) (tag string, ok bool) {
	obj, _ := item.(map[string]any)
	dst, _ := obj["dst"].([]any)
	if len(dst) != 1 {
		return "", false
	}
	tag, ok = dst[0].(string)
	return tag, ok
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
	if _, ok := m.Regions[in.RegionID]; !ok {
		return RegionEdits{}, nil
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
		return RegionEdits{Warnings: []string{fmt.Sprintf("region %d holds no node for %s", in.RegionID, rec.HostName)}}, nil
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
