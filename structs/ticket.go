package structs

import "database/sql"

type Ticket struct {
	ID           string
	ThreadID     string
	OwnerID      string
	GuildID      string
	State        int8 // 0=open, 1=resolved
	LogMessageID string
	CreatedAt    string
	ResolvedAt   sql.NullString // null if ticket is open
	ResolvedBy   sql.NullString
	Content      sql.NullString
}

// a message string looks like
// <<user_id>>: <message>
// ex:
// <123456789>: This is a message
// <123456789>: This is another message

type TicketSettings struct {
	GuildID      string
	ChannelID    string
	LogChannelID string
}
