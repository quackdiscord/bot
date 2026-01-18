package structs

import "time"

// ModProfile represents a moderators activity profile within a server
type ModProfile struct {
	ModID     string `json:"mod_id"`
	GuildID   string `json:"guild_id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`

	CaseStats   ModCaseStats   `json:"case_stats"`
	TicketStats ModTicketStats `json:"ticket_stats"`
	AppealStats ModAppealStats `json:"appeal_stats"`
	NoteStats   ModNoteStats   `json:"note_stats"`

	Activity ModActivity `json:"activity"`
	Patterns ModPatterns `json:"patterns"`

	RecentCases   []*Case `json:"recent_cases"`
	FrequentUsers []*User `json:"frequent_users"`

	AdminNotes []*Note `json:"admin_notes"`
}

// ModCaseStats tracks case-related statistics
type ModCaseStats struct {
	TotalCases     int `json:"total_cases"`
	Warns          int `json:"warns"`
	Bans           int `json:"bans"`
	Kicks          int `json:"kicks"`
	Timeouts       int `json:"timeouts"`
	MessageDeletes int `json:"message_deletes"`
	Unbans         int `json:"unbans"`

	CasesLast24h int `json:"cases_last_24h"`
	CasesLast7d  int `json:"cases_last_7d"`
	CasesLast30d int `json:"cases_last_30d"`

	TopReasons    []ReasonFrequency `json:"top_reasons"` // parsed from full reason strings into categories
	UniqueReasons int               `json:"unique_reasons"`
	RecentReasons []string          `json:"recent_reasons"`
}

// ReasonFrequency tracks how often a reason is used
type ReasonFrequency struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// ModTicketStats tracks ticket handling statistics
type ModTicketStats struct {
	TotalResolved   int `json:"total_resolved"`
	ResolvedLast24h int `json:"resolved_last_24h"`
	ResolvedLast7d  int `json:"resolved_last_7d"`
	ResolvedLast30d int `json:"resolved_last_30d"`

	AvgResolutionTime time.Duration `json:"avg_resolution_time"`
	FastestResolution time.Duration `json:"fastest_resolution"`
	SlowestResolution time.Duration `json:"slowest_resolution"`
}

// ModNoteStats tracks note-related statistics
type ModNoteStats struct {
	TotalNotes   int `json:"total_notes"`
	NotesLast24h int `json:"notes_last_24h"`
	NotesLast7d  int `json:"notes_last_7d"`
	NotesLast30d int `json:"notes_last_30d"`
	UniqueUsers  int `json:"unique_users"`
}

// ModAppealStats tracks appeal-related statistics
type ModAppealStats struct {
	TotalAppeals       int    `json:"total_appeals"`
	MostCommonDecision string `json:"most_common_decision"` // either accepted or rejected
	AppealsLast24h     int    `json:"appeals_last_24h"`
	AppealsLast7d      int    `json:"appeals_last_7d"`
	AppealsLast30d     int    `json:"appeals_last_30d"`
	UniqueUsers        int    `json:"unique_users"`
}

// ModActivity tracks when and how often the mod is active
type ModActivity struct {
	FirstActionAt time.Time `json:"first_action_at"`
	LastActionAt  time.Time `json:"last_action_at"`
	DaysActive    int       `json:"days_active"`

	AvgActionsPerDay   float64 `json:"avg_actions_per_day"`
	AvgActionsPerWeek  float64 `json:"avg_actions_per_week"`
	AvgActionsPerMonth float64 `json:"avg_actions_per_month"`

	ActionsByHour    [24]int `json:"actions_by_hour"`
	ActionsByWeekday [7]int  `json:"actions_by_weekday"`

	CurrentStreakDays int       `json:"current_streak_days"`
	LongestStreakDays int       `json:"longest_streak_days"`
	LastActiveDate    time.Time `json:"last_active_date"`
}

// ModPatterns tracks behavioral patterns and potential concerns
type ModPatterns struct {
	ReasonDetailScore float64 `json:"reason_detail_score"`

	UniqueUsersPunished int     `json:"unique_users_punished"`
	RepeatOffenderRatio float64 `json:"repeat_offender_ratio"`
	SingleUserMaxCases  int     `json:"single_user_max_cases"`

	Flags []ModFlag `json:"flags,omitempty"`
}

// ModFlag represents a potential concern for admin review
type ModFlag struct {
	Type     string `json:"type"`     // "low_reason_detail", "high_repeat_target", "unusual_hours", etc.
	Severity string `json:"severity"` // "info", "warning", "alert"
	Value    string `json:"value"`    // the actual metric value that triggered the flag
	Label    string `json:"label"`    // the human-readable label for the flag
}

// ModFrequentUsers represents a user frequently actioned by this mod
type ModFrequentUser struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	CaseCount int       `json:"case_count"`
	LastCase  time.Time `json:"last_case"`
	CaseTypes []int     `json:"case_types"`
}

// ModComparison for comparing a mod against guild averages
type ModComparison struct {
	GuildAvgCasesPerMod   float64 `json:"guild_avg_cases_per_mod"`
	GuildAvgTicketsPerMod float64 `json:"guild_avg_tickets_per_mod"`
	ModRankByCases        int     `json:"mod_rank_by_cases"` // 1 = most cases
	ModRankByTickets      int     `json:"mod_rank_by_tickets"`
	PercentileActivity    float64 `json:"percentile_activity"` // 0-100
}
