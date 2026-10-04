package honeypot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// warningTimeout bounds one guild's warning refresh.
const warningTimeout = 10 * time.Second

// legacyHoneypotWarning is the old generated default. A guild that still has
// it saved gets the wording derived from its live template instead.
const legacyHoneypotWarning = "# Warning!\nThis channel catches spam and scam accounts. Do not post here. Posting here triggers this server's honeypot moderation rule."

// WarningPolicy reads the outcomes a template can produce, so the warning
// names the real punishment. *quack.TemplateService implements it.
type WarningPolicy interface {
	UnattendedTemplateActions(ctx context.Context, guildID, templateID string) ([]quack.ActionType, error)
}

// warnings keeps each guild's warning post current: the incident count after
// every saved case, and a new post when the old one is deleted. It is
// presentation only; a failure here never undoes or repeats moderation.
type warnings struct {
	session *discordgo.Session
	service *Service
	policy  WarningPolicy
	guilds  *modules.Guilds
	locks   *guildLocks

	// seeded and cursor track the startup pass that queues every enabled
	// guild's refresh. Only the refresh loop touches them.
	seeded bool
	cursor string
}

// deleted queues a refresh when one of messageIDs in channelID is the
// guild's current warning, so it is posted again. Stale and unrelated
// deletions do nothing.
func (w *warnings) deleted(ctx context.Context, guildID, channelID string, messageIDs []string) error {
	if channelID == "" || len(messageIDs) == 0 {
		return nil
	}
	settings, _, err := w.service.loadSettings(ctx, guildID)
	if err != nil {
		return err
	}
	if settings.ChannelDiscordID != channelID || !slices.Contains(messageIDs, settings.WarningMessageID) {
		return nil
	}
	return w.service.RequestWarningRefresh(ctx, guildID)
}

// run queues every enabled guild's refresh once after a restart, then
// serves the refreshes that are due.
func (w *warnings) run(ctx context.Context) error {
	for !w.seeded {
		ids, err := w.service.ConfiguredWarningGuilds(ctx, w.cursor)
		if err != nil {
			return fmt.Errorf("queue honeypot warnings after restart: %w", err)
		}
		for _, id := range ids {
			if err := w.service.RequestWarningRefresh(ctx, id); err != nil {
				return fmt.Errorf("queue honeypot warnings after restart: %w", err)
			}
			w.cursor = id
		}
		w.seeded = len(ids) < warningSeedPage
	}
	return w.process(ctx)
}

// process serves one batch of due refreshes. A failed refresh backs off and
// is logged; only storage errors are returned.
func (w *warnings) process(ctx context.Context) error {
	rows, err := w.service.WarningRefreshes(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		refreshCtx, cancel := context.WithTimeout(ctx, warningTimeout)
		deliveryErr := w.refresh(refreshCtx, row.GuildID)
		cancel()
		if err := w.service.CompleteWarningRefresh(ctx, row, deliveryErr != nil); err != nil {
			return err
		}
		if deliveryErr != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Honeypot warning refresh will retry", "guild_id", row.GuildID, "error", deliveryErr)
		}
	}
	return nil
}

// refresh edits the guild's warning to the current wording and count,
// posting a replacement if it was deleted. A disabled or unconfigured
// honeypot needs nothing. It holds the guild's lock, shared with Setup.
func (w *warnings) refresh(ctx context.Context, guildID string) error {
	release, err := w.locks.lock(ctx, guildID)
	if err != nil {
		return err
	}
	defer release()
	settings, status, err := w.service.Settings(ctx, modules.Actor{GuildID: guildID, CanManage: true})
	if err != nil {
		return err
	}
	if !status.Enabled || !status.Configured {
		return nil
	}
	discordGuildID, err := w.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return err
	}
	channel, err := w.session.Channel(settings.ChannelDiscordID, rest(ctx)...)
	if err != nil {
		return err
	}
	if channel.GuildID != discordGuildID {
		return errors.New("honeypot warning channel is outside the guild")
	}
	text, err := resolveHoneypotWarning(ctx, w.policy, guildID, settings)
	if err != nil {
		return err
	}
	content := honeypotWarningContent(text, status.Statistics.Created)
	if settings.WarningMessageID != "" {
		edited, err := w.edit(ctx, channel.ID, settings.WarningMessageID, content)
		if err != nil || edited {
			return err
		}
	}
	message, err := w.sendReplacement(ctx, guildID, settings, content)
	if err != nil {
		return err
	}
	return w.service.RecordWarningReplacement(ctx, guildID, settings, message.ID)
}

// edit rewrites the warning in place. It reports false, without error, when
// the warning was deleted and needs replacing.
func (w *warnings) edit(ctx context.Context, channelID, messageID, content string) (bool, error) {
	_, err := w.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:              messageID,
		Channel:         channelID,
		Content:         &content,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, rest(ctx)...)
	if restCode(err) == discordgo.ErrCodeUnknownMessage {
		return false, nil
	}
	return err == nil, err
}

// sendReplacement posts a new warning behind the send fence, so a post whose
// outcome is unknown is never repeated, across retries and restarts. Only a
// definite refusal from Discord lifts the fence again.
func (w *warnings) sendReplacement(ctx context.Context, guildID string, settings Settings, content string) (*discordgo.Message, error) {
	if err := w.service.ReserveWarningSend(ctx, guildID, settings); err != nil {
		return nil, err
	}
	bot := &discord.Bot{Session: w.session}
	message, err := bot.Send(ctx, settings.ChannelDiscordID, discord.Content(content, false))
	switch {
	case err != nil && warningDefinitelyNotSent(err):
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return nil, errors.Join(err, w.service.ReleaseWarningSend(releaseCtx, guildID, settings))
	case err != nil:
		return nil, errors.Join(ErrWarningDeliveryUnknown, err)
	case message == nil || message.ID == "":
		return nil, ErrWarningDeliveryUnknown
	}
	return message, nil
}

// warningDefinitelyNotSent reports a definite refusal from Discord. Network
// and server errors may hide a post that went through.
func warningDefinitelyNotSent(err error) bool {
	var limit *discordgo.RateLimitError
	if errors.As(err, &limit) {
		return true
	}
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		switch restErr.Response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return true
		}
	}
	return false
}

// honeypotWarningContent is the warning post: the warning, then the count
// of incidents caught. It counts incidents rather than bans because the
// template may not ban.
func honeypotWarningContent(warning string, count uint64) string {
	if strings.TrimSpace(warning) == "" {
		warning = "# Do not post here\nThis channel catches spam and scam accounts."
	}
	label := "incidents"
	if count == 1 {
		label = "incident"
	}
	return fmt.Sprintf("%s\n\n-# %d %s caught.", warning, count, label)
}

// resolveHoneypotWarning returns the admin's own warning when there is one.
// Otherwise it words one from the template's live outcomes, so a changed
// punishment shows up on the next refresh. It refuses to guess a
// punishment it cannot read.
func resolveHoneypotWarning(ctx context.Context, policy WarningPolicy, guildID string, settings Settings) (string, error) {
	if strings.TrimSpace(settings.WarningText) != "" && settings.WarningText != legacyHoneypotWarning {
		return settings.WarningText, nil
	}
	actions, err := policy.UnattendedTemplateActions(ctx, guildID, settings.TemplateID)
	if err != nil {
		return "", err
	}
	var outcomes []string
	for _, action := range actions {
		var outcome string
		switch action {
		case quack.ActionBanUser:
			outcome = "ban you from this server"
		case quack.ActionKickUser:
			outcome = "kick you from this server"
		case quack.ActionTimeoutUser:
			outcome = "time you out"
		case quack.ActionSendDM:
			outcome = "send you a warning by DM"
		case "":
			outcome = "record a moderation case"
		default:
			return "", errors.New("honeypot warning has an unsupported action")
		}
		if !slices.Contains(outcomes, outcome) {
			outcomes = append(outcomes, outcome)
		}
	}
	if len(outcomes) == 0 {
		return "", errors.New("honeypot warning has no configured outcome")
	}
	consequence := "Posting here will " + outcomes[0] + "."
	if len(outcomes) > 1 {
		consequence = "Depending on your previous cases, posting here can " + strings.Join(outcomes, " or ") + "."
	}
	return "# Do not post here\n" + consequence + " This channel catches spam and scam accounts.", nil
}

// guildLocks serializes one family of per-guild operations (setup and
// warning refresh) without blocking other guilds. Locks live as long as the
// module, so a waiter never ends up holding a detached copy.
type guildLocks struct{ gates sync.Map }

// lock waits for the guild's lock, honoring ctx, and returns its release,
// which must be called exactly once.
func (l *guildLocks) lock(ctx context.Context, guildID string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidate := make(chan struct{}, 1)
	candidate <- struct{}{}
	value, _ := l.gates.LoadOrStore(guildID, candidate)
	gate := value.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate:
		if err := ctx.Err(); err != nil {
			gate <- struct{}{}
			return nil, err
		}
		return func() { gate <- struct{}{} }, nil
	}
}
