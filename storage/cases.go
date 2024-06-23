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
	err2 := stmtOut.QueryRow(id, guildID).Scan(&c.ID, &c.UserID, &c.ModeratorID, &c.GuildID, &c.Reason, &c.Type, &c.CreatedAt)
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
	stmtOut, err := services.DB.Prepare("SELECT * FROM cases WHERE user_id = ? AND guild_id = ? ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}

	// query the database
	rows, err2 := stmtOut.Query(userID, guildID)
	if err2 != nil {
		return nil, err2
	}

	var cases []*structs.Case
	for rows.Next() {
		var c structs.Case
		err3 := rows.Scan(&c.ID, &c.UserID, &c.ModeratorID, &c.GuildID, &c.Reason, &c.Type, &c.CreatedAt)
		if err3 != nil {
			return nil, err
		}

		cases = append(cases, &c)
	}

	return cases, nil
}

func FindLatestCase(guildID string) (*structs.Case, error) {
	if guildID == "" {
		return nil, nil
	}

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM cases WHERE guild_id = ? ORDER BY created_at DESC LIMIT 1")
	if err != nil {
		return nil, err
	}

	// query the database
	var c structs.Case
	err2 := stmtOut.QueryRow(guildID).Scan(&c.ID, &c.UserID, &c.ModeratorID, &c.GuildID, &c.Reason, &c.Type, &c.CreatedAt)
	if err2 != nil {
		return nil, err
	}

	return &c, nil
}

func CreateCase(c *structs.Case) error {

	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO cases (id, user_id, guild_id, moderator_id, reason, type) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtIns.Exec(c.ID, c.UserID, c.GuildID, c.ModeratorID, c.Reason, c.Type)
	if err2 != nil {
		return err2
	}

	return nil

}

func DeleteCaseByID(id string, guildID string) (bool, error) {
	if guildID == "" {
		return false, nil
	}

	if id == "" {
		return false, nil
	}

	// prepare the statement
	stmtDel, err := services.DB.Prepare("DELETE FROM cases WHERE id = ? AND guild_id = ?")
	if err != nil {
		return false, err
	}

	// execute the statement
	_, err2 := stmtDel.Exec(id, guildID)
	if err2 != nil {
		return false, err2
	}

	return true, nil

}

func DeleteCasesByUserID(userID string, guildID string) (bool, error) {
	if guildID == "" {
		return false, nil
	}

	if userID == "" {
		return false, nil
	}

	// prepare the statement
	stmtDel, err := services.DB.Prepare("DELETE FROM cases WHERE user_id = ? AND guild_id = ?")
	if err != nil {
		return false, err
	}

	// execute the statement
	_, err2 := stmtDel.Exec(userID, guildID)
	if err2 != nil {
		return false, err2
	}

	return true, nil

}

func DeleteLatestCase(guildID string) (bool, error) {
	if guildID == "" {
		return false, nil
	}

	// prepare the statement
	stmtDel, err := services.DB.Prepare("DELETE FROM cases WHERE guild_id = ? ORDER BY created_at DESC LIMIT 1")
	if err != nil {
		return false, err
	}

	// execute the statement
	_, err2 := stmtDel.Exec(guildID)
	if err2 != nil {
		return false, err2
	}

	return true, nil

}
