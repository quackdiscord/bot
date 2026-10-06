package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCaseRoutes(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
		response := send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID)
		expectStatus(t, response, http.StatusCreated)
		var body struct {
			Case struct {
				ID                  string            `json:"id"`
				CaseNumber          uint64            `json:"case_number"`
				TargetDiscordUserID string            `json:"target_discord_user_id"`
				Reason              string            `json:"reason"`
				Validity            string            `json:"validity"`
				Source              string            `json:"source"`
				Actions             []json.RawMessage `json:"actions"`
			} `json:"case"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		c := body.Case
		if c.ID == "" || c.CaseNumber != 1 || c.TargetDiscordUserID != "target-1" || c.Reason != "No spam" ||
			c.Validity != "valid" || c.Source != "dashboard" || len(c.Actions) != 0 {
			t.Fatalf("case = %+v", c)
		}
		var raw struct {
			Case map[string]json.RawMessage `json:"case"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &raw)
		for _, retired := range []string{"severity", "weight", "status", "reason_override"} {
			if _, ok := raw.Case[retired]; ok {
				t.Errorf("case exposes retired field %q", retired)
			}
		}
	})
	t.Run("reason override is rejected", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
		payload := `{"template_id":"` + templateID + `","target_discord_user_id":"target-1","reason_override":"invented"}`
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", payload, sessionID), http.StatusBadRequest)
	})
	t.Run("errors", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
		for _, test := range []struct {
			name, body string
			want       int
		}{
			{"invalid payload", `{`, http.StatusBadRequest},
			{"missing template", casePayload("missing-template", "target-1"), http.StatusNotFound},
			{"validation", casePayload(templateID, ""), http.StatusBadRequest},
		} {
			t.Run(test.name, func(t *testing.T) {
				expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", test.body, sessionID), test.want)
			})
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, 0)
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID), http.StatusForbidden)
	})
}

func TestCaseReadRoutes(t *testing.T) {
	server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
	expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID), http.StatusCreated)

	list := send(t, server, http.MethodGet, "/guilds/guild-1/cases?limit=10&target_discord_user_id=target-1", "", sessionID)
	expectStatus(t, list, http.StatusOK)
	var listBody struct {
		Total int `json:"total"`
		Cases []struct {
			SelectedLevel *struct {
				ID string `json:"id"`
			} `json:"selected_level"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil || listBody.Total != 1 ||
		len(listBody.Cases) != 1 || listBody.Cases[0].SelectedLevel == nil {
		t.Fatalf("list = %s (err %v)", list.Body.String(), err)
	}

	detail := send(t, server, http.MethodGet, "/guilds/guild-1/cases/1", "", sessionID)
	expectStatus(t, detail, http.StatusOK)
	var detailBody struct {
		Case struct {
			ID     string            `json:"id"`
			Events []json.RawMessage `json:"events"`
		} `json:"case"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil || detailBody.Case.ID == "" || len(detailBody.Case.Events) != 1 {
		t.Fatalf("detail = %s (err %v)", detail.Body.String(), err)
	}

	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/users/target-1/cases?limit=10", "", sessionID), http.StatusOK)
	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/cases/missing", "", sessionID), http.StatusNotFound)
	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/cases?limit=0", "", sessionID), http.StatusBadRequest)
}
