package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

func init() {
	services.Commands[userCmd.Name] = &services.Command{
		ApplicationCommand: userCmd,
		Handler:            handleUser,
	}
}

var userCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "user",
	Description:              "Get a users moderation profile view",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to get the moderation profile view for",
			Required:    true,
		},
	},
}

func handleUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	user := i.ApplicationCommandData().Options[0].UserValue(s)

	go func() {
		// try to get guild member for nickname and join date
		member, _ := s.GuildMember(i.GuildID, user.ID)

		// get account creation time
		createdAt, _ := lib.GetUserCreationTime(user.ID)

		// get the users info from the database
		casesCount, err := storage.CountCasesByUserID(user.ID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to count cases by user id", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to count cases by user id")}})
			return
		}

		// get the most recent case for this user
		cases, err := storage.FindCasesByUserIDPaginated(user.ID, i.GuildID, 1, 0)
		if err != nil {
			log.Error().AnErr("Failed to find cases by user id", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to find cases by user id")}})
			return
		}

		notes, err := storage.FindNoteByUserID(user.ID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to find notes by user id", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to find notes by user id")}})
			return
		}

		appeals, err := storage.FindAppealsByUserID(user.ID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to find appeals by user id", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to find appeals by user id")}})
			return
		}

		ticketsCount, err := storage.GetUsersTotalTicketsCount(user.ID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to get users total tickets count", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to get users total tickets count")}})
			return
		}

		// build user info section
		nickname := "None"
		joinedAt := "Unknown"
		if member != nil {
			if member.Nick != "" {
				nickname = member.Nick
			}
			if !member.JoinedAt.IsZero() {
				joinedAt = fmt.Sprintf("<t:%d:R>", member.JoinedAt.Unix())
			}
		}

		userInfo := fmt.Sprintf(
			"**ID:** `%s`\n**Username:** %s\n**Nickname:** %s\n**Account Created:** <t:%d:R>\n**Joined Server:** %s",
			user.ID,
			user.Username,
			nickname,
			createdAt.Unix(),
			joinedAt,
		)

		// build last case info
		lastCaseInfo := "None"
		if len(cases) > 0 {
			lastCase := cases[0]
			lastCaseInfo = formatLastCase(lastCase)
		}

		// build last note info
		lastNoteInfo := "None"
		if len(notes) > 0 {
			lastNote := notes[0]
			lastNoteInfo = formatLastNote(lastNote)
		}

		embed := components.NewEmbed().
			SetAuthor(fmt.Sprintf("%s's Profile", user.Username), user.AvatarURL("")).
			SetThumbnail(user.AvatarURL("512")).
			SetDescription(userInfo).
			AddField("Cases", fmt.Sprintf("%d", casesCount)).
			AddField("Notes", fmt.Sprintf("%d", len(notes))).
			AddField("Appeals", fmt.Sprintf("%d", len(appeals))).
			AddField("Tickets", fmt.Sprintf("%d", ticketsCount)).
			AddField("Last Case", lastCaseInfo).
			AddField("Last Note", lastNoteInfo).
			SetColor("NotQuiteBlack").
			InlineAllFields().
			MessageEmbed

		// make the last case and last note fields not inline (full width)
		if len(embed.Fields) >= 5 {
			embed.Fields[4].Inline = false
		}
		if len(embed.Fields) >= 6 {
			embed.Fields[5].Inline = false
		}

		// build action buttons
		buttons := []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						CustomID: fmt.Sprintf("user-view-cases:%s", user.ID),
						Label:    "View Cases",
						Style:    discordgo.SecondaryButton,
					},
					discordgo.Button{
						CustomID: fmt.Sprintf("user-view-notes:%s", user.ID),
						Label:    "View Notes",
						Style:    discordgo.SecondaryButton,
					},
					discordgo.Button{
						CustomID: fmt.Sprintf("user-copy-id:%s", user.ID),
						Label:    "Copy User ID",
						Style:    discordgo.SecondaryButton,
					},
				},
			},
		}

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &buttons})
	}()

	return LoadingResponse()
}

func formatLastCase(c *structs.Case) string {
	parsedTime, _ := time.Parse("2006-01-02 15:04:05", c.CreatedAt)
	unixTime := parsedTime.Unix()

	typeStr := "Case"
	switch c.Type {
	case 0:
		typeStr = "Warn"
	case 1:
		typeStr = "Ban"
	case 2:
		typeStr = "Kick"
	case 3:
		typeStr = "Unban"
	case 4:
		typeStr = "Timeout"
	case 5:
		typeStr = "Message Delete"
	}

	reason := c.Reason
	if len(reason) > 50 {
		reason = reason[:47] + "..."
	}

	return fmt.Sprintf("<:text3:1229350410293350471> `%s`\n<:text2:1229344477131309136> %s <t:%d:R>\n<:text:1229343822337802271> `%s`", c.ID, typeStr, unixTime, reason)
}

func formatLastNote(n *structs.Note) string {
	parsedTime, _ := time.Parse("2006-01-02 15:04:05", n.CreatedAt)
	unixTime := parsedTime.Unix()

	content := n.Content
	if len(content) > 50 {
		content = content[:47] + "..."
	}

	return fmt.Sprintf("<:text3:1229350410293350471> `%s`\n<:text2:1229344477131309136> <t:%d:R>\n<:text:1229343822337802271> `%s`", n.ID, unixTime, content)
}
