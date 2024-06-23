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

type TicketSettings struct {
	GuildID      string
	ChannelID    string
	LogChannelID string
}
