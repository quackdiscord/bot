package api

import (
	"encoding/json"
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

// caseCreateRequest is a case opened from the dashboard. The reason comes
// from the template and the source is always "dashboard"; neither can be
// supplied.
type caseCreateRequest struct {
	TemplateID              string                        `json:"template_id"`
	TargetDiscordUserID     string                        `json:"target_discord_user_id"`
	ContextChannelDiscordID string                        `json:"context_channel_discord_id"`
	ContextMessageDiscordID string                        `json:"context_message_discord_id"`
	ContextURL              string                        `json:"context_url"`
	Metadata                json.RawMessage               `json:"metadata"`
	ContextValues           []quack.CaseContextValueInput `json:"context_values"`
	EvidenceLinks           []string                      `json:"evidence_links"`
	ReplacesCaseID          string                        `json:"replaces_case_id"`
}

type voidCaseRequest struct {
	Reason            string  `json:"reason"`
	ReplacementCaseID *string `json:"replacement_case_id"`
}

func (s *Server) listCases(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.List(r.Context(), GuildStaff(r.Context()), caseListInput(r))
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// createCase opens a case. The Idempotency-Key also reaches the case
// service, which deduplicates inside its lock.
func (s *Server) createCase(w http.ResponseWriter, r *http.Request) {
	var input caseCreateRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid case payload")
		return
	}
	created, err := s.services.Cases.Create(r.Context(), GuildStaff(r.Context()), quack.CaseInput{
		TemplateID:              input.TemplateID,
		TargetDiscordUserID:     input.TargetDiscordUserID,
		Source:                  quack.CaseSourceDashboard,
		ContextChannelDiscordID: input.ContextChannelDiscordID,
		ContextMessageDiscordID: input.ContextMessageDiscordID,
		ContextURL:              input.ContextURL,
		Metadata:                input.Metadata,
		ContextValues:           input.ContextValues,
		EvidenceLinks:           input.EvidenceLinks,
		ReplacesCaseID:          input.ReplacesCaseID,
		IdempotencyKey:          r.Header.Get(idempotencyKeyHeader),
	})
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"case": created})
}

// getCase looks a case up by ID or case number.
func (s *Server) getCase(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.Get(r.Context(), GuildStaff(r.Context()), r.PathValue("caseRef"))
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"case": result})
}

// voidCase marks a case invalid. The case and its history are kept.
func (s *Server) voidCase(w http.ResponseWriter, r *http.Request) {
	var input voidCaseRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid void payload")
		return
	}
	result, err := s.services.Cases.Void(r.Context(), GuildStaff(r.Context()), r.PathValue("caseRef"),
		input.Reason, input.ReplacementCaseID)
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"case": result})
}

func (s *Server) listUserCases(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.UserHistory(r.Context(), GuildStaff(r.Context()),
		r.PathValue("targetDiscordUserID"), caseListInput(r))
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// listMemberCases lists the signed-in member's cases in a guild by internal
// guild ID. It needs no guild membership, only the member's identity.
func (s *Server) listMemberCases(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.ListMemberCases(r.Context(), r.PathValue("guildID"),
		sessionFrom(r.Context()).DiscordUserID, caseListInput(r))
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// getMemberCase returns the member-facing view of a case, and only to its
// target.
func (s *Server) getMemberCase(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.GetMemberCase(r.Context(), r.PathValue("caseID"), sessionFrom(r.Context()).DiscordUserID)
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"case": result})
}

// caseListInput passes the list filters through as strings; the case
// service validates them.
func caseListInput(r *http.Request) quack.CaseListInput {
	q := r.URL.Query()
	return quack.CaseListInput{
		Limit:                  q.Get("limit"),
		Offset:                 q.Get("offset"),
		TargetDiscordUserID:    q.Get("target_discord_user_id"),
		ModeratorDiscordUserID: q.Get("moderator_discord_user_id"),
		TemplateID:             q.Get("template_id"),
		Validity:               q.Get("validity"),
		CaseNumber:             q.Get("case_number"),
		ActionResult:           q.Get("action_result"),
		AppealStatus:           q.Get("appeal_status"),
		CreatedAfter:           q.Get("created_after"),
		CreatedBefore:          q.Get("created_before"),
	}
}
