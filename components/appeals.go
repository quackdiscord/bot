package components

import (
	"database/sql"
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

// appealData holds parsed appeal information for accept/reject handlers
type appealData struct {
	ID         string
	UserID     string
	GuildID    string
	Content    string
	CaseID     string
	BanCase    *structs.Case
	Guild      *discordgo.Guild
	InviteLink string
}

// extractModalInput parses TextInput from modal submission data
func extractModalInput(data discordgo.ModalSubmitInteractionData, targetID string) string {
	for idx, c := range data.Components {
		log.Debug().Msgf("[extractModalInput] row %d type: %T", idx, c)

		// Handle both value and pointer ActionsRow types
		var components []discordgo.MessageComponent
		if row, ok := c.(discordgo.ActionsRow); ok {
			components = row.Components
		} else if rowp, ok := c.(*discordgo.ActionsRow); ok {
			components = rowp.Components
		} else {
			continue
		}

		log.Debug().Msgf("[extractModalInput] row has %d components", len(components))
		for j, comp := range components {
			log.Debug().Msgf("[extractModalInput] comp %d type: %T", j, comp)

			// Handle both value and pointer TextInput types
			var textInput *discordgo.TextInput
			if ti, ok := comp.(discordgo.TextInput); ok {
				textInput = &ti
			} else if tip, ok := comp.(*discordgo.TextInput); ok {
				textInput = tip
			} else {
				continue
			}

			log.Debug().Msgf("[extractModalInput] found TextInput id=%s len(value)=%d", textInput.CustomID, len(textInput.Value))
			if textInput.CustomID == targetID {
				return textInput.Value
			}
		}
	}
	return ""
}

// respondWithError sends an ephemeral error response
func respondWithError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	s.InteractionRespond(i.Interaction, ComplexResponse(&discordgo.InteractionResponseData{
		Flags:  discordgo.MessageFlagsEphemeral,
		Embeds: []*discordgo.MessageEmbed{ErrorEmbed(message)},
	}))
}

// findLatestBanCase retrieves the user's most recent ban case in the guild
func findLatestBanCase(userID, guildID string) *string {
	cases, err := storage.FindCasesByUserID(userID, guildID)
	if err != nil {
		log.Error().AnErr("Failed to find cases for association", err)
		return nil
	}
	if len(cases) > 0 && cases[0].Type == 1 { // 1 = ban
		return &cases[0].ID
	}
	return nil
}

// buildStaffEmbed creates the embed sent to staff channel for review
func buildStaffEmbed(userID, content string, banCase *structs.Case, caseID *string) *discordgo.MessageEmbed {
	embedDescription := fmt.Sprintf("<@%s> submitted a ban appeal:\n```%s```", userID, content)
	if caseID != nil && banCase != nil {
		embedDescription += fmt.Sprintf("\n\nBanned for: `%s`\n-# <:text:1229343822337802271> Case ID: `%s`", banCase.Reason, *caseID)
	}

	return NewEmbed().
		SetDescription(embedDescription).
		SetColor("Main").
		SetAuthor("Ban Appeal", "").
		SetTimestamp().
		MessageEmbed
}

// updateOriginalDMMessage edits the original ban DM to show appeal submitted
func updateOriginalDMMessage(s *discordgo.Session, channelID, messageID, guildID string) {
	if channelID == "" || messageID == "" {
		return
	}

	origMsg, err := s.ChannelMessage(channelID, messageID)
	if err != nil {
		log.Debug().Msgf("[updateOriginalDMMessage] failed to fetch original message: %s", err.Error())
		return
	}

	if len(origMsg.Embeds) == 0 {
		return
	}

	disabledComponents := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Appeal Submitted",
				Style:    discordgo.SuccessButton,
				CustomID: "appeal-open:" + guildID,
				Disabled: true,
			},
		}},
	}

	embeds := []*discordgo.MessageEmbed{origMsg.Embeds[0]}
	_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:         messageID,
		Channel:    channelID,
		Embeds:     &embeds,
		Components: &disabledComponents,
	})
}

// parseAppealData fetches and processes appeal data for accept/reject handlers
func parseAppealData(s *discordgo.Session, appealID string) (*appealData, error) {
	var userID, guildID, content, caseID string
	row := services.DB.QueryRow("SELECT user_id, guild_id, content, case_id FROM appeals WHERE id = ?", appealID)
	if err := row.Scan(&userID, &guildID, &content, &caseID); err != nil {
		return nil, err
	}

	data := &appealData{
		ID:      appealID,
		UserID:  userID,
		GuildID: guildID,
		Content: content,
		CaseID:  caseID,
	}

	// Fetch ban case info
	banCase, err := storage.FindCaseByID(caseID, guildID)
	if err != nil {
		log.Error().AnErr("Failed to find ban case", err)
		// Fallback case data
		banCase = &structs.Case{
			ID:      caseID,
			Reason:  "Not Found",
			GuildID: guildID,
		}
	}
	data.BanCase = banCase

	// Fetch guild info
	guild, err := s.Guild(guildID)
	if err != nil {
		log.Error().AnErr("Failed to get guild", err)
	} else {
		data.Guild = guild
	}

	// Generate invite link for accepted appeals
	invites, err := s.GuildInvites(guildID)
	if err != nil {
		log.Error().AnErr("Failed to generate invite link", err)
	} else if len(invites) > 0 {
		data.InviteLink = "https://discord.gg/" + invites[0].Code
	}

	return data, nil
}

// buildReviewEmbed creates the updated embed after appeal review
func buildReviewEmbed(data *appealData, reviewerName, reviewerAvatar, color string) *discordgo.MessageEmbed {
	description := fmt.Sprintf("<@%s> submitted an appeal.\n```%s```", data.UserID, data.Content)
	if data.BanCase != nil {
		description += fmt.Sprintf("\n\nBanned for: `%s`\n-# <:text:1229343822337802271> Case ID: `%s`", data.BanCase.Reason, data.CaseID)
	}

	return NewEmbed().
		SetDescription(description).
		SetColor(color).
		SetAuthor(fmt.Sprintf("Reviewed by %s", reviewerName), reviewerAvatar).
		SetTimestamp().
		MessageEmbed
}

// handleAppealOpen shows a modal for the user to submit an appeal
func handleAppealOpen(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	log.Debug().Msgf("[handleAppealOpen] invoked; GuildID: %s", i.GuildID)

	// Parse guild ID from custom ID (for DM interactions)
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, ":", 2)
	guildID := i.GuildID
	if len(parts) == 2 && guildID == "" {
		guildID = parts[1]
	}
	log.Debug().Msgf("[handleAppealOpen] resolved guildID: %s", guildID)

	// Check if appeals are configured
	settings, err := storage.FindAppealSettingsByGuildID(guildID)
	if err != nil || settings == nil {
		if err != nil {
			log.Debug().Msgf("[handleAppealOpen] storage error: %s", err.Error())
		}
		responseData := &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{ErrorEmbed("Appeals are not configured for this server.")},
		}
		if i.GuildID != "" {
			responseData.Flags = discordgo.MessageFlagsEphemeral
		}
		return ComplexResponse(responseData)
	}

	// Prepare modal placeholder (Discord limit: 100 chars)
	placeholder := settings.Message
	if len(placeholder) > 100 {
		placeholder = placeholder[:100]
	}
	log.Debug().Msgf("[handleAppealOpen] building modal with placeholder length: %d", len(placeholder))

	// Build and return modal
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "appeal-modal:" + guildID + ":" + i.ChannelID + ":" + i.Message.ID,
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

	// Parse modal custom ID
	data := i.ModalSubmitData()
	log.Debug().Msgf("[handleAppealSubmit] components top-level count: %d", len(data.Components))
	parts := strings.Split(data.CustomID, ":")
	if len(parts) < 2 {
		log.Debug().Msgf("[handleAppealSubmit] invalid custom id: %s", data.CustomID)
		return
	}

	guildID := parts[1]
	var origChannelID, origMessageID string
	if len(parts) >= 4 {
		origChannelID = parts[2]
		origMessageID = parts[3]
	}
	log.Debug().Msgf("[handleAppealSubmit] guildID: %s", guildID)

	// Verify appeals are configured
	settings, err := storage.FindAppealSettingsByGuildID(guildID)
	if err != nil || settings == nil {
		if err != nil {
			log.Debug().Msgf("[handleAppealSubmit] storage error: %s", err.Error())
		}
		respondWithError(s, i, "Appeals are not configured for this server.")
		return
	}

	// Check for existing appeals
	existingAppeals, err := storage.FindOpenAndRejectedAppealsByUserID(i.User.ID, guildID)
	if err != nil {
		log.Error().AnErr("Failed to find appeals", err)
	}
	if len(existingAppeals) > 0 {
		respondWithError(s, i, "You either have an appeal pending or have been rejected. Please wait for a review.")
		return
	}

	// Extract user input from modal
	userInput := extractModalInput(data, "appeal-message")
	log.Debug().Msgf("[handleAppealSubmit] collected userInput length: %d", len(userInput))

	if userInput == "" {
		respondWithError(s, i, "Please provide a message.")
		return
	}

	// Find associated ban case
	caseIDPtr := findLatestBanCase(i.User.ID, guildID)

	// Create appeal
	appealID, _ := lib.GenID()
	appeal := &structs.Appeal{
		ID:      appealID,
		GuildID: guildID,
		UserID:  i.User.ID,
		Content: userInput,
		Status:  0,
	}
	if caseIDPtr != nil {
		appeal.CaseID = sql.NullString{String: *caseIDPtr, Valid: true}
	}

	if err := storage.CreateAppeal(appeal); err != nil {
		log.Error().AnErr("Failed to create appeal", err)
		log.Debug().Msgf("[handleAppealSubmit] CreateAppeal error: %s", err.Error())
		respondWithError(s, i, "Failed to submit appeal. Please try again later.")
		return
	}

	// Build staff review embed
	var banCase *structs.Case
	if caseIDPtr != nil {
		latestCases, _ := storage.FindCasesByUserID(i.User.ID, guildID)
		if len(latestCases) > 0 {
			banCase = latestCases[0]
		}
	}
	staffEmbed := buildStaffEmbed(i.User.ID, userInput, banCase, caseIDPtr)

	// Send to staff channel
	msg, err := s.ChannelMessageSendComplex(settings.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{staffEmbed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{CustomID: "appeal-accept:" + appealID, Label: "Accept", Style: discordgo.SuccessButton},
				discordgo.Button{CustomID: "appeal-reject:" + appealID, Label: "Reject", Style: discordgo.DangerButton},
			}},
		},
	})

	if err == nil {
		_ = storage.SetAppealReviewMessage(appealID, msg.ID)
	} else {
		log.Debug().Msgf("[handleAppealSubmit] ChannelMessageSendComplex error: %s", err.Error())
	}

	// Acknowledge modal submission
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Appeal submitted. A moderator will review it soon.",
		},
	}); err != nil {
		log.Debug().Msgf("[handleAppealSubmit] failed to ack modal submit: %s", err.Error())
	}

	// Update original DM message
	updateOriginalDMMessage(s, origChannelID, origMessageID, guildID)
}

// handleAppealAccept processes appeal acceptance
func handleAppealAccept(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// Permission check
	if i.Member == nil || (i.Member.Permissions&discordgo.PermissionBanMembers) != discordgo.PermissionBanMembers {
		return ContentResponse(config.Bot.ErrMsgPrefix+"You don't have permission to accept appeals.", true)
	}

	// Parse appeal ID
	componentData := i.MessageComponentData()
	parts := strings.SplitN(componentData.CustomID, ":", 2)
	if len(parts) != 2 {
		return ContentResponse(config.Bot.ErrMsgPrefix+"Invalid appeal.", true)
	}
	appealID := parts[1]

	// Fetch appeal data
	data, err := parseAppealData(s, appealID)
	if err != nil {
		return ContentResponse(config.Bot.ErrMsgPrefix+"Failed to look up appeal.", true)
	}

	// Unban user
	if err := s.GuildBanDelete(data.GuildID, data.UserID); err != nil {
		log.Error().AnErr("Failed to unban on appeal accept", err)
		log.Debug().Msgf("[handleAppealAccept] GuildBanDelete error: %s", err.Error())
		return ContentResponse(config.Bot.ErrMsgPrefix+"Failed to unban user.", true)
	}

	// Update appeal status
	_ = storage.UpdateAppealStatus(appealID, 1, i.Member.User.ID)

	// Send welcome DM
	if data.Guild != nil {
		dmEmbed := NewEmbed().
			SetTitle("Ban Appeal Accepted").
			SetDescription("You can rejoin the server using [this link]("+data.InviteLink+").").
			SetColor("Green").
			SetAuthor(data.Guild.Name, data.Guild.IconURL("")).
			MessageEmbed
		_ = utils.DMUserEmbed(data.UserID, dmEmbed, s)
	}

	// Create unban case
	caseID, _ := lib.GenID()
	caseData := &structs.Case{
		ID:          caseID,
		Type:        3, // unban
		Reason:      "Ban appeal accepted. ID: " + appealID,
		UserID:      data.UserID,
		ModeratorID: i.Member.User.ID,
		GuildID:     data.GuildID,
	}

	if err := storage.CreateCase(caseData); err != nil {
		log.Error().AnErr("Failed to create case", err)
		return ContentResponse(config.Bot.ErrMsgPrefix+"Failed to create case.", true)
	}

	// Update review message
	reviewEmbed := buildReviewEmbed(data, i.Member.User.Username, i.Member.User.AvatarURL(""), "Green")
	return UpdateResponse(&discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{reviewEmbed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{CustomID: "appeal-accept:" + appealID, Label: "Accepted", Style: discordgo.SuccessButton, Disabled: true},
			}},
		},
	})
}

// handleAppealReject processes appeal rejection
func handleAppealReject(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// Permission check
	if i.Member == nil || (i.Member.Permissions&discordgo.PermissionBanMembers) != discordgo.PermissionBanMembers {
		return ContentResponse(config.Bot.ErrMsgPrefix+"You don't have permission to reject appeals.", true)
	}

	// Parse appeal ID
	componentData := i.MessageComponentData()
	parts := strings.SplitN(componentData.CustomID, ":", 2)
	if len(parts) != 2 {
		return ContentResponse(config.Bot.ErrMsgPrefix+"Invalid appeal.", true)
	}
	appealID := parts[1]

	// Update appeal status
	_ = storage.UpdateAppealStatus(appealID, 2, i.Member.User.ID)

	// Fetch appeal data
	data, err := parseAppealData(s, appealID)
	if err != nil {
		return ContentResponse(config.Bot.ErrMsgPrefix+"Failed to look up appeal.", true)
	}

	// Send rejection DM
	if data.UserID != "" && data.Guild != nil {
		dmEmbed := NewEmbed().
			SetTitle("Ban Appeal Rejected").
			SetDescription("Please contact a moderator if you believe this is an error.").
			SetColor("Red").
			SetAuthor(data.Guild.Name, data.Guild.IconURL("")).
			MessageEmbed
		_ = utils.DMUserEmbed(data.UserID, dmEmbed, s)
	}

	// Update review message
	reviewEmbed := buildReviewEmbed(data, i.Member.User.Username, i.Member.User.AvatarURL(""), "Red")
	return UpdateResponse(&discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{reviewEmbed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{CustomID: "appeal-reject:" + appealID, Label: "Rejected", Style: discordgo.DangerButton, Disabled: true},
			}},
		},
	})
}
