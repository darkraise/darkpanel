package send

import (
	"context"
	"encoding/json"
	"net/http"
)

// maxDiscordContent is Discord's limit on a message's content.
const maxDiscordContent = 2000

type discordMessage struct {
	Content         string `json:"content"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

// discord posts ev to the channel's webhook. No mention in the text pings
// anyone: allowed_mentions parses none.
func (s *sender) discord(ctx context.Context, ch Channel, ev Event) (string, error) {
	m := discordMessage{Content: truncate(text(ev), maxDiscordContent)}
	m.AllowedMentions.Parse = []string{}
	body, err := json.Marshal(m)
	if err != nil {
		return CodeInvalid, err
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return s.post(ctx, "Discord", ch.WebhookURL, h, body)
}
