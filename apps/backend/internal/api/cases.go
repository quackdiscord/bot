package api

import (
	"encoding/json"
	"net/http"

	"github.com/quackdiscord/bot/internal/modules"
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
	Reason string `json:"reason"`
	// ReplacementCaseID is still accepted for compatibility, but setting it
	// is a 400.
	ReplacementCaseID *string `json:"replacement_case_id" deprecated:"true"`
}

// caseListQuery holds the case list filters. They pass through as strings,
// unchecked; the case service validates them.
type caseListQuery struct {
	Limit                  string                      `query:"limit" type:"integer" minimum:"1" maximum:"100"`
	Offset                 string                      `query:"offset" type:"integer" minimum:"0" maximum:"100000"`
	TargetDiscordUserID    string                      `query:"target_discord_user_id"`
	ModeratorDiscordUserID string                      `query:"moderator_discord_user_id"`
	TemplateID             string                      `query:"template_id"`
	Validity               quack.CaseValidity          `query:"validity"`
	CaseNumber             string                      `query:"case_number" type:"integer"`
	ActionResult           quack.ActionExecutionStatus `query:"action_result"`
	AppealStatus           quack.AppealStatus          `query:"appeal_status"`
	CreatedAfter           string                      `query:"created_after" format:"date-time"`
	CreatedBefore          string                      `query:"created_before" format:"date-time"`
}

// caseEnvelope wraps a case that was just created or voided.
type caseEnvelope struct {
	Case *quack.CaseResponse `json:"case" nullable:"false"`
}

// caseDetailEnvelope wraps a case with its full history, for staff.
type caseDetailEnvelope struct {
	Case *quack.CaseDetailResponse `json:"case" nullable:"false"`
}

// memberCaseEnvelope wraps the member-facing view of a case.
type memberCaseEnvelope struct {
	Case *quack.MemberCaseDetail `json:"case" nullable:"false"`
}

func (s *Server) listCases(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.List(r.Context(), quack.StaffFromContext(r.Context()), caseListInput(r))
	if err != nil {
		caseErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// createCase opens a case. The Idempotency-Key also reaches the case
// service, which deduplicates inside its lock.
func (s *Server) createCase(w http.ResponseWriter, r *http.Request) {
	var input caseCreateRequest
	if !decode(w, r, &input, "invalid case payload") {
		return
	}
	created, err := s.services.Cases.Create(r.Context(), quack.StaffFromContext(r.Context()), quack.CaseInput{
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
		caseErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, caseEnvelope{Case: created})
}

// getCase looks a case up by ID or case number.
func (s *Server) getCase(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.Get(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("caseRef"))
	if err != nil {
		caseErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, caseDetailEnvelope{Case: result})
}

// getEvidenceFile redirects to a viewable copy of an evidence file.
func (s *Server) getEvidenceFile(w http.ResponseWriter, r *http.Request) {
	link, err := s.services.Cases.EvidenceFileURL(r.Context(), quack.StaffFromContext(r.Context()),
		r.PathValue("caseRef"), r.PathValue("attachmentID"))
	if err != nil {
		caseErrors.write(w, r, err)
		return
	}
	http.Redirect(w, r, link, http.StatusFound)
}

// voidCase marks a case invalid. The case and its history are kept.
func (s *Server) voidCase(w http.ResponseWriter, r *http.Request) {
	var input voidCaseRequest
	if !decode(w, r, &input, "invalid void payload") {
		return
	}
	if input.ReplacementCaseID != nil {
		// The field is still in the contract, but a correction is a void
		// followed by a new case with replaces_case_id.
		writeError(w, r, http.StatusBadRequest, codeValidation,
			quack.ErrCaseValidation.Error()+": create the replacement after voiding this case")
		return
	}
	result, err := s.services.Cases.Void(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("caseRef"), input.Reason)
	if err != nil {
		caseErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, caseEnvelope{Case: result})
}

func (s *Server) listUserCases(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.UserHistory(r.Context(), quack.StaffFromContext(r.Context()),
		r.PathValue("targetDiscordUserID"), caseListInput(r))
	if err != nil {
		caseErrors.write(w, r, err)
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
		caseErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// getMemberCase returns the member-facing view of a case, and only to its
// target.
func (s *Server) getMemberCase(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Cases.GetMemberCase(r.Context(), r.PathValue("caseID"),
		sessionFrom(r.Context()).DiscordUserID)
	if err != nil {
		caseErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, memberCaseEnvelope{Case: result})
}

// getMemberEvidenceFile redirects the case's member to a viewable copy of
// an evidence file.
func (s *Server) getMemberEvidenceFile(w http.ResponseWriter, r *http.Request) {
	link, err := s.services.Cases.MemberEvidenceFileURL(r.Context(), r.PathValue("caseID"),
		sessionFrom(r.Context()).DiscordUserID, r.PathValue("attachmentID"))
	if err != nil {
		caseErrors.write(w, r, err)
		return
	}
	http.Redirect(w, r, link, http.StatusFound)
}

// caseListInput reads the case list filters.
func caseListInput(r *http.Request) quack.CaseListInput {
	var q caseListQuery
	modules.DecodeQuery(r, &q)
	return quack.CaseListInput{
		Limit:                  q.Limit,
		Offset:                 q.Offset,
		TargetDiscordUserID:    q.TargetDiscordUserID,
		ModeratorDiscordUserID: q.ModeratorDiscordUserID,
		TemplateID:             q.TemplateID,
		Validity:               string(q.Validity),
		CaseNumber:             q.CaseNumber,
		ActionResult:           string(q.ActionResult),
		AppealStatus:           string(q.AppealStatus),
		CreatedAfter:           q.CreatedAfter,
		CreatedBefore:          q.CreatedBefore,
	}
}
