package moduleintegration

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// ticketPermissionOverwrites constructs the exact private text-channel ACL.
func ticketPermissionOverwrites(guildID, ownerID, botID string, staffRoleIDs []string) []*discordgo.PermissionOverwrite {
	overwrites := []*discordgo.PermissionOverwrite{
		{ID: guildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
		{ID: ownerID, Type: discordgo.PermissionOverwriteTypeMember, Allow: ticketChannelPermissions},
		{ID: botID, Type: discordgo.PermissionOverwriteTypeMember, Allow: ticketChannelPermissions | discordgo.PermissionManageChannels},
	}
	for _, roleID := range staffRoleIDs {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			overwrites = append(overwrites, &discordgo.PermissionOverwrite{ID: roleID, Type: discordgo.PermissionOverwriteTypeRole, Allow: ticketChannelPermissions})
		}
	}
	return overwrites
}

// validateTicketACL checks a ticket channel's overwrites: @everyone denied,
// and the owner, the bot, and every configured staff role allowed. A ticket
// channel is not a staff-only channel, since its owner is a member, so it
// does not use discord.Bot.ValidateStaffChannel.
func validateTicketACL(channel *discordgo.Channel, guildID, ownerID, botID string, staffRoleIDs []string) error {
	if channel == nil || channel.GuildID != guildID {
		return errors.New("channel is outside the configured guild")
	}
	deniedEveryone := false
	allowedRoles := map[string]bool{}
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == guildID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			if overwrite.Allow&discordgo.PermissionViewChannel != 0 {
				return errors.New("channel explicitly grants everyone visibility")
			}
			if overwrite.Deny&discordgo.PermissionViewChannel != 0 {
				deniedEveryone = true
			}
		}
		if overwrite.Type == discordgo.PermissionOverwriteTypeRole && overwrite.Allow&discordgo.PermissionViewChannel != 0 {
			allowedRoles[overwrite.ID] = true
		}
	}
	if !deniedEveryone {
		return errors.New("channel is not staff-only")
	}
	for _, roleID := range staffRoleIDs {
		if !allowedRoles[roleID] {
			return fmt.Errorf("staff role %s cannot view ticket channel", roleID)
		}
	}
	ownerVisible := false
	botVisible := false
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == ownerID && overwrite.Type == discordgo.PermissionOverwriteTypeMember && overwrite.Allow&discordgo.PermissionViewChannel != 0 {
			ownerVisible = true
		}
		if overwrite.ID == botID && overwrite.Type == discordgo.PermissionOverwriteTypeMember && overwrite.Allow&discordgo.PermissionViewChannel != 0 {
			botVisible = true
		}
	}
	if !ownerVisible {
		return errors.New("ticket owner cannot view private channel")
	}
	if !botVisible {
		return errors.New("Discord bot cannot view private channel")
	}
	return nil
}

var _ tickets.DiscordClient = ticketDiscordClient{}

var _ generallogging.DeliveryClient = loggingDiscordClient{}
