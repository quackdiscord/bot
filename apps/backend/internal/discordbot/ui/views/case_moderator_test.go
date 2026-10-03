package views

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestCaseDetailSeparatesStateContextEvidenceAndRecovery(t *testing.T) {
	detail := &quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case-1", CaseNumber: 7, TargetDiscordUserID: "target", Reason: "Official reason", Validity: quack.CaseValidityValid, Source: quack.CaseSourceDiscord, ContextValues: []quack.CaseContextValueResponse{{Key: "details", Label: "Details", Value: "Visible context"}}, SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "Timeout"}}}, Actions: []quack.CaseActionDetailResponse{{CaseActionResponse: quack.CaseActionResponse{ID: "action-1", ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionFailed}, LastErrorCode: "permission_denied"}}, Evidence: []quack.CaseEvidenceResponse{{MessageURL: "https://discord.com/channels/1/2/3", CaptureOutcome: "captured"}}, Events: []quack.CaseEventResponse{{EventType: quack.CaseEventCreated, Body: "Case created"}}}
	message := CaseDetailMessage(detail)
	if message.Ephemeral || len(message.Embeds) != 1 || len(message.Components) != 1 {
		t.Fatalf("unexpected detail view: %+v", message)
	}
	fields := map[string]string{}
	for _, field := range message.Embeds[0].Fields {
		fields[field.Name] = field.Value
	}
	for _, required := range []string{"Validity", "Enforcement", "Visible context", "Evidence", "History"} {
		if strings.TrimSpace(fields[required]) == "" {
			t.Fatalf("missing separated %s field: %+v", required, fields)
		}
	}
	row := message.Components[0].(discordgo.ActionsRow)
	if len(row.Components) < 3 {
		t.Fatalf("expected void, retry, and dismiss controls: %+v", row)
	}
}

func TestCaseListPaginationIsStableAndScoped(t *testing.T) {
	message := CaseListMessage(&quack.CaseListResponse{Cases: []quack.CaseResponse{{CaseNumber: 9, TargetDiscordUserID: "member", Validity: quack.CaseValidityVoided}}, Total: 21, Limit: 10, Offset: 10}, 2, "member")
	if message.Ephemeral || len(message.Components) != 1 || !strings.Contains(message.Embeds[0].Footer.Text, "Page 2/3") {
		t.Fatalf("unexpected pagination: %+v", message)
	}
}
