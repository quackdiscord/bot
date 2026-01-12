package events

import (
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

func HandleHoneypotMessage(s *discordgo.Session, m *discordgo.MessageCreate, h *structs.Honeypot) {
	// check the users perms
	// if they have the "MODERATE_MEMBERS" permission, they can bypass this
	member, err := s.GuildMember(m.GuildID, m.Author.ID)
	if err != nil {
		log.Error().AnErr("Failed to get guild member", err)
		services.CaptureError(err)
		return
	}

	guild, err := s.Guild(m.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to get guild", err)
		services.CaptureError(err)
		return
	}

	perms := computeMemberPerms(guild, member)
	if perms&discordgo.PermissionAdministrator != 0 || perms&discordgo.PermissionModerateMembers != 0 {
		log.Info().Msg("User has permission, bypassing honeypot")
		return
	}

	log.Info().Msgf("Honeypot message detected from %s", m.Author.ID)

	// first delete the message
	err = s.ChannelMessageDelete(m.ChannelID, m.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete message", err)
		services.CaptureError(err)
		return
	}

	// create the case object
	id, _ := lib.GenID()
	caseData := &structs.Case{
		ID:          id,
		Type:        1,
		Reason:      "Fell for honeypot",
		UserID:      m.Author.ID,
		GuildID:     m.GuildID,
		ModeratorID: s.State.User.ID,
	}

	dmDescription := "🚨 You have been banned from **" + guild.Name + "** for ```Falling for the honeypot```"
	dmEmbed := components.NewEmbed().
		SetDescription(dmDescription).
		SetColor("Red").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetFooter("Case ID: " + id).
		SetTimestamp().MessageEmbed

	// attempt to DM the user
	// If appeals are configured, include appeal button in DM
	dmComponents := []discordgo.MessageComponent{}
	if asettings, _ := storage.FindAppealSettingsByGuildID(m.GuildID); asettings != nil {
		dmComponents = []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "Appeal this ban", Style: discordgo.PrimaryButton, CustomID: "appeal-open:" + m.GuildID},
			}},
		}
		dmDescription += "\n\nThis ban can be appealed.\n\n**If the button below doesn't work, please click [here](https://discord.com/oauth2/authorize?client_id=" + s.State.User.ID + ") and select \"Add to My Apps\", then try again.**"
		dmEmbed.Description = dmDescription
	}
	dmChannel, derr := s.UserChannelCreate(m.Author.ID)
	if derr == nil {
		_, err = s.ChannelMessageSendComplex(dmChannel.ID, &discordgo.MessageSend{
			Embeds:     []*discordgo.MessageEmbed{dmEmbed},
			Components: dmComponents,
		})
	} else {
		err = derr
	}

	// for now we just ban
	err = s.GuildBanCreateWithReason(m.GuildID, m.Author.ID, "Fell for honeypot", 1)
	if err != nil {
		log.Error().AnErr("Failed to ban user", err)
		services.CaptureError(err)
		return
	}

	// save the case
	err = storage.CreateCase(caseData)
	if err != nil {
		log.Error().AnErr("Failed to save case", err)
		services.CaptureError(err)
		return
	}

	// increment the actions taken for the honeypot
	err = storage.IncrementHoneypotActions(m.ChannelID)
	if err != nil {
		log.Error().AnErr("Failed to increment honeypot actions", err)
		services.CaptureError(err)
		return
	}

	// update the channel message
	if h.MessageID != "" {
		_, err = s.ChannelMessageEdit(m.ChannelID, h.MessageID, h.Message.String+"\n\n-# <:ban:1165590688554033183> Banned **"+strconv.Itoa(h.ActionsTaken+1)+"** users so far.")
		if err != nil {
			log.Error().AnErr("Failed to update honeypot message", err)
			services.CaptureError(err)
			return
		}
	}
}

// computeMemberPerms calculates a member's permissions from their roles
func computeMemberPerms(guild *discordgo.Guild, member *discordgo.Member) int64 {
	// guild owner has all permissions
	if guild.OwnerID == member.User.ID {
		return discordgo.PermissionAll
	}

	// build a map of role ID to role for quick lookup
	roleMap := make(map[string]*discordgo.Role)
	for _, role := range guild.Roles {
		roleMap[role.ID] = role
	}

	// start with @everyone permissions
	var perms int64
	if everyone, ok := roleMap[guild.ID]; ok {
		perms = everyone.Permissions
	}

	// add permissions from each role the member has
	for _, roleID := range member.Roles {
		if role, ok := roleMap[roleID]; ok {
			perms |= role.Permissions
		}
	}

	// admin has all permissions
	if perms&discordgo.PermissionAdministrator != 0 {
		return discordgo.PermissionAll
	}

	return perms
}
