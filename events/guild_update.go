package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onGuildUpdate)
}

func onGuildUpdate(s *discordgo.Session, up *discordgo.GuildUpdate) {
	large := 0
	if up.Guild.Large {
		large = 1
	}

	// update the guild
	err := storage.UpdateGuild(&structs.Guild{
		ID:              up.Guild.ID,
		Name:            up.Guild.Name,
		Description:     up.Guild.Description,
		MemberCount:     up.Guild.MemberCount,
		IsPremium:       int(up.Guild.PremiumTier),
		Large:           large,
		VanityURL:       up.Guild.VanityURLCode,
		JoinedAt:        up.Guild.JoinedAt.Format("2006-01-02 15:04:05"),
		OwnerID:         up.Guild.OwnerID,
		ShardID:         0,
		BannerURL:       up.Guild.BannerURL(""),
		Icon:            up.Guild.IconURL(""),
		MaxMembers:      up.Guild.MaxMembers,
		Partnered:       0,
		AFKChannelID:    up.Guild.AfkChannelID,
		AFKTimeout:      up.Guild.AfkTimeout,
		MFALevel:        int(up.Guild.MfaLevel),
		NSFWLevel:       int(up.Guild.NSFWLevel),
		PerferedLocale:  up.Guild.PreferredLocale,
		RulesChannelID:  up.Guild.RulesChannelID,
		SystemChannelID: up.Guild.SystemChannelID,
	})

	if err != nil {
		log.Error().AnErr("Failed to update guild", err)
	}

	log.Info().Msgf("Guild %s updated", up.Guild.ID)
}
