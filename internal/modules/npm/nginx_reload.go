package npm

import (
	"context"
	"errors"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// nginxReloadWait bounds one reloadNginx: the listing and the update together.
const nginxReloadWait = 10 * time.Second

// reloadNginx makes NPM test and reload nginx by sending the enabled proxy host with the lowest id back unchanged: NPM
// regenerates that host's file, runs nginx -t and reloads nginx on every update of an enabled host, and answers
// meta.nginx_online false when the test failed. With no enabled host there is nothing nginx holds that a reload would
// re-read, so it reports false with no error and writes nothing. Any other failure is the error, whose text is the
// reason.
func (n nm) reloadNginx(ctx context.Context, env core.Env) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, nginxReloadWait)
	defer cancel()
	api, err := n.d.api(ctx, env)
	if err != nil {
		return false, err
	}
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return false, err
	}
	pick := -1
	for i, h := range hosts {
		if bool(h.Enabled) && (pick < 0 || h.ID < hosts[pick].ID) {
			pick = i
		}
	}
	if pick < 0 {
		return false, nil
	}
	answer, err := api.UpdateProxyHost(ctx, hosts[pick].ID, proxyHostEditBody(hosts[pick]))
	if err != nil {
		return false, err
	}
	if online := answer.Meta.NginxOnline; online != nil && !*online {
		if reason := answer.Meta.NginxErr; reason != nil && *reason != "" {
			return false, errors.New(*reason)
		}
		return false, errors.New("nginx did not come online")
	}
	return true, nil
}
