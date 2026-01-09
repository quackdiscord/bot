package structs

import "database/sql"

type Honeypot struct {
	ID      string
	GuildID string
	Action  string
	// message is string nullable
	Message sql.NullString
}
