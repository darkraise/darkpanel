package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// regionSet is derp.region.set: it computes the policy edits that publish a server's region and applies them to
// the tailnet policy file.
func (d dp) regionSet() core.ActionDef {
	return core.DefineComposite[RegionSetInput, RegionChanged](core.ActionSpec{
		Name: ID + ".region.set", Summary: "Publish a DERP server as a region of the tailnet's DERP map.", Version: 1,
		Unwind: core.UnwindReverse,
	}, regionSetComposer{})
}

// regionRemove is derp.region.remove: it computes the policy edits that withdraw a region and applies them to the
// tailnet policy file.
func (d dp) regionRemove() core.ActionDef {
	return core.DefineComposite[RegionRemoveInput, RegionChanged](core.ActionSpec{
		Name: ID + ".region.remove", Summary: "Remove a region from the tailnet's DERP map.", Version: 1,
		Unwind: core.UnwindReverse,
	}, regionRemoveComposer{})
}

// certRotate is derp.cert.rotate: it renews an ip-mode server's certificate and, when the server publishes a region,
// publishes the new pin after checking that the host does not depend on that region alone.
func (d dp) certRotate() core.ActionDef {
	return core.DefineComposite[CertRotateInput, CertRotated](core.ActionSpec{
		Name: ID + ".cert.rotate", Version: 1, Unwind: core.UnwindReverse,
		Summary: "Renew the IP certificate, then publish its pin; between the two steps clients holding the old pin cannot use the region.",
	}, certRotateComposer{})
}

// regionCompositePatch is the input of the region step: the edits step's edits as a whole-string reference, which
// keeps their JSON type, an array.
const regionCompositePatch = `{"edits":"${steps.edits.output.edits}"}`

// regionCompositeRegion is the step that applies the edits step's edits to the tailnet policy file.
func regionCompositeRegion() core.Step {
	return core.Step{
		ID: "region", Action: core.ActionRef{Name: "tailnet.policy.patch", Version: 1},
		Input: json.RawMessage(regionCompositePatch), After: []string{"edits"},
	}
}

// regionCompositeEdits is the derp.region.edits step with input in, after the steps after.
func regionCompositeEdits(in any, after []string) (core.Step, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return core.Step{}, fmt.Errorf("derp: %w", err)
	}
	return core.Step{ID: "edits", Action: core.ActionRef{Name: ID + ".region.edits", Version: 1}, Input: raw, After: after}, nil
}

// regionCompositeOutput is the region step's output: the policy's ETag as the patch reported it.
func regionCompositeOutput(steps map[string]json.RawMessage) (RegionChanged, error) {
	raw, ok := steps["region"]
	if !ok {
		return RegionChanged{}, errors.New("derp: the region change has no output of its region step")
	}
	var out RegionChanged
	if err := json.Unmarshal(raw, &out); err != nil {
		return RegionChanged{}, fmt.Errorf("derp: the region step's output: %w", err)
	}
	return out, nil
}

// regionSetComposer publishes a region in two steps: compute the edits, then patch the policy.
type regionSetComposer struct{}

// Steps returns the edits and region steps for in.
func (regionSetComposer) Steps(_ context.Context, _ core.Env, in RegionSetInput) ([]core.Step, error) {
	// The engine reads every "${" of a template as a reference, so the typed
	// values reach the edits step only as references to the input, and an
	// absent optional member is left out because a reference to it would fail
	// the plan.
	edits := map[string]string{
		"op": "set", "hostId": "${input.hostId}", "regionId": "${input.regionId}",
		"regionCode": "${input.regionCode}", "regionName": "${input.regionName}",
	}
	if in.OmitDefaultRegions {
		edits["omitDefaultRegions"] = "${input.omitDefaultRegions}"
	}
	if in.Acknowledge != "" {
		edits["acknowledge"] = "${input.acknowledge}"
	}
	step, err := regionCompositeEdits(edits, nil)
	if err != nil {
		return nil, err
	}
	return []core.Step{step, regionCompositeRegion()}, nil
}

// Output is the region step's ETag.
func (regionSetComposer) Output(_ context.Context, _ core.Env, _ RegionSetInput, steps map[string]json.RawMessage) (RegionChanged, error) {
	return regionCompositeOutput(steps)
}

// regionRemoveComposer withdraws a region in two steps: compute the edits, then patch the policy.
type regionRemoveComposer struct{}

// Steps returns the edits and region steps for in.
func (regionRemoveComposer) Steps(_ context.Context, _ core.Env, _ RegionRemoveInput) ([]core.Step, error) {
	step, err := regionCompositeEdits(map[string]string{"op": "remove", "regionId": "${input.regionId}"}, nil)
	if err != nil {
		return nil, err
	}
	return []core.Step{step, regionCompositeRegion()}, nil
}

// Output is the region step's ETag.
func (regionRemoveComposer) Output(_ context.Context, _ core.Env, _ RegionRemoveInput, steps map[string]json.RawMessage) (RegionChanged, error) {
	return regionCompositeOutput(steps)
}

// certRotateComposer rotates an ip-mode certificate: check that the host keeps
// a path to the controller without its own region, renew the certificate, and
// publish the new pin in the region. A server without a region only renews.
type certRotateComposer struct{}

// Steps returns the rotation's steps for the server the controller knows on in.HostID.
func (certRotateComposer) Steps(ctx context.Context, env core.Env, in CertRotateInput) ([]core.Step, error) {
	rec, ok, err := loadServerRecord(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errcode.New(errcode.DERPNotReady, "no DERP server is known on "+in.HostID)
	}
	cert := core.Step{ID: "cert", Host: in.HostID, Action: core.ActionRef{Name: ID + ".cert.renew", Version: 1}, Input: json.RawMessage(`{}`)}
	if rec.RegionID == 0 {
		return []core.Step{cert}, nil
	}
	fallback, err := json.Marshal(FallbackCheckInput{HostID: "${input.hostId}", RegionID: rec.RegionID})
	if err != nil {
		return nil, fmt.Errorf("derp: %w", err)
	}
	cert.After = []string{"fallback"}
	edits, err := regionCompositeEdits(RegionEditsInput{
		Op: "rotate", HostID: "${input.hostId}", RegionID: rec.RegionID, CertName: "${steps.cert.output.certName}",
	}, []string{"cert"})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "fallback", Action: core.ActionRef{Name: ID + ".fallback.check", Version: 1}, Input: fallback},
		cert,
		edits,
		regionCompositeRegion(),
	}, nil
}

// Output is the cert step's new pin and expiry.
func (certRotateComposer) Output(_ context.Context, _ core.Env, _ CertRotateInput, steps map[string]json.RawMessage) (CertRotated, error) {
	raw, ok := steps["cert"]
	if !ok {
		return CertRotated{}, errors.New("derp: the rotation has no output of its cert step")
	}
	var out CertRenewed
	if err := json.Unmarshal(raw, &out); err != nil {
		return CertRotated{}, fmt.Errorf("derp: the rotation's cert output: %w", err)
	}
	return CertRotated{CertName: out.CertName, CertNotAfter: out.CertNotAfter}, nil
}
