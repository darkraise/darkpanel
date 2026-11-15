package npm

import (
	"context"
	"errors"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// reloadNginx tests and reloads nginx in darkpanel-npm (nginxReloader.Reload).
func (n nm) reloadNginx(ctx context.Context) error {
	return n.d.reload(ctx)
}

// reloadRefusal is the refusal of a reload that failed where: a failed configuration test names the custom locations
// whose static name has no answer, the usual cause, when the best-effort listing finds any.
func (n nm) reloadRefusal(ctx context.Context, env core.Env, err error, where string) error {
	if errors.Is(err, errNginxTest) {
		if us, listErr := n.unansweredForwards(ctx, env); listErr == nil && len(us) > 0 {
			return errcode.New(errcode.NPMForwardUnanswered, unansweredDetail(us))
		}
		return errcode.New(errcode.NPMReserved, "nginx could not be reloaded "+where+": nginx's configuration test failed")
	}
	return errcode.New(errcode.NPMUnhealthy, "nginx could not be reloaded "+where+": "+err.Error())
}
