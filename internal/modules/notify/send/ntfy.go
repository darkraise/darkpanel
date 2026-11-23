package send

import (
	"context"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// ntfyPriority maps a severity to ntfy's priority: 3 default, 4 high, 5 max.
var ntfyPriority = map[string]string{"info": "3", "warning": "4", "critical": "5"}

// ntfy publishes ev's body to the channel's topic, its title and priority
// in headers and the access token, when there is one, as a bearer token.
func (s *sender) ntfy(ctx context.Context, ch Channel, ev Event) (string, error) {
	target := strings.TrimRight(ch.Server, "/") + "/" + url.PathEscape(ch.Topic)
	h := http.Header{}
	h.Set("Title", mime.QEncoding.Encode("utf-8", oneLine(ev.Title)))
	priority, ok := ntfyPriority[ev.Severity]
	if !ok {
		priority = "3"
	}
	h.Set("Priority", priority)
	h.Set("Content-Type", "text/plain; charset=utf-8")
	if ch.Token != "" {
		h.Set("Authorization", "Bearer "+ch.Token)
	}
	return s.post(ctx, "ntfy", target, h, []byte(ev.Body))
}
