package system

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// autoUpgradesFile sets the two periodic apt values unattended-upgrades
// needs; it sorts after the distribution's 20auto-upgrades, so it wins.
const autoUpgradesFile = "/etc/apt/apt.conf.d/52darkpanel-auto-upgrades"

// AutoupdatesInput is the input of system.autoupdates.set.
type AutoupdatesInput struct {
	Enabled bool `json:"enabled" jsonschema_description:"Turn automatic security updates on or off."`
}

func (m module) autoupdatesSetAction() core.ActionDef {
	return core.DefineTask[AutoupdatesInput, AutoUpdates](core.ActionSpec{
		Name: ID + ".autoupdates.set", Version: 1,
		Summary:      "Turn unattended-upgrades' automatic updates on or off.",
		Class:        core.ClassCompensable,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 2 * time.Minute,
	}, autoupdatesHandler{m: m})
}

type autoupdatesHandler struct{ m module }

func autoUpgradesContent(on bool) []byte {
	v := "0"
	if on {
		v = "1"
	}
	return fmt.Appendf(nil, "APT::Periodic::Update-Package-Lists %q;\nAPT::Periodic::Unattended-Upgrade %q;\n", v, v)
}

// requireUnattended refuses when unattended-upgrades is not installed.
func requireUnattended(ctx context.Context, env core.Env) error {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return err
	}
	if _, ok := versions["unattended-upgrades"]; !ok {
		return errcode.New(errcode.SystemUnattendedAbsent, "Install unattended-upgrades first.")
	}
	return nil
}

// fileState is the content of autoUpgradesFile as a JSON string, or null when
// the file does not exist.
func (m module) fileState() (core.StateDoc, error) {
	b, err := m.d.readFile(autoUpgradesFile)
	if errors.Is(err, fs.ErrNotExist) {
		return core.StateDoc{State: json.RawMessage("null")}, nil
	}
	if err != nil {
		return core.StateDoc{}, fmt.Errorf("system: read %s: %w", autoUpgradesFile, err)
	}
	return contentState(b), nil
}

func contentState(b []byte) core.StateDoc {
	s, _ := json.Marshal(string(b))
	return core.StateDoc{State: s}
}

func (h autoupdatesHandler) Plan(ctx context.Context, env core.Env, in AutoupdatesInput) (core.PlanOutcome, error) {
	if err := requireUnattended(ctx, env); err != nil {
		return core.PlanOutcome{}, err
	}
	cur, err := h.m.fileState()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256(cur.State)
	out := core.PlanOutcome{StateHash: hex.EncodeToString(sum[:])}
	if bytes.Equal(cur.State, contentState(autoUpgradesContent(in.Enabled)).State) {
		return out, nil
	}
	word := "off"
	if in.Enabled {
		word = "on"
	}
	out.Diff = []core.DiffLine{{Op: "+", Text: "Turn automatic updates " + word + " (" + autoUpgradesFile + ")"}}
	return out, nil
}

func (h autoupdatesHandler) Task(ctx context.Context, env core.Env, in AutoupdatesInput) (AutoUpdates, error) {
	if err := requireUnattended(ctx, env); err != nil {
		return AutoUpdates{}, core.Definite(err)
	}
	before, err := h.m.fileState()
	if err != nil {
		return AutoUpdates{}, core.Definite(err)
	}
	data := autoUpgradesContent(in.Enabled)
	after := contentState(data)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: ID + ".autoupdates:" + autoUpgradesFile, Local: true, InverseLocal: true,
		Before: before, After: &after,
	}, nil)
	if err != nil {
		return AutoUpdates{}, err
	}
	if err := env.Files.WriteFile(ctx, ID, autoUpgradesFile, data, 0o644); err != nil {
		return AutoUpdates{}, err
	}
	if err := env.Effects.Made(ctx, seq, ID+".autoupdates:"+autoUpgradesFile, after); err != nil {
		return AutoUpdates{}, err
	}
	return AutoUpdates{Installed: true, Enabled: in.Enabled}, nil
}

// Verify checks the file holds the requested values.
func (h autoupdatesHandler) Verify(_ context.Context, _ core.Env, in AutoupdatesInput, _ AutoUpdates) error {
	cur, err := h.m.fileState()
	if err != nil {
		return err
	}
	if !bytes.Equal(cur.State, contentState(autoUpgradesContent(in.Enabled)).State) {
		return fmt.Errorf("system: %s does not hold the requested values", autoUpgradesFile)
	}
	return nil
}

func (h autoupdatesHandler) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	cur, err := h.m.fileState()
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: cur, Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Inverse puts back the file the change found: its bytes, or no file.
func (h autoupdatesHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	var prior *string
	if err := json.Unmarshal(e.Before.State, &prior); err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("system: read the effect's prior state: %w", err))
	}
	if prior == nil {
		return core.InverseResult{}, env.Files.Remove(ctx, ID, autoUpgradesFile)
	}
	return core.InverseResult{}, env.Files.WriteFile(ctx, ID, autoUpgradesFile, []byte(*prior), 0o644)
}
