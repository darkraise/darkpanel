package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// grantScope maps a grant role to the scope it gives.
var grantScope = map[string]core.Scope{GrantViewer: core.ScopeRead, GrantOperator: core.ScopeOperate}

// accessQuerier is a transaction or the reader pool.
type accessQuerier interface {
	querier
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// resolveAccess returns what user may do on each host, read from its grants
// at this request, so a grant change applies to the next request.
// tokenScope is the scope of the bearer token in use, "" for a session; it
// caps every entry, so a token never exceeds its user.
func resolveAccess(ctx context.Context, q accessQuerier, user User, tokenScope core.Scope) (core.Access, error) {
	switch user.Role {
	case RoleAdmin:
		if tokenScope == "" || tokenScope == core.ScopeAdmin {
			return core.Access{Admin: true, Fleet: true}, nil
		}
		return core.Access{Fleet: true, All: tokenScope}, nil
	case RoleMember:
	default:
		return core.Access{}, fmt.Errorf("auth: user %s has unknown role %q", user.ID, user.Role)
	}
	var a core.Access
	rows, err := q.QueryContext(ctx, `SELECT g.role, m.host_id FROM core_user_grants g
		LEFT JOIN core_host_group_members m ON g.target <> '*' AND m.group_id = g.target
		WHERE g.user_id = ? AND (g.target = '*' OR m.host_id IS NOT NULL)`, user.ID)
	if err != nil {
		return core.Access{}, fmt.Errorf("auth: load grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var role string
		var host sql.NullString
		if err := rows.Scan(&role, &host); err != nil {
			return core.Access{}, fmt.Errorf("auth: load grants: %w", err)
		}
		scope := grantScope[role]
		switch {
		case !host.Valid:
			a.All = higher(a.All, scope)
		case a.Hosts == nil:
			a.Hosts = map[string]core.Scope{host.String: scope}
		default:
			a.Hosts[host.String] = higher(a.Hosts[host.String], scope)
		}
	}
	if err := rows.Err(); err != nil {
		return core.Access{}, fmt.Errorf("auth: load grants: %w", err)
	}
	var local string
	err = q.QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'local_host_id'`).Scan(&local)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return core.Access{}, fmt.Errorf("auth: load the local host id: %w", err)
	}
	if local != "" && a.On(local) != "" {
		a.Ceil = map[string]core.Scope{local: core.ScopeRead}
		if _, ok := a.Hosts[local]; ok {
			a.Hosts[local] = core.ScopeRead
		}
	}
	if tokenScope != "" {
		a.All = lower(a.All, tokenScope)
		for h, s := range a.Hosts {
			a.Hosts[h] = lower(s, tokenScope)
		}
		for h, s := range a.Ceil {
			a.Ceil[h] = lower(s, tokenScope)
		}
	}
	return a, nil
}

// higher returns the higher of two scopes; "" is below every scope.
func higher(a, b core.Scope) core.Scope {
	if b == "" || a.Allows(b) {
		return a
	}
	return b
}

// lower returns the lower of two scopes; "" is below every scope.
func lower(a, b core.Scope) core.Scope {
	if a == "" || b.Allows(a) {
		return a
	}
	return b
}
