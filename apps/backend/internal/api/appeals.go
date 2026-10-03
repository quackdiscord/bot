package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/quackdiscord/bot/internal/quack"
)

type appealSettingsRequest struct {
	Questions []quack.AppealQuestion `json:"questions"`
}

// appealReversalRequest names the execution to undo on the appealed case.
type appealReversalRequest struct {
	OriginalExecutionID string           `json:"original_execution_id"`
	ActionType          quack.ActionType `json:"action_type"`
	Confirm             bool             `json:"confirm"`
}

// appealDecision is one staff decision on an appeal, such as
// AppealService.Accept.
type appealDecision func(ctx context.Context, staff *quack.GuildStaffContext, appealID, reason string) (*quack.AppealResponse, error)

// getAppealSettings returns the guild's appeal form. Appeal routes only
// require guild membership up front, so the read capability is checked here.
func (s *Server) getAppealSettings(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	if !staff.Can(quack.PermissionActionGuildSettingsRead) {
		appealErrors.write(w, r, quack.ErrAppealPermissionDenied)
		return
	}
	result, err := s.services.Appeals.GetSettings(r.Context(), staff.Guild.ID)
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// updateAppealSettings replaces the appeal form. Existing appeals keep the
// form they were submitted with.
func (s *Server) updateAppealSettings(w http.ResponseWriter, r *http.Request) {
	var input appealSettingsRequest
	if !decode(w, r, &input, "invalid appeal settings payload") {
		return
	}
	result, err := s.services.Appeals.UpdateSettings(r.Context(), quack.StaffFromContext(r.Context()), input.Questions)
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listStaffAppeals(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	status := quack.AppealStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	result, err := s.services.Appeals.ListStaff(r.Context(), quack.StaffFromContext(r.Context()), status, limit, offset)
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getStaffAppeal(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	result, err := s.services.Appeals.GetStaff(r.Context(), staff, r.PathValue("appealID"))
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeal": result})
}

// decideAppeal returns the handler for one staff decision. Each takes a
// reason. Accepting an appeal does not reverse anything; that is a
// separate, confirmed reversal.
func decideAppeal(decide appealDecision) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input quack.AppealDecisionInput
		if !decode(w, r, &input, "invalid appeal decision payload") {
			return
		}
		result, err := decide(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("appealID"), input.Reason)
		if err != nil {
			appealErrors.write(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"appeal": result})
	}
}

// reverseAcceptedAppeal queues a confirmed reversal of an action on the
// case of an accepted appeal.
func (s *Server) reverseAcceptedAppeal(w http.ResponseWriter, r *http.Request) {
	var input appealReversalRequest
	if err := decodeJSON(r, &input); err != nil || !input.Confirm {
		writeError(w, r, http.StatusBadRequest, codeValidation, "confirmed reversal payload is required")
		return
	}
	staff := quack.StaffFromContext(r.Context())
	appeal, err := s.services.Appeals.GetStaff(r.Context(), staff, r.PathValue("appealID"))
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	if appeal.Status != quack.AppealStatusAccepted {
		appealErrors.write(w, r, quack.ErrAppealConflict)
		return
	}
	result, err := s.services.Actions.ReverseForAppeal(r.Context(), staff, appeal.CaseID,
		input.OriginalExecutionID, input.ActionType, &appeal.ID)
	if err != nil {
		caseErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"action": result})
}

// submitAppeal files the member's one appeal for a case.
func (s *Server) submitAppeal(w http.ResponseWriter, r *http.Request) {
	var input quack.AppealSubmissionInput
	if !decode(w, r, &input, "invalid appeal payload") {
		return
	}
	result, err := s.services.Appeals.Submit(r.Context(), r.PathValue("caseID"),
		sessionFrom(r.Context()).DiscordUserID, input)
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"appeal": result})
}

func (s *Server) getMemberAppeal(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Appeals.GetMember(r.Context(), r.PathValue("appealID"),
		sessionFrom(r.Context()).DiscordUserID)
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeal": result})
}

// submitAppealInformation answers staff's request for more information.
func (s *Server) submitAppealInformation(w http.ResponseWriter, r *http.Request) {
	var input quack.AppealInformationInput
	if !decode(w, r, &input, "invalid appeal information payload") {
		return
	}
	result, err := s.services.Appeals.SubmitInformation(r.Context(), r.PathValue("appealID"),
		sessionFrom(r.Context()).DiscordUserID, input)
	if err != nil {
		appealErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeal": result})
}
