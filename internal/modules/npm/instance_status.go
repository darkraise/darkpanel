package npm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// statusProbeTimeout bounds the status read's probe of NPM's API, so an NPM that hangs reads as not ready.
const statusProbeTimeout = 5 * time.Second

// instanceStatus is npm.instance.status: the host's NPM instance, whether its container runs and its API answers,
// the public mark, the exposure of its admin interface, and whether an upgrade is available.
func (n nm) instanceStatus() core.ActionDef {
	return core.DefineRead[StatusInput, InstanceStatus](core.ActionSpec{
		Name:    "npm.instance.status",
		Summary: "Show the Nginx Proxy Manager instance of this host and whether it runs and answers.",
		Version: 1,
		Side:    core.SideAgent,
	}, statusReader{n: n})
}

// statusReader is the handler of npm.instance.status.
type statusReader struct{ n nm }

// Read never fails because the instance is unhealthy: an Engine that does not answer reads as a container that does
// not run, and an NPM that does not answer as an API that is not ready. A database error fails it, and so does a
// cancelled request, which is the read's error rather than an unhealthy instance.
func (r statusReader) Read(ctx context.Context, env core.Env, _ StatusInput) (InstanceStatus, error) {
	row, ok, exposure, err := statusStored(ctx, env)
	if err != nil {
		return InstanceStatus{}, err
	}
	if !ok {
		return InstanceStatus{PinnedImage: Image, Public: "no"}, nil
	}
	st := InstanceStatus{
		Deployed:         true,
		ContainerID:      row.ContainerID,
		Image:            row.Image,
		PinnedImage:      Image,
		Public:           row.Public,
		AdminEmail:       row.AdminEmail,
		UpgradeAvailable: row.Image != Image,
		AdminExposure:    exposure,
	}
	c, err := r.n.d.docker.InspectContainer(ctx, row.ContainerID)
	switch {
	case err == nil:
		st.Running = c.State.Running
	case cancelled(ctx, err):
		return InstanceStatus{}, err
	}
	if st.Running {
		probe, cancel := context.WithTimeout(ctx, statusProbeTimeout)
		_, err := r.n.d.anon().Health(probe)
		cancel()
		if err != nil && cancelled(ctx, err) {
			return InstanceStatus{}, err
		}
		st.APIReady = err == nil
	}
	return st, nil
}

// statusStored reads the instance row and, when one is deployed, the admin exposure in one read transaction, so the
// two belong to the same moment. ok is false when no instance is deployed.
func statusStored(ctx context.Context, env core.Env) (row instanceRow, ok bool, exposure *AdminExposure, err error) {
	tx, err := env.DB.Read().BeginTx(ctx, nil)
	if err != nil {
		return instanceRow{}, false, nil, fmt.Errorf("npm: begin a read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if row, ok, err = loadInstance(ctx, tx); err != nil || !ok {
		return instanceRow{}, false, nil, err
	}
	if exposure, err = statusAdminExposure(ctx, tx); err != nil {
		return instanceRow{}, false, nil, err
	}
	return row, true, exposure, nil
}

// cancelled reports whether a failed call ended because the request did: its context is done or the call says it was
// cancelled.
func cancelled(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled)
}

// statusAdminExposure is the exposure of NPM's admin interface: the first host name in sort order among the
// reservations of npm.admin.expose, with the ids of its proxy host and certificate (0 for one it does not have). It
// is nil when the admin interface is not exposed. The panel's own exposure is not the admin interface's.
func statusAdminExposure(ctx context.Context, q queryer) (*AdminExposure, error) {
	rows, err := q.QueryContext(ctx, "SELECT kind, object_id, exposure FROM npm_reserved WHERE owner = 'npm.admin.expose' ORDER BY exposure, kind")
	if err != nil {
		return nil, fmt.Errorf("npm: read the admin exposure: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out *AdminExposure
	for rows.Next() {
		var (
			kind, exposure string
			id             int
		)
		if err := rows.Scan(&kind, &id, &exposure); err != nil {
			return nil, fmt.Errorf("npm: read the admin exposure: %w", err)
		}
		if out == nil {
			out = &AdminExposure{Hostname: exposure}
		}
		if exposure != out.Hostname {
			break
		}
		switch kind {
		case "proxy_host":
			out.ProxyHostID = id
		case "certificate":
			out.CertificateID = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npm: read the admin exposure: %w", err)
	}
	return out, nil
}
