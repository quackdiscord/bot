package structs

type LogUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
}

type LogSettings struct {
	GuildID           string `json:"guild_id"`
	MessageChannelID  string `json:"message_channel_id"`
	MessageWebhookURL string `json:"message_webhook_url"`
	MemberChannelID   string `json:"member_channel_id"`
	MemberWebhookURL  string `json:"member_webhook_url"`
}
