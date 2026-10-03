package honeypot

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// Gateway handlers, exported for the external tests.
var (
	OnMessageCreate     = (*Module).onMessageCreate
	OnMessageDelete     = (*Module).onMessageDelete
	OnMessageDeleteBulk = (*Module).onMessageDeleteBulk
	OnChannelDelete     = (*Module).onChannelDelete
	OnGuildDelete       = (*Module).onGuildDelete
)

// CheckEnablement runs the module's registered enablement check.
var CheckEnablement = (*Module).checkEnablement

// RefreshWarning refreshes one guild's warning now, whether or not it is
// due.
func RefreshWarning(ctx context.Context, m *Module, guildID string) error {
	return m.warnings.refresh(ctx, guildID)
}

// ProcessWarnings serves due warning refreshes without the startup pass.
func ProcessWarnings(ctx context.Context, m *Module) error {
	return m.warnings.process(ctx)
}

// NewChannelValidator returns the live channel validator.
func NewChannelValidator(session *discordgo.Session, guilds *modules.Guilds) ChannelValidator {
	return channelValidator{session: session, guilds: guilds}
}
