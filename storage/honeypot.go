package storage

import (
	"context"
	"slices"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

// create a new honeypot save in db and redis list
func CreateHoneypot(h *structs.Honeypot) error {
	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO honeypots (id, guild_id, action, message, actions_taken, message_id) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	_, err = stmtIns.Exec(h.ID, h.GuildID, h.Action, h.Message, h.ActionsTaken, h.MessageID)
	if err != nil {
		return err
	}

	// add the channel id to the redis list
	err = services.Redis.RPush(context.Background(), "honeypots", h.ID).Err()
	if err != nil {
		return err
	}

	return nil
}

// check if a channel id is a honeypot channel
func IsHoneypotChannel(id string) bool {
	// check the redis list
	members, err := services.Redis.LRange(context.Background(), "honeypots", 0, -1).Result()
	if err != nil {
		return false
	}

	// check if the channel id is in the list
	return slices.Contains(members, id)
}

// get a honeypot object by id
func GetHoneypot(id string) (*structs.Honeypot, error) {
	// prepare the statement
	stmt, err := services.DB.Prepare("SELECT id, guild_id, action, message, actions_taken, message_id FROM honeypots WHERE id = ?")
	if err != nil {
		return nil, err
	}

	// execute the statement
	row := stmt.QueryRow(id)
	var h structs.Honeypot
	err = row.Scan(&h.ID, &h.GuildID, &h.Action, &h.Message, &h.ActionsTaken)
	if err != nil {
		return nil, err
	}

	return &h, nil
}

// increment the actions taken for a honeypot
func IncrementHoneypotActions(id string) error {
	// prepare the statement
	stmt, err := services.DB.Prepare("UPDATE honeypots SET actions_taken = actions_taken + 1 WHERE id = ?")
	if err != nil {
		return err
	}

	// execute the statement
	_, err = stmt.Exec(id)
	if err != nil {
		return err
	}

	return nil
}

// get the actions taken for a honeypot
func GetHoneypotActions(id string) (int, error) {
	// prepare the statement
	stmt, err := services.DB.Prepare("SELECT actions_taken FROM honeypots WHERE id = ?")
	if err != nil {
		return 0, err
	}

	// execute the statement
	var actionsTaken int
	err = stmt.QueryRow(id).Scan(&actionsTaken)
	if err != nil {
		return 0, err
	}

	return actionsTaken, nil
}
