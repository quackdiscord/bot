package api

import (
	"net/http"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// auditLogQuery holds the audit log filters. They pass through as strings;
// the audit service validates them. before_id is the next_cursor of the
// previous page.
type auditLogQuery struct {
	Limit               string            `query:"limit" type:"integer" minimum:"1" maximum:"100"`
	Offset              string            `query:"offset" type:"integer" minimum:"0" maximum:"100000"`
	ActorDiscordUserID  string            `query:"actor_discord_user_id"`
	Source              quack.AuditSource `query:"source"`
	Action              string            `query:"action"`
	ResourceType        string            `query:"resource_type"`
	ResourceID          string            `query:"resource_id"`
	Result              quack.AuditResult `query:"result"`
	CaseID              string            `query:"case_id"`
	MemberDiscordUserID string            `query:"member_discord_user_id"`
	CreatedAfter        string            `query:"created_after" format:"date-time"`
	CreatedBefore       string            `query:"created_before" format:"date-time"`
	BeforeID            string            `query:"before_id" maxLength:"256"`
}

// statisticsQuery is the statistics range in RFC 3339. To defaults to now
// and From to a month before To.
type statisticsQuery struct {
	From string `query:"from" format:"date-time"`
	To   string `query:"to" format:"date-time"`
}

// listAuditLog lists the guild's audit entries.
func (s *Server) listAuditLog(w http.ResponseWriter, r *http.Request) {
	var q auditLogQuery
	modules.DecodeQuery(r, &q)
	result, err := s.services.Audits.List(r.Context(), quack.StaffFromContext(r.Context()), quack.AuditListInput{
		Limit:               q.Limit,
		Offset:              q.Offset,
		ActorDiscordUserID:  q.ActorDiscordUserID,
		Source:              string(q.Source),
		Action:              q.Action,
		ResourceType:        q.ResourceType,
		ResourceID:          q.ResourceID,
		Result:              string(q.Result),
		CaseID:              q.CaseID,
		MemberDiscordUserID: q.MemberDiscordUserID,
		CreatedAfter:        q.CreatedAfter,
		CreatedBefore:       q.CreatedBefore,
		BeforeID:            q.BeforeID,
	})
	if err != nil {
		auditErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// getStatistics returns moderation counts for the guild over [from, to).
func (s *Server) getStatistics(w http.ResponseWriter, r *http.Request) {
	var q statisticsQuery
	modules.DecodeQuery(r, &q)
	result, err := s.services.Statistics.Get(r.Context(), quack.StaffFromContext(r.Context()),
		quack.StatisticsInput(q))
	if err != nil {
		statisticsErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
