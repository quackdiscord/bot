package api

import (
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

// listAuditLog lists the guild's audit entries. Filters pass through as
// strings; the audit service validates them.
func (s *Server) listAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	result, err := s.services.Audits.List(r.Context(), quack.StaffFromContext(r.Context()), quack.AuditListInput{
		Limit:               q.Get("limit"),
		Offset:              q.Get("offset"),
		ActorDiscordUserID:  q.Get("actor_discord_user_id"),
		Source:              q.Get("source"),
		Action:              q.Get("action"),
		ResourceType:        q.Get("resource_type"),
		ResourceID:          q.Get("resource_id"),
		Result:              q.Get("result"),
		CaseID:              q.Get("case_id"),
		MemberDiscordUserID: q.Get("member_discord_user_id"),
		CreatedAfter:        q.Get("created_after"),
		CreatedBefore:       q.Get("created_before"),
		ReadSource:          quack.AuditSourceAPI,
		BeforeID:            q.Get("before_id"),
	})
	if err != nil {
		writeAuditError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// getStatistics returns moderation counts for the guild over [from, to).
func (s *Server) getStatistics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	result, err := s.services.Statistics.Get(r.Context(), quack.StaffFromContext(r.Context()),
		quack.StatisticsInput{From: q.Get("from"), To: q.Get("to")})
	if err != nil {
		writeStatisticsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
