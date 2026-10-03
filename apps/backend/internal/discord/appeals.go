package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// The route of the reversal confirmation button on an accepted appeal. Its
// payload is "appeal,execution,action".
const (
	appealNamespace     = "appeal"
	appealReverseAction = "reverse"
)

// AppealSettingsStore is the storage the AppealNotifier reads to find a
// guild's staff channel.
type AppealSettingsStore interface {
	GetGuildSettings(ctx context.Context, guildID string) (*quack.GuildSettings, error)
	GetGuildByID(ctx context.Context, guildID string) (*quack.Guild, error)
}

// AppealNotifier delivers the appeal outbox through Discord. It implements
// quack.AppealNotifier. Messages never name the staff member involved.
type AppealNotifier struct {
	bot      *Bot
	channels appealChannels
}

// NewAppealNotifier returns an AppealNotifier that sends through bot. Staff
// notifications go to the guild's audit mirror channel, found in store.
func NewAppealNotifier(bot *Bot, store AppealSettingsStore) *AppealNotifier {
	return &AppealNotifier{bot: bot, channels: appealChannels{store: store, validator: bot}}
}

// staffChannelValidator is the staff-only check appealChannels runs before
// every send; tests replace the live Discord check.
type staffChannelValidator interface {
	ValidateStaffChannel(ctx context.Context, guildID, channelID string) error
}

// appealChannels finds a guild's appeal staff channel: the audit mirror
// channel, re-checked as staff-only before every send.
type appealChannels struct {
	store     AppealSettingsStore
	validator staffChannelValidator
}

// SendAppealMemberNotification sends a status update to the member by DM.
func (n *AppealNotifier) SendAppealMemberNotification(ctx context.Context, discordUserID, body string) (string, error) {
	if strings.TrimSpace(discordUserID) == "" {
		return "", errors.New("appeal member is unknown")
	}
	channel, err := n.bot.Session.UserChannelCreate(discordUserID, rest(ctx)...)
	if err != nil {
		return "", notificationError("appeal_dm_channel", err)
	}
	message, err := n.bot.Send(ctx, channel.ID, Signal("appeal", body, false))
	if err != nil {
		return "", notificationError("appeal_dm", err)
	}
	return message.ID, nil
}

// notificationError classifies a failed appeal notification send, so the
// dispatcher can record a coarse code without reading Discord's text. A
// deadline is kept as is, so it still reads as a timeout.
func notificationError(operation string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, classify(operation, err, false))
}

// SendAppealStaffNotification posts a queue update to the guild's staff
// channel.
func (n *AppealNotifier) SendAppealStaffNotification(ctx context.Context, guildID, body string) (string, error) {
	channelID, err := n.channels.staffChannel(ctx, guildID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(channelID) == "" {
		return "", errors.New("appeal staff channel is unavailable")
	}
	message, err := n.bot.Send(ctx, channelID, Signal("appeal", body, false))
	if err != nil {
		return "", notificationError("appeal_staff_notification", err)
	}
	return message.ID, nil
}

// staffChannel returns the guild's appeal staff channel, or "" if it has
// none configured.
func (c appealChannels) staffChannel(ctx context.Context, guildID string) (string, error) {
	settings, err := c.store.GetGuildSettings(ctx, guildID)
	if err != nil || settings == nil {
		return "", err
	}
	guild, err := c.store.GetGuildByID(ctx, guildID)
	if err != nil || guild == nil {
		return "", errors.New("appeal guild is unavailable")
	}
	channelID := settings.AuditMirrorChannelDiscordID
	if err := c.validator.ValidateStaffChannel(ctx, guild.DiscordGuildID, channelID); err != nil {
		return "", err
	}
	return channelID, nil
}

// appealReversal handles the "Confirm ..." button on an accepted appeal.
// Acceptance never reverses anything by itself; this button queues the
// reversal after live permission and hierarchy checks.
func appealReversal(services *quack.Services) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		if i.GuildID == "" || i.Member == nil || i.Member.User == nil {
			return Immediate(Error("Open this appeal in the server’s review queue to remove the punishment."))
		}
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		if err != nil {
			return Immediate(Error("That punishment button is broken. Open the case to try again."))
		}
		parts := strings.Split(id.Payload, ",")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return Immediate(Error("That punishment button is broken. Open the case to try again."))
		}
		appealID, executionID, actionType := parts[0], parts[1], quack.ActionType(parts[2])
		if actionType != quack.ActionRemoveTimeout && actionType != quack.ActionUnbanUser {
			return Immediate(Error("Only bans and timeouts can be removed here."))
		}
		return AsyncPublic(func(ctx context.Context, responder Responder) error {
			userID, name := interactionMember(i)
			staff, err := services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
				DiscordGuildID: i.GuildID,
				DiscordUserID:  userID,
				DisplayName:    name,
			})
			if err != nil {
				_, _ = responder.EditOriginal(ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
				return nil
			}
			appeal, err := services.Appeals.GetStaff(ctx, staff, appealID)
			if err != nil || appeal.Status != quack.AppealStatusAccepted {
				_, _ = responder.EditOriginal(ErrorEdit("Accept the appeal before removing its punishment."))
				return nil
			}
			_, err = services.Actions.ReverseForAppeal(ctx, staff, appeal.CaseID, executionID, actionType, &appeal.ID)
			if err != nil {
				_, _ = responder.EditOriginal(ErrorEdit("I couldn’t queue the punishment removal. Check your moderation permissions and try again."))
				return nil
			}
			_, err = Publish(responder, Signal("retry", "Punishment removal queued. Check the case for the result.", false))
			return err
		})
	}
}
