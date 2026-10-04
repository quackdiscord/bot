package discord

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// launchAnnouncementBody is the v5 announcement: what changed, the three
// things staff should do first, and where to learn more. The removed
// commands are in code spans so they are never resolved as live commands.
var launchAnnouncementBody = "## " + discordtext.Icon("duck") + " Quack v5 is here\n" +
	"Quack has been rebuilt, and moderation works differently now.\n\n" +
	"**What changed**\n" +
	"`/warn`, `/timeout`, `/kick`, and `/ban` are gone. Instead, your server has **rules**. " +
	"A rule says what happens when someone breaks it, and how that gets stricter if they keep doing it. " +
	"Moderators run /case add, pick the member and the rule, and Quack picks the punishment.\n\n" +
	"**What to do now**\n" +
	"1. Check your starter rule, **General rule violation**: a warning, then a 24-hour timeout at 3 cases, then a ban at 5. Change it or add your own in the dashboard.\n" +
	"2. Run /setup appeals to choose where appeals go.\n" +
	"3. Make sure Quack's role is above the roles of the people it moderates.\n\n" +
	"Cases from before v5 are kept as history and don't count toward the new punishments.\n" +
	"-# Questions? Run /help or read the docs."

// launchAnnouncement returns the announcement for one guild, with buttons
// to the getting-started docs and the guild's dashboard when there is one.
func launchAnnouncement(links quack.DashboardLinks, discordGuildID string) Message {
	message := Content(launchAnnouncementBody, false)
	var buttons []discordgo.MessageComponent
	buttons = appendLink(buttons, links.Docs("getting-started"), "Read the docs")
	buttons = appendLink(buttons, links.Staff(discordGuildID), "Open dashboard")
	if len(buttons) > 0 {
		message.Components = []discordgo.MessageComponent{Row(buttons...)}
	}
	return message
}

// SendLaunchAnnouncement posts the v5 announcement where the guild's staff
// will see it: the audit channel, then Discord's community updates
// channel, then the system messages channel, and finally a DM to the
// owner. It stops at the first that works and returns every failure when
// none does.
func (b *Bot) SendLaunchAnnouncement(ctx context.Context, target quack.LaunchAnnouncementTarget) error {
	message := launchAnnouncement(b.Dashboard, target.DiscordGuildID)
	var failures []error
	for _, channelID := range b.announcementChannels(ctx, target) {
		if _, err := b.Send(ctx, channelID, message); err == nil {
			return nil
		} else {
			failures = append(failures, classify("announcement_send", err, false))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if owner := strings.TrimSpace(target.OwnerDiscordUserID); owner != "" {
		channel, err := b.Session.UserChannelCreate(owner, rest(ctx)...)
		if err == nil {
			_, err = b.Send(ctx, channel.ID, message)
		}
		if err == nil {
			return nil
		}
		failures = append(failures, classify("announcement_dm", err, false))
	}
	if len(failures) == 0 {
		return errors.New("no channel or owner to announce to")
	}
	return errors.Join(failures...)
}

// announcementChannels lists the guild's staff-facing channels in the
// order SendLaunchAnnouncement tries them, without blanks or repeats. A
// guild Discord cannot describe right now leaves just the audit channel.
func (b *Bot) announcementChannels(ctx context.Context, target quack.LaunchAnnouncementTarget) []string {
	candidates := []string{target.AuditMirrorChannelDiscordID}
	if guild, err := b.authorizationGuild(ctx, target.DiscordGuildID); err == nil {
		candidates = append(candidates, guild.PublicUpdatesChannelID, guild.SystemChannelID)
	}
	var channels []string
	for _, id := range candidates {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(channels, id) {
			channels = append(channels, id)
		}
	}
	return channels
}
