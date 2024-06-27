package services

import (
	"container/list"
	"sync"

	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
)

const MaxMessageCacheSize = 5_000

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

func ReadyMessageCache() {
	MsgCache = NewMessageCache()
	log.Info("Message cache ready")
}

func NewMessageCache() *MessageCache {
	return &MessageCache{
		Messages: make(map[string]*list.Element),
		Order:    list.New(),
		MaxSize:  MaxMessageCacheSize,
	}
}

func (mc *MessageCache) AddMessage(msg *discordgo.Message) {
	mc.Mutex.Lock()
	defer mc.Mutex.Unlock()

	// if message already exists in cache, move it to the front
	if elem, exists := mc.Messages[msg.ID]; exists {
		mc.Order.MoveToFront(elem)
		elem.Value.(*CachedMessage).Content = msg.Content
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
			delete(mc.Messages, oldest.Value.(*CachedMessage).ID)
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
