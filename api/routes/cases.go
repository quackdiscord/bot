package routes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/api/middleware"
	"github.com/quackdiscord/bot/app"
)

func createCase(c *gin.Context, services *app.Services) {
	var input app.CaseInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid case payload"})
		return
	}

	created, err := services.Cases.Create(c.Request.Context(), middleware.GetGuildContext(c), input)
	if err != nil {
		writeCaseError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"case": created})
}

func writeCaseError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, app.ErrCaseValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrCasePermissionDenied):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrCaseTemplateNotAvailable):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "case operation failed"})
	}
}
