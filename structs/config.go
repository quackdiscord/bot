package structs

type Config struct {
	Environment string
	API         APIConfig
	Discord     DiscordConfig
	Auth        AuthConfig
	Storage     StorageConfig
	EventQueue  EventQueueConfig
}

type APIConfig struct {
	Port string
}

type DiscordConfig struct {
	Token            string
	AppID            string
	ClientSecret     string
	OAuthRedirectURI string
	OAuthScopes      string
	CommandGuildID   string
	CommandPrune     bool
}

type AuthConfig struct {
	SessionCookieName string
	SessionTTLHours   int
	StateTTLMinutes   int
	PostLoginRedirect string
	CookieSecure      bool
}

type StorageConfig struct {
	DBDSN    string
	RedisURL string
}

type EventQueueConfig struct {
	Size    int
	Workers int
}
