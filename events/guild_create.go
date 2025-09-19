package events

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onGuildCreate)
}

func onGuildCreate(s *discordgo.Session, gc *discordgo.GuildCreate) {
	// check if guild is in the db already
	if storage.QuickCheckGuildExists(gc.Guild.ID) {
		return
	}

	// create the guild
	err := storage.CreateGuild(&structs.Guild{
		ID:              gc.Guild.ID,
		Name:            gc.Guild.Name,
		Description:     gc.Guild.Description,
		MemberCount:     gc.Guild.MemberCount,
		IsPremium:       int(gc.Guild.PremiumTier),
		Large:           0,
		VanityURL:       gc.Guild.VanityURLCode,
		JoinedAt:        gc.Guild.JoinedAt.Format("2006-01-02 15:04:05"),
		OwnerID:         gc.Guild.OwnerID,
		ShardID:         0,
		BannerURL:       gc.Guild.BannerURL(""),
		Icon:            gc.Guild.IconURL(""),
		MaxMembers:      gc.Guild.MaxMembers,
		Partnered:       0,
		AFKChannelID:    gc.Guild.AfkChannelID,
		AFKTimeout:      gc.Guild.AfkTimeout,
		MFALevel:        int(gc.Guild.MfaLevel),
		NSFWLevel:       int(gc.Guild.NSFWLevel),
		PerferedLocale:  gc.Guild.PreferredLocale,
		RulesChannelID:  gc.Guild.RulesChannelID,
		SystemChannelID: gc.Guild.SystemChannelID,
	})

	if err != nil {
		log.Error().AnErr("Failed to create guild", err)
	}

	// update the guild count channel
	_, err = s.ChannelEdit(config.Bot.GuildCountChannel, &discordgo.ChannelEdit{
		Name: fmt.Sprintf("%d servers", len(s.State.Guilds)),
	})

	if err != nil {
		log.Error().AnErr("Failed to update guild count channel", err)
	}

	log.Info().Msgf("Guild created %s", gc.Guild.ID)
}
