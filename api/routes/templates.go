package routes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/api/middleware"
	"github.com/quackdiscord/bot/app"
)

func listTemplates(c *gin.Context, services *app.Services) {
	guildContext := middleware.GetGuildContext(c)
	templates, err := services.Templates.List(c.Request.Context(), guildContext)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list templates"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"templates": templates})
}

func createTemplate(c *gin.Context, services *app.Services) {
	var input app.TemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template payload"})
		return
	}

	template, err := services.Templates.Create(c.Request.Context(), middleware.GetGuildContext(c), input)
	if err != nil {
		writeTemplateError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"template": template})
}

func getTemplate(c *gin.Context, services *app.Services) {
	template, err := services.Templates.Get(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"))
	if err != nil {
		writeTemplateError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"template": template})
}

func updateTemplate(c *gin.Context, services *app.Services) {
	var input app.TemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template payload"})
		return
	}

	template, err := services.Templates.Update(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"), input)
	if err != nil {
		writeTemplateError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"template": template})
}

func archiveTemplate(c *gin.Context, services *app.Services) {
	template, err := services.Templates.Archive(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"))
	if err != nil {
		writeTemplateError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"template": template})
}

func writeTemplateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, app.ErrTemplateValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrTemplateNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "template operation failed"})
	}
}
