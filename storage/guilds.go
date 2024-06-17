package storage

import (
	"context"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

// create a new guild
func CreateGuild(g *structs.Guild) error {
	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO guilds (id, name, description, member_count, is_premium, large, vanity_url, joined_at, owner_id, shard_id, banner_url, icon, max_members, partnered, afk_channel_id, afk_timeout, mfa_level, nsfw_level, preferred_locale, rules_channel_id, system_channel_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtIns.Exec(g.ID, g.Name, g.Description, g.MemberCount, g.IsPremium, g.Large, g.VanityURL, g.JoinedAt, g.OwnerID, g.ShardID, g.BannerURL, g.Icon, g.MaxMembers, g.Partnered, g.AFKChannelID, g.AFKTimeout, g.MFALevel, g.NSFWLevel, g.PerferedLocale, g.RulesChannelID, g.SystemChannelID)
	if err2 != nil {
		return err2
	}

	return nil
}

// update a guild
func UpdateGuild(g *structs.Guild) error {
	// prepare the statement
	stmtUpd, err := services.DB.Prepare("UPDATE guilds SET name = ?, description = ?, member_count = ?, is_premium = ?, large = ?, vanity_url = ?, joined_at = ?, owner_id = ?, shard_id = ?, banner_url = ?, icon = ?, max_members = ?, partnered = ?, afk_channel_id = ?, afk_timeout = ?, mfa_level = ?, nsfw_level = ?, preferred_locale = ?, rules_channel_id = ?, system_channel_id = ? WHERE id = ?")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtUpd.Exec(g.Name, g.Description, g.MemberCount, g.IsPremium, g.Large, g.VanityURL, g.JoinedAt, g.OwnerID, g.ShardID, g.BannerURL, g.Icon, g.MaxMembers, g.Partnered, g.AFKChannelID, g.AFKTimeout, g.MFALevel, g.NSFWLevel, g.PerferedLocale, g.RulesChannelID, g.SystemChannelID, g.ID)
	if err2 != nil {
		return err2
	}

	return nil
}

// delete a guild
func DeleteGuild(id string) error {
	// prepare the statement
	stmtDel, err := services.DB.Prepare("DELETE FROM guilds WHERE id = ?")
	if err != nil {
		return err
	}

	// execute the statement
	_, err2 := stmtDel.Exec(id)
	if err2 != nil {
		return err2
	}

	// remove the guild from the redis set
	err = services.Redis.SRem(context.Background(), "guilds", id).Err()
	if err != nil {
		return err
	}

	return nil
}

// find a guild by id
func FindGuildByID(id string) (*structs.Guild, error) {
	// prepare the statement
	stmtOut, err := services.DB.Prepare("SELECT * FROM guilds WHERE id = ?")
	if err != nil {
		return nil, err
	}

	// query the db
	var g structs.Guild
	err2 := stmtOut.QueryRow(id).Scan(&g.ID, &g.Name, &g.Description, &g.MemberCount, &g.IsPremium, &g.Large, &g.VanityURL, &g.JoinedAt, &g.OwnerID, &g.ShardID, &g.BannerURL, &g.Icon, &g.MaxMembers, &g.Partnered, &g.AFKChannelID, &g.AFKTimeout, &g.MFALevel, &g.NSFWLevel, &g.PerferedLocale, &g.RulesChannelID, &g.SystemChannelID)
	if err2 != nil {
		return nil, err2
	}

	return &g, nil
}

// quick check if a guild exists (for guild_create event on startup)
func QuickCheckGuildExists(id string) bool {
	mem, err := services.Redis.SIsMember(context.Background(), "guilds", id).Result()
	return err == nil && mem
}
