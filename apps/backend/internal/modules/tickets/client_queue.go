package tickets

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// PublishQueue posts the ticket to the staff queue, or edits its existing
// post in place; with a transcript, the post says the ticket closed and
// carries the transcript file. The queue channel must still be staff-only
// before every send.
func (c channels) PublishQueue(ctx context.Context, ticket *Ticket, settings Settings, transcript *Transcript) (*QueueReceipt, error) {
	discordGuildID, err := c.guilds.DiscordID(ctx, ticket.GuildID)
	if err != nil {
		return nil, err
	}
	if err := c.bot().ValidateStaffChannel(ctx, discordGuildID, settings.QueueChannelDiscordID); err != nil {
		return nil, err
	}
	message := queuePostMessage(ticket, transcript)
	var sent *discordgo.Message
	if ticket.LogMessageDiscordID != "" && ticket.LogChannelDiscordID == settings.QueueChannelDiscordID {
		edit := c.messageEdit(ctx, settings.QueueChannelDiscordID, ticket.LogMessageDiscordID, message)
		if transcript != nil {
			// New files are added to a post's existing ones; keep only this
			// upload so a retried close ends with one transcript.
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
		return nil, err
	}
	if sent == nil || sent.ID == "" {
		return nil, errors.New("ticket queue message was not returned")
	}
	if transcript != nil && len(sent.Attachments) == 0 {
		return nil, errors.New("ticket transcript attachment was not returned")
	}
	return &QueueReceipt{MessageID: sent.ID, URL: messageURL(discordGuildID, settings.QueueChannelDiscordID, sent.ID)}, nil
}

// definitelyRefused reports whether Discord certainly rejected a send.
// Anything else, such as a lost response, may have been delivered.
func definitelyRefused(err error) bool {
	if _, ok := errors.AsType[*discordgo.RateLimitError](err); ok {
		return true
	}
	if restErr, ok := errors.AsType[*discordgo.RESTError](err); ok && restErr.Response != nil {
		switch restErr.Response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return true
		}
	}
	return false
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
		if definitelyRefused(err) {
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
