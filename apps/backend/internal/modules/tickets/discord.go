package tickets

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// Discord page sizes for the member listings syncThreadMembers walks.
const (
	guildMemberPage  = 1000
	threadMemberPage = 100
	messagePage      = 100
)

// channels is the DiscordClient that talks to Discord: it creates ticket
// threads or channels, keeps them private, and captures transcripts. Like
// the rest of Quack it never retries a REST call on its own.
type channels struct {
	session *discordgo.Session
	guilds  *modules.Guilds
}

// CreateChannel creates a private thread under the entry channel or, with
// threads off, a private text channel in the entry channel's category.
func (c channels) CreateChannel(ctx context.Context, guildID, ownerID string, settings Settings) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	discordGuildID, err := c.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return "", err
	}
	entry, err := c.session.Channel(settings.EntryChannelDiscordID, rest(ctx)...)
	if err != nil {
		return "", fmt.Errorf("fetch entry channel: %w", err)
	}
	if entry.GuildID != discordGuildID {
		return "", errors.New("ticket entry channel belongs to another guild")
	}
	name := "ticket-" + ticketNameSuffix(ownerID)
	if settings.UsePrivateThreads {
		// The thread starts with only the bot; EnsureAccess adds the owner
		// and staff before the ticket is committed.
		thread, err := c.session.ThreadStartComplex(settings.EntryChannelDiscordID, &discordgo.ThreadStart{
			Name:                name,
			Type:                discordgo.ChannelTypeGuildPrivateThread,
			AutoArchiveDuration: 1440,
			Invitable:           false,
		}, rest(ctx)...)
		if err != nil {
			return "", fmt.Errorf("start ticket thread: %w", err)
		}
		return thread.ID, nil
	}
	botID, err := c.botUserID(ctx)
	if err != nil {
		return "", err
	}
	channel, err := c.session.GuildChannelCreateComplex(discordGuildID, discordgo.GuildChannelCreateData{
		Name:                 name,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             entry.ParentID,
		PermissionOverwrites: ticketPermissionOverwrites(discordGuildID, ownerID, botID, settings.StaffRoleDiscordIDs),
	}, rest(ctx)...)
	if err != nil {
		return "", fmt.Errorf("create ticket channel: %w", err)
	}
	return channel.ID, nil
}

// EnsureAccess makes a ticket visible to exactly its owner, current staff,
// and the bot. A private thread gets its members synced; a text channel gets
// its overwrites replaced and then checked.
func (c channels) EnsureAccess(ctx context.Context, guildID, channelID, ownerID string, staffRoleIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := c.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, rest(ctx)...)
	if err != nil {
		return fmt.Errorf("fetch ticket channel: %w", err)
	}
	if channel.GuildID != discordGuildID {
		return errors.New("ticket channel belongs to another guild")
	}
	if channel.IsThread() {
		if channel.Type != discordgo.ChannelTypeGuildPrivateThread {
			return errors.New("ticket thread is not private")
		}
		if err := c.session.ThreadMemberAdd(channelID, ownerID, rest(ctx)...); err != nil {
			return fmt.Errorf("add ticket owner: %w", err)
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
		return fmt.Errorf("set ticket permissions: %w", err)
	}
	return validateTicketACL(updated, discordGuildID, ownerID, botID, staffRoleIDs)
}

// SendReply posts body in the ticket with mentions suppressed.
func (c channels) SendReply(ctx context.Context, channelID, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         body,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, rest(ctx)...)
	return err
}

// CaptureTranscript reads the ticket's whole message history and renders it
// oldest first, one line per message plus one per attachment.
func (c channels) CaptureTranscript(ctx context.Context, channelID string) (string, error) {
	var messages []*discordgo.Message
	before := ""
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page, err := c.session.ChannelMessages(channelID, messagePage, before, "", "", rest(ctx)...)
		if err != nil {
			return "", fmt.Errorf("read ticket messages: %w", err)
		}
		messages = append(messages, page...)
		if len(page) < messagePage {
			break
		}
		before = page[len(page)-1].ID
	}
	slices.SortFunc(messages, func(a, b *discordgo.Message) int { return a.Timestamp.Compare(b.Timestamp) })
	var transcript strings.Builder
	for _, message := range messages {
		authorID := "unknown"
		if message.Author != nil {
			authorID = message.Author.ID
		}
		fmt.Fprintf(&transcript, "[%s] %s: %s\n",
			message.Timestamp.UTC().Format(time.RFC3339), authorID, message.Content)
		for _, attachment := range message.Attachments {
			fmt.Fprintf(&transcript, "  attachment: %s (%d bytes)\n", attachment.Filename, attachment.Size)
		}
	}
	return transcript.String(), nil
}

// ArchiveChannel closes a ticket in Discord once its transcript is saved: a
// thread is archived and locked; a text channel is renamed closed-* and made
// read-only for everyone it was shared with.
func (c channels) ArchiveChannel(ctx context.Context, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, rest(ctx)...)
	if err != nil {
		return fmt.Errorf("fetch ticket channel: %w", err)
	}
	if channel.IsThread() {
		archived, locked := true, true
		_, err = c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{
			Archived: &archived,
			Locked:   &locked,
		}, rest(ctx)...)
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
	_, err = c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{
		Name:                 name,
		PermissionOverwrites: overwrites,
	}, rest(ctx)...)
	return err
}

// DeleteChannel deletes a channel created for a ticket that was never
// committed.
func (c channels) DeleteChannel(ctx context.Context, channelID string) error {
	_, err := c.session.ChannelDelete(channelID, rest(ctx)...)
	return err
}

// syncThreadMembers makes a private thread's members exactly the owner, the
// bot, and members who currently hold a staff role. Private threads ignore
// role overwrites, so membership is the only access control; it follows a
// fresh member list so a demoted moderator loses access.
func (c channels) syncThreadMembers(ctx context.Context, discordGuildID, threadID, ownerID string, staffRoleIDs []string) error {
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	wanted := map[string]bool{ownerID: true, botID: true}
	staffRoles := make(map[string]bool, len(staffRoleIDs))
	for _, roleID := range staffRoleIDs {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			staffRoles[roleID] = true
		}
	}
	after := ""
	for len(staffRoles) > 0 {
		members, err := c.session.GuildMembers(discordGuildID, after, guildMemberPage, rest(ctx)...)
		if err != nil {
			return fmt.Errorf("list guild members: %w", err)
		}
		for _, member := range members {
			if member == nil || member.User == nil || member.User.ID == "" {
				return errors.New("discord returned an invalid guild member")
			}
			if slices.ContainsFunc(member.Roles, func(roleID string) bool { return staffRoles[roleID] }) {
				wanted[member.User.ID] = true
			}
		}
		if len(members) < guildMemberPage {
			break
		}
		next := members[len(members)-1].User.ID
		if next == after {
			return errors.New("discord repeated a guild-member page")
		}
		after = next
	}

	present := make(map[string]bool)
	after = ""
	for {
		members, err := c.session.ThreadMembers(threadID, threadMemberPage, false, after, rest(ctx)...)
		if err != nil {
			return fmt.Errorf("list thread members: %w", err)
		}
		for _, member := range members {
			if member == nil || member.UserID == "" {
				return errors.New("discord returned an invalid thread member")
			}
			present[member.UserID] = true
			if !wanted[member.UserID] {
				if err := c.session.ThreadMemberRemove(threadID, member.UserID, rest(ctx)...); err != nil {
					return fmt.Errorf("remove thread member: %w", err)
				}
			}
		}
		if len(members) < threadMemberPage {
			break
		}
		next := members[len(members)-1].UserID
		if next == after {
			return errors.New("discord repeated a thread-member page")
		}
		after = next
	}

	for userID := range wanted {
		if present[userID] || userID == botID {
			continue
		}
		if err := c.session.ThreadMemberAdd(threadID, userID, rest(ctx)...); err != nil {
			return fmt.Errorf("add thread member: %w", err)
		}
	}
	return nil
}

// botUserID returns the bot's user ID, which explicit overwrites need.
func (c channels) botUserID(ctx context.Context) (string, error) {
	if c.session.State != nil && c.session.State.User != nil && c.session.State.User.ID != "" {
		return c.session.State.User.ID, nil
	}
	user, err := c.session.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("fetch bot user: %w", err)
	}
	if user == nil || user.ID == "" {
		return "", errors.New("discord bot identity is unavailable")
	}
	return user.ID, nil
}

// rest returns per-call options: the caller's context and no retries.
func rest(ctx context.Context) []discordgo.RequestOption {
	return []discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}
}

// ticketPermissionOverwrites is the full ACL of a private text-channel
// ticket: @everyone denied, the owner, bot, and staff roles allowed.
func ticketPermissionOverwrites(discordGuildID, ownerID, botID string, staffRoleIDs []string) []*discordgo.PermissionOverwrite {
	overwrites := []*discordgo.PermissionOverwrite{
		{
			ID:   discordGuildID,
			Type: discordgo.PermissionOverwriteTypeRole,
			Deny: discordgo.PermissionViewChannel,
		},
		{
			ID:    ownerID,
			Type:  discordgo.PermissionOverwriteTypeMember,
			Allow: ticketChannelPermissions,
		},
		{
			ID:    botID,
			Type:  discordgo.PermissionOverwriteTypeMember,
			Allow: ticketChannelPermissions | discordgo.PermissionManageChannels,
		},
	}
	for _, roleID := range staffRoleIDs {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			overwrites = append(overwrites, &discordgo.PermissionOverwrite{
				ID:    roleID,
				Type:  discordgo.PermissionOverwriteTypeRole,
				Allow: ticketChannelPermissions,
			})
		}
	}
	return overwrites
}

// validateTicketACL checks a ticket channel's overwrites: @everyone denied,
// and the owner, the bot, and every configured staff role allowed. A ticket
// channel is shared with its owner, a member, so it cannot use the core
// staff-only channel check.
func validateTicketACL(channel *discordgo.Channel, discordGuildID, ownerID, botID string, staffRoleIDs []string) error {
	if channel == nil || channel.GuildID != discordGuildID {
		return errors.New("channel is outside the configured guild")
	}
	deniedEveryone, ownerVisible, botVisible := false, false, false
	allowedRoles := map[string]bool{}
	for _, overwrite := range channel.PermissionOverwrites {
		visible := overwrite.Allow&discordgo.PermissionViewChannel != 0
		switch overwrite.Type {
		case discordgo.PermissionOverwriteTypeRole:
			if overwrite.ID == discordGuildID {
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
