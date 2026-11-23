package coreactions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// groupNamePattern is a host group's name (M10 spec section 1.1).
var groupNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,47}$`)

// GroupQuerier is the transaction or reader pool a GroupReferrer reads
// through.
type GroupQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GroupReferrer names each thing that stores host group groupID, such as
// "grant of mia (viewer)" or "channel ops", so core.group.delete can refuse
// with group.in_use while any remains. A module that stores group ids
// registers one with RegisterGroupReferrer.
type GroupReferrer func(ctx context.Context, q GroupQuerier, groupID string) ([]string, error)

var groupReferrers = struct {
	sync.Mutex
	byID map[string]GroupReferrer
}{byID: map[string]GroupReferrer{"core.grants": grantReferrer}}

// RegisterGroupReferrer adds r under id, replacing any referrer with that id,
// and returns a function that removes it.
func RegisterGroupReferrer(id string, r GroupReferrer) (remove func()) {
	groupReferrers.Lock()
	defer groupReferrers.Unlock()
	groupReferrers.byID[id] = r
	return func() {
		groupReferrers.Lock()
		defer groupReferrers.Unlock()
		delete(groupReferrers.byID, id)
	}
}

// groupReferences returns what every registered referrer names for
// groupID, in referrer id order.
func groupReferences(ctx context.Context, q GroupQuerier, groupID string) ([]string, error) {
	groupReferrers.Lock()
	ids := make([]string, 0, len(groupReferrers.byID))
	for id := range groupReferrers.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	refs := make([]GroupReferrer, len(ids))
	for i, id := range ids {
		refs[i] = groupReferrers.byID[id]
	}
	groupReferrers.Unlock()
	var out []string
	for i, r := range refs {
		names, err := r(ctx, q, groupID)
		if err != nil {
			return nil, fmt.Errorf("coreactions: group referrer %s: %w", ids[i], err)
		}
		out = append(out, names...)
	}
	return out, nil
}

// grantReferrer names each member grant that targets groupID.
func grantReferrer(ctx context.Context, q GroupQuerier, groupID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT u.username, g.role FROM core_user_grants g
		JOIN core_users u ON u.id = g.user_id WHERE g.target = ? ORDER BY u.username`, groupID)
	if err != nil {
		return nil, fmt.Errorf("read grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var user, role string
		if err := rows.Scan(&user, &role); err != nil {
			return nil, fmt.Errorf("read grants: %w", err)
		}
		out = append(out, fmt.Sprintf("grant of %s (%s)", user, role))
	}
	return out, rows.Err()
}

// Group is a host group with its member hosts.
type Group struct {
	ID         string    `json:"id" jsonschema_description:"The group's id, which grants and notification routes name."`
	Name       string    `json:"name" jsonschema_description:"The group's name, unique without regard to case."`
	CreatedAt  time.Time `json:"createdAt"`
	HostIDs    []string  `json:"hostIds" jsonschema_description:"The ids of the group's hosts, sorted."`
	HostCount  int       `json:"hostCount" jsonschema_description:"How many hosts the group holds."`
	GrantCount int       `json:"grantCount" jsonschema_description:"How many member grants target the group."`
}

// GroupList is the output of core.group.list/v1.
type GroupList struct {
	Groups []Group `json:"groups" jsonschema_description:"Every host group, sorted by name."`
}

// GroupCreateInput is the input of core.group.create/v1.
type GroupCreateInput struct {
	Name string `json:"name" jsonschema:"pattern=^[A-Za-z0-9][A-Za-z0-9 ._-]{0\\,47}$" jsonschema_description:"The name: 1 to 48 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit."`
}

// GroupRenameInput is the input of core.group.rename/v1.
type GroupRenameInput struct {
	ID   string `json:"id" jsonschema:"minLength=1" jsonschema_description:"The group's id."`
	Name string `json:"name" jsonschema:"pattern=^[A-Za-z0-9][A-Za-z0-9 ._-]{0\\,47}$" jsonschema_description:"The new name, as core.group.create takes it."`
}

// GroupMembersInput is the input of core.group.set-members/v1.
type GroupMembersInput struct {
	ID      string   `json:"id" jsonschema:"minLength=1" jsonschema_description:"The group's id."`
	HostIDs []string `json:"hostIds" jsonschema_description:"The ids of the hosts the group holds, replacing its members; each names a host once."`
}

// GroupIDInput is the input of core.group.delete/v1.
type GroupIDInput struct {
	ID string `json:"id" jsonschema:"minLength=1" jsonschema_description:"The group's id."`
}

func (m module) groupActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[GroupCreateInput, Group](core.ActionSpec{
			Name: "core.group.create", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.group.delete",
			Summary: "Create a host group with no hosts.",
		}, groupCreate{}),
		core.DefineMutating[GroupRenameInput, Group](core.ActionSpec{
			Name: "core.group.rename", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.group.rename",
			Summary: "Rename a host group.",
		}, groupRename{}),
		core.DefineMutating[GroupMembersInput, Group](core.ActionSpec{
			Name: "core.group.set-members", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.group.set-members",
			Summary: "Replace the hosts of a host group.",
		}, groupSetMembers{}),
		core.DefineMutating[GroupIDInput, Group](core.ActionSpec{
			Name: "core.group.delete", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			MinScope: core.ScopeAdmin,
			Summary:  "Delete a host group, refused while a grant, alert rule or notification route names it; returns the removed group; the confirmation phrase is its name.",
		}, groupDelete{}),
		core.DefineRead[NoInput, GroupList](core.ActionSpec{
			Name: "core.group.list", Version: 1, Side: core.SideController, HostData: core.HostDataFleet,
			Summary: "List the host groups with their hosts and how many grants target each.",
		}, groupListRead{}),
	}
}

func errGroupNotFound(id string) error {
	return errcode.New(errcode.NotFound, "no host group "+id)
}

// loadGroups reads every group, or only id when it is not "".
func loadGroups(ctx context.Context, q GroupQuerier, id string) ([]Group, error) {
	query := `SELECT g.id, g.name, g.created_at,
		(SELECT count(*) FROM core_user_grants u WHERE u.target = g.id) FROM core_host_groups g`
	args := []any{}
	if id != "" {
		query += ` WHERE g.id = ?`
		args = append(args, id)
	}
	rows, err := q.QueryContext(ctx, query+` ORDER BY g.name COLLATE NOCASE, g.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("coreactions: read groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	groups := []Group{}
	at := map[string]int{}
	for rows.Next() {
		var g Group
		var created string
		if err := rows.Scan(&g.ID, &g.Name, &created, &g.GrantCount); err != nil {
			return nil, fmt.Errorf("coreactions: read groups: %w", err)
		}
		if g.CreatedAt, err = time.Parse(timeFormat, created); err != nil {
			return nil, fmt.Errorf("coreactions: group %s created_at %q: %w", g.ID, created, err)
		}
		g.HostIDs = []string{}
		at[g.ID] = len(groups)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coreactions: read groups: %w", err)
	}
	_ = rows.Close()
	members, err := q.QueryContext(ctx, `SELECT group_id, host_id FROM core_host_group_members ORDER BY group_id, host_id`)
	if err != nil {
		return nil, fmt.Errorf("coreactions: read group members: %w", err)
	}
	defer func() { _ = members.Close() }()
	for members.Next() {
		var gid, host string
		if err := members.Scan(&gid, &host); err != nil {
			return nil, fmt.Errorf("coreactions: read group members: %w", err)
		}
		if i, ok := at[gid]; ok {
			groups[i].HostIDs = append(groups[i].HostIDs, host)
			groups[i].HostCount++
		}
	}
	if err := members.Err(); err != nil {
		return nil, fmt.Errorf("coreactions: read group members: %w", err)
	}
	return groups, nil
}

func loadGroup(ctx context.Context, q GroupQuerier, id string) (Group, error) {
	groups, err := loadGroups(ctx, q, id)
	if err != nil {
		return Group{}, err
	}
	if len(groups) == 0 {
		return Group{}, errGroupNotFound(id)
	}
	return groups[0], nil
}

// checkGroupName refuses a malformed name, or one another group than id
// already has in any case.
func checkGroupName(ctx context.Context, q GroupQuerier, id, name string) error {
	if !groupNamePattern.MatchString(name) {
		return fieldRefusal("/name", "must be 1 to 48 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit")
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_host_groups WHERE name = ? COLLATE NOCASE AND id <> ?`,
		name, id).Scan(&n); err != nil {
		return fmt.Errorf("coreactions: check group name: %w", err)
	}
	if n > 0 {
		return fieldRefusal("/name", "another host group already has this name")
	}
	return nil
}

func wantGroupName(ctx context.Context, env core.Env, id, name string) (Group, error) {
	g, err := loadGroup(ctx, env.DB.Read(), id)
	if err != nil {
		return Group{}, err
	}
	if g.Name != name {
		return Group{}, fmt.Errorf("coreactions: group %s is named %q, want %q", id, g.Name, name)
	}
	return g, nil
}

type groupCreate struct{}

func (groupCreate) Plan(ctx context.Context, env core.Env, in GroupCreateInput) (core.PlanOutcome, error) {
	if err := checkGroupName(ctx, env.DB.Read(), "", in.Name); err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("host group %s, with no hosts", in.Name)},
			{Op: " ", Text: "compensated by core.group.delete"},
		},
		Guards: []core.Guard{}, StateHash: stateHash(in.Name),
	}, nil
}

func (groupCreate) Apply(ctx context.Context, env core.Env, in GroupCreateInput) (Group, error) {
	now := env.Clock.Now()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return Group{}, fmt.Errorf("coreactions: group id: %w", err)
	}
	var g Group
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := checkGroupName(ctx, tx, "", in.Name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_host_groups (id, name, created_at) VALUES (?, ?, ?)`,
			id.String(), in.Name, formatTime(now)); err != nil {
			return fmt.Errorf("coreactions: create group: %w", err)
		}
		g, err = loadGroup(ctx, tx, id.String())
		return err
	})
	return g, err
}

// Verify finds the group by name, the only key the input holds.
func (groupCreate) Verify(ctx context.Context, env core.Env, in GroupCreateInput, _ Group) error {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_host_groups WHERE name = ?`, in.Name).Scan(&n); err != nil {
		return fmt.Errorf("coreactions: verify group: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("coreactions: no host group is named %q", in.Name)
	}
	return nil
}

type groupRename struct{}

func (groupRename) Plan(ctx context.Context, env core.Env, in GroupRenameInput) (core.PlanOutcome, error) {
	g, err := loadGroup(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if err := checkGroupName(ctx, env.DB.Read(), g.ID, in.Name); err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: " ", Text: fmt.Sprintf("host group %s keeps its name; nothing changes", g.Name)}}
	if g.Name != in.Name {
		diff = []core.DiffLine{
			{Op: "-", Text: "host group " + g.Name},
			{Op: "+", Text: "host group " + in.Name},
			{Op: " ", Text: "compensated by core.group.rename with name " + g.Name},
		}
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(g.ID, g.Name)}, nil
}

func (groupRename) Apply(ctx context.Context, env core.Env, in GroupRenameInput) (Group, error) {
	var g Group
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := loadGroup(ctx, tx, in.ID); err != nil {
			return err
		}
		if err := checkGroupName(ctx, tx, in.ID, in.Name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_host_groups SET name = ? WHERE id = ?`, in.Name, in.ID); err != nil {
			return fmt.Errorf("coreactions: rename group: %w", err)
		}
		var err error
		g, err = loadGroup(ctx, tx, in.ID)
		return err
	})
	return g, err
}

func (groupRename) Verify(ctx context.Context, env core.Env, in GroupRenameInput, _ Group) error {
	_, err := wantGroupName(ctx, env, in.ID, in.Name)
	return err
}

type groupSetMembers struct{}

// hostNames checks that hosts name existing hosts, each once, and returns
// their names.
func hostNames(ctx context.Context, q GroupQuerier, hosts []string) (map[string]string, error) {
	names := map[string]string{}
	for i, h := range hosts {
		at := fmt.Sprintf("/hostIds/%d", i)
		if _, dup := names[h]; dup {
			return nil, fieldRefusal(at, "names a host another entry names")
		}
		var name string
		err := q.QueryRowContext(ctx, `SELECT name FROM core_hosts WHERE id = ?`, h).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fieldRefusal(at, "is not a host")
		}
		if err != nil {
			return nil, fmt.Errorf("coreactions: read hosts: %w", err)
		}
		names[h] = name
	}
	return names, nil
}

func (groupSetMembers) Plan(ctx context.Context, env core.Env, in GroupMembersInput) (core.PlanOutcome, error) {
	g, err := loadGroup(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	names, err := hostNames(ctx, env.DB.Read(), in.HostIDs)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	oldNames, err := hostNames(ctx, env.DB.Read(), g.HostIDs)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	for _, h := range g.HostIDs {
		if _, kept := names[h]; !kept {
			diff = append(diff, core.DiffLine{Op: "-", Text: fmt.Sprintf("host %s (%s) in group %s", oldNames[h], h, g.Name)})
		}
	}
	for _, h := range sortedCopy(in.HostIDs) {
		if !slices.Contains(g.HostIDs, h) {
			diff = append(diff, core.DiffLine{Op: "+", Text: fmt.Sprintf("host %s (%s) in group %s", names[h], h, g.Name)})
		}
	}
	if len(diff) == 0 {
		diff = []core.DiffLine{{Op: " ", Text: fmt.Sprintf("host group %s keeps its hosts; nothing changes", g.Name)}}
	} else {
		diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.group.set-members with the previous hosts"})
	}
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash: stateHash(append([]string{g.ID, g.Name}, g.HostIDs...)...),
	}, nil
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	sort.Strings(out)
	return out
}

func (groupSetMembers) Apply(ctx context.Context, env core.Env, in GroupMembersInput) (Group, error) {
	var g Group
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := loadGroup(ctx, tx, in.ID); err != nil {
			return err
		}
		if _, err := hostNames(ctx, tx, in.HostIDs); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_host_group_members WHERE group_id = ?`, in.ID); err != nil {
			return fmt.Errorf("coreactions: set group members: %w", err)
		}
		for _, h := range in.HostIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO core_host_group_members (group_id, host_id) VALUES (?, ?)`,
				in.ID, h); err != nil {
				return fmt.Errorf("coreactions: set group members: %w", err)
			}
		}
		var err error
		g, err = loadGroup(ctx, tx, in.ID)
		return err
	})
	return g, err
}

func (groupSetMembers) Verify(ctx context.Context, env core.Env, in GroupMembersInput, _ Group) error {
	g, err := loadGroup(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return err
	}
	if want := sortedCopy(in.HostIDs); !slices.Equal(g.HostIDs, want) {
		return fmt.Errorf("coreactions: group %s holds %v, want %v", in.ID, g.HostIDs, want)
	}
	return nil
}

type groupDelete struct{}

// inUse returns group.in_use naming every reference to g, or nil.
func inUse(ctx context.Context, q GroupQuerier, g Group) error {
	refs, err := groupReferences(ctx, q, g.ID)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	return errcode.New(errcode.GroupInUse, fmt.Sprintf("host group %s is in use: %s", g.Name, strings.Join(refs, ", ")))
}

func (groupDelete) Plan(ctx context.Context, env core.Env, in GroupIDInput) (core.PlanOutcome, error) {
	g, err := loadGroup(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if err := inUse(ctx, env.DB.Read(), g); err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("host group %s, with its %s; the hosts stay", g.Name, plural(g.HostCount, "host"))},
		},
		Guards:       []core.Guard{},
		StateHash:    stateHash(append([]string{g.ID, g.Name}, g.HostIDs...)...),
		Confirmation: g.Name,
	}, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Apply checks the references again inside its transaction, so a grant or
// rule added since the plan still refuses the delete.
func (groupDelete) Apply(ctx context.Context, env core.Env, in GroupIDInput) (Group, error) {
	var g Group
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if g, err = loadGroup(ctx, tx, in.ID); err != nil {
			return err
		}
		if err := inUse(ctx, tx, g); err != nil {
			return core.Definite(err)
		}
		for _, q := range []string{
			`DELETE FROM core_host_group_members WHERE group_id = ?`,
			`DELETE FROM core_host_groups WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, in.ID); err != nil {
				return fmt.Errorf("coreactions: delete group: %w", err)
			}
		}
		return nil
	})
	return g, err
}

func (groupDelete) Verify(ctx context.Context, env core.Env, in GroupIDInput, _ Group) error {
	_, err := loadGroup(ctx, env.DB.Read(), in.ID)
	switch {
	case errcode.From(err).Code == errcode.NotFound:
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("coreactions: host group %s still exists", in.ID)
}

type groupListRead struct{}

func (groupListRead) Read(ctx context.Context, env core.Env, _ NoInput) (GroupList, error) {
	groups, err := loadGroups(ctx, env.DB.Read(), "")
	if err != nil {
		return GroupList{}, err
	}
	return GroupList{Groups: groups}, nil
}
