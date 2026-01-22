package actions

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

type UnbanParams struct {
	GuildID     string
	UserID      string
	ModeratorID string
	Reason      string
}

type UnbanResult struct {
	Case     *structs.Case
	DMFailed bool
	Error    error
}

func Unban(s *discordgo.Session, p UnbanParams) UnbanResult {
	id, _ := lib.GenID()

	dmFailed := false
	if err := DMUserUnbanNotice(s, p, id); err != nil {
		dmFailed = true
	}

	if err := s.GuildBanDelete(p.GuildID, p.UserID); err != nil {
		return UnbanResult{Error: err}
	}

	caseData := &structs.Case{
		ID:          id,
		Type:        3,
		Reason:      p.Reason,
		UserID:      p.UserID,
		ModeratorID: p.ModeratorID,
		GuildID:     p.GuildID,
	}
	if err := storage.CreateCase(caseData); err != nil {
		return UnbanResult{Error: err}
	}

	appeals, err := storage.FindAppealsByUserID(p.UserID, p.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to find appeals", err)
	}
	if len(appeals) > 0 {
		for _, appeal := range appeals {
			if appeal.Status == 0 || appeal.Status == 2 {
				err = storage.UpdateAppealStatus(appeal.ID, 1, p.ModeratorID)
				if err != nil {
					log.Error().AnErr("Failed to update appeal status", err)
				}
			}
		}
	}

	return UnbanResult{Case: caseData, DMFailed: dmFailed}
}

func DMUserUnbanNotice(s *discordgo.Session, p UnbanParams, caseID string) error {
	guild, err := s.Guild(p.GuildID)
	if err != nil {
		return err
	}

	invites, err := s.GuildInvites(guild.ID)
	inviteLink := ""
	if err == nil {
		inviteLink = "https://discord.gg/" + invites[0].Code
	}

	embed := components.NewEmbed().
		SetDescription("You have been unbanned from **"+guild.Name+"** for ```"+p.Reason+"```\n\nYou can rejoin the server using [this link]("+inviteLink+").").
		SetColor("Green").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetFooter("Case ID: " + caseID).
		SetTimestamp().MessageEmbed

	return DMGuildUserEmbed(p.UserID, p.GuildID, embed)
}
