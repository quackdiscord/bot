package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/quack"
)

// errQueueUnverifiable reports that a queue post could not be read to
// check it, as opposed to a post that was read and does not match.
var errQueueUnverifiable = errors.New("ticket queue message could not be verified")

// PublishQueue posts the ticket to the staff queue, or edits its existing
// post in place; with a transcript, the post says the ticket closed and
// carries the transcript file. The queue channel must still be staff-only
// before every send.
func (c channels) PublishQueue(ctx context.Context, ticket *Ticket, settings Settings, transcript *Transcript) (*QueueReceipt, error) {
	discordGuildID, err := c.guilds.DiscordID(ctx, ticket.GuildID)
	if err != nil {
		return nil, errors.Join(ErrQueueNotSent, err)
	}
	if err := c.bot().ValidateStaffChannel(ctx, discordGuildID, settings.QueueChannelDiscordID); err != nil {
		return nil, errors.Join(ErrQueueNotSent, err)
	}
	message := queuePostMessage(ticket, transcript)
	var sent *discordgo.Message
	if ticket.LogMessageDiscordID != "" && ticket.LogChannelDiscordID == settings.QueueChannelDiscordID {
		edit := c.messageEdit(ctx, settings.QueueChannelDiscordID, ticket.LogMessageDiscordID, message)
		if transcript != nil {
			// New files are added to a post's existing ones; keep only this
			// upload so a retry or adopted post ends with one transcript.
			edit.Attachments = &[]*discordgo.MessageAttachment{{ID: "0", Filename: edit.Files[0].Name}}
		}
		sent, err = c.session.ChannelMessageEditComplex(edit, rest(ctx)...)
	} else {
		sent, err = c.bot().Send(ctx, settings.QueueChannelDiscordID, message)
	}
	if isRESTCode(err, discordgo.ErrCodeUnknownMessage) {
		return nil, ErrQueueMessageMissing
	}
	if err != nil {
		return nil, queueSendError(err)
	}
	if sent == nil || sent.ID == "" {
		return nil, errors.New("ticket queue message was not returned")
	}
	if transcript != nil && len(sent.Attachments) == 0 {
		return nil, errors.New("ticket transcript attachment was not returned")
	}
	return &QueueReceipt{MessageID: sent.ID, URL: messageURL(discordGuildID, settings.QueueChannelDiscordID, sent.ID)}, nil
}

// QueueMessageExists reads a saved queue post. Only Discord saying the
// message or channel is gone reports false; any other failure is an error,
// so the thread is kept.
func (c channels) QueueMessageExists(ctx context.Context, channelID, messageID string) (bool, error) {
	_, err := c.session.ChannelMessage(channelID, messageID, rest(ctx)...)
	if isRESTCode(err, discordgo.ErrCodeUnknownMessage, discordgo.ErrCodeUnknownChannel) {
		return false, nil
	}
	return err == nil, err
}

// queueSendError marks a send Discord definitely refused with
// ErrQueueNotSent. Anything else, such as a lost response, may have been
// delivered.
func queueSendError(err error) error {
	if _, ok := errors.AsType[*discordgo.RateLimitError](err); ok {
		return errors.Join(ErrQueueNotSent, err)
	}
	if restErr, ok := errors.AsType[*discordgo.RESTError](err); ok && restErr.Response != nil {
		switch restErr.Response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return errors.Join(ErrQueueNotSent, err)
		}
	}
	return err
}

// ValidateQueueMessage checks, with fresh reads, that link is a post
// by this bot in the ticket's recorded queue channel in its guild, carrying
// this ticket's controls.
func (c channels) ValidateQueueMessage(ctx context.Context, ticket *Ticket, link string) (*QueueReceipt, error) {
	if ticket.ID == "" || ticket.LogChannelDiscordID == "" || len(link) > 2048 {
		return nil, ErrInvalidQueueReceipt
	}
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, ErrInvalidQueueReceipt
	}
	ref, err := quack.ParseDiscordMessageLink(link)
	if err != nil || ref.ChannelID != ticket.LogChannelDiscordID {
		return nil, ErrInvalidQueueReceipt
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	discordGuildID, err := c.guilds.DiscordID(ctx, ticket.GuildID)
	if err != nil {
		return nil, errQueueUnverifiable
	}
	if ref.GuildID != discordGuildID {
		return nil, ErrInvalidQueueReceipt
	}
	message, err := c.session.ChannelMessage(ref.ChannelID, ref.MessageID, rest(ctx)...)
	if err != nil {
		return nil, errQueueUnverifiable
	}
	if message.ID != ref.MessageID || message.ChannelID != ref.ChannelID || message.Author == nil ||
		message.WebhookID != "" || (message.GuildID != "" && message.GuildID != discordGuildID) {
		return nil, ErrInvalidQueueReceipt
	}
	// Message responses may omit guild_id, so check the channel's guild
	// rather than trust the link.
	channel, err := c.session.Channel(ref.ChannelID, rest(ctx)...)
	if err != nil {
		return nil, errQueueUnverifiable
	}
	if channel.ID != ref.ChannelID || channel.GuildID != discordGuildID {
		return nil, ErrInvalidQueueReceipt
	}
	bot, err := c.session.User("@me", rest(ctx)...)
	if err != nil {
		return nil, errQueueUnverifiable
	}
	if bot.ID == "" || message.Author.ID != bot.ID || !queueControlsMatch(message.Components, ticket.ID) {
		return nil, ErrInvalidQueueReceipt
	}
	return &QueueReceipt{MessageID: message.ID, URL: messageURL(discordGuildID, ref.ChannelID, message.ID)}, nil
}

// queueControl is the routing part of a component, at any nesting depth.
type queueControl struct {
	CustomID   string         `json:"custom_id"`
	Components []queueControl `json:"components"`
	Accessory  *queueControl  `json:"accessory"`
}

// queueControlsMatch reports whether components are an open or closed
// queue post's controls for ticketID: a view button, optionally close, and
// nothing routed anywhere else. Message text and file names prove nothing.
func queueControlsMatch(components []discordgo.MessageComponent, ticketID string) bool {
	raw, err := json.Marshal(components)
	if err != nil {
		return false
	}
	var controls []queueControl
	if json.Unmarshal(raw, &controls) != nil {
		return false
	}
	foundView := false
	var check func([]queueControl) bool
	check = func(items []queueControl) bool {
		for _, item := range items {
			if item.CustomID != "" {
				id, err := discord.DecodeCustomID(item.CustomID)
				if err != nil || id.Namespace != componentNamespace || id.Version != "v1" || id.Payload != ticketID ||
					(id.Action != "view" && id.Action != "close") {
					return false
				}
				foundView = foundView || id.Action == "view"
			}
			if !check(item.Components) {
				return false
			}
			if item.Accessory != nil && !check([]queueControl{*item.Accessory}) {
				return false
			}
		}
		return true
	}
	return check(controls) && foundView
}

// DeliverCloseNotice DMs the member their transcript, naming the server
// and ticket but never linking staff channels. With reconcileOnly it only
// searches the DM channel for an earlier notice whose send was uncertain.
func (c channels) DeliverCloseNotice(ctx context.Context, ticket *Ticket, transcript *Transcript, reconcileOnly bool) (string, error) {
	channel, err := c.session.UserChannelCreate(ticket.OwnerDiscordUserID, rest(ctx)...)
	if err != nil {
		if reconcileOnly {
			return "", err
		}
		return "", errors.Join(ErrCloseNoticeNotSent, err)
	}
	filename := transcriptFilename(ticket.ID)
	if reconcileOnly {
		return c.findCloseNotice(ctx, ticket, channel.ID, filename)
	}
	discordGuildID, err := c.guilds.DiscordID(ctx, ticket.GuildID)
	if err != nil {
		return "", errors.Join(ErrCloseNoticeNotSent, err)
	}
	guild, err := c.session.State.Guild(discordGuildID)
	if err != nil {
		guild, err = c.session.Guild(discordGuildID, rest(ctx)...)
	}
	if err != nil {
		return "", errors.Join(ErrCloseNoticeNotSent, fmt.Errorf("fetch guild name: %w", err))
	}
	sent, err := c.bot().Send(ctx, channel.ID, closeNoticeMessage(guild.Name, ticket.ID, transcript))
	if err != nil {
		if errors.Is(queueSendError(err), ErrQueueNotSent) {
			return "", errors.Join(ErrCloseNoticeNotSent, err)
		}
		return "", err
	}
	if sent == nil || sent.ID == "" || len(sent.Attachments) == 0 {
		return "", errors.New("member transcript receipt unavailable")
	}
	return sent.ID, nil
}

// findCloseNotice looks through the DM channel, newest first and back to
// the ticket's close, for a notice the bot sent with the transcript.
func (c channels) findCloseNotice(ctx context.Context, ticket *Ticket, channelID, filename string) (string, error) {
	botID, err := c.botID(ctx)
	if err != nil {
		return "", err
	}
	before := ""
	for {
		messages, err := c.session.ChannelMessages(channelID, messagePage, before, "", "", rest(ctx)...)
		if err != nil {
			return "", err
		}
		for _, message := range messages {
			if message.Author == nil || message.Author.ID != botID || !strings.Contains(message.Content, "Ticket "+ticket.ID) {
				continue
			}
			for _, attachment := range message.Attachments {
				if attachment.Filename == filename {
					return message.ID, nil
				}
			}
		}
		if len(messages) < messagePage {
			break
		}
		last := messages[len(messages)-1]
		if ticket.ResolvedAt != nil && last.Timestamp.Before(*ticket.ResolvedAt) {
			break
		}
		before = last.ID
	}
	return "", errors.New("member close notice delivery is unconfirmed")
}

// messageURL links a message in a guild channel.
func messageURL(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}
