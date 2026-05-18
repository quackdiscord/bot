package structs

import "time"

type OAuthState struct {
	RedirectTo   string    `json:"redirect_to"`
	ResponseMode string    `json:"response_mode"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuthSession struct {
	ID               string    `json:"id"`
	DiscordUserID    string    `json:"discord_user_id"`
	Username         string    `json:"username"`
	GlobalName       string    `json:"global_name"`
	Avatar           string    `json:"avatar"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	Scope            string    `json:"scope"`
	TokenExpiresAt   time.Time `json:"token_expires_at"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	LastSeenAt       time.Time `json:"last_seen_at"`
}
