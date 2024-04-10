package storage

import (
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

func FindCaseByID(id string, guildID string) (*structs.Case, error) {
	if id == "" {
		return nil, nil
	}

	if guildID == "" {
		return nil, nil
	}

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM cases WHERE id = ? AND guild_id = ?")
	if err != nil {
		return nil, err
	}

	// query the database
	var c structs.Case
	err2 := stmtOut.QueryRow(id, guildID).Scan(&c.ID, &c.UserID, &c.GuildID, &c.ModeratorID, &c.Reason, &c.Type, &c.CreatedAt)
	if err2 != nil {
		return nil, err
	}

	return &c, nil
}

func FindCasesByUserID(userID string, guildID string) ([]*structs.Case, error) {
	if userID == "" {
		return nil, nil
	}

	if guildID == "" {
		return nil, nil
	}

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM cases WHERE user_id = ? AND guild_id = ?")
	if err != nil {
		return nil, err
	}

	// query the database
	rows, err2 := stmtOut.Query(userID, guildID)
	if err2 != nil {
		return nil, err
	}

	var cases []*structs.Case
	for rows.Next() {
		var c structs.Case
		err3 := rows.Scan(&c.ID, &c.UserID, &c.GuildID, &c.ModeratorID, &c.Reason, &c.Type, &c.CreatedAt)
		if err3 != nil {
			return nil, err
		}

		cases = append(cases, &c)
	}

	return cases, nil
}

func CreateCase(c *structs.Case) error {

	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO cases (id, user_id, guild_id, moderator_id, reason, type, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtIns.Exec(c.ID, c.UserID, c.GuildID, c.ModeratorID, c.Reason, c.Type, c.CreatedAt)
	if err2 != nil {
		return err2
	}

	return nil

}
