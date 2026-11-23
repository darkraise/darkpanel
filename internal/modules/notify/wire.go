package notify

import "time"

// NtfyInput is an ntfy channel's settings. Topic and Token act as
// credentials: write-only, and an update keeps a stored one it omits.
type NtfyInput struct {
	Server string `json:"server,omitempty" jsonschema_description:"The ntfy server, an http or https URL; https://ntfy.sh when omitted."`
	Topic  string `json:"topic,omitempty" secret:"true" jsonschema_description:"The topic, 1 to 64 letters, digits, hyphens or underscores; write-only, required on create."`
	Token  string `json:"token,omitempty" secret:"true" jsonschema_description:"An access token sent as a bearer token; optional and write-only."`
}

// TelegramInput is a Telegram channel's settings.
type TelegramInput struct {
	BotToken string `json:"botToken,omitempty" secret:"true" jsonschema_description:"The bot's token from BotFather; write-only, required on create."`
	ChatID   string `json:"chatId" jsonschema_description:"The chat id, a number, or @ and a public channel's name."`
}

// DiscordInput is a Discord channel's settings.
type DiscordInput struct {
	WebhookURL string `json:"webhookUrl,omitempty" secret:"true" jsonschema_description:"The channel's https://discord.com/api/webhooks/... URL; write-only, required on create."`
}

// EmailInput is an email channel's settings.
type EmailInput struct {
	Host       string   `json:"host" jsonschema:"minLength=1" jsonschema_description:"The SMTP server's host name or address."`
	Port       int      `json:"port" jsonschema:"minimum=1,maximum=65535" jsonschema_description:"The SMTP server's port."`
	Security   string   `json:"security" jsonschema:"enum=starttls,enum=tls,enum=none" jsonschema_description:"starttls, tls (implicit TLS) or none; none only to a loopback host."`
	Username   string   `json:"username,omitempty" jsonschema_description:"The SMTP user name; omitted when the server needs no sign-in."`
	Password   string   `json:"password,omitempty" secret:"true" jsonschema_description:"The SMTP password, with a user name; write-only, and an update keeps a stored one it omits."`
	From       string   `json:"from" jsonschema:"minLength=1" jsonschema_description:"The sender address."`
	Recipients []string `json:"recipients" jsonschema:"minItems=1,maxItems=10" jsonschema_description:"1 to 10 recipient addresses."`
}

// WebhookInput is a webhook channel's settings. The URL acts as a
// credential: write-only, and an update keeps a stored one it omits.
type WebhookInput struct {
	URL    string `json:"url,omitempty" secret:"true" jsonschema_description:"The http or https URL the JSON body is posted to; write-only, required on create."`
	Secret string `json:"secret,omitempty" secret:"true" jsonschema_description:"An HMAC-SHA256 key; the body's signature is sent as X-Darkpanel-Signature: sha256=<hex>. Optional and write-only."`
}

// ChannelInput is the input of notify.channel.create/v1: the settings
// object named by kind and no other.
type ChannelInput struct {
	Kind        string         `json:"kind" jsonschema:"enum=ntfy,enum=telegram,enum=discord,enum=email,enum=webhook" jsonschema_description:"The channel kind."`
	Name        string         `json:"name" jsonschema:"pattern=^[A-Za-z0-9][A-Za-z0-9 ._-]{0\\,47}$" jsonschema_description:"The name: 1 to 48 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit."`
	MinSeverity string         `json:"minSeverity" jsonschema:"enum=info,enum=warning,enum=critical" jsonschema_description:"The lowest severity the channel receives."`
	Kinds       []string       `json:"kinds,omitempty" jsonschema_description:"The event kinds the channel receives; empty receives every kind."`
	GroupIDs    []string       `json:"groupIds,omitempty" jsonschema_description:"The host groups whose hosts' events the channel receives; empty receives every host's and host-less events."`
	Ntfy        *NtfyInput     `json:"ntfy,omitempty" jsonschema_description:"The settings of an ntfy channel."`
	Telegram    *TelegramInput `json:"telegram,omitempty" jsonschema_description:"The settings of a Telegram channel."`
	Discord     *DiscordInput  `json:"discord,omitempty" jsonschema_description:"The settings of a Discord channel."`
	Email       *EmailInput    `json:"email,omitempty" jsonschema_description:"The settings of an email channel."`
	Webhook     *WebhookInput  `json:"webhook,omitempty" jsonschema_description:"The settings of a webhook channel."`
}

// ChannelUpdateInput is the input of notify.channel.update/v1. It replaces
// the channel's settings, except a write-only value it omits; the kind
// stays, and the settings object is the one its kind names. A stored
// write-only value is kept only while its destination stays: a new ntfy
// server, SMTP host, port or security, or webhook URL needs the required
// ones entered again and drops the optional ones.
type ChannelUpdateInput struct {
	ID          string         `json:"id" jsonschema:"minLength=1" jsonschema_description:"The channel's id."`
	Name        string         `json:"name" jsonschema:"pattern=^[A-Za-z0-9][A-Za-z0-9 ._-]{0\\,47}$" jsonschema_description:"The name, as notify.channel.create takes it."`
	MinSeverity string         `json:"minSeverity" jsonschema:"enum=info,enum=warning,enum=critical" jsonschema_description:"The lowest severity the channel receives."`
	Kinds       []string       `json:"kinds,omitempty" jsonschema_description:"The event kinds the channel receives; empty receives every kind."`
	GroupIDs    []string       `json:"groupIds,omitempty" jsonschema_description:"The host groups whose hosts' events the channel receives; empty receives every host's and host-less events."`
	Enabled     bool           `json:"enabled" jsonschema_description:"Whether the channel receives events."`
	Ntfy        *NtfyInput     `json:"ntfy,omitempty" jsonschema_description:"The settings of an ntfy channel."`
	Telegram    *TelegramInput `json:"telegram,omitempty" jsonschema_description:"The settings of a Telegram channel."`
	Discord     *DiscordInput  `json:"discord,omitempty" jsonschema_description:"The settings of a Discord channel."`
	Email       *EmailInput    `json:"email,omitempty" jsonschema_description:"The settings of an email channel."`
	Webhook     *WebhookInput  `json:"webhook,omitempty" jsonschema_description:"The settings of a webhook channel."`
}

// ChannelIDInput is the input of notify.channel.delete/v1.
type ChannelIDInput struct {
	ID string `json:"id" jsonschema:"minLength=1" jsonschema_description:"The channel's id."`
}

// NoInput is the empty input of notify.channel.list/v1.
type NoInput struct{}

// NtfyView is an ntfy channel's settings as reads show them.
type NtfyView struct {
	Server   string `json:"server" jsonschema_description:"The ntfy server."`
	Topic    string `json:"topic" jsonschema_description:"The topic's first 4 characters and an ellipsis."`
	TokenSet bool   `json:"tokenSet" jsonschema_description:"Whether an access token is stored."`
}

// TelegramView is a Telegram channel's settings as reads show them.
type TelegramView struct {
	ChatID string `json:"chatId" jsonschema_description:"The chat id."`
}

// DiscordView is a Discord channel's settings as reads show them: the
// webhook URL is never returned.
type DiscordView struct{}

// EmailView is an email channel's settings as reads show them.
type EmailView struct {
	Host        string   `json:"host" jsonschema_description:"The SMTP server's host."`
	Port        int      `json:"port" jsonschema_description:"The SMTP server's port."`
	Security    string   `json:"security" jsonschema_description:"starttls, tls or none."`
	Username    string   `json:"username" jsonschema_description:"The SMTP user name; empty for none."`
	PasswordSet bool     `json:"passwordSet" jsonschema_description:"Whether a password is stored."`
	From        string   `json:"from" jsonschema_description:"The sender address."`
	Recipients  []string `json:"recipients" jsonschema_description:"The recipient addresses."`
}

// WebhookView is a webhook channel's settings as reads show them.
type WebhookView struct {
	URL       string `json:"url" jsonschema_description:"The URL's first 4 characters and an ellipsis."`
	SecretSet bool   `json:"secretSet" jsonschema_description:"Whether an HMAC key is stored."`
}

// Delivery is the result of a channel's latest finished delivery.
type Delivery struct {
	State string    `json:"state" jsonschema:"enum=sent,enum=failed" jsonschema_description:"sent or failed."`
	At    time.Time `json:"at" jsonschema_description:"When the delivery finished."`
	Error string    `json:"error,omitempty" jsonschema_description:"A failed delivery's reason: an HTTP status, an SMTP reply code or a short code; never a provider's body."`
}

// Channel is a notification channel without its write-only values.
type Channel struct {
	ID           string        `json:"id" jsonschema_description:"The channel's id."`
	Kind         string        `json:"kind" jsonschema_description:"ntfy, telegram, discord, email or webhook."`
	Name         string        `json:"name" jsonschema_description:"The channel's name."`
	MinSeverity  string        `json:"minSeverity" jsonschema_description:"The lowest severity the channel receives."`
	Kinds        []string      `json:"kinds" jsonschema_description:"The event kinds the channel receives; empty receives every kind."`
	GroupIDs     []string      `json:"groupIds" jsonschema_description:"The host groups the channel is limited to; empty is every host."`
	Enabled      bool          `json:"enabled" jsonschema_description:"Whether the channel receives events."`
	CreatedAt    time.Time     `json:"createdAt"`
	Ntfy         *NtfyView     `json:"ntfy,omitempty" jsonschema:"nullable" jsonschema_description:"An ntfy channel's settings; absent for another kind."`
	Telegram     *TelegramView `json:"telegram,omitempty" jsonschema:"nullable" jsonschema_description:"A Telegram channel's settings; absent for another kind."`
	Discord      *DiscordView  `json:"discord,omitempty" jsonschema:"nullable" jsonschema_description:"A Discord channel's settings; absent for another kind."`
	Email        *EmailView    `json:"email,omitempty" jsonschema:"nullable" jsonschema_description:"An email channel's settings; absent for another kind."`
	Webhook      *WebhookView  `json:"webhook,omitempty" jsonschema:"nullable" jsonschema_description:"A webhook channel's settings; absent for another kind."`
	LastDelivery *Delivery     `json:"lastDelivery" jsonschema:"nullable" jsonschema_description:"The latest finished delivery; null before the first."`
}

// ChannelList is the output of notify.channel.list/v1.
type ChannelList struct {
	Channels []Channel `json:"channels" jsonschema_description:"Every channel, sorted by name."`
}
