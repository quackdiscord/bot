package components

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	Components["appeal-open"] = handleAppealOpen
	Components["appeal-accept"] = handleAppealAccept
	Components["appeal-reject"] = handleAppealReject
}

// handleAppealOpen shows a modal for the user to submit an appeal
func handleAppealOpen(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	log.Debug().Msgf("[handleAppealOpen] invoked; GuildID: %s", i.GuildID)
	// data.CustomID: "appeal-open:<guildID>"
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, ":", 2)
	guildID := i.GuildID
	if len(parts) == 2 {
		// if triggered from a DM, guildID will be empty; parse from custom id
		if guildID == "" {
			guildID = parts[1]
		}
	}
	log.Debug().Msgf("[handleAppealOpen] resolved guildID: %s", guildID)

	settings, err := storage.FindAppealSettingsByGuildID(guildID)
	if err != nil || settings == nil {
		if err != nil {
			log.Debug().Msgf("[handleAppealOpen] storage error: %s", err.Error())
		}
		data := &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{ErrorEmbed("Appeals are not configured for this server.")}}
		if i.GuildID != "" {
			data.Flags = discordgo.MessageFlagsEphemeral
		}
		return ComplexResponse(data)
	}

	// Discord limits: TextInput Label max ~45, Placeholder max ~100. Keep static label and truncate placeholder.
	placeholder := settings.Message
	if len(placeholder) > 100 {
		placeholder = placeholder[:100]
	}
	log.Debug().Msgf("[handleAppealOpen] building modal with placeholder length: %d", len(placeholder))

	// Build modal
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "appeal-modal:" + guildID,
			Title:    "Ban Appeal",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:    "appeal-message",
						Label:       "Appeal message",
						Style:       discordgo.TextInputParagraph,
						Placeholder: placeholder,
						Required:    true,
						MaxLength:   2000,
					},
				}},
			},
		},
	}
}

// handleAppealSubmit processes a submitted modal
func handleAppealSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	log.Debug().Msgf("[handleAppealSubmit] invoked")
	// Registered in events router for modal submissions
	data := i.ModalSubmitData()
	log.Debug().Msgf("[handleAppealSubmit] components top-level count: %d", len(data.Components))
	parts := strings.SplitN(data.CustomID, ":", 2)
	if len(parts) != 2 {
		log.Debug().Msgf("[handleAppealSubmit] invalid custom id: %s", data.CustomID)
		return
	}
	guildID := parts[1]
	log.Debug().Msgf("[handleAppealSubmit] guildID: %s", guildID)

	// fetch settings
	settings, err := storage.FindAppealSettingsByGuildID(guildID)
	if err != nil || settings == nil {
		if err != nil {
			log.Debug().Msgf("[handleAppealSubmit] storage error: %s", err.Error())
		}
		s.InteractionRespond(i.Interaction, ComplexResponse(&discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{ErrorEmbed("Appeals are not configured for this server.")},
		}))
		return
	}

	// check if the user already has an appeal pending for this server
	appeals, err := storage.FindOpenAndRejectedAppealsByUserID(i.User.ID, guildID)
	if err != nil {
		log.Error().AnErr("Failed to find appeals", err)
	}
	if len(appeals) > 0 {
		// user already has an appeal pending or has been rejected
		s.InteractionRespond(i.Interaction, ComplexResponse(&discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{ErrorEmbed("You either have an appeal pending or have been rejected. Please wait for a review.")},
		}))
		return
	}

	// Get user input
	var userInput string
	// Note: older discordgo versions do not expose a Values map; iterate components below
	for idx, c := range data.Components {
		log.Debug().Msgf("[handleAppealSubmit] row %d type: %T", idx, c)
		if row, ok := c.(discordgo.ActionsRow); ok {
			log.Debug().Msgf("[handleAppealSubmit] row has %d components (value)", len(row.Components))
			for j, comp := range row.Components {
				log.Debug().Msgf("[handleAppealSubmit]  comp %d type: %T", j, comp)
				if ti, ok := comp.(discordgo.TextInput); ok {
					log.Debug().Msgf("[handleAppealSubmit]  found TextInput (value) id=%s len(value)=%d", ti.CustomID, len(ti.Value))
					if ti.CustomID == "appeal-message" {
						userInput = ti.Value
					}
				} else if tip, ok := comp.(*discordgo.TextInput); ok {
					log.Debug().Msgf("[handleAppealSubmit]  found *TextInput (ptr) id=%s len(value)=%d", tip.CustomID, len(tip.Value))
					if tip.CustomID == "appeal-message" {
						userInput = tip.Value
					}
				}
			}
			continue
		}
		if rowp, ok := c.(*discordgo.ActionsRow); ok {
			log.Debug().Msgf("[handleAppealSubmit] row has %d components (ptr)", len(rowp.Components))
			for j, comp := range rowp.Components {
				log.Debug().Msgf("[handleAppealSubmit]  comp %d type: %T", j, comp)
				if ti, ok := comp.(discordgo.TextInput); ok {
					log.Debug().Msgf("[handleAppealSubmit]  found TextInput (value) id=%s len(value)=%d", ti.CustomID, len(ti.Value))
					if ti.CustomID == "appeal-message" {
						userInput = ti.Value
					}
				} else if tip, ok := comp.(*discordgo.TextInput); ok {
					log.Debug().Msgf("[handleAppealSubmit]  found *TextInput (ptr) id=%s len(value)=%d", tip.CustomID, len(tip.Value))
					if tip.CustomID == "appeal-message" {
						userInput = tip.Value
					}
				}
			}
			continue
		}
	}
	log.Debug().Msgf("[handleAppealSubmit] collected userInput length: %d", len(userInput))

	if userInput == "" {
		s.InteractionRespond(i.Interaction, ComplexResponse(&discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{ErrorEmbed("Please provide a message.")},
		}))
		return
	}

	// Save appeal
	id, _ := lib.GenID()
	appeal := &structs.Appeal{
		ID:      id,
		GuildID: guildID,
		UserID:  i.User.ID,
		Content: userInput,
		Status:  0,
	}
	if err := storage.CreateAppeal(appeal); err != nil {
		log.Error().AnErr("Failed to create appeal", err)
		log.Debug().Msgf("[handleAppealSubmit] CreateAppeal error: %s", err.Error())
		s.InteractionRespond(i.Interaction, ComplexResponse(&discordgo.InteractionResponseData{
			Flags:  discordgo.MessageFlagsEphemeral,
			Embeds: []*discordgo.MessageEmbed{ErrorEmbed("Failed to submit appeal. Please try again later.")},
		}))
		return
	}

	// Send to staff channel
	// get the users latest case in the server
	cases, err := storage.FindCasesByUserID(i.User.ID, guildID)
	if err != nil {
		log.Error().AnErr("Failed to find cases", err)
	}
	if len(cases) == 0 {
		log.Debug().Msgf("[handleAppealSubmit] no cases found for user: %s", i.User.ID)
	}

	embedDescription := fmt.Sprintf("<@%s> submitted a ban appeal:\n```%s```", i.User.ID, userInput)
	if len(cases) > 0 && cases[0].Type == 1 {
		// has cases and latest case is a ban
		embedDescription += "\n\nBanned for: `" + cases[0].Reason + "`\n-# Case ID: `" + cases[0].ID + "`"
	}

	embed := NewEmbed().SetDescription(embedDescription).
		SetColor("Main").
		SetAuthor(i.User.Username, i.User.AvatarURL("")).
		SetTimestamp().
		MessageEmbed

	msg, err := s.ChannelMessageSendComplex(settings.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{CustomID: "appeal-accept:" + id, Label: "Accept", Style: discordgo.SuccessButton},
				discordgo.Button{CustomID: "appeal-reject:" + id, Label: "Reject", Style: discordgo.DangerButton},
			}},
		},
	})
	if err == nil {
		_ = storage.SetAppealReviewMessage(id, msg.ID)
	} else {
		log.Debug().Msgf("[handleAppealSubmit] ChannelMessageSendComplex error: %s", err.Error())
	}

	// Ack user
	ackEmbed := NewEmbed().
		SetDescription("Your appeal has been submitted. A moderator will review it soon.").
		SetColor("Main").
		MessageEmbed
	ack := &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{ackEmbed}}
	if i.GuildID != "" {
		ack.Flags = discordgo.MessageFlagsEphemeral
	}
	s.InteractionRespond(i.Interaction, ComplexResponse(ack))
}

func handleAppealAccept(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// permission check
	if i.Member == nil || (i.Member.Permissions&discordgo.PermissionBanMembers) != discordgo.PermissionBanMembers {
		return ContentResponse(config.Bot.ErrMsgPrefix+"You don't have permission to accept appeals.", true)
	}
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, ":", 2)
	if len(parts) != 2 {
		return ContentResponse(config.Bot.ErrMsgPrefix+"Invalid appeal.", true)
	}
	appealID := parts[1]

	// unban the user; need to fetch user & guild from message context
	// The staff review message is in the configured guild/channel; Interaction has GuildID
	// fetch the appeal to get user id
	// lightweight: query for user_id and guild_id
	var userID, guildID string
	var appealContent string
	row := services.DB.QueryRow("SELECT user_id, guild_id, content FROM appeals WHERE id = ?", appealID)
	if err := row.Scan(&userID, &guildID, &appealContent); err != nil {
		return ContentResponse(config.Bot.ErrMsgPrefix+"Failed to look up appeal.", true)
	}

	// try to unban
	if err := s.GuildBanDelete(guildID, userID); err != nil {
		log.Error().AnErr("Failed to unban on appeal accept", err)
		log.Debug().Msgf("[handleAppealAccept] GuildBanDelete error: %s", err.Error())
		return ContentResponse(config.Bot.ErrMsgPrefix+"Failed to unban user.", true)
	}

	_ = storage.UpdateAppealStatus(appealID, 1, i.Member.User.ID)

	// generate an invite link
	invites, err := s.GuildInvites(guildID)
	if err != nil {
		log.Error().AnErr("Failed to generate invite link", err)
	}
	inviteLink := "https://discord.gg/" + invites[0].Code

	// DM user
	_ = utils.DMUser(userID, "## ✅ Your appeal was accepted.\nYou can now [rejoin]("+inviteLink+") the server.", s)

	// disable buttons but keep the original embed
	embed := NewEmbed().
		SetDescription(fmt.Sprintf("<@%s>'s appeal was accepted by <@%s>.\n```%s```", userID, i.Member.User.ID, appealContent)).
		SetColor("Green").
		SetTimestamp().
		SetAuthor(i.Member.User.Username, i.Member.User.AvatarURL("")).
		MessageEmbed

	return UpdateResponse(&discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{CustomID: "appeal-accept:" + appealID, Label: "Accept", Style: discordgo.SuccessButton, Disabled: true},
				discordgo.Button{CustomID: "appeal-reject:" + appealID, Label: "Reject", Style: discordgo.DangerButton, Disabled: true},
			}},
		},
	})
}

func handleAppealReject(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// permission check
	if i.Member == nil || (i.Member.Permissions&discordgo.PermissionBanMembers) != discordgo.PermissionBanMembers {
		return ContentResponse(config.Bot.ErrMsgPrefix+"You don't have permission to reject appeals.", true)
	}
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, ":", 2)
	if len(parts) != 2 {
		return ContentResponse(config.Bot.ErrMsgPrefix+"Invalid appeal.", true)
	}
	appealID := parts[1]

	_ = storage.UpdateAppealStatus(appealID, 2, i.Member.User.ID)

	// DM user
	var userID string
	var appealContent string
	row := services.DB.QueryRow("SELECT user_id, content FROM appeals WHERE id = ?", appealID)
	_ = row.Scan(&userID, &appealContent)
	if userID != "" {
		_ = utils.DMUserEmbed(userID, NewEmbed().
			SetDescription("❌ Your appeal was rejected. Please contact a moderator if you believe this is an error.").
			SetColor("Red").SetTimestamp().MessageEmbed, s)
	}

	// disable buttons but keep the original embed
	embed := NewEmbed().
		SetDescription(fmt.Sprintf("<@%s>'s appeal was rejected by <@%s>.\n```%s```", userID, i.Member.User.ID, appealContent)).
		SetColor("Red").
		SetAuthor(i.Member.User.Username, i.Member.User.AvatarURL("")).
		SetTimestamp().
		MessageEmbed

	return UpdateResponse(&discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{CustomID: "appeal-accept:" + appealID, Label: "Accept", Style: discordgo.SuccessButton, Disabled: true},
				discordgo.Button{CustomID: "appeal-reject:" + appealID, Label: "Reject", Style: discordgo.DangerButton, Disabled: true},
			}},
		},
	})
}
