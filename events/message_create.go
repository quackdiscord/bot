package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
)

func init() {
	Events = append(Events, onMessageCreate)
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Store the message in the cache
	services.CacheMutex.Lock()
	defer services.CacheMutex.Unlock()
	if len(services.MessageCache) >= services.MaxMessageCacheSize {
		// Remove the oldest message if the cache size limit is reached
		oldestMessageID := services.CacheOrder[0]
		delete(services.MessageCache, oldestMessageID)
		services.CacheOrder = services.CacheOrder[1:]
	}

	// check if the message is already in the cache
	_, exists := services.MessageCache[m.ID]
	if exists {
		return
	}

	services.MessageCache[m.ID] = m.Message
	services.CacheOrder = append(services.CacheOrder, m.ID)

	if m.Author.ID == s.State.User.ID {
		return
	}

	// store the message in redis (this will check if the message is in a ticket automatically)
	storage.StoreTicketMessage(m.ChannelID, m.Content, m.Author.Username)
}
