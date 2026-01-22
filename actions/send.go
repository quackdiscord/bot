package actions

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
)

func DMGuildUserComplex(userID, guildID string, data *discordgo.MessageSend) error {
	s := services.Discord
	if s == nil {
		return fmt.Errorf("discord session not found")
	}

	c, err := s.UserChannelCreate(userID)
	if err != nil {
		return err
	}

	_, err = s.ChannelMessageSendComplex(c.ID, data)
	return err
}

func DMGuildUserEmbed(userID, guildID string, embed *discordgo.MessageEmbed) error {
	s := services.Discord
	if s == nil {
		return fmt.Errorf("discord session not found")
	}

	c, err := s.UserChannelCreate(userID)
	if err != nil {
		return err
	}

	_, err = s.ChannelMessageSendEmbed(c.ID, embed)
	return err
}
