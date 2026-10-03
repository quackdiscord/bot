package views

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestAppealEntryMessageRequiresHTTPSAndTargetsOwnedCase(t *testing.T) {
	if _, err := AppealEntryMessage("http://dashboard.example", "guild", "case"); err == nil {
		t.Fatal("insecure appeal entry URL was accepted")
	}
	message, err := AppealEntryMessage("https://dashboard.example/base?secret=drop", "guild id", "case/id")
	if err != nil {
		t.Fatalf("appeal entry message: %v", err)
	}
	row, ok := message.Components[0].(discordgo.ActionsRow)
	if !ok || len(row.Components) != 1 {
		t.Fatalf("missing appeal link row: %+v", message.Components)
	}
	button, ok := row.Components[0].(discordgo.Button)
	if !ok || button.Style != discordgo.LinkButton || !strings.HasPrefix(button.URL, "https://dashboard.example/") || strings.Contains(button.URL, "secret") {
		t.Fatalf("unsafe appeal link: %+v", button)
	}
}

func TestAppealStaffMessageOffersOnlyExplicitReversalControls(t *testing.T) {
	message := AppealStaffMessage(&quack.AppealResponse{ID: "appeal", CaseID: "case", TargetDiscordUserID: "target", Status: quack.AppealStatusAccepted, ReversalOffers: []quack.AppealReversalOffer{{OriginalExecutionID: "execution", ActionType: quack.ActionUnbanUser}}})
	if len(message.Components) != 1 || len(message.Embeds) != 1 {
		t.Fatalf("expected one explicit reversal offer: %+v", message)
	}
	row := message.Components[0].(discordgo.ActionsRow)
	button := row.Components[0].(discordgo.Button)
	if button.Style != discordgo.DangerButton || !strings.Contains(button.CustomID, "appeal:reverse:v1") {
		t.Fatalf("reversal was not an explicit confirmation control: %+v", button)
	}
}
