package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestAuditLogRoute(t *testing.T) {
	moderator, moderatorSession, _ := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
	expectStatus(t, send(t, moderator, http.MethodGet, "/guilds/guild-1/audit-log", "", moderatorSession), http.StatusOK)

	admin, adminSession, templateID := caseHarness(t, uint64(discordgo.PermissionAdministrator))
	expectStatus(t, send(t, admin, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), adminSession), http.StatusCreated)
	response := send(t, admin, http.MethodGet, "/guilds/guild-1/audit-log?action=case.create&result=success", "", adminSession)
	expectStatus(t, response, http.StatusOK)
	var body struct {
		Total   int `json:"total"`
		Entries []struct {
			Action string `json:"action"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Total != 1 ||
		len(body.Entries) != 1 || body.Entries[0].Action != "case.create" {
		t.Fatalf("audit = %s (err %v)", response.Body.String(), err)
	}
	expectStatus(t, send(t, admin, http.MethodGet, "/guilds/guild-1/audit-log?result=partial", "", adminSession), http.StatusBadRequest)
}
