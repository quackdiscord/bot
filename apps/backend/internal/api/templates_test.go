package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestTemplateRoutes(t *testing.T) {
	t.Run("moderator can read but not write", func(t *testing.T) {
		server, sessionID, _ := templateHarness(t, uint64(discordgo.PermissionModerateMembers))
		expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/templates", "", sessionID), http.StatusOK)
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/templates", templatePayload("spam"), sessionID), http.StatusForbidden)
		expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/templates/template-1", templatePayload("spam"), sessionID), http.StatusForbidden)
		expectStatus(t, send(t, server, http.MethodDelete, "/guilds/guild-1/templates/template-1", "", sessionID), http.StatusForbidden)
	})
	t.Run("manager can create, update, and archive", func(t *testing.T) {
		server, sessionID, _ := templateHarness(t, uint64(discordgo.PermissionManageGuild))
		created := send(t, server, http.MethodPost, "/guilds/guild-1/templates", templatePayload("spam"), sessionID)
		expectStatus(t, created, http.StatusCreated)
		var body struct {
			Template struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			} `json:"template"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || body.Template.ID == "" || body.Template.Slug != "spam" {
			t.Fatalf("created = %s (err %v)", created.Body.String(), err)
		}
		path := "/guilds/guild-1/templates/" + body.Template.ID
		expectStatus(t, send(t, server, http.MethodPatch, path, templatePayload("spam-updated"), sessionID), http.StatusOK)
		expectStatus(t, send(t, server, http.MethodDelete, path, "", sessionID), http.StatusOK)
	})
	t.Run("retired fields are rejected", func(t *testing.T) {
		server, sessionID, _ := templateHarness(t, uint64(discordgo.PermissionManageGuild))
		payload := strings.Replace(templatePayload("retired"), `"levels": [`, `"enabled": true, "levels": [`, 1)
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/templates", payload, sessionID), http.StatusBadRequest)
	})
}
