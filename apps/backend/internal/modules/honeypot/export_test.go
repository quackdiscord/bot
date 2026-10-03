package honeypot

// Gateway handlers, exported for the external tests.
var (
	OnChannelDelete = (*Module).onChannelDelete
	OnGuildDelete   = (*Module).onGuildDelete
)
