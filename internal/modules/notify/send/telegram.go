package send

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// maxTelegramText is the Bot API's limit on a message's text.
const maxTelegramText = 4096

type telegramMessage struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// telegram sends ev as a plain text message through the Bot API's
// sendMessage. The request URL holds the bot token, so post never names it.
func (s *sender) telegram(ctx context.Context, ch Channel, ev Event) (string, error) {
	body, err := json.Marshal(telegramMessage{ChatID: ch.ChatID, Text: truncate(text(ev), maxTelegramText), DisableWebPagePreview: true})
	if err != nil {
		return CodeInvalid, err
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return s.post(ctx, "Telegram", strings.TrimRight(s.telegramBase, "/")+"/bot"+ch.BotToken+"/sendMessage", h, body)
}
