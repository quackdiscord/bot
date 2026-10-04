// Package discord connects Quack to Discord: the REST client behind the quack
// ports, the interaction router, the /case command, and the messages it sends.
//
// Messages are text in the discordtext conversation layout. Views write icon
// placeholders and command references like /case view; the router, the
// interaction responder, and Bot.Send resolve both for the sending
// application just before sending. Slash commands that post a result use
// AsyncPublic: success replaces the public placeholder in place, and an
// ErrorEdit goes privately to the invoking user instead.
//
// Authorization uses current Discord state: the gateway's live state while
// the session is connected, and REST for anything state lacks (see live.go).
// REST calls carry the caller's context and never retry on their own,
// because Quack's workers own retry policy.
package discord

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// Bot owns the Discord session and implements the quack ports that talk to
// Discord: guild directory, enforcement, direct messages, evidence, staff
// channel validation, and the audit mirror.
type Bot struct {
	// Session is exposed so the composition root can set gateway intents and
	// optional modules can subscribe to gateway events.
	Session *discordgo.Session
	// Dashboard builds the dashboard links on Quack's messages: staff views,
	// receipts, the appeal queue, the audit mirror, setup, and member
	// appeal DMs. The zero value leaves every link out. Set it before
	// NewRouter.
	Dashboard quack.DashboardLinks

	httpClient *http.Client
	connected  atomic.Bool

	// directoryUsers and directoryChannels cache the dashboard's display
	// lookups; see directory.go.
	directoryUsers    ttlCache[directoryEntry]
	directoryChannels ttlCache[[]DirectoryChannel]

	// live says when gateway state can answer authorization and holds
	// members fetched over REST; see live.go.
	live liveState
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
	session.AddHandler(bot.live.track)
	return bot, nil
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

// classify turns a failed REST call into a quack.DiscordError whose code is
// prefixed with operation. The message never includes Discord's response
// body. For irreversible operations a server or network error may have been
// applied anyway, so it is reported as uncertain instead of retryable.
func classify(operation string, err error, irreversible bool) error {
	var rateLimit *discordgo.RateLimitError
	if errors.As(err, &rateLimit) {
		return quack.DiscordError{
			Code:      operation + "_" + quack.DiscordFailureRateLimited,
			Message:   "Discord rate limit reached",
			Retryable: true,
		}
	}
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Response == nil {
		return quack.DiscordError{
			Code:             operation + "_network_error",
			Message:          "Discord request failed",
			Retryable:        !irreversible,
			OutcomeUncertain: irreversible,
		}
	}
	status := restErr.Response.StatusCode
	code, retryable, uncertain := "discord_failure", false, false
	switch {
	case status == http.StatusBadRequest:
		code = "validation_failed"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = quack.DiscordFailurePermissionDenied
	case status == http.StatusNotFound:
		code = "unknown_member_or_resource"
	case status == http.StatusTooManyRequests:
		code, retryable = quack.DiscordFailureRateLimited, true
	case status >= 500:
		code, retryable, uncertain = "discord_server_error", !irreversible, irreversible
	}
	return quack.DiscordError{
		Code:             operation + "_" + code,
		Message:          "Discord rejected the moderation request",
		Retryable:        retryable,
		OutcomeUncertain: uncertain,
	}
}

// statusCode returns the HTTP status of a Discord REST error, or 0.
func statusCode(err error) int {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		return restErr.Response.StatusCode
	}
	return 0
}
