package storage

import (
	"database/sql"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

// create a new log settings object
func CreateLogSettings(g *structs.LogSettings) error {
	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO log_settings (guild_id, message_channel_id, message_webhook_url, member_channel_id, member_webhook_url) VALUES(?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtIns.Exec(g.GuildID, g.MessageChannelID, g.MessageWebhookURL, g.MemberChannelID, g.MemberWebhookURL)
	if err2 != nil {
		return err2
	}

	return nil
}

// update a log settings object
func UpdateLogSettings(g *structs.LogSettings) error {
	// prepare the statement
	stmtUpd, err := services.DB.Prepare("UPDATE log_settings SET message_channel_id = ?, message_webhook_url = ?, member_channel_id = ?, member_webhook_url = ? WHERE guild_id = ?")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtUpd.Exec(g.MessageChannelID, g.MessageWebhookURL, g.MemberChannelID, g.MemberWebhookURL, g.GuildID)
	if err2 != nil {
		return err2
	}

	return nil
}

// delete a log settings object
func DeleteLogSettings(id string) error {
	// prepare the statement
	stmtDel, err := services.DB.Prepare("DELETE FROM log_settings WHERE guild_id = ?")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtDel.Exec(id)
	if err2 != nil {
		return err2
	}

	return nil
}

// find a log settings object by guild id
func FindLogSettingsByID(id string) (*structs.LogSettings, error) {
	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM log_settings WHERE guild_id = ?")
	if err != nil {
		return nil, err
	}

	// query the db
	var g structs.LogSettings
	err2 := stmtOut.QueryRow(id).Scan(&g.GuildID, &g.MessageChannelID, &g.MessageWebhookURL, &g.MemberChannelID, &g.MemberWebhookURL)
	if err2 != nil {
		if err2 == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err2
	}

	return &g, nil
}
