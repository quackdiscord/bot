package api

import (
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.services.Templates.List(r.Context(), quack.StaffFromContext(r.Context()))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, codeInternal, "failed to list templates")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var input quack.TemplateInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid template payload")
		return
	}
	template, err := s.services.Templates.Create(r.Context(), quack.StaffFromContext(r.Context()), input)
	if err != nil {
		writeTemplateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"template": template})
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request) {
	template, err := s.services.Templates.Get(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("templateID"))
	if err != nil {
		writeTemplateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	var input quack.TemplateInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid template payload")
		return
	}
	staff := quack.StaffFromContext(r.Context())
	template, err := s.services.Templates.Update(r.Context(), staff, r.PathValue("templateID"), input)
	if err != nil {
		writeTemplateError(w, r, err)
		return
	}
	s.templateChanged(r, staff, template.ID)
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

// archiveTemplate soft-deletes a template; restoreTemplate brings it back.
func (s *Server) archiveTemplate(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	template, err := s.services.Templates.Archive(r.Context(), staff, r.PathValue("templateID"))
	if err != nil {
		writeTemplateError(w, r, err)
		return
	}
	s.templateChanged(r, staff, template.ID)
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

func (s *Server) restoreTemplate(w http.ResponseWriter, r *http.Request) {
	template, err := s.services.Templates.Restore(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("templateID"))
	if err != nil {
		writeTemplateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

// exportTemplate returns the template's policy without guild-specific IDs,
// for importing into another guild.
func (s *Server) exportTemplate(w http.ResponseWriter, r *http.Request) {
	policy, err := s.services.Templates.Export(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("templateID"))
	if err != nil {
		writeTemplateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request) {
	var input quack.TemplateImportInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid import payload")
		return
	}
	template, err := s.services.Templates.Import(r.Context(), quack.StaffFromContext(r.Context()), input)
	if err != nil {
		writeTemplateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"template": template})
}

func (s *Server) templateChanged(r *http.Request, staff *quack.GuildStaffContext, templateID string) {
	if s.templateChanges != nil {
		s.templateChanges.HandleTemplateChange(r.Context(), staff.Guild.ID, templateID)
	}
}
