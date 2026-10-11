package logging

import (
	"strconv"
	"sync"
	"time"
)

// Discord reports a deletion with only the message's ID, so a deleted
// message Quack never cached could only be logged as "a message from a
// member". Someone clearing old history deletes hundreds that way, one at a
// time, so these are gathered per channel and posted as one summary.
const (
	// uncachedDeleteWindow is how long a channel's first uncached deletion
	// waits for others before its batch is posted.
	uncachedDeleteWindow = time.Minute
	// maxUncachedDeletes is the most message IDs one summary lists; a full
	// batch is posted at once.
	maxUncachedDeletes = 50
)

// deleteBatcher gathers uncached deletions per channel and hands each batch
// to flush when its window ends, it fills, or Flush is called.
type deleteBatcher struct {
	window time.Duration
	flush  func(guildID, channelID string, messageIDs []string)

	mu      sync.Mutex
	pending map[batchKey]*deleteBatch
}

// batchKey is the channel a batch belongs to.
type batchKey struct{ guildID, channelID string }

// deleteBatch is one channel's waiting deletions.
type deleteBatch struct {
	messageIDs []string
	timer      *time.Timer
}

// newDeleteBatcher returns a batcher that posts each channel's deletions
// through flush once window has passed since the first.
func newDeleteBatcher(window time.Duration, flush func(guildID, channelID string, messageIDs []string)) *deleteBatcher {
	return &deleteBatcher{window: window, flush: flush, pending: make(map[batchKey]*deleteBatch)}
}

// Add queues a deleted message for its channel's next summary.
func (b *deleteBatcher) Add(guildID, channelID, messageID string) {
	key := batchKey{guildID, channelID}
	b.mu.Lock()
	batch := b.pending[key]
	if batch == nil {
		batch = &deleteBatch{}
		batch.timer = time.AfterFunc(b.window, func() { b.expire(key, batch) })
		b.pending[key] = batch
	}
	batch.messageIDs = append(batch.messageIDs, messageID)
	full := len(batch.messageIDs) >= maxUncachedDeletes
	if full {
		batch.timer.Stop()
		delete(b.pending, key)
	}
	b.mu.Unlock()
	if full {
		b.flush(guildID, channelID, batch.messageIDs)
	}
}

// Flush posts every waiting batch now. Stop calls it so a shutdown does not
// drop deletions still inside their window.
func (b *deleteBatcher) Flush() {
	b.mu.Lock()
	pending := b.pending
	b.pending = make(map[batchKey]*deleteBatch)
	b.mu.Unlock()
	for key, batch := range pending {
		batch.timer.Stop()
		b.flush(key.guildID, key.channelID, batch.messageIDs)
	}
}

// expire posts batch when its window ends, unless it was already posted
// because it filled or was flushed.
func (b *deleteBatcher) expire(key batchKey, batch *deleteBatch) {
	b.mu.Lock()
	if b.pending[key] != batch {
		b.mu.Unlock()
		return
	}
	delete(b.pending, key)
	b.mu.Unlock()
	b.flush(key.guildID, key.channelID, batch.messageIDs)
}

// uncachedDeleteEvents turns a channel's uncached deletions into the events
// to log. Messages cached since they were queued are logged one by one with
// their content; a lone unknown message is a plain deletion, and two or more
// become one summary.
func uncachedDeleteEvents(service *Service, guildID, channelID string, messageIDs []string) []Event {
	var events []Event
	var unknown []string
	for _, id := range messageIDs {
		if service.Cached(guildID, id) {
			events = append(events, Event{GuildID: guildID, ChannelDiscordID: channelID, MessageDiscordID: id, Type: MessageDelete})
		} else {
			unknown = append(unknown, id)
		}
	}
	switch len(unknown) {
	case 0:
	case 1:
		events = append(events, Event{GuildID: guildID, ChannelDiscordID: channelID, MessageDiscordID: unknown[0], Type: MessageDelete})
	default:
		events = append(events, Event{
			GuildID: guildID, ChannelDiscordID: channelID, Type: MessageDelete, MessageIDs: unknown,
			Metadata: map[string]string{"message_count": strconv.Itoa(len(unknown))},
		})
	}
	return events
}
