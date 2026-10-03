package tickets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// ticketChannelPermissions is what the owner, staff roles, and the bot get
// in a private text-channel ticket.
const ticketChannelPermissions = discordgo.PermissionViewChannel |
	discordgo.PermissionSendMessages |
	discordgo.PermissionReadMessageHistory |
	discordgo.PermissionAttachFiles |
	discordgo.PermissionEmbedLinks

// channels is the DiscordClient that talks to Discord: it creates ticket
// threads or channels, keeps them private, and captures transcripts. Like
// the rest of Quack it never retries a REST call on its own.
type channels struct {
	session *discordgo.Session
	guilds  *modules.Guilds
}

// rest returns per-call options: the caller's context and no retries.
func rest(ctx context.Context) []discordgo.RequestOption {
	return []discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}
}

// CreatePrivateTicketChannel creates a private thread under the entry
// channel or, with threads off, a private text channel in its category.
func (c channels) CreatePrivateTicketChannel(ctx context.Context, guildID, ownerID string, settings Settings) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	discordGuildID, err := c.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return "", err
	}
	entry, err := c.session.Channel(settings.EntryChannelDiscordID, rest(ctx)...)
	if err != nil {
		return "", err
	}
	if entry.GuildID != discordGuildID {
		return "", errors.New("ticket entry channel belongs to another guild")
	}
	name := "ticket-" + ticketNameSuffix(ownerID)
	if settings.UsePrivateThreads {
		thread, err := c.session.ThreadStartComplex(settings.EntryChannelDiscordID, &discordgo.ThreadStart{
			Name: name, Type: discordgo.ChannelTypeGuildPrivateThread,
			AutoArchiveDuration: 1440, Invitable: false,
		}, rest(ctx)...)
		if err != nil {
			return "", err
		}
		// EnsureTicketPermissions adds the owner and staff before the
		// ticket is committed.
		return thread.ID, nil
	}
	botID, err := c.botUserID(ctx)
	if err != nil {
		return "", err
	}
	channel, err := c.session.GuildChannelCreateComplex(discordGuildID, discordgo.GuildChannelCreateData{
		Name: name, Type: discordgo.ChannelTypeGuildText, ParentID: entry.ParentID,
		PermissionOverwrites: ticketPermissionOverwrites(discordGuildID, ownerID, botID, settings.StaffRoleDiscordIDs),
	}, rest(ctx)...)
	if err != nil {
		return "", err
	}
	return channel.ID, nil
}

// EnsureTicketPermissions makes a ticket visible to exactly its owner,
// current staff, and the bot. A private thread gets its members synced; a
// text channel gets its overwrites replaced and then checked.
func (c channels) EnsureTicketPermissions(ctx context.Context, channelID, ownerID, guildID string, staffRoleIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := c.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, rest(ctx)...)
	if err != nil {
		return err
	}
	if channel.GuildID != discordGuildID {
		return errors.New("ticket channel belongs to another guild")
	}
	if channel.IsThread() {
		if channel.Type != discordgo.ChannelTypeGuildPrivateThread {
			return errors.New("ticket thread is not private")
		}
		if err := c.session.ThreadMemberAdd(channelID, ownerID, rest(ctx)...); err != nil {
			return err
		}
		return c.syncThreadMembers(ctx, discordGuildID, channelID, ownerID, staffRoleIDs)
	}
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	updated, err := c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{
		PermissionOverwrites: ticketPermissionOverwrites(discordGuildID, ownerID, botID, staffRoleIDs),
	}, rest(ctx)...)
	if err != nil {
		return err
	}
	return validateTicketACL(updated, discordGuildID, ownerID, botID, staffRoleIDs)
}

// botUserID returns the bot's user ID, which explicit overwrites need.
func (c channels) botUserID(ctx context.Context) (string, error) {
	if c.session.State != nil && c.session.State.User != nil && c.session.State.User.ID != "" {
		return c.session.State.User.ID, nil
	}
	user, err := c.session.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	if user == nil || user.ID == "" {
		return "", errors.New("discord bot identity is unavailable")
	}
	return user.ID, nil
}

// DeleteProvisionalTicketChannel deletes a channel created for a ticket
// that was never committed.
func (c channels) DeleteProvisionalTicketChannel(ctx context.Context, channelID string) error {
	_, err := c.session.ChannelDelete(channelID, rest(ctx)...)
	return err
}

// SendTicketReply posts body in the ticket with mentions suppressed.
func (c channels) SendTicketReply(ctx context.Context, channelID, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: body, AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, rest(ctx)...)
	return err
}

// CaptureTicketTranscript reads the ticket's whole message history and
// renders it oldest first.
func (c channels) CaptureTicketTranscript(ctx context.Context, channelID string) (string, error) {
	before := ""
	messages := make([]*discordgo.Message, 0, 100)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page, err := c.session.ChannelMessages(channelID, 100, before, "", "", rest(ctx)...)
		if err != nil {
			return "", err
		}
		messages = append(messages, page...)
		if len(page) < 100 {
			break
		}
		before = page[len(page)-1].ID
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Timestamp.Before(messages[j].Timestamp) })
	var transcript strings.Builder
	for _, message := range messages {
		authorID := "unknown"
		if message.Author != nil {
			authorID = message.Author.ID
		}
		fmt.Fprintf(&transcript, "[%s] %s: %s\n", message.Timestamp.UTC().Format(time.RFC3339), authorID, message.Content)
		for _, attachment := range message.Attachments {
			fmt.Fprintf(&transcript, "  attachment: %s (%d bytes)\n", attachment.Filename, attachment.Size)
		}
	}
	return transcript.String(), nil
}

// ArchiveTicketChannel closes a ticket in Discord once its transcript is
// saved: a thread is archived and locked; a text channel is renamed
// closed-* and made read-only.
func (c channels) ArchiveTicketChannel(ctx context.Context, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, rest(ctx)...)
	if err != nil {
		return err
	}
	if channel.IsThread() {
		archived, locked := true, true
		_, err = c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{Archived: &archived, Locked: &locked}, rest(ctx)...)
		return err
	}
	overwrites := make([]*discordgo.PermissionOverwrite, 0, len(channel.PermissionOverwrites))
	for _, existing := range channel.PermissionOverwrites {
		overwrite := *existing
		if overwrite.ID != channel.GuildID {
			overwrite.Allow &^= discordgo.PermissionSendMessages
			overwrite.Deny |= discordgo.PermissionSendMessages
		}
		overwrites = append(overwrites, &overwrite)
	}
	name := channel.Name
	if !strings.HasPrefix(name, "closed-") {
		name = "closed-" + name
	}
	_, err = c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{Name: name, PermissionOverwrites: overwrites}, rest(ctx)...)
	return err
}

// syncThreadMembers makes a private thread's members exactly the owner, the
// bot, and members who currently hold a staff role. Private threads ignore
// role overwrites, so membership is the only access control; it follows a
// fresh member list so a demoted moderator loses access.
func (c channels) syncThreadMembers(ctx context.Context, guildID, threadID, ownerID string, staffRoleIDs []string) error {
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	wanted := map[string]struct{}{ownerID: {}, botID: {}}
	roles := make(map[string]struct{}, len(staffRoleIDs))
	for _, roleID := range staffRoleIDs {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			roles[roleID] = struct{}{}
		}
	}
	after := ""
	for len(roles) > 0 {
		members, err := c.session.GuildMembers(guildID, after, 1000, rest(ctx)...)
		if err != nil {
			return err
		}
		for _, member := range members {
			if member == nil || member.User == nil || member.User.ID == "" {
				return errors.New("discord returned an invalid guild member")
			}
			for _, roleID := range member.Roles {
				if _, ok := roles[roleID]; ok {
					wanted[member.User.ID] = struct{}{}
					break
				}
			}
		}
		if len(members) < 1000 {
			break
		}
		next := members[len(members)-1].User.ID
		if next == after {
			return errors.New("discord repeated a guild-member page")
		}
		after = next
	}
	present := make(map[string]struct{})
	after = ""
	for {
		members, err := c.session.ThreadMembers(threadID, 100, false, after, rest(ctx)...)
		if err != nil {
			return err
		}
		for _, member := range members {
			if member == nil || member.UserID == "" {
				return errors.New("discord returned an invalid thread member")
			}
			present[member.UserID] = struct{}{}
			if _, ok := wanted[member.UserID]; !ok {
				if err := c.session.ThreadMemberRemove(threadID, member.UserID, rest(ctx)...); err != nil {
					return err
				}
			}
		}
		if len(members) < 100 {
			break
		}
		next := members[len(members)-1].UserID
		if next == after {
			return errors.New("discord repeated a thread-member page")
		}
		after = next
	}
	for userID := range wanted {
		if _, ok := present[userID]; ok || userID == botID {
			continue
		}
		if err := c.session.ThreadMemberAdd(threadID, userID, rest(ctx)...); err != nil {
			return err
		}
	}
	return nil
}

// ticketPermissionOverwrites is the full ACL of a private text-channel
// ticket: @everyone denied, the owner, bot, and staff roles allowed.
func ticketPermissionOverwrites(guildID, ownerID, botID string, staffRoleIDs []string) []*discordgo.PermissionOverwrite {
	overwrites := []*discordgo.PermissionOverwrite{
		{ID: guildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
		{ID: ownerID, Type: discordgo.PermissionOverwriteTypeMember, Allow: ticketChannelPermissions},
		{ID: botID, Type: discordgo.PermissionOverwriteTypeMember, Allow: ticketChannelPermissions | discordgo.PermissionManageChannels},
	}
	for _, roleID := range staffRoleIDs {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			overwrites = append(overwrites, &discordgo.PermissionOverwrite{
				ID: roleID, Type: discordgo.PermissionOverwriteTypeRole, Allow: ticketChannelPermissions,
			})
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
	ownerVisible, botVisible := false, false
	for _, overwrite := range channel.PermissionOverwrites {
		visible := overwrite.Allow&discordgo.PermissionViewChannel != 0
		switch overwrite.Type {
		case discordgo.PermissionOverwriteTypeRole:
			if overwrite.ID == guildID {
				if visible {
					return errors.New("channel explicitly grants everyone visibility")
				}
				deniedEveryone = deniedEveryone || overwrite.Deny&discordgo.PermissionViewChannel != 0
			}
			if visible {
				allowedRoles[overwrite.ID] = true
			}
		case discordgo.PermissionOverwriteTypeMember:
			ownerVisible = ownerVisible || overwrite.ID == ownerID && visible
			botVisible = botVisible || overwrite.ID == botID && visible
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
	if !ownerVisible {
		return errors.New("ticket owner cannot view private channel")
	}
	if !botVisible {
		return errors.New("discord bot cannot view private channel")
	}
	return nil
}

// ticketNameSuffix keeps channel names short and free of anything but the
// tail of the owner's ID.
func ticketNameSuffix(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if len(ownerID) > 12 {
		ownerID = ownerID[len(ownerID)-12:]
	}
	if ownerID == "" {
		return "member"
	}
	return ownerID
}
