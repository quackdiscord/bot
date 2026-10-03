package honeypot

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

type caseCreatorFake struct {
	guildID string
	input   quack.CaseInput
}

func (f *caseCreatorFake) CreateSystemHoneypot(_ context.Context, guildID string, input quack.CaseInput) (*quack.CaseResponse, error) {
	f.guildID, f.input = guildID, input
	return &quack.CaseResponse{ID: "case-1"}, nil
}

// templateStoreFake returns one template with a single default level
// running action.
type templateStoreFake struct{ action quack.ActionType }

func (f templateStoreFake) GetCaseTemplateExpanded(context.Context, string, string) (*quack.ExpandedCaseTemplate, error) {
	level := quack.ExpandedCaseTemplateLevel{Level: quack.CaseTemplateLevel{IsDefault: true}}
	if f.action != "" {
		level.Actions = []quack.CaseTemplateLevelAction{{ActionType: f.action}}
	}
	return &quack.ExpandedCaseTemplate{Levels: []quack.ExpandedCaseTemplateLevel{level}}, nil
}

func TestTemplateValidatorAcceptsOnlyEnforcementActions(t *testing.T) {
	for action, valid := range map[quack.ActionType]bool{
		"":                      true,
		quack.ActionTimeoutUser: true,
		quack.ActionKickUser:    true,
		quack.ActionBanUser:     true,
		quack.ActionSendDM:      false,
	} {
		err := templateValidator{store: templateStoreFake{action}}.ValidateHoneypotTemplate(context.Background(), "guild", "template")
		if (err == nil) != valid {
			t.Errorf("action %q: err = %v, want valid %t", action, err, valid)
		}
	}
}

func TestCaseApplierKeepsTheNormalCaseEnvelope(t *testing.T) {
	creator := &caseCreatorFake{}
	applier := caseApplier{cases: creator}
	request := ApplyRequest{
		GuildID: "guild", TemplateID: "template", TargetDiscordUserID: "target",
		ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
		ContextURL: "https://discord.com/channels/1/2/3", IdempotencyKey: "honeypot:guild:message",
		Source: SourceHoneypot, ActorType: ActorTypeSystem,
	}
	result, err := applier.ApplyHoneypotCase(context.Background(), request)
	if err != nil || result.CaseID != "case-1" {
		t.Fatalf("apply = %+v, %v", result, err)
	}
	want := quack.CaseInput{
		TemplateID: "template", TargetDiscordUserID: "target", Source: quack.CaseSourceHoneypot,
		ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
		ContextURL: request.ContextURL, IdempotencyKey: request.IdempotencyKey,
	}
	if creator.guildID != "guild" || creator.input.TemplateID != want.TemplateID ||
		creator.input.TargetDiscordUserID != want.TargetDiscordUserID || creator.input.Source != want.Source ||
		creator.input.ContextChannelDiscordID != want.ContextChannelDiscordID ||
		creator.input.ContextMessageDiscordID != want.ContextMessageDiscordID ||
		creator.input.ContextURL != want.ContextURL || creator.input.IdempotencyKey != want.IdempotencyKey {
		t.Fatalf("case input = %q %+v, want %+v", creator.guildID, creator.input, want)
	}
	request.ActorDiscordUserID = "fabricated-staff"
	if _, err := applier.ApplyHoneypotCase(context.Background(), request); err == nil {
		t.Fatal("accepted a staff attribution")
	}
}
