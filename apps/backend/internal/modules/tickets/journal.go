package tickets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm/clause"
)

// journalBufferLimit bounds the journal writes kept in memory for retry
// while the database refuses them.
const journalBufferLimit = 1000

var (
	// ErrJournalIncomplete reports that a ticket message could be neither
	// saved nor buffered for retry. Closing that ticket is refused, since
	// its transcript would silently miss the message.
	ErrJournalIncomplete = errors.New("ticket message journal is incomplete")
	// ErrJournalCutoff reports a message delivered after its ticket's
	// closing snapshot was taken. A surviving message is still in the final
	// history; one already deleted cannot join this transcript.
	ErrJournalCutoff = errors.New("ticket transcript snapshot cutoff has passed")
)

// journalRecord is a row of ticket_message_journal: a ticket message's text
// as first received, kept so a message deleted before close still reaches
// the transcript. Entries expire with the transcript once the ticket
// closes; open tickets' entries have no expiry.
type journalRecord struct {
	GuildID                string     `gorm:"type:char(26);primaryKey"`
	ThreadDiscordChannelID string     `gorm:"size:32;primaryKey"`
	MessageDiscordID       string     `gorm:"size:32;primaryKey"`
	TicketID               string     `gorm:"type:char(26);not null;index"`
	AuthorDiscordUserID    string     `gorm:"size:32;not null"`
	AuthorName             string     `gorm:"size:255"`
	Body                   string     `gorm:"type:text;not null"`
	SentAt                 time.Time  `gorm:"not null"`
	ExpiresAt              *time.Time `gorm:"index"`
}

func (journalRecord) TableName() string { return "ticket_message_journal" }

// journal is the Service's in-memory side of the message journal: which
// threads are open tickets, writes waiting for retry, and the per-thread
// gate that orders journal writes against a closing snapshot. Its mutex
// never covers database work. It cannot recover events Discord never
// delivered, or buffered writes lost in a crash during a database outage.
type journal struct {
	mu sync.Mutex
	// known maps open ticket threads to their guild, so unrelated messages
	// are dropped without touching the database.
	known map[string]string
	// pending holds writes that failed, keyed by journalKey.
	pending map[string]pendingMessage
	// blocked marks threads that lost a write to a full buffer; allBlocked
	// is set when even that set overflows, or open threads failed to load.
	blocked    map[string]bool
	allBlocked bool
	threads    map[string]*journalThread
	locks      keyedLocks
}

// pendingMessage is a journal write waiting for retry.
type pendingMessage struct {
	guildID, threadID string
	message           TranscriptMessage
}

// journalThread is one thread's admission state while operations hold it.
// sealed is set while a close snapshots the thread.
type journalThread struct {
	references int
	sealed     bool
}

func threadKey(guildID, threadID string) string { return guildID + ":" + threadID }

func journalKey(guildID, threadID, messageID string) string {
	return threadKey(guildID, threadID) + ":" + messageID
}

// loadJournalThreads loads which threads are open tickets. It reads IDs
// only, never message content. A failure blocks every close until restart.
func (s *Service) loadJournalThreads() {
	var records []ticketRecord
	err := s.store.db.Select("guild_id, thread_discord_channel_id").
		Where("status = ?", StatusOpen).Find(&records).Error
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	s.journal.known = make(map[string]string, len(records))
	if err != nil {
		s.journal.allBlocked = true
		slog.Error("Ticket journal could not load open tickets; closing is blocked until restart", "error", err)
		return
	}
	for _, record := range records {
		s.journal.known[record.ThreadDiscordChannelID] = record.GuildID
	}
}

// KnownMessageThread returns the guild of an open ticket thread. It does no
// database work, so unrelated gateway traffic costs nothing during an
// outage.
func (s *Service) KnownMessageThread(threadID string) (string, bool) {
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	guildID, ok := s.journal.known[threadID]
	return guildID, ok
}

// rememberJournalThread starts journaling a newly saved ticket, before its
// owner is invited and can post.
func (s *Service) rememberJournalThread(ticket *Ticket) {
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	s.journal.known[ticket.ThreadDiscordChannelID] = ticket.GuildID
}

// forgetJournalThread stops journaling a closed ticket's thread.
func (s *Service) forgetJournalThread(threadID string) {
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	delete(s.journal.known, threadID)
}

// journalThread attaches to threadID's admission state; the returned func
// detaches, dropping the state once nothing holds it.
func (s *Service) journalThread(threadID string) (*journalThread, func()) {
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	if s.journal.threads == nil {
		s.journal.threads = map[string]*journalThread{}
	}
	state := s.journal.threads[threadID]
	if state == nil {
		state = &journalThread{}
		s.journal.threads[threadID] = state
	}
	state.references++
	return state, func() {
		s.journal.mu.Lock()
		defer s.journal.mu.Unlock()
		state.references--
		if state.references == 0 {
			delete(s.journal.threads, threadID)
		}
	}
}

// RecordMessage journals a message posted in an open ticket thread,
// whether or not logging is on. The first text received wins over replays
// and edits. A failed write stays buffered for the close to retry; a full
// buffer returns ErrJournalIncomplete and blocks closing that ticket.
func (s *Service) RecordMessage(ctx context.Context, guildID, threadID string, message TranscriptMessage) error {
	if guildID == "" || threadID == "" || message.MessageID == "" || message.AuthorID == "" {
		return errors.New("ticket message identity is incomplete")
	}
	if known, ok := s.KnownMessageThread(threadID); !ok || known != guildID {
		return nil
	}
	state, detach := s.journalThread(threadID)
	defer detach()
	key := journalKey(guildID, threadID, message.MessageID)
	s.journal.mu.Lock()
	if state.sealed {
		s.journal.mu.Unlock()
		return ErrJournalCutoff
	}
	// Buffer before waiting for the gate, so a close that wins the gate
	// still flushes this message.
	err := s.bufferMessage(guildID, threadID, message)
	s.journal.mu.Unlock()
	if err != nil {
		return err
	}
	release, err := s.journal.locks.acquire(ctx, threadID)
	if err != nil {
		return err
	}
	defer release()
	s.journal.mu.Lock()
	pending, ok := s.journal.pending[key]
	s.journal.mu.Unlock()
	if !ok {
		// A closing snapshot already saved it.
		return nil
	}
	if err := s.store.recordMessage(ctx, guildID, threadID, pending.message); err != nil {
		return err
	}
	s.journal.mu.Lock()
	delete(s.journal.pending, key)
	s.journal.mu.Unlock()
	return nil
}

// bufferMessage holds message for writing, keeping the first text seen. If
// the buffer is full, the thread is marked blocked. Callers hold
// s.journal.mu.
func (s *Service) bufferMessage(guildID, threadID string, message TranscriptMessage) error {
	key := journalKey(guildID, threadID, message.MessageID)
	if s.journal.pending == nil {
		s.journal.pending = map[string]pendingMessage{}
	}
	if _, ok := s.journal.pending[key]; ok {
		return nil
	}
	if len(s.journal.pending) >= journalBufferLimit {
		if s.journal.blocked == nil {
			s.journal.blocked = map[string]bool{}
		}
		if len(s.journal.blocked) < journalBufferLimit {
			s.journal.blocked[threadKey(guildID, threadID)] = true
		} else {
			s.journal.allBlocked = true
		}
		return ErrJournalIncomplete
	}
	// Attachments come from the final history, not the journal.
	message.Attachments = nil
	s.journal.pending[key] = pendingMessage{guildID: guildID, threadID: threadID, message: message}
	return nil
}

// recordMessage inserts one journal entry for the open ticket in exactly
// this guild and thread; anything else is ignored.
func (s *Store) recordMessage(ctx context.Context, guildID, threadID string, message TranscriptMessage) error {
	var ticket ticketRecord
	result := s.db.WithContext(ctx).Select("id").
		Where("guild_id = ? AND thread_discord_channel_id = ? AND status = ?", guildID, threadID, StatusOpen).
		Limit(1).Find(&ticket)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&journalRecord{
		GuildID:                guildID,
		ThreadDiscordChannelID: threadID,
		MessageDiscordID:       message.MessageID,
		TicketID:               ticket.ID,
		AuthorDiscordUserID:    message.AuthorID,
		AuthorName:             message.AuthorName,
		Body:                   message.Body,
		SentAt:                 message.SentAt,
	}).Error
}

// flushJournal retries every buffered write for ticket, then returns its
// journal. Any failure, or a lost write, stops the close before anything
// is resolved or deleted.
func (s *Service) flushJournal(ctx context.Context, ticket *Ticket) ([]journalRecord, error) {
	s.journal.mu.Lock()
	blocked := s.journal.allBlocked || s.journal.blocked[threadKey(ticket.GuildID, ticket.ThreadDiscordChannelID)]
	pending := map[string]pendingMessage{}
	for key, message := range s.journal.pending {
		if message.guildID == ticket.GuildID && message.threadID == ticket.ThreadDiscordChannelID {
			pending[key] = message
		}
	}
	s.journal.mu.Unlock()
	if blocked {
		return nil, ErrJournalIncomplete
	}
	for key, message := range pending {
		if err := s.store.recordMessage(ctx, ticket.GuildID, message.threadID, message.message); err != nil {
			return nil, err
		}
		s.journal.mu.Lock()
		delete(s.journal.pending, key)
		s.journal.mu.Unlock()
	}
	var records []journalRecord
	err := s.store.db.WithContext(ctx).
		Where("guild_id = ? AND ticket_id = ?", ticket.GuildID, ticket.ID).
		Find(&records).Error
	return records, err
}

// sealJournal takes ticket's thread gate and refuses later messages with
// ErrJournalCutoff until the returned func releases it. Every message
// admitted before is already buffered for the closing flush.
func (s *Service) sealJournal(ctx context.Context, ticket *Ticket) (func(), error) {
	state, detach := s.journalThread(ticket.ThreadDiscordChannelID)
	release, err := s.journal.locks.acquire(ctx, ticket.ThreadDiscordChannelID)
	if err != nil {
		detach()
		return nil, err
	}
	s.journal.mu.Lock()
	state.sealed = true
	s.journal.mu.Unlock()
	return func() {
		s.journal.mu.Lock()
		state.sealed = false
		s.journal.mu.Unlock()
		release()
		detach()
	}, nil
}

// ResolveWithHistory closes a ticket with a transcript merged from its
// journal and surviving, the thread's history captured after it was
// locked. The journal's original text wins; the surviving copy adds
// attachments and any message the journal missed.
func (s *Service) ResolveWithHistory(ctx context.Context, actor modules.Actor, ticketID string, surviving []TranscriptMessage) (*Ticket, error) {
	ticket, err := s.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	release, err := s.sealJournal(ctx, ticket)
	if err != nil {
		return nil, err
	}
	defer release()
	journaled, err := s.flushJournal(ctx, ticket)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]TranscriptMessage, len(surviving)+len(journaled))
	for _, message := range surviving {
		if message.MessageID == "" {
			return nil, errors.New("ticket transcript contains a message without an ID")
		}
		if _, ok := merged[message.MessageID]; !ok {
			merged[message.MessageID] = message
		}
	}
	for _, original := range journaled {
		message := merged[original.MessageDiscordID]
		message.MessageID = original.MessageDiscordID
		message.AuthorID, message.AuthorName = original.AuthorDiscordUserID, original.AuthorName
		message.Body, message.SentAt = original.Body, original.SentAt
		merged[message.MessageID] = message
	}
	messages := make([]TranscriptMessage, 0, len(merged))
	for _, message := range merged {
		messages = append(messages, message)
	}
	return s.Resolve(ctx, actor, ticketID, FormatTranscript(messages))
}

// FormatTranscript renders messages oldest first, one line each plus their
// attachments. Equal timestamps fall back to snowflake order, and repeated
// IDs are written once.
func FormatTranscript(messages []TranscriptMessage) string {
	messages = slices.Clone(messages)
	slices.SortStableFunc(messages, func(a, b TranscriptMessage) int {
		if c := a.SentAt.Compare(b.SentAt); c != 0 {
			return c
		}
		if c := len(a.MessageID) - len(b.MessageID); c != 0 {
			return c
		}
		return strings.Compare(a.MessageID, b.MessageID)
	})
	var transcript strings.Builder
	seen := map[string]bool{}
	for _, message := range messages {
		if seen[message.MessageID] {
			continue
		}
		seen[message.MessageID] = true
		author := "unknown"
		if message.AuthorID != "" {
			author = message.AuthorName + " (" + message.AuthorID + ")"
		}
		fmt.Fprintf(&transcript, "[%s] %s: %s\n", message.SentAt.UTC().Format(time.RFC3339), author, message.Body)
		for _, attachment := range message.Attachments {
			fmt.Fprintf(&transcript, "  attachment: %s (%d bytes)\n", attachment.Name, attachment.Size)
			if attachment.URL != "" {
				fmt.Fprintf(&transcript, "  original attachment URL (may expire): %s\n", attachment.URL)
			}
		}
	}
	return transcript.String()
}
