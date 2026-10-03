// Package discord connects Quack to Discord: the REST client behind the quack
// ports, the interaction router, the /case command, and the messages it sends.
//
// Authorization always comes from fresh REST reads; the gateway cache is only
// used for display and readiness. REST calls carry the caller's context and
// never retry on their own, because Quack's workers own retry policy.
package discord

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"
)

// Bot owns the Discord session and implements the quack ports that talk to
// Discord: guild directory, enforcement, direct messages, evidence, staff
// channel validation, and the audit mirror.
type Bot struct {
	// Session is exposed so the composition root can set gateway intents and
	// optional modules can subscribe to gateway events.
	Session *discordgo.Session

	httpClient *http.Client
	connected  atomic.Bool
}

// New creates a bot for token. Only the Guilds intent is requested; the
// caller adds whatever enabled modules need before calling Open.
func New(token string) (*Bot, error) {
	session, err := discordgo.New(token)
	if err != nil {
		return nil, err
	}
	session.Identify.Intents = discordgo.IntentGuilds
	session.StateEnabled = true
	session.State.MaxMessageCount = 5000
	bot := &Bot{Session: session, httpClient: http.DefaultClient}
	session.AddHandler(bot.trackGateway)
	return bot, nil
}

// trackGateway keeps readiness in step with the gateway, so health checks
// fail while discordgo reconnects.
func (b *Bot) trackGateway(_ *discordgo.Session, event any) {
	switch event.(type) {
	case *discordgo.Ready, *discordgo.Resumed, *discordgo.Connect:
		b.connected.Store(true)
	case *discordgo.Disconnect:
		b.connected.Store(false)
	}
}

// Open connects to the gateway.
func (b *Bot) Open() error {
	if err := b.Session.Open(); err != nil {
		return err
	}
	b.connected.Store(true)
	return nil
}

// Close disconnects from the gateway. Closing a bot that is not connected is
// a no-op, so shutdown can call it unconditionally.
func (b *Bot) Close() error {
	if !b.connected.Swap(false) {
		return nil
	}
	return b.Session.Close()
}

// Status reports gateway readiness for health checks: whether the bot is
// connected, its username, and the heartbeat latency in milliseconds.
func (b *Bot) Status() (bool, string, int64) {
	if !b.connected.Load() || b.Session.State == nil || b.Session.State.User == nil {
		return false, "", 0
	}
	return true, b.Session.State.User.Username, b.Session.HeartbeatLatency().Milliseconds()
}

// botID returns the bot's own user ID, asking Discord when the gateway has
// not delivered it yet.
func (b *Bot) botID(ctx context.Context) (string, error) {
	if b.Session.State != nil && b.Session.State.User != nil && b.Session.State.User.ID != "" {
		return b.Session.State.User.ID, nil
	}
	user, err := b.Session.User("@me", rest(ctx)...)
	if err != nil || user == nil || user.ID == "" {
		return "", errors.New("discord bot identity is unavailable")
	}
	return user.ID, nil
}

// rest returns the options for one REST call: the caller's context and no
// retries, since retry policy belongs to Quack's workers.
func rest(ctx context.Context, extra ...discordgo.RequestOption) []discordgo.RequestOption {
	return append([]discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}, extra...)
}
