package discord

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestMessagesMatchGolden renders every case and appeal view and compares it
// with testdata/views.golden.json, so copy, layout, and custom IDs cannot
// drift unnoticed. Regenerate the golden file only for a deliberate change.
func TestMessagesMatchGolden(t *testing.T) {
	level := func(name string, position int) *quack.CaseSelectedLevel {
		return &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: name, Position: position}}
	}
	created := &quack.CaseResponse{
		ID: "case-1", CaseNumber: 7, TargetDiscordUserID: "target", SelectedLevel: level("", 2),
		Actions: []quack.CaseActionResponse{
			{ID: "a1", ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionPending},
			{ID: "a2", ActionType: quack.ActionBanUser, Status: quack.ActionExecutionSucceeded},
		},
	}
	detail := &quack.CaseDetailResponse{
		CaseResponse: quack.CaseResponse{
			ID: "case-1", CaseNumber: 7, TargetDiscordUserID: "target", Reason: "Official reason",
			Validity: quack.CaseValidityValid, Source: quack.CaseSourceDiscord, SelectedLevel: level("Timeout", 0),
			ContextValues: []quack.CaseContextValueResponse{{Key: "details", Label: "Details", Value: "Visible context"}},
		},
		Actions: []quack.CaseActionDetailResponse{{
			CaseActionResponse: quack.CaseActionResponse{
				ID: "action-1", ActionType: quack.ActionBanUser, Status: quack.ActionExecutionSucceeded,
			},
			AttemptCount:  2,
			LastErrorCode: "permission_denied",
		}},
		Evidence: []quack.CaseEvidenceResponse{
			{
				MessageURL:     "https://discord.com/channels/1/2/3",
				CaptureOutcome: "captured",
				Attachments: []quack.CaseEvidenceAttachmentResponse{
					{Filename: "a.png", OriginalURL: "https://cdn/a.png", CopyOutcome: "copied"},
				},
			},
			{CaptureOutcome: "deleted"},
		},
		Events: []quack.CaseEventResponse{{EventType: quack.CaseEventCreated, Body: "Case created"}},
	}
	list := &quack.CaseListResponse{Total: 21, Cases: []quack.CaseResponse{
		{CaseNumber: 9, TargetDiscordUserID: "member", Validity: quack.CaseValidityVoided, SelectedLevel: level("Warn", 0)},
		{CaseNumber: 8, TargetDiscordUserID: "other", Validity: quack.CaseValidityValid},
	}}
	failed := &quack.FailedCaseActionResult{Total: 12, Executions: []quack.CaseActionExecution{
		{ID: "exec-1", CaseID: "case-1", ActionType: quack.ActionKickUser, LastErrorCode: "kick_permission_or_hierarchy_denied"},
		{ID: "exec-2", CaseID: "case-2", ActionType: quack.ActionBanUser},
	}}
	appeal := &quack.AppealResponse{
		ID: "appeal", CaseID: "case", TargetDiscordUserID: "target", Status: quack.AppealStatusAccepted,
		Events:         []quack.AppealEventResponse{{Type: "submitted", ActorType: "member", ActorDiscordUserID: "target", Body: "please"}},
		ReversalOffers: []quack.AppealReversalOffer{{OriginalExecutionID: "execution", ActionType: quack.ActionUnbanUser}},
	}
	entry, err := appealEntryMessage("https://dash.example/base/", "guild", "case 1")
	if err != nil {
		t.Fatal(err)
	}
	noTimestamps := func(m Message) Message {
		for _, e := range m.Embeds {
			e.Timestamp = ""
		}
		return m
	}
	errorResponse := Error("Nope")
	errorResponse.Data.Embeds[0].Timestamp = ""
	out := map[string]any{
		"case_created":     caseCreatedMessage(created, &quack.TemplateResponse{Slug: "spam"}),
		"case_created_nil": caseCreatedMessage(nil, nil),
		"case_detail":      noTimestamps(caseDetailMessage(detail)),
		"case_detail_nil":  noTimestamps(caseDetailMessage(nil)),
		"case_list_user":   caseListMessage(list, 2, "member"),
		"case_list_empty":  caseListMessage(nil, 0, ""),
		"failures":         failedActionMessage(failed, 1),
		"failures_empty":   failedActionMessage(nil, 1),
		"appeal_staff":     appealStaffMessage(appeal),
		"appeal_entry":     entry,
		"error_response":   errorResponse,
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	AssertGolden(t, "testdata/views.golden.json", string(body))
}

func TestAppealEntryRequiresHTTPSAndDropsQuery(t *testing.T) {
	if _, err := appealEntryMessage("http://dashboard.example", "guild", "case"); err == nil {
		t.Fatal("insecure appeal entry URL was accepted")
	}
	message, err := appealEntryMessage("https://dashboard.example/base?secret=drop", "guild id", "case/id")
	if err != nil {
		t.Fatal(err)
	}
	button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if button.Style != discordgo.LinkButton || !strings.HasPrefix(button.URL, "https://dashboard.example/") || strings.Contains(button.URL, "secret") {
		t.Fatalf("unsafe appeal link: %+v", button)
	}
}

func TestCaseDetailOffersReverseOrRecovery(t *testing.T) {
	for status, want := range map[quack.ActionExecutionStatus][]string{
		quack.ActionExecutionFailed:    {"case:void:", "case:retry:", "case:dismiss:"},
		quack.ActionExecutionSucceeded: {"case:void:", "case:reverse:v1:case-1|action-1|remove_timeout"},
	} {
		detail := &quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case-1"}, Actions: []quack.CaseActionDetailResponse{
			{CaseActionResponse: quack.CaseActionResponse{ID: "action-1", ActionType: quack.ActionTimeoutUser, Status: status}},
		}}
		row := caseDetailMessage(detail).Components[0].(discordgo.ActionsRow)
		if len(row.Components) != len(want) {
			t.Fatalf("%s: got %d controls, want %d", status, len(row.Components), len(want))
		}
		for i, prefix := range want {
			if id := row.Components[i].(discordgo.Button).CustomID; !strings.HasPrefix(id, prefix) {
				t.Errorf("%s: control %d is %q, want prefix %q", status, i, id, prefix)
			}
		}
	}
}
