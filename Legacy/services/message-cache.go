package services

import (
	"container/list"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/rs/zerolog/log"
)

var MsgCache *MessageCache

// message cache stuff
type CachedMessage struct {
	ID          string
	Content     string
	ChannelID   string
	GuildID     string
	Author      *discordgo.User
	Attachments []*discordgo.MessageAttachment
}

type MessageCache struct {
	Messages map[string]*list.Element
	Order    *list.List
	MaxSize  int
	Mutex    sync.Mutex
}

func ReadyMessageCache(size int) {
	MsgCache = NewMessageCache(size)
	log.Info().Msgf("Message cache ready with size %d", size)
}

func NewMessageCache(size int) *MessageCache {
	return &MessageCache{
		Messages: make(map[string]*list.Element),
		Order:    list.New(),
		MaxSize:  size,
	}
}

func (mc *MessageCache) AddMessage(msg *discordgo.Message) {
	if msg == nil {
		return
	}

	mc.Mutex.Lock()
	defer mc.Mutex.Unlock()

	// if message already exists in cache, move it to the front
	if elem, exists := mc.Messages[msg.ID]; exists {
		mc.Order.MoveToFront(elem)
		if msg.Content != "" {
			elem.Value.(*CachedMessage).Content = msg.Content
		}
		return
	}

	// add new message to the front
	newMessage := &CachedMessage{
		ID:          msg.ID,
		Content:     msg.Content,
		ChannelID:   msg.ChannelID,
		GuildID:     msg.GuildID,
		Author:      msg.Author,
		Attachments: msg.Attachments,
	}
	elem := mc.Order.PushFront(newMessage)
	mc.Messages[msg.ID] = elem

	// check if the cache exceeds the max size
	if mc.Order.Len() > mc.MaxSize {
		oldest := mc.Order.Back()
		if oldest != nil {
			mc.Order.Remove(oldest)
			if cachedMsg, ok := oldest.Value.(*CachedMessage); ok {
				delete(mc.Messages, cachedMsg.ID)
			}
		}
	}
}

func (mc *MessageCache) GetMessage(id string) (*CachedMessage, bool) {
	mc.Mutex.Lock()
	defer mc.Mutex.Unlock()

	if elem, exists := mc.Messages[id]; exists {
		mc.Order.MoveToFront(elem)
		return elem.Value.(*CachedMessage), true
	}

	return nil, false
}
