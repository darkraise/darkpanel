package npm

import (
	"context"
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
// not run, and an NPM that does not answer as an API that is not ready. Only a database error fails it.
func (r statusReader) Read(ctx context.Context, env core.Env, _ StatusInput) (InstanceStatus, error) {
	row, ok, err := loadInstance(ctx, env.DB.Read())
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
	}
	if c, err := r.n.d.docker.InspectContainer(ctx, row.ContainerID); err == nil {
		st.Running = c.State.Running
	}
	if st.Running {
		probe, cancel := context.WithTimeout(ctx, statusProbeTimeout)
		_, err := r.n.d.anon().Health(probe)
		cancel()
		st.APIReady = err == nil
	}
	if st.AdminExposure, err = statusAdminExposure(ctx, env.DB.Read()); err != nil {
		return InstanceStatus{}, err
	}
	return st, nil
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
