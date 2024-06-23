package storage

import (
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

func FindNoteByID(id string, guildID string) (*structs.Note, error) {
	if id == "" {
		return nil, nil
	}

	if guildID == "" {
		return nil, nil
	}

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM notes WHERE id = ? AND guild_id = ?")
	if err != nil {
		return nil, err
	}

	// query the db
	var n structs.Note
	err2 := stmtOut.QueryRow(id, guildID).Scan(&n.ID, &n.UserID, &n.ModeratorID, &n.GuildID, &n.Content, &n.CreatedAt)
	if err2 != nil {
		return nil, err
	}

	return &n, nil
}

func FindNoteByUserID(userID string, guildID string) ([]*structs.Note, error) {
	if userID == "" {
		return nil, nil
	}

	if guildID == "" {
		return nil, nil
	}

	// prepare statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM notes WHERE user_id = ? AND guild_id = ? ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}

	// query the db
	rows, err2 := stmtOut.Query(userID, guildID)
	if err2 != nil {
		return nil, err2
	}

	var notes []*structs.Note
	for rows.Next() {
		var n structs.Note
		err3 := rows.Scan(&n.ID, &n.UserID, &n.ModeratorID, &n.GuildID, &n.Content, &n.CreatedAt)
		if err3 != nil {
			return nil, err3
		}

		notes = append(notes, &n)
	}

	return notes, nil
}

func FindLatestNote(guildID string) (*structs.Note, error) {
	if guildID == "" {
		return nil, nil
	}

	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM notes WHERE guild_id = ? ORDER BY created_at DESC LIMIT 1")
	if err != nil {
		return nil, err
	}

	// query the db
	var n structs.Note
	err2 := stmtOut.QueryRow(guildID).Scan(&n.ID, &n.UserID, &n.ModeratorID, &n.GuildID, &n.Content, &n.CreatedAt)
	if err2 != nil {
		return nil, err2
	}

	return &n, nil
}

func CreateNote(n *structs.Note) error {
	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO notes (id, user_id, guild_id, moderator_id, content) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statment
	_, err2 := stmtIns.Exec(n.ID, n.UserID, n.GuildID, n.ModeratorID, n.Content)
	if err2 != nil {
		return err2
	}

	return nil
}

func DeleteNoteByID(id string, guildID string) (bool, error) {
	if guildID == "" {
		return false, nil
	}

	if id == "" {
		return false, nil
	}

	// prepare the statement
	stmtDel, err := services.DB.Prepare("DELETE FROM notes WHERE id = ? AND guild_id = ?")
	if err != nil {
		return false, nil
	}

	// execute the statement
	_, err2 := stmtDel.Exec(id, guildID)
	if err2 != nil {
		return false, err2
	}

	return true, nil
}

func DeleteNoteByUserID(userID string, guildID string) (bool, error) {
	if guildID == "" {
		return false, nil
	}

	if userID == "" {
		return false, nil
	}

	// prepare the statement
	stmtDel, err := services.DB.Prepare("DELETE FROM notes WHERE user_id = ? AND guild_id = ?")
	if err != nil {
		return false, nil
	}

	// execute the statement
	_, err2 := stmtDel.Exec(userID, guildID)
	if err2 != nil {
		return false, err2
	}

	return true, nil
}

func DeleteLatestNote(guildID string) (bool, error) {
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
