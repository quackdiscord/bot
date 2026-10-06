package tickets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// Discord page sizes for the listings the client walks.
const (
	threadMemberPage = 100
	messagePage      = 100
)

// channels is the DiscordClient that talks to Discord. Like the rest of
// Quack it never retries a REST call on its own; the adapter decides what
// is safe to repeat.
type channels struct {
	session *discordgo.Session
	guilds  *modules.Guilds
}

// bot wraps the session for the discord package's helpers.
func (c channels) bot() *discord.Bot { return &discord.Bot{Session: c.session} }

// CreateThread starts a private, non-invitable thread under the entry
// channel. It starts with only the bot; EnsureAccess invites the owner.
func (c channels) CreateThread(ctx context.Context, guildID, ownerID string, settings Settings) (string, error) {
	discordGuildID, err := c.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return "", err
	}
	entry, err := c.session.Channel(settings.EntryChannelDiscordID, rest(ctx)...)
	if err != nil {
		return "", fmt.Errorf("fetch entry channel: %w", err)
	}
	if entry.GuildID != discordGuildID || entry.Type != discordgo.ChannelTypeGuildText {
		return "", errors.New("ticket entry must be a text channel in this guild")
	}
	thread, err := c.session.ThreadStartComplex(settings.EntryChannelDiscordID, &discordgo.ThreadStart{
		Name:                "ticket-" + ticketNameSuffix(ownerID),
		Type:                discordgo.ChannelTypeGuildPrivateThread,
		AutoArchiveDuration: 1440,
		Invitable:           false,
	}, rest(ctx)...)
	if err != nil {
		return "", fmt.Errorf("start ticket thread: %w", err)
	}
	return thread.ID, nil
}

// EnsureAccess invites the owner into a private ticket thread in the guild
// and removes members who are no longer staff. Moderators join through the
// queue post when they choose to, so staff are never mass-invited.
func (c channels) EnsureAccess(ctx context.Context, guildID, threadID, ownerID string) error {
	discordGuildID, err := c.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return err
	}
	thread, err := c.session.Channel(threadID, rest(ctx)...)
	if err != nil {
		return fmt.Errorf("fetch ticket thread: %w", err)
	}
	if thread.GuildID != discordGuildID || thread.Type != discordgo.ChannelTypeGuildPrivateThread {
		return errors.New("ticket must be a private thread in this guild")
	}
	if err := c.session.ThreadMemberAdd(threadID, ownerID, rest(ctx)...); err != nil {
		return fmt.Errorf("add ticket owner: %w", err)
	}
	return c.syncThreadMembers(ctx, discordGuildID, threadID, ownerID)
}

// syncThreadMembers removes thread members who are no longer staff: anyone
// but the owner, the bot, and the guild owner who lacks Administrator,
// Moderate Members, and the guild's moderator roles now (see
// quack.StaffRoles.IsDiscordStaff). It reads only the thread's members,
// never the whole guild, and invites nobody.
func (c channels) syncThreadMembers(ctx context.Context, discordGuildID, threadID, ownerID string) error {
	botID, err := c.botID(ctx)
	if err != nil {
		return err
	}
	guild, err := c.session.Guild(discordGuildID, rest(ctx)...)
	if err != nil {
		return fmt.Errorf("fetch guild: %w", err)
	}
	staffRoles, err := c.guilds.StaffRoles(ctx, discordGuildID)
	if err != nil {
		return fmt.Errorf("load staff roles: %w", err)
	}
	roles := make(map[string]int64, len(guild.Roles))
	for _, role := range guild.Roles {
		if role != nil {
			roles[role.ID] = role.Permissions
		}
	}
	after := ""
	for {
		members, err := c.session.ThreadMembers(threadID, threadMemberPage, false, after, rest(ctx)...)
		if err != nil {
			return fmt.Errorf("list thread members: %w", err)
		}
		for _, member := range members {
			if member == nil || member.UserID == "" {
				return errors.New("discord returned an invalid thread member")
			}
			if member.UserID == ownerID || member.UserID == botID || member.UserID == guild.OwnerID {
				continue
			}
			current, err := c.session.GuildMember(discordGuildID, member.UserID, rest(ctx)...)
			if err != nil && !isRESTCode(err, discordgo.ErrCodeUnknownMember) {
				return fmt.Errorf("fetch thread member: %w", err)
			}
			if current != nil {
				permissions := roles[discordGuildID]
				for _, id := range current.Roles {
					permissions |= roles[id]
				}
				if staffRoles.IsDiscordStaff(uint64(permissions), current.Roles) {
					continue
				}
			}
			if err := c.session.ThreadMemberRemove(threadID, member.UserID, rest(ctx)...); err != nil {
				return fmt.Errorf("remove thread member: %w", err)
			}
		}
		if len(members) < threadMemberPage {
			return nil
		}
		next := members[len(members)-1].UserID
		if next == after {
			return errors.New("discord repeated a thread-member page")
		}
		after = next
	}
}

// SendWelcome greets the owner in their new thread with a Close button. It
// may mention only the owner.
func (c channels) SendWelcome(ctx context.Context, ticket *Ticket) error {
	message := welcomeMessage(ticket)
	_, err := c.bot().Send(ctx, ticket.ThreadDiscordChannelID, message)
	return err
}

// FreezeThread archives and locks a thread before its history is captured.
// Repeating it is harmless.
func (c channels) FreezeThread(ctx context.Context, threadID string) error {
	archived, locked := true, true
	_, err := c.session.ChannelEditComplex(threadID, &discordgo.ChannelEdit{Archived: &archived, Locked: &locked}, rest(ctx)...)
	return err
}

// CaptureMessages reads a thread's whole history, checking that every page
// moves backwards so a misbehaving response cannot loop forever.
func (c channels) CaptureMessages(ctx context.Context, threadID string) ([]TranscriptMessage, error) {
	var messages []TranscriptMessage
	before := ""
	for {
		page, err := c.session.ChannelMessages(threadID, messagePage, before, "", "", rest(ctx)...)
		if err != nil {
			return nil, fmt.Errorf("read ticket messages: %w", err)
		}
		for _, message := range page {
			if message == nil || message.ID == "" {
				return nil, errors.New("discord returned an incomplete ticket history page")
			}
			messages = append(messages, transcriptMessage(message))
		}
		if len(page) < messagePage {
			return messages, nil
		}
		next := page[len(page)-1].ID
		if before != "" && !snowflakeBefore(next, before) {
			return nil, errors.New("discord ticket history pagination did not advance")
		}
		before = next
	}
}

// DeleteThread deletes a closed ticket's thread; a thread already gone
// counts as deleted, so a retry finishes.
func (c channels) DeleteThread(ctx context.Context, threadID string) error {
	_, err := c.session.ChannelDelete(threadID, rest(ctx)...)
	if isRESTCode(err, discordgo.ErrCodeUnknownChannel) {
		return nil
	}
	return err
}

// messageEdit replaces a posted message with message, resolved for the
// bot's application. Mentions stay suppressed.
func (c channels) messageEdit(ctx context.Context, channelID, messageID string, message discord.Message) *discordgo.MessageEdit {
	appID, _ := c.botID(ctx)
	message = message.ForApplication(appID)
	return &discordgo.MessageEdit{
		ID:              messageID,
		Channel:         channelID,
		Content:         &message.Content,
		Components:      &message.Components,
		Embeds:          &[]*discordgo.MessageEmbed{},
		Files:           message.Files,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
}

// botID returns the bot's user ID.
func (c channels) botID(ctx context.Context) (string, error) {
	if c.session.State != nil && c.session.State.User != nil && c.session.State.User.ID != "" {
		return c.session.State.User.ID, nil
	}
	user, err := c.session.User("@me", rest(ctx)...)
	if err != nil {
		return "", fmt.Errorf("fetch bot user: %w", err)
	}
	if user.ID == "" {
		return "", errors.New("discord bot identity is unavailable")
	}
	return user.ID, nil
}

// transcriptMessage converts a Discord message for the transcript.
func transcriptMessage(message *discordgo.Message) TranscriptMessage {
	snapshot := TranscriptMessage{MessageID: message.ID, Body: message.Content, SentAt: message.Timestamp}
	if message.Author != nil {
		snapshot.AuthorID, snapshot.AuthorName = message.Author.ID, message.Author.Username
	}
	for _, attachment := range message.Attachments {
		if attachment != nil {
			snapshot.Attachments = append(snapshot.Attachments, TranscriptAttachment{
				Name: attachment.Filename, Size: attachment.Size, URL: attachment.URL,
			})
		}
	}
	return snapshot
}

// snowflakeBefore reports whether snowflake a is older than b.
func snowflakeBefore(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// ticketNameSuffix keeps thread names short and free of anything but the
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

// isRESTCode reports whether err is a Discord API error with code.
func isRESTCode(err error, codes ...int) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Message == nil {
		return false
	}
	for _, code := range codes {
		if restErr.Message.Code == code {
			return true
		}
	}
	return false
}

// rest returns per-call options: the caller's context and no retries.
func rest(ctx context.Context) []discordgo.RequestOption {
	return []discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}
}
