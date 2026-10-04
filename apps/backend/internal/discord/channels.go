package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// staffPermissions are the guild permissions that make someone staff for a
// staff-only channel. Quack's staff-only channels carry case evidence, audit
// events, appeals, and server logs, and v5 grants reading those to the owner,
// Administrator, and Moderate Members. Manage Guild alone configures Quack
// but does not read moderation history, so it does not count.
const staffPermissions = discordgo.PermissionAdministrator | discordgo.PermissionModerateMembers

// ValidateStaffChannel checks that channelID is a text channel in guildID
// that only staff can see: @everyone is denied View Channel, and every role
// or member the channel lets in is the bot or currently holds
// staffPermissions. Members are checked live, so a demoted moderator's
// leftover overwrite makes the channel fail. It is the single staff-only
// check for the audit mirror, evidence copies, appeal notifications, and
// general logging.
func (b *Bot) ValidateStaffChannel(ctx context.Context, guildID, channelID string) error {
	channel, err := b.Session.Channel(channelID, rest(ctx)...)
	if err != nil || channel.GuildID != guildID || channel.Type != discordgo.ChannelTypeGuildText {
		return errors.New("destination must be a private text channel in this guild")
	}
	guild, err := b.Session.Guild(guildID, rest(ctx)...)
	if err != nil {
		return quack.ErrAuthorizationUnavailable
	}
	botID, err := b.botID(ctx)
	if err != nil {
		return quack.ErrAuthorizationUnavailable
	}
	rolePermissions := make(map[string]int64, len(guild.Roles))
	for _, role := range guild.Roles {
		if role != nil {
			rolePermissions[role.ID] = role.Permissions
		}
	}
	private := false
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite == nil {
			continue
		}
		if overwrite.Type == discordgo.PermissionOverwriteTypeRole && overwrite.ID == guildID {
			private = overwrite.Deny&discordgo.PermissionViewChannel != 0 && overwrite.Allow&discordgo.PermissionViewChannel == 0
		}
		if overwrite.Allow&discordgo.PermissionViewChannel == 0 {
			continue
		}
		if overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			if rolePermissions[overwrite.ID]&staffPermissions == 0 {
				return errors.New("destination grants access to a non-staff role")
			}
			continue
		}
		if overwrite.ID == botID {
			continue
		}
		member, err := b.member(ctx, guild, overwrite.ID)
		if err != nil || !member.Present || member.PermissionBits&uint64(staffPermissions) == 0 {
			return errors.New("destination grants access to a non-staff member")
		}
	}
	if !private {
		return errors.New("destination must deny public access")
	}
	return nil
}

// SendAuditMirror posts one audit entry to the guild's audit mirror channel
// after re-checking that the channel is still staff-only. It shares nothing
// with the optional general-logging module. It returns the posted message's
// ID.
func (b *Bot) SendAuditMirror(ctx context.Context, message quack.AuditMirrorMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := b.ValidateStaffChannel(ctx, message.DiscordGuildID, message.ChannelDiscordID); err != nil {
		return "", fmt.Errorf("%w: private destination validation failed", quack.ErrAuditMirrorChannelUnavailable)
	}
	sent, err := b.Send(ctx, message.ChannelDiscordID, auditMirrorMessage(message, b.Dashboard))
	switch {
	case err == nil:
		if sent == nil {
			return "", nil
		}
		return sent.ID, nil
	case statusCode(err) == http.StatusForbidden || statusCode(err) == http.StatusNotFound:
		return "", fmt.Errorf("%w: Discord rejected configured channel", quack.ErrAuditMirrorChannelUnavailable)
	default:
		return "", errors.New("discord audit mirror delivery failed")
	}
}
