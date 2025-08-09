package structs

import "database/sql"

// Appeal represents a user's ban appeal submission
// status: 0=pending, 1=accepted, 2=rejected
type Appeal struct {
	ID              string
	GuildID         string
	UserID          string
	Content         string
	Status          int8
	CreatedAt       string
	ResolvedAt      sql.NullString
	ResolvedBy      sql.NullString
	ReviewMessageID sql.NullString
}

// AppealSettings represents guild-level configuration for appeals
type AppealSettings struct {
	GuildID   string
	Message   string
	ChannelID string
}
