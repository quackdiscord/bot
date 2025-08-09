package storage

import (
	"database/sql"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

// CreateAppealSettings inserts appeal settings for a guild
func CreateAppealSettings(s *structs.AppealSettings) error {
	stmt, err := services.DB.Prepare("INSERT INTO appeals_settings (guild_id, message, channel_id) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	_, err = stmt.Exec(s.GuildID, s.Message, s.ChannelID)
	if err != nil {
		return err
	}
	return nil
}

// UpsertAppealSettings sets/updates settings for a guild
func UpsertAppealSettings(s *structs.AppealSettings) error {
	// try update first; if no rows affected, insert
	upd, err := services.DB.Prepare("UPDATE appeals_settings SET message = ?, channel_id = ? WHERE guild_id = ?")
	if err != nil {
		return err
	}
	res, err := upd.Exec(s.Message, s.ChannelID, s.GuildID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return CreateAppealSettings(s)
	}
	return nil
}

// FindAppealSettingsByGuildID fetches appeal settings for a guild, or nil if not set
func FindAppealSettingsByGuildID(guildID string) (*structs.AppealSettings, error) {
	stmt, err := services.DB.Prepare("SELECT guild_id, message, channel_id FROM appeals_settings WHERE guild_id = ?")
	if err != nil {
		return nil, err
	}
	var s structs.AppealSettings
	err = stmt.QueryRow(guildID).Scan(&s.GuildID, &s.Message, &s.ChannelID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// CreateAppeal inserts a new appeal row
func CreateAppeal(a *structs.Appeal) error {
	stmt, err := services.DB.Prepare("INSERT INTO appeals (id, guild_id, user_id, content, status) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	_, err = stmt.Exec(a.ID, a.GuildID, a.UserID, a.Content, a.Status)
	if err != nil {
		return err
	}
	return nil
}

// UpdateAppealStatus updates the appeal status and reviewer
func UpdateAppealStatus(id string, status int8, reviewerID string) error {
	stmt, err := services.DB.Prepare("UPDATE appeals SET status = ?, resolved_by = ?, resolved_at = CURRENT_TIMESTAMP WHERE id = ?")
	if err != nil {
		return err
	}
	_, err = stmt.Exec(status, reviewerID, id)
	return err
}

// SetAppealReviewMessage stores the message id of the staff review message
func SetAppealReviewMessage(id string, messageID string) error {
	stmt, err := services.DB.Prepare("UPDATE appeals SET review_message_id = ? WHERE id = ?")
	if err != nil {
		return err
	}
	_, err = stmt.Exec(messageID, id)
	return err
}

// FindAppealsByUserID fetches appeals for a user
func FindAppealsByUserID(userID string, guildID string) ([]*structs.Appeal, error) {
	stmt, err := services.DB.Prepare("SELECT id, guild_id, user_id, content, status, created_at, resolved_at, resolved_by, review_message_id FROM appeals WHERE user_id = ? AND guild_id = ?")
	if err != nil {
		return nil, err
	}
	rows, err := stmt.Query(userID, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	appeals := []*structs.Appeal{}
	for rows.Next() {
		var a structs.Appeal
		err = rows.Scan(&a.ID, &a.GuildID, &a.UserID, &a.Content, &a.Status, &a.CreatedAt, &a.ResolvedAt, &a.ResolvedBy, &a.ReviewMessageID)
		if err != nil {
			return nil, err
		}
		appeals = append(appeals, &a)
	}
	return appeals, nil
}

// FindOpenAndRejectedAppealsByUserID fetches appeals for a user
func FindOpenAndRejectedAppealsByUserID(userID string, guildID string) ([]*structs.Appeal, error) {
	stmt, err := services.DB.Prepare("SELECT id, guild_id, user_id, content, status, created_at, resolved_at, resolved_by, review_message_id FROM appeals WHERE user_id = ? AND guild_id = ? AND (status = 0 OR status = 2)")
	if err != nil {
		return nil, err
	}
	rows, err := stmt.Query(userID, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	appeals := []*structs.Appeal{}
	for rows.Next() {
		var a structs.Appeal
		err = rows.Scan(&a.ID, &a.GuildID, &a.UserID, &a.Content, &a.Status, &a.CreatedAt, &a.ResolvedAt, &a.ResolvedBy, &a.ReviewMessageID)
		if err != nil {
			return nil, err
		}
		appeals = append(appeals, &a)
	}
	return appeals, nil
}
