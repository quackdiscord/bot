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

type appealReversalRequest struct {
	OriginalExecutionID string           `json:"original_execution_id"`
	ActionType          quack.ActionType `json:"action_type"`
	Confirm             bool             `json:"confirm"`
}

func (s *Server) getAppealSettings(w http.ResponseWriter, r *http.Request) {
	staff := GuildStaff(r.Context())
	if !staff.Can(quack.PermissionActionGuildSettingsRead) {
		writeAppealError(w, r, quack.ErrAppealPermissionDenied)
		return
	}
	result, err := s.services.Appeals.GetSettings(r.Context(), staff.Guild.ID)
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// updateAppealSettings replaces the appeal form. Existing appeals keep the
// form they were submitted with.
func (s *Server) updateAppealSettings(w http.ResponseWriter, r *http.Request) {
	var input appealSettingsRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid appeal settings payload")
		return
	}
	result, err := s.services.Appeals.UpdateSettings(r.Context(), GuildStaff(r.Context()), input.Questions)
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listStaffAppeals(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid pagination")
		return
	}
	status := quack.AppealStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	result, err := s.services.Appeals.ListStaff(r.Context(), GuildStaff(r.Context()), status, limit, offset)
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getStaffAppeal(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Appeals.GetStaff(r.Context(), GuildStaff(r.Context()), r.PathValue("appealID"))
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeal": result})
}

// transitionAppeal returns the handler for one staff decision on an appeal.
// Each takes a reason. Accepting an appeal does not reverse anything; that
// is a separate, confirmed reversal.
func (s *Server) transitionAppeal(transition string) http.HandlerFunc {
	type decide func(ctx context.Context, staff *quack.GuildStaffContext, appealID, reason string) (*quack.AppealResponse, error)
	appeals := s.services.Appeals
	apply := map[string]decide{
		"request-information": appeals.RequestInformation,
		"reopen":              appeals.Reopen,
		"accept":              appeals.Accept,
		"reject":              appeals.Reject,
		"close":               appeals.Close,
	}[transition]
	return func(w http.ResponseWriter, r *http.Request) {
		var input quack.AppealDecisionInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, r, http.StatusBadRequest, codeValidation, "invalid appeal decision payload")
			return
		}
		result, err := apply(r.Context(), GuildStaff(r.Context()), r.PathValue("appealID"), input.Reason)
		if err != nil {
			writeAppealError(w, r, err)
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
	staff := GuildStaff(r.Context())
	appeal, err := s.services.Appeals.GetStaff(r.Context(), staff, r.PathValue("appealID"))
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	if appeal.Status != quack.AppealStatusAccepted {
		writeAppealError(w, r, quack.ErrAppealConflict)
		return
	}
	appealID := appeal.ID
	result, err := s.services.Actions.ReverseForAppeal(r.Context(), staff, appeal.CaseID,
		input.OriginalExecutionID, input.ActionType, &appealID)
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"action": result})
}

// submitAppeal files the member's one appeal for a case.
func (s *Server) submitAppeal(w http.ResponseWriter, r *http.Request) {
	var input quack.AppealSubmissionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid appeal payload")
		return
	}
	result, err := s.services.Appeals.Submit(r.Context(), r.PathValue("caseID"), sessionFrom(r.Context()).DiscordUserID, input)
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"appeal": result})
}

func (s *Server) getMemberAppeal(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Appeals.GetMember(r.Context(), r.PathValue("appealID"), sessionFrom(r.Context()).DiscordUserID)
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeal": result})
}

// submitAppealInformation answers staff's request for more information.
func (s *Server) submitAppealInformation(w http.ResponseWriter, r *http.Request) {
	var input quack.AppealInformationInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid appeal information payload")
		return
	}
	result, err := s.services.Appeals.SubmitInformation(r.Context(), r.PathValue("appealID"),
		sessionFrom(r.Context()).DiscordUserID, input)
	if err != nil {
		writeAppealError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeal": result})
}
