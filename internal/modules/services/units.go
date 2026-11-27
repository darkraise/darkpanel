package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// listTypes are the unit types services.unit.list shows.
var listTypes = []string{"service", "socket", "timer", "mount", "path", "target"}

// activeStates are the values the list's state filter takes.
var activeStates = []string{"active", "reloading", "inactive", "failed", "activating", "deactivating", "maintenance"}

// unitName is a systemd unit name with its type suffix.
var unitName = regexp.MustCompile(`^[A-Za-z0-9:_.\\@-]{1,240}\.(service|socket|timer|mount|path|target|swap|slice|scope|device|automount)$`)

// ListInput filters services.unit.list.
type ListInput struct {
	Type  string `json:"type,omitempty"`  // one of service, socket, timer, mount, path, target
	State string `json:"state,omitempty"` // an active state, such as failed
}

// Unit is one unit of the list.
type Unit struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Enabled     string `json:"enabled"` // the unit file state; "" without a unit file
	MainPID     uint32 `json:"mainPid,omitempty"`
}

// UnitList is the answer of services.unit.list.
type UnitList struct {
	Units []Unit `json:"units"`
}

// GetInput names the unit of services.unit.get.
type GetInput struct {
	Unit string `json:"unit"`
}

// UnitDetail is the answer of services.unit.get.
type UnitDetail struct {
	Unit
	MemoryCurrent *uint64    `json:"memoryCurrent,omitempty" jsonschema:"nullable"` // bytes
	Tasks         *uint64    `json:"tasks,omitempty" jsonschema:"nullable"`
	Restarts      uint32     `json:"restarts"`
	StartedAt     *time.Time `json:"startedAt,omitempty" jsonschema:"nullable"`
	UnitFile      string     `json:"unitFile,omitempty"`
	UnitFileText  string     `json:"unitFileText,omitempty" jsonschema_description:"The unit file text, at most 64 KiB; empty unless the caller has the admin scope, since a unit can hold secrets."`
	Truncated     bool       `json:"truncated,omitempty" jsonschema_description:"Whether the unit file text was cut at 64 KiB."`
	DropIns       []string   `json:"dropIns"`
}

func (m module) reads() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[ListInput, UnitList](core.ActionSpec{
			Name: ID + ".unit.list", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "List the services, sockets, timers, mounts, paths and targets of the host with their state.",
		}, listHandler{}),
		core.DefineRead[GetInput, UnitDetail](core.ActionSpec{
			Name: ID + ".unit.get", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "Show one unit: its state, memory, tasks, restarts, start time, unit file and drop-ins.",
		}, getHandler{}),
	}
}

// unitAdmin is env's unit client, which the agent gives the services
// module unscoped to the units it may read and drive (ADR 0020).
func unitAdmin(env core.Env) (core.UnitAdmin, error) {
	u, ok := env.Units.(core.UnitAdmin)
	if !ok {
		return nil, errors.New("services: the unit client cannot read every unit")
	}
	return u, nil
}

func invalid(path, msg string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: path, Message: msg}}})
}

// checkUnitName refuses a name that is not a unit name with a type.
func checkUnitName(name string) error {
	if !unitName.MatchString(name) {
		return invalid("/unit", fmt.Sprintf("%q is not a unit name such as nginx.service", name))
	}
	return nil
}

func unitType(name string) string { return name[strings.LastIndexByte(name, '.')+1:] }

func listUnit(u core.UnitInfo) Unit {
	return Unit{Name: u.ID, Type: unitType(u.ID), Description: u.Description, Load: u.LoadState,
		Active: u.ActiveState, Sub: u.SubState, Enabled: u.UnitFileState, MainPID: u.MainPID}
}

type listHandler struct{}

func (listHandler) Read(ctx context.Context, env core.Env, in ListInput) (UnitList, error) {
	if in.Type != "" && !slices.Contains(listTypes, in.Type) {
		return UnitList{}, invalid("/type", "type is one of "+strings.Join(listTypes, ", "))
	}
	if in.State != "" && !slices.Contains(activeStates, in.State) {
		return UnitList{}, invalid("/state", "state is one of "+strings.Join(activeStates, ", "))
	}
	admin, err := unitAdmin(env)
	if err != nil {
		return UnitList{}, err
	}
	all, err := admin.ListAll(ctx)
	if err != nil {
		return UnitList{}, err
	}
	out := UnitList{Units: []Unit{}}
	for _, u := range all {
		t := unitType(u.ID)
		if !slices.Contains(listTypes, t) || (in.Type != "" && t != in.Type) || (in.State != "" && u.ActiveState != in.State) {
			continue
		}
		out.Units = append(out.Units, listUnit(u))
	}
	return out, nil
}

type getHandler struct{}

func (getHandler) Read(ctx context.Context, env core.Env, in GetInput) (UnitDetail, error) {
	if err := checkUnitName(in.Unit); err != nil {
		return UnitDetail{}, err
	}
	u, err := inspect(ctx, env, in.Unit)
	if err != nil {
		return UnitDetail{}, err
	}
	d := UnitDetail{Unit: listUnit(u), MemoryCurrent: u.MemoryCurrent, Tasks: u.TasksCurrent, Restarts: u.Restarts,
		UnitFile: u.FragmentPath, DropIns: u.DropInPaths}
	if core.RequireScope(env, core.ScopeAdmin) == nil {
		d.UnitFileText, d.Truncated = u.FileText, u.FileTruncated
	}
	if d.DropIns == nil {
		d.DropIns = []string{}
	}
	if !u.StartedAt.IsZero() {
		t := u.StartedAt
		d.StartedAt = &t
	}
	return d, nil
}

// inspect reads unit and refuses one systemd does not know with not_found.
func inspect(ctx context.Context, env core.Env, unit string) (core.UnitInfo, error) {
	admin, err := unitAdmin(env)
	if err != nil {
		return core.UnitInfo{}, err
	}
	u, err := admin.Inspect(ctx, unit)
	if err != nil {
		return core.UnitInfo{}, err
	}
	if u.LoadState == "not-found" {
		return core.UnitInfo{}, core.Definite(errcode.New(errcode.NotFound, "no unit "+unit))
	}
	return u, nil
}
