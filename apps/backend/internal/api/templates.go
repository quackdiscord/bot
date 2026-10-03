package api

import (
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.services.Templates.List(r.Context(), quack.StaffFromContext(r.Context()))
	if err != nil {
		templateErrors.withFallback("failed to list templates").write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var input quack.TemplateInput
	if !decode(w, r, &input, "invalid template payload") {
		return
	}
	template, err := s.services.Templates.Create(r.Context(), quack.StaffFromContext(r.Context()), input)
	if err != nil {
		templateErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"template": template})
}

// importTemplate creates a template from a policy exported by
// exportTemplate, possibly in another guild.
func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request) {
	var input quack.TemplateImportInput
	if !decode(w, r, &input, "invalid import payload") {
		return
	}
	template, err := s.services.Templates.Import(r.Context(), quack.StaffFromContext(r.Context()), input)
	if err != nil {
		templateErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"template": template})
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	template, err := s.services.Templates.Get(r.Context(), staff, r.PathValue("templateID"))
	if err != nil {
		templateErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	var input quack.TemplateInput
	if !decode(w, r, &input, "invalid template payload") {
		return
	}
	staff := quack.StaffFromContext(r.Context())
	template, err := s.services.Templates.Update(r.Context(), staff, r.PathValue("templateID"), input)
	if err != nil {
		templateErrors.write(w, r, err)
		return
	}
	s.templateChanged(r, staff, template.ID)
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

// archiveTemplate soft-deletes a template, which restoreTemplate undoes.
func (s *Server) archiveTemplate(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	template, err := s.services.Templates.Archive(r.Context(), staff, r.PathValue("templateID"))
	if err != nil {
		templateErrors.write(w, r, err)
		return
	}
	s.templateChanged(r, staff, template.ID)
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

func (s *Server) restoreTemplate(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	template, err := s.services.Templates.Restore(r.Context(), staff, r.PathValue("templateID"))
	if err != nil {
		templateErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": template})
}

// exportTemplate returns the template's policy without guild-specific IDs,
// for importing into another guild.
func (s *Server) exportTemplate(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	policy, err := s.services.Templates.Export(r.Context(), staff, r.PathValue("templateID"))
	if err != nil {
		templateErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

// templateChanged tells TemplateChanges, if set, that a template was
// updated or archived.
func (s *Server) templateChanged(r *http.Request, staff *quack.GuildStaffContext, templateID string) {
	if s.templateChanges != nil {
		s.templateChanges.HandleTemplateChange(r.Context(), staff.Guild.ID, templateID)
	}
}
