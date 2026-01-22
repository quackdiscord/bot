package actions

import (
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

type BanParams struct {
	GuildID     string
	UserID      string
	ModeratorID string
	Reason      string
	AllowAppeal bool
}

type BanResult struct {
	Case     *structs.Case
	DMFailed bool
	Error    error
}

func Ban(s *discordgo.Session, p BanParams) BanResult {
	if p.UserID == p.ModeratorID {
		return BanResult{Error: errors.New("you cannot ban yourself")}
	} else if p.UserID == s.State.User.ID {
		return BanResult{Error: errors.New("you cannot ban the bot")}
	}

	id, _ := lib.GenID()

	dmFailed := false
	if err := DMUserBanNotice(s, p, id); err != nil {
		dmFailed = true
	}

	if err := s.GuildBanCreateWithReason(p.GuildID, p.UserID, p.Reason, 1); err != nil {
		return BanResult{Error: err}
	}

	caseData := &structs.Case{
		ID:          id,
		Type:        1,
		Reason:      p.Reason,
		UserID:      p.UserID,
		GuildID:     p.GuildID,
		ModeratorID: p.ModeratorID,
	}
	if err := storage.CreateCase(caseData); err != nil {
		return BanResult{Error: err}
	}

	return BanResult{Case: caseData, DMFailed: dmFailed}
}

func DMUserBanNotice(s *discordgo.Session, p BanParams, caseID string) error {
	guild, err := s.Guild(p.GuildID)
	if err != nil {
		return err
	}

	desc := "🚨 You have been banned from **" + guild.Name + "** for ```" + p.Reason + "```"
	embed := components.NewEmbed().
		SetDescription(desc).
		SetColor("Red").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetFooter("Case ID: " + caseID).
		SetTimestamp().MessageEmbed

	components := []discordgo.MessageComponent{}
	if asettings, _ := storage.FindAppealSettingsByGuildID(guild.ID); asettings != nil && p.AllowAppeal {
		components = []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "Appeal this ban", Style: discordgo.PrimaryButton, CustomID: "appeal-open:" + guild.ID},
			}},
		}
		desc += "\n\nThis ban can be appealed.\n\n**If the button below doesn't work, please click [here](https://discord.com/oauth2/authorize?client_id=" + s.State.User.ID + ") and select \"Add to My Apps\", then try again.**"
		embed.Description = desc
	}

	return DMGuildUserComplex(p.UserID, guild.ID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
}
