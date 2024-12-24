package utils

import "github.com/bwmarrin/discordgo"

func DMUserEmbed(userID string, embed *discordgo.MessageEmbed, s *discordgo.Session) error {
	dmChannel, err := s.UserChannelCreate(userID)
	if err != nil {
		return err
	} else {
		_, err = s.ChannelMessageSendEmbed(dmChannel.ID, embed)
		if err != nil {
			return err
		}
	}
	return nil
}
