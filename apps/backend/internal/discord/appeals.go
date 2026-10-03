package discord

import (
	"context"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// appealReversal handles the "Confirm ..." button on an accepted appeal. The
// payload is "appeal,execution,action". Acceptance never reverses anything
// by itself; this button queues the reversal after live permission and
// hierarchy checks.
func appealReversal(services *quack.Services) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		if i.GuildID == "" || i.Member == nil || i.Member.User == nil {
			return Immediate(Error("This reversal control is unavailable."))
		}
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		if err != nil {
			return Immediate(Error("This reversal control is invalid."))
		}
		parts := strings.Split(id.Payload, ",")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return Immediate(Error("This reversal control is invalid."))
		}
		appealID, executionID, actionType := parts[0], parts[1], quack.ActionType(parts[2])
		if actionType != quack.ActionRemoveTimeout && actionType != quack.ActionUnbanUser {
			return Immediate(Error("This reversal type is invalid."))
		}
		return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
			userID, name := interactionMember(i)
			staff, err := services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
				DiscordGuildID: i.GuildID,
				DiscordUserID:  userID,
				DisplayName:    name,
			})
			if err != nil {
				_, _ = responder.EditOriginal(ErrorEdit("Live Discord authorization failed."))
				return nil
			}
			appeal, err := services.Appeals.GetStaff(ctx, staff, appealID)
			if err != nil || appeal.Status != quack.AppealStatusAccepted {
				_, _ = responder.EditOriginal(ErrorEdit("This appeal is not eligible for reversal."))
				return nil
			}
			if _, err := services.Actions.ReverseForAppeal(ctx, staff, appeal.CaseID, executionID, actionType, &appeal.ID); err != nil {
				_, _ = responder.EditOriginal(ErrorEdit("The reversal could not be authorized or queued."))
				return nil
			}
			_, err = Publish(responder, Content("**Reversal Queued**\nThe confirmed reversal passed live permission and hierarchy checks.", false))
			return err
		})
	}
}

// AppealStaffChannels finds where a guild's appeal notifications go.
type AppealStaffChannels interface {
	// AppealStaffChannel returns the staff-only channel for the guild with
	// internal ID guildID.
	AppealStaffChannel(ctx context.Context, guildID string) (string, error)
}

// AppealNotifier delivers the appeal outbox through Discord. It implements
// quack.AppealNotifier. Messages never name the staff member involved.
type AppealNotifier struct {
	bot      *Bot
	channels AppealStaffChannels
}

// NewAppealNotifier returns an AppealNotifier that sends through bot and
// finds staff destinations with channels.
func NewAppealNotifier(bot *Bot, channels AppealStaffChannels) *AppealNotifier {
	return &AppealNotifier{bot: bot, channels: channels}
}

// SendAppealMemberNotification sends a status update to the member by DM.
func (n *AppealNotifier) SendAppealMemberNotification(ctx context.Context, discordUserID, body string) (string, error) {
	if strings.TrimSpace(discordUserID) == "" {
		return "", errors.New("appeal member is unknown")
	}
	channel, err := n.bot.Session.UserChannelCreate(discordUserID, rest(ctx)...)
	if err != nil {
		return "", err
	}
	message, err := n.bot.send(ctx, channel.ID, &discordgo.MessageSend{Content: body})
	if err != nil {
		return "", err
	}
	return message.ID, nil
}

// SendAppealStaffNotification posts a queue update to the guild's staff
// channel.
func (n *AppealNotifier) SendAppealStaffNotification(ctx context.Context, guildID, body string) (string, error) {
	channelID, err := n.channels.AppealStaffChannel(ctx, guildID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(channelID) == "" {
		return "", errors.New("appeal staff channel is unavailable")
	}
	message, err := n.bot.send(ctx, channelID, &discordgo.MessageSend{Content: body})
	if err != nil {
		return "", err
	}
	return message.ID, nil
}
