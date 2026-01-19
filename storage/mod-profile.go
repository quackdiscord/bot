package storage

import (
	"database/sql"
	"time"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

// ----
// Case stats for moderators (from cases table)
// ----

// GetModCaseStats returns case statistics for a moderator
func GetModCaseStats(modID, guildID string) (*structs.ModCaseStats, error) {
	stats := &structs.ModCaseStats{}

	// Get counts by type
	// Type: 0=warn, 1=ban, 2=kick, 3=unban, 4=timeout, 5=messagedelete
	stmt, err := services.DB.Prepare(`
		SELECT type, COUNT(*) as count 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ? 
		GROUP BY type
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var caseType int
		var count int
		if err := rows.Scan(&caseType, &count); err != nil {
			return nil, err
		}
		stats.TotalCases += count
		switch caseType {
		case 0:
			stats.Warns = count
		case 1:
			stats.Bans = count
		case 2:
			stats.Kicks = count
		case 3:
			stats.Unbans = count
		case 4:
			stats.Timeouts = count
		case 5:
			stats.MessageDeletes = count
		}
	}

	// Get cases in time windows
	stats.CasesLast24h, _ = getModCasesInWindow(modID, guildID, "24 HOUR")
	stats.CasesLast7d, _ = getModCasesInWindow(modID, guildID, "7 DAY")
	stats.CasesLast30d, _ = getModCasesInWindow(modID, guildID, "30 DAY")

	// Get unique reasons count
	reasonStmt, err := services.DB.Prepare(`
		SELECT COUNT(DISTINCT reason) 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ?
	`)
	if err == nil {
		defer reasonStmt.Close()
		reasonStmt.QueryRow(modID, guildID).Scan(&stats.UniqueReasons)
	}

	// Get top reasons
	stats.TopReasons, _ = getModTopReasons(modID, guildID, 5)

	return stats, nil
}

func getModCasesInWindow(modID, guildID, window string) (int, error) {
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(*) 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ? 
		AND created_at > DATE_SUB(NOW(), INTERVAL ` + window + `)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow(modID, guildID).Scan(&count)
	return count, err
}

func getModTopReasons(modID, guildID string, limit int) ([]structs.ReasonFrequency, error) {
	stmt, err := services.DB.Prepare(`
		SELECT reason, COUNT(*) as count 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ? 
		GROUP BY reason 
		ORDER BY count DESC 
		LIMIT ?
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reasons []structs.ReasonFrequency
	for rows.Next() {
		var rf structs.ReasonFrequency
		if err := rows.Scan(&rf.Reason, &rf.Count); err != nil {
			return nil, err
		}
		reasons = append(reasons, rf)
	}
	return reasons, nil
}

// ----
// Ticket stats for moderators (from tickets table)
// ----

// GetModTicketStats returns ticket statistics for a moderator
func GetModTicketStats(modID, guildID string) (*structs.ModTicketStats, error) {
	stats := &structs.ModTicketStats{}

	// Get total resolved
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(*) 
		FROM tickets 
		WHERE resolved_by = ? AND guild_id = ?
	`)
	if err != nil {
		return stats, err
	}
	defer stmt.Close()
	stmt.QueryRow(modID, guildID).Scan(&stats.TotalResolved)

	// Get resolved in time windows
	stats.ResolvedLast24h, _ = getModTicketsInWindow(modID, guildID, "24 HOUR")
	stats.ResolvedLast7d, _ = getModTicketsInWindow(modID, guildID, "7 DAY")
	stats.ResolvedLast30d, _ = getModTicketsInWindow(modID, guildID, "30 DAY")

	return stats, nil
}

func getModTicketsInWindow(modID, guildID, window string) (int, error) {
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(*) 
		FROM tickets 
		WHERE resolved_by = ? AND guild_id = ? 
		AND resolved_at > DATE_SUB(NOW(), INTERVAL ` + window + `)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow(modID, guildID).Scan(&count)
	return count, err
}

// ----
// Appeal stats for moderators (from appeals table)
// ----

// GetModAppealStats returns appeal statistics for a moderator
func GetModAppealStats(modID, guildID string) (*structs.ModAppealStats, error) {
	stats := &structs.ModAppealStats{}

	// Get total handled
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(*) 
		FROM appeals 
		WHERE resolved_by = ? AND guild_id = ?
	`)
	if err != nil {
		return stats, err
	}
	defer stmt.Close()
	stmt.QueryRow(modID, guildID).Scan(&stats.TotalAppeals)

	// Get handled in time windows
	stats.AppealsLast24h, _ = getModAppealsInWindow(modID, guildID, "24 HOUR")
	stats.AppealsLast7d, _ = getModAppealsInWindow(modID, guildID, "7 DAY")
	stats.AppealsLast30d, _ = getModAppealsInWindow(modID, guildID, "30 DAY")

	// Get most common decision
	decisionStmt, err := services.DB.Prepare(`
		SELECT status, COUNT(*) as count 
		FROM appeals 
		WHERE resolved_by = ? AND guild_id = ? AND status != 0
		GROUP BY status 
		ORDER BY count DESC 
		LIMIT 1
	`)
	if err == nil {
		defer decisionStmt.Close()
		var status int
		var count int
		if decisionStmt.QueryRow(modID, guildID).Scan(&status, &count) == nil {
			if status == 1 {
				stats.MostCommonDecision = "Accepted"
			} else if status == 2 {
				stats.MostCommonDecision = "Rejected"
			}
		}
	}

	// Get unique users
	uniqueStmt, err := services.DB.Prepare(`
		SELECT COUNT(DISTINCT user_id) 
		FROM appeals 
		WHERE resolved_by = ? AND guild_id = ?
	`)
	if err == nil {
		defer uniqueStmt.Close()
		uniqueStmt.QueryRow(modID, guildID).Scan(&stats.UniqueUsers)
	}

	return stats, nil
}

func getModAppealsInWindow(modID, guildID, window string) (int, error) {
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(*) 
		FROM appeals 
		WHERE resolved_by = ? AND guild_id = ? 
		AND resolved_at > DATE_SUB(NOW(), INTERVAL ` + window + `)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow(modID, guildID).Scan(&count)
	return count, err
}

// ----
// Note stats for moderators (from notes table)
// ----

// GetModNoteStats returns note statistics for a moderator
func GetModNoteStats(modID, guildID string) (*structs.ModNoteStats, error) {
	stats := &structs.ModNoteStats{}

	// Get total notes created
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(*) 
		FROM notes 
		WHERE moderator_id = ? AND guild_id = ?
	`)
	if err != nil {
		return stats, err
	}
	defer stmt.Close()
	stmt.QueryRow(modID, guildID).Scan(&stats.TotalNotes)

	// Get notes in time windows
	stats.NotesLast24h, _ = getModNotesInWindow(modID, guildID, "24 HOUR")
	stats.NotesLast7d, _ = getModNotesInWindow(modID, guildID, "7 DAY")
	stats.NotesLast30d, _ = getModNotesInWindow(modID, guildID, "30 DAY")

	// Get unique users noted
	uniqueStmt, err := services.DB.Prepare(`
		SELECT COUNT(DISTINCT user_id) 
		FROM notes 
		WHERE moderator_id = ? AND guild_id = ?
	`)
	if err == nil {
		defer uniqueStmt.Close()
		uniqueStmt.QueryRow(modID, guildID).Scan(&stats.UniqueUsers)
	}

	return stats, nil
}

func getModNotesInWindow(modID, guildID, window string) (int, error) {
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(*) 
		FROM notes 
		WHERE moderator_id = ? AND guild_id = ? 
		AND created_at > DATE_SUB(NOW(), INTERVAL ` + window + `)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow(modID, guildID).Scan(&count)
	return count, err
}

// ----
// Activity stats
// ----

// GetModActivityStats returns activity statistics for a moderator
func GetModActivityStats(modID, guildID string) (*structs.ModActivity, error) {
	activity := &structs.ModActivity{}

	// Get first and last action timestamps from cases
	stmt, err := services.DB.Prepare(`
		SELECT MIN(created_at), MAX(created_at) 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ?
	`)
	if err != nil {
		return activity, err
	}
	defer stmt.Close()

	var firstAt, lastAt sql.NullString
	if stmt.QueryRow(modID, guildID).Scan(&firstAt, &lastAt) == nil {
		if firstAt.Valid {
			activity.FirstActionAt, _ = parseTimestamp(firstAt.String)
		}
		if lastAt.Valid {
			activity.LastActionAt, _ = parseTimestamp(lastAt.String)
		}
	}

	// Get unique days active
	daysStmt, err := services.DB.Prepare(`
		SELECT COUNT(DISTINCT DATE(created_at)) 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ?
	`)
	if err == nil {
		defer daysStmt.Close()
		daysStmt.QueryRow(modID, guildID).Scan(&activity.DaysActive)
	}

	return activity, nil
}

// GetModActivityByHour returns case counts grouped by hour (0-23 UTC)
func GetModActivityByHour(modID, guildID string) ([24]int, error) {
	var hours [24]int

	stmt, err := services.DB.Prepare(`
		SELECT HOUR(created_at) as hour, COUNT(*) as count
		FROM cases
		WHERE moderator_id = ? AND guild_id = ?
		GROUP BY HOUR(created_at)
		ORDER BY hour
	`)
	if err != nil {
		return hours, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID)
	if err != nil {
		return hours, err
	}
	defer rows.Close()

	for rows.Next() {
		var hour, count int
		if err := rows.Scan(&hour, &count); err == nil {
			if hour >= 0 && hour < 24 {
				hours[hour] = count
			}
		}
	}

	return hours, nil
}

// GetModActivityByWeekday returns case counts grouped by weekday (0=Sunday, 6=Saturday)
func GetModActivityByWeekday(modID, guildID string) ([7]int, error) {
	var days [7]int

	stmt, err := services.DB.Prepare(`
		SELECT DAYOFWEEK(created_at) as dow, COUNT(*) as count
		FROM cases
		WHERE moderator_id = ? AND guild_id = ?
		GROUP BY DAYOFWEEK(created_at)
		ORDER BY dow
	`)
	if err != nil {
		return days, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID)
	if err != nil {
		return days, err
	}
	defer rows.Close()

	for rows.Next() {
		var dow, count int
		if err := rows.Scan(&dow, &count); err == nil {
			// MySQL DAYOFWEEK: 1=Sunday, 7=Saturday, convert to 0-indexed
			if dow >= 1 && dow <= 7 {
				days[dow-1] = count
			}
		}
	}

	return days, nil
}

// GetServerActivityByHour returns server-wide case counts grouped by hour
func GetServerActivityByHour(guildID string) ([24]int, error) {
	var hours [24]int

	stmt, err := services.DB.Prepare(`
		SELECT HOUR(created_at) as hour, COUNT(*) as count
		FROM cases
		WHERE guild_id = ?
		GROUP BY HOUR(created_at)
		ORDER BY hour
	`)
	if err != nil {
		return hours, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(guildID)
	if err != nil {
		return hours, err
	}
	defer rows.Close()

	for rows.Next() {
		var hour, count int
		if err := rows.Scan(&hour, &count); err == nil {
			if hour >= 0 && hour < 24 {
				hours[hour] = count
			}
		}
	}

	return hours, nil
}

// GetServerActivityByWeekday returns server-wide case counts grouped by weekday
func GetServerActivityByWeekday(guildID string) ([7]int, error) {
	var days [7]int

	stmt, err := services.DB.Prepare(`
		SELECT DAYOFWEEK(created_at) as dow, COUNT(*) as count
		FROM cases
		WHERE guild_id = ?
		GROUP BY DAYOFWEEK(created_at)
		ORDER BY dow
	`)
	if err != nil {
		return days, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(guildID)
	if err != nil {
		return days, err
	}
	defer rows.Close()

	for rows.Next() {
		var dow, count int
		if err := rows.Scan(&dow, &count); err == nil {
			// MySQL DAYOFWEEK: 1=Sunday, 7=Saturday, convert to 0-indexed
			if dow >= 1 && dow <= 7 {
				days[dow-1] = count
			}
		}
	}

	return days, nil
}

// ----
// Pattern stats
// ----

// GetModPatternStats returns behavioral pattern statistics for a moderator
func GetModPatternStats(modID, guildID string) (*structs.ModPatterns, error) {
	patterns := &structs.ModPatterns{}

	// Get unique users punished
	stmt, err := services.DB.Prepare(`
		SELECT COUNT(DISTINCT user_id) 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ?
	`)
	if err != nil {
		return patterns, err
	}
	defer stmt.Close()
	stmt.QueryRow(modID, guildID).Scan(&patterns.UniqueUsersPunished)

	// Get max cases against single user
	maxStmt, err := services.DB.Prepare(`
		SELECT COUNT(*) as case_count 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ? 
		GROUP BY user_id 
		ORDER BY case_count DESC 
		LIMIT 1
	`)
	if err == nil {
		defer maxStmt.Close()
		maxStmt.QueryRow(modID, guildID).Scan(&patterns.SingleUserMaxCases)
	}

	return patterns, nil
}

// ----
// Frequent targets
// ----

// ModFrequentTarget represents a user frequently actioned by a mod
type ModFrequentTarget struct {
	UserID    string
	CaseCount int
}

// GetModFrequentTargets returns users most frequently actioned by this mod
func GetModFrequentTargets(modID, guildID string, limit int) ([]ModFrequentTarget, error) {
	stmt, err := services.DB.Prepare(`
		SELECT user_id, COUNT(*) as case_count 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ? 
		GROUP BY user_id 
		ORDER BY case_count DESC 
		LIMIT ?
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ModFrequentTarget
	for rows.Next() {
		var t ModFrequentTarget
		if err := rows.Scan(&t.UserID, &t.CaseCount); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// ----
// Recent cases by mod
// ----

// GetModRecentCases returns the most recent cases created by this mod
func GetModRecentCases(modID, guildID string, limit int) ([]*structs.Case, error) {
	stmt, err := services.DB.Prepare(`
		SELECT id, user_id, moderator_id, guild_id, reason, type, created_at, context_url 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ? 
		ORDER BY created_at DESC 
		LIMIT ?
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cases []*structs.Case
	for rows.Next() {
		var c structs.Case
		if err := rows.Scan(&c.ID, &c.UserID, &c.ModeratorID, &c.GuildID, &c.Reason, &c.Type, &c.CreatedAt, &c.ContextURL); err != nil {
			return nil, err
		}
		cases = append(cases, &c)
	}
	return cases, nil
}

// GetModRecentCasesInDays returns cases created by this mod within the last N days
func GetModRecentCasesInDays(modID, guildID string, days, limit int) ([]*structs.Case, error) {
	stmt, err := services.DB.Prepare(`
		SELECT id, user_id, moderator_id, guild_id, reason, type, created_at, context_url 
		FROM cases 
		WHERE moderator_id = ? AND guild_id = ? 
		AND created_at > DATE_SUB(NOW(), INTERVAL ? DAY)
		ORDER BY created_at DESC 
		LIMIT ?
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID, days, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cases []*structs.Case
	for rows.Next() {
		var c structs.Case
		if err := rows.Scan(&c.ID, &c.UserID, &c.ModeratorID, &c.GuildID, &c.Reason, &c.Type, &c.CreatedAt, &c.ContextURL); err != nil {
			return nil, err
		}
		cases = append(cases, &c)
	}
	return cases, nil
}

// ----
// List all mods
// ----

// ModSummary represents a quick summary of a mod's activity
type ModSummary struct {
	ModID           string
	TotalCases      int
	TicketsResolved int
	AppealsHandled  int
	LastActionAt    sql.NullString
}

// GetAllModsWithStats returns all moderators in a guild with summary stats
func GetAllModsWithStats(guildID string) ([]ModSummary, error) {
	// Get all unique moderator IDs from cases, tickets, and appeals
	stmt, err := services.DB.Prepare(`
		SELECT 
			mod_id,
			COALESCE(case_count, 0) as case_count,
			COALESCE(ticket_count, 0) as ticket_count,
			COALESCE(appeal_count, 0) as appeal_count,
			last_action
		FROM (
			SELECT moderator_id as mod_id FROM cases WHERE guild_id = ?
			UNION
			SELECT resolved_by as mod_id FROM tickets WHERE guild_id = ? AND resolved_by IS NOT NULL
			UNION
			SELECT resolved_by as mod_id FROM appeals WHERE guild_id = ? AND resolved_by IS NOT NULL
		) AS all_mods
		LEFT JOIN (
			SELECT moderator_id, COUNT(*) as case_count, MAX(created_at) as last_case
			FROM cases WHERE guild_id = ?
			GROUP BY moderator_id
		) AS case_stats ON all_mods.mod_id = case_stats.moderator_id
		LEFT JOIN (
			SELECT resolved_by, COUNT(*) as ticket_count
			FROM tickets WHERE guild_id = ?
			GROUP BY resolved_by
		) AS ticket_stats ON all_mods.mod_id = ticket_stats.resolved_by
		LEFT JOIN (
			SELECT resolved_by, COUNT(*) as appeal_count
			FROM appeals WHERE guild_id = ?
			GROUP BY resolved_by
		) AS appeal_stats ON all_mods.mod_id = appeal_stats.resolved_by
		LEFT JOIN (
			SELECT moderator_id, MAX(created_at) as last_action
			FROM cases WHERE guild_id = ?
			GROUP BY moderator_id
		) AS activity ON all_mods.mod_id = activity.moderator_id
		ORDER BY case_count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(guildID, guildID, guildID, guildID, guildID, guildID, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mods []ModSummary
	for rows.Next() {
		var m ModSummary
		if err := rows.Scan(&m.ModID, &m.TotalCases, &m.TicketsResolved, &m.AppealsHandled, &m.LastActionAt); err != nil {
			return nil, err
		}
		mods = append(mods, m)
	}
	return mods, nil
}

// GetModsSortedByCases returns moderators sorted by case count
func GetModsSortedByCases(guildID string, limit int) ([]ModSummary, error) {
	stmt, err := services.DB.Prepare(`
		SELECT 
			moderator_id,
			COUNT(*) as case_count,
			MAX(created_at) as last_action
		FROM cases 
		WHERE guild_id = ?
		GROUP BY moderator_id
		HAVING MAX(created_at) > DATE_SUB(NOW(), INTERVAL 30 DAY)
		ORDER BY case_count DESC
		LIMIT ?
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mods []ModSummary
	for rows.Next() {
		var m ModSummary
		if err := rows.Scan(&m.ModID, &m.TotalCases, &m.LastActionAt); err != nil {
			return nil, err
		}
		mods = append(mods, m)
	}

	// Fetch ticket and appeal counts for each mod
	for i := range mods {
		mods[i].TicketsResolved, _ = getModTicketCountSimple(mods[i].ModID, guildID)
		mods[i].AppealsHandled, _ = getModAppealCountSimple(mods[i].ModID, guildID)
	}

	return mods, nil
}

func getModTicketCountSimple(modID, guildID string) (int, error) {
	stmt, err := services.DB.Prepare(`SELECT COUNT(*) FROM tickets WHERE resolved_by = ? AND guild_id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var count int
	stmt.QueryRow(modID, guildID).Scan(&count)
	return count, nil
}

func getModAppealCountSimple(modID, guildID string) (int, error) {
	stmt, err := services.DB.Prepare(`SELECT COUNT(*) FROM appeals WHERE resolved_by = ? AND guild_id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var count int
	stmt.QueryRow(modID, guildID).Scan(&count)
	return count, nil
}

// ----
// Mod Notes (admin notes about staff members)
// Uses mod_notes table, but same Note struct
// In this context: UserID = mod being noted, ModeratorID = admin who wrote note
// ----

// CreateModNote creates a new admin note about a staff member
func CreateModNote(n *structs.Note) error {
	stmt, err := services.DB.Prepare(`
		INSERT INTO mod_notes (id, mod_id, guild_id, author_id, content) 
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(n.ID, n.UserID, n.GuildID, n.ModeratorID, n.Content)
	return err
}

// FindModNoteByID finds a specific mod note by ID
func FindModNoteByID(id, guildID string) (*structs.Note, error) {
	if id == "" || guildID == "" {
		return nil, nil
	}

	stmt, err := services.DB.Prepare(`
		SELECT id, mod_id, author_id, guild_id, content, created_at 
		FROM mod_notes 
		WHERE id = ? AND guild_id = ?
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var n structs.Note
	err = stmt.QueryRow(id, guildID).Scan(&n.ID, &n.UserID, &n.ModeratorID, &n.GuildID, &n.Content, &n.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &n, nil
}

// FindModNotesByModID finds all admin notes about a specific staff member
func FindModNotesByModID(modID, guildID string) ([]*structs.Note, error) {
	if modID == "" || guildID == "" {
		return nil, nil
	}

	stmt, err := services.DB.Prepare(`
		SELECT id, mod_id, author_id, guild_id, content, created_at 
		FROM mod_notes 
		WHERE mod_id = ? AND guild_id = ? 
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(modID, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []*structs.Note
	for rows.Next() {
		var n structs.Note
		if err := rows.Scan(&n.ID, &n.UserID, &n.ModeratorID, &n.GuildID, &n.Content, &n.CreatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, &n)
	}

	return notes, nil
}

// DeleteModNoteByID deletes a specific mod note
func DeleteModNoteByID(id, guildID string) (bool, error) {
	if id == "" || guildID == "" {
		return false, nil
	}

	stmt, err := services.DB.Prepare(`DELETE FROM mod_notes WHERE id = ? AND guild_id = ?`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()

	result, err := stmt.Exec(id, guildID)
	if err != nil {
		return false, err
	}

	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// DeleteModNotesByModID deletes all notes about a specific staff member
func DeleteModNotesByModID(modID, guildID string) (bool, error) {
	if modID == "" || guildID == "" {
		return false, nil
	}

	stmt, err := services.DB.Prepare(`DELETE FROM mod_notes WHERE mod_id = ? AND guild_id = ?`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()

	result, err := stmt.Exec(modID, guildID)
	if err != nil {
		return false, err
	}

	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// CountModNotesByModID returns the count of notes about a staff member
func CountModNotesByModID(modID, guildID string) (int, error) {
	stmt, err := services.DB.Prepare(`SELECT COUNT(*) FROM mod_notes WHERE mod_id = ? AND guild_id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var count int
	stmt.QueryRow(modID, guildID).Scan(&count)
	return count, nil
}

// ----
// Helper functions
// ----

func parseTimestamp(ts string) (time.Time, error) {
	// MySQL timestamp format
	return time.Parse("2006-01-02 15:04:05", ts)
}
