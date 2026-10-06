package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/bwmarrin/discordgo"
)

// SetupChannelKind picks the permissions for a channel /setup creates. It
// never changes the overwrites of a channel an administrator chose or
// already configured.
type SetupChannelKind int

const (
	// SetupStaffChannel is a destination only staff can read.
	SetupStaffChannel SetupChannelKind = iota
	// SetupTicketEntry is a public, read-only channel holding the ticket
	// button, where Quack opens private threads.
	SetupTicketEntry
	// SetupHoneypotChannel is a public trap members can post in.
	SetupHoneypotChannel
)

// SetupChannel returns the channel a /setup subcommand should use: the one
// the administrator specified, else the configured one if it still exists,
// else a new text channel called name with permissions for kind, a topic,
// and, for staff destinations, a short introduction post.
//
// A new staff channel admits moderatorRoleIDs, the guild's configured
// moderator roles, or when none of them exist every role with
// Administrator or Moderate Members.
//
// Only a configured channel Discord confirms is gone is replaced; any other
// failure stops setup, so a transient error never creates a duplicate.
// Callers must check Manage Server first and validate Quack's own
// permissions before saving. Every error is a *UserError.
func SetupChannel(
	ctx context.Context, session *discordgo.Session,
	guildID, specified, configured, name string, kind SetupChannelKind, moderatorRoleIDs []string,
) (string, error) {
	if specified != "" {
		return specified, nil
	}
	if configured != "" {
		channel, err := session.Channel(configured, rest(ctx)...)
		if err == nil && channel.GuildID == guildID && channel.Type == discordgo.ChannelTypeGuildText {
			return configured, nil
		}
		if !isUnknownChannel(err) {
			return "", &UserError{Message: "Could not access the configured channel. Check Quack's permissions or specify another channel."}
		}
	}
	guild, err := session.Guild(guildID, rest(ctx)...)
	if err != nil {
		return "", &UserError{Message: "Could not read the server's roles. Try again."}
	}
	bot := &Bot{Session: session}
	botID, err := bot.botID(ctx)
	if err != nil {
		return "", &UserError{Message: "Could not identify Quack's server account. Try again."}
	}
	topic, intro := setupChannelPresentation(name, kind)
	channel, err := session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:                 name,
		Type:                 discordgo.ChannelTypeGuildText,
		Topic:                topic,
		PermissionOverwrites: setupChannelPermissions(guild, botID, kind, moderatorRoleIDs),
	}, rest(ctx)...)
	if err != nil || channel.ID == "" {
		return "", &UserError{Message: fmt.Sprintf(
			"Could not create #%s. Quack needs Manage Channels permission. You can also specify an existing channel.", name)}
	}
	// The channel exists now, so its ID is returned even if the introduction
	// fails: the caller saves it rather than creating another on retry.
	if intro != "" {
		if _, err := bot.Send(ctx, channel.ID, Content(intro, false)); err != nil {
			slog.WarnContext(ctx, "Could not send new channel introduction", "channel_id", channel.ID, "error", err)
		}
	}
	return channel.ID, nil
}

// isUnknownChannel reports whether Discord said the channel no longer
// exists. Any other failure, transient ones included, must not be taken as
// leave to create a replacement.
func isUnknownChannel(err error) bool {
	var rest *discordgo.RESTError
	if !errors.As(err, &rest) || rest.Response == nil || rest.Message == nil {
		return false
	}
	return rest.Response.StatusCode == http.StatusNotFound && rest.Message.Code == discordgo.ErrCodeUnknownChannel
}

// setupChannelPermissions gives a new channel usable defaults without
// guessing role names. Staff destinations admit the guild's configured
// moderator roles, or when none still exist the roles holding
// staffPermissions, as quack's moderator fallback does, so
// the channel passes ValidateStaffChannel; administrators keep Discord's
// normal bypass. Quack's own overwrite grants what each feature needs.
func setupChannelPermissions(guild *discordgo.Guild, botID string, kind SetupChannelKind, moderatorRoleIDs []string) []*discordgo.PermissionOverwrite {
	read := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	write := read | discordgo.PermissionSendMessages | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks
	everyone := &discordgo.PermissionOverwrite{ID: guild.ID, Type: discordgo.PermissionOverwriteTypeRole}
	bot := &discordgo.PermissionOverwrite{ID: botID, Type: discordgo.PermissionOverwriteTypeMember, Allow: write}
	overwrites := []*discordgo.PermissionOverwrite{everyone, bot}
	switch kind {
	case SetupStaffChannel:
		everyone.Deny = discordgo.PermissionViewChannel
		byRole := slices.ContainsFunc(guild.Roles, func(role *discordgo.Role) bool {
			return role != nil && role.ID != guild.ID && slices.Contains(moderatorRoleIDs, role.ID)
		})
		for _, role := range guild.Roles {
			if role == nil || role.ID == guild.ID {
				continue
			}
			staff := role.Permissions&staffPermissions != 0
			if byRole {
				staff = slices.Contains(moderatorRoleIDs, role.ID)
			}
			if staff {
				overwrites = append(overwrites, &discordgo.PermissionOverwrite{
					ID:    role.ID,
					Type:  discordgo.PermissionOverwriteTypeRole,
					Allow: write,
				})
			}
		}
	case SetupTicketEntry:
		everyone.Allow = read | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionAttachFiles
		everyone.Deny = discordgo.PermissionSendMessages | discordgo.PermissionCreatePublicThreads | discordgo.PermissionCreatePrivateThreads
		bot.Allow |= discordgo.PermissionCreatePrivateThreads | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionManageThreads
	case SetupHoneypotChannel:
		everyone.Allow = write
		bot.Allow |= discordgo.PermissionManageMessages
	}
	return overwrites
}

// setupChannelPresentation is the topic and introduction of a new channel.
// Ticket entry and honeypot channels get no introduction: their panel or
// warning is the first post, and a second one would compete with it.
func setupChannelPresentation(name string, kind SetupChannelKind) (topic, intro string) {
	switch kind {
	case SetupTicketEntry:
		return "Need a hand? Open a private ticket with the team below.", ""
	case SetupHoneypotChannel:
		return "Do not post here. Read the warning below for what happens if you do.", ""
	}
	switch name {
	case "appeals":
		return "Case appeals for the team to review.",
			"# Appeals\nNew appeals will appear here for the team to review."
	case "moderation-log":
		return "Quack case activity, moderation actions, and settings changes.",
			"# Moderation log\nQuack will keep case activity, moderation actions, and settings changes here."
	case "discord-log":
		return "Message activity, member arrivals and departures, and server changes recorded by Quack.",
			"# Server activity\nQuack will record message edits and deletions, member arrivals and departures, bans, and server changes here."
	case "ticket-log":
		return "Support tickets and updates for the team.",
			"# Tickets\nNew tickets and updates will appear here. Open a ticket's thread to help out."
	default:
		return "Updates from Quack for the moderation team.",
			"# Quack updates\nUpdates for the team will appear here."
	}
}
