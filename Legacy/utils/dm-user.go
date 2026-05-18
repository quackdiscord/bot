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

func DMUser(userID string, content string, s *discordgo.Session) error {
	dmChannel, err := s.UserChannelCreate(userID)
	if err != nil {
		return err
	}
	_, err = s.ChannelMessageSend(dmChannel.ID, content)
	return err
}
