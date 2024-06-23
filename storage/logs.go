package storage

import (
	"context"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

// create a new log settings object in redis
func CreateLogSettings(g *structs.LogSettings) error {
	// create a map from the struct to a map
	m := map[string]string{
		"guild_id":            g.GuildID,
		"message_channel_id":  g.MessageChannelID,
		"message_webhook_url": g.MessageWebhookURL,
		"member_channel_id":   g.MemberChannelID,
		"member_webhook_url":  g.MemberWebhookURL,
	}

	// save the map to redis using the guild id as the key
	_, err := services.Redis.HMSet(context.Background(), "ls_"+g.GuildID, m).Result()
	if err != nil {
		return err
	}

	return nil
}

// update a log settings object in redis
func UpdateLogSettings(g *structs.LogSettings) error {
	// create a map from the struct to a map
	m := map[string]string{
		"message_channel_id":  g.MessageChannelID,
		"message_webhook_url": g.MessageWebhookURL,
		"member_channel_id":   g.MemberChannelID,
		"member_webhook_url":  g.MemberWebhookURL,
	}

	// save the map to redis using the guild id as the key
	_, err := services.Redis.HMSet(context.Background(), "ls_"+g.GuildID, m).Result()
	if err != nil {
		return err
	}

	return nil
}

// delete a log settings object in redis
func DeleteLogSettings(id string) error {
	// delete the hash table entry
	_, err := services.Redis.HDel(context.Background(), "ls_"+id, "message_channel_id", "message_webhook_url", "member_channel_id", "member_webhook_url").Result()
	if err != nil {
		return err
	}

	return nil
}

// find a log settings object by guild id in redis
func FindLogSettingsByID(id string) (*structs.LogSettings, error) {
	// get the hash table entry
	m, err := services.Redis.HGetAll(context.Background(), "ls_"+id).Result()
	if err != nil {
		return nil, err
	}

	// create a struct from the map
	g := structs.LogSettings{
		GuildID:           id,
		MessageChannelID:  m["message_channel_id"],
		MessageWebhookURL: m["message_webhook_url"],
		MemberChannelID:   m["member_channel_id"],
		MemberWebhookURL:  m["member_webhook_url"],
	}

	return &g, nil
}
