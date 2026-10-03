package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestCaseComponentRegistrarInstallsRealRecoveryAndPaginationHandlers(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	if err := RegisterCaseComponents(registry); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"list_prev", "list_next", "user_prev", "user_next", "failures_prev", "failures_next", "retry", "dismiss", "void", "reverse", "message_template"} {
		if _, ok, err := registry.LookupComponent(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "payload"})); err != nil || !ok {
			t.Fatalf("component %s not registered: ok=%v err=%v", action, ok, err)
		}
	}
	for _, action := range []string{"void_submit", "reverse_submit", "context_submit"} {
		if _, ok, err := registry.LookupModal(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "payload"})); err != nil || !ok {
			t.Fatalf("modal %s not registered: ok=%v err=%v", action, ok, err)
		}
	}
}

func TestCaseAddUsesStructuredModalAndKeepsPublicSummaryLimited(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guildContext := caseCommandGuildContext(t, services)
	template := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{Slug: "abuse", Name: "Abuse", ReasonTemplate: "Abusive behavior", ContextFields: []quack.TemplateContextFieldInput{{Key: "details", Label: "What happened?", FieldType: quack.ContextFieldLongText, Position: 1, Required: true}}, Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})

	result := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: caseAddInteraction(template.ID, "target-2", uint64(discordgo.PermissionModerateMembers))})
	if result.Response == nil || result.Response.Type != discordgo.InteractionResponseModal || len(result.Response.Data.Components) != 1 {
		t.Fatalf("expected structured context modal, got %+v", result.Response)
	}
	customID := result.Response.Data.CustomID
	modal := interaction(discordgo.InteractionModalSubmit, uint64(discordgo.PermissionModerateMembers), nil)
	modal.ID = "modal-interaction-2"
	modal.Data = discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "context_details", Value: "Repeated abusive replies"}}}}}
	modalResult := handleContextModal(ui.Context{Context: context.Background(), Services: services, Interaction: modal})
	if modalResult.Response == nil || modalResult.Task == nil || modalResult.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected private validation acknowledgement, got %+v", modalResult)
	}
	responder := &fakeResponder{}
	if err := modalResult.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if !responder.deleted || responder.followup.Ephemeral || len(responder.followup.Embeds) != 0 || responder.editCount != 1 {
		t.Fatalf("expected public text after private validation, got %+v", responder)
	}
	for _, want := range []string{"<@target-2>", "Abuse", "Default"} {
		if !strings.Contains(responder.followup.Content, want) {
			t.Fatalf("missing %q: %+v", want, responder.followup)
		}
	}
	for _, hidden := range []string{"Moderator", "Matching Cases", "Visible context", "Evidence", "Repeated abusive replies"} {
		if strings.Contains(responder.followup.Content, hidden) {
			t.Fatalf("public result leaked %s", hidden)
		}
	}

}

func TestMessageContextActionOffersActiveTemplateSelection(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guildContext := caseCommandGuildContext(t, services)
	createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{Slug: "other", Name: "Other", ReasonTemplate: "Other reason", Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})
	interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: "message-command", Type: discordgo.InteractionApplicationCommand, GuildID: "guild-1", ChannelID: "channel-1", Member: &discordgo.Member{User: &discordgo.User{ID: "mod-1", Username: "mod"}, Permissions: int64(discordgo.PermissionModerateMembers)}, Data: discordgo.ApplicationCommandInteractionData{Name: messageCaseCommandName, TargetID: "message-1", Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Messages: map[string]*discordgo.Message{"message-1": {ID: "message-1", ChannelID: "channel-1", Author: &discordgo.User{ID: "target-1"}}}}}}}
	result := HandleMessageCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Response == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || len(result.Response.Data.Components) != 1 {
		t.Fatalf("expected private template selection, got %+v", result.Response)
	}
	row := result.Response.Data.Components[0].(discordgo.ActionsRow)
	menu := row.Components[0].(discordgo.SelectMenu)
	if len(menu.Options) != 2 || !strings.Contains(menu.CustomID, "case:message_template:v1:") {
		t.Fatalf("unexpected active-template selector: %+v", menu)
	}
}

func TestCaseContextWizardSupportsMoreThanFiveStructuredFields(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guildContext := caseCommandGuildContext(t, services)
	fields := make([]quack.TemplateContextFieldInput, 0, 6)
	for index := 1; index <= 6; index++ {
		fields = append(fields, quack.TemplateContextFieldInput{Key: fmt.Sprintf("field_%d", index), Label: fmt.Sprintf("Field %d", index), FieldType: quack.ContextFieldShortText, Position: index, Required: true})
	}
	template := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{Slug: "many-fields", Name: "Many Fields", ReasonTemplate: "Many fields", ContextFields: fields, Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})
	command := caseAddInteraction(template.ID, "target-many", uint64(discordgo.PermissionModerateMembers))
	command.ID = "interaction-many"
	first := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: command})
	if first.Response == nil || first.Response.Type != discordgo.InteractionResponseModal || len(first.Response.Data.Components) != 5 {
		t.Fatalf("expected first five-field modal page, got %+v", first.Response)
	}
	firstComponents := make([]discordgo.MessageComponent, 0, 5)
	for index := 1; index <= 5; index++ {
		firstComponents = append(firstComponents, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: fmt.Sprintf("context_field_%d", index), Value: fmt.Sprintf("value-%d", index)}}})
	}
	firstSubmit := interaction(discordgo.InteractionModalSubmit, uint64(discordgo.PermissionModerateMembers), nil)
	firstSubmit.ID = "modal-many-1"
	firstSubmit.Data = discordgo.ModalSubmitInteractionData{CustomID: first.Response.Data.CustomID, Components: firstComponents}
	continued := handleContextModal(ui.Context{Context: context.Background(), Services: services, Interaction: firstSubmit})
	if continued.Response == nil || continued.Response.Type != discordgo.InteractionResponseChannelMessageWithSource || len(continued.Response.Data.Components) != 1 {
		t.Fatalf("expected private continuation component, got %+v", continued.Response)
	}
	row := continued.Response.Data.Components[0].(discordgo.ActionsRow)
	nextID := row.Components[0].(discordgo.Button).CustomID
	nextInteraction := interaction(discordgo.InteractionMessageComponent, uint64(discordgo.PermissionModerateMembers), nil)
	nextInteraction.ID = "component-many"
	nextInteraction.Data = discordgo.MessageComponentInteractionData{CustomID: nextID}
	next := handleContextNextComponent(ui.Context{Context: context.Background(), Services: services, Interaction: nextInteraction})
	if next.Response == nil || next.Response.Type != discordgo.InteractionResponseModal || len(next.Response.Data.Components) != 1 {
		t.Fatalf("expected final context modal page, got %+v", next.Response)
	}
	finalSubmit := interaction(discordgo.InteractionModalSubmit, uint64(discordgo.PermissionModerateMembers), nil)
	finalSubmit.ID = "modal-many-2"
	finalSubmit.Data = discordgo.ModalSubmitInteractionData{CustomID: next.Response.Data.CustomID, Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "context_field_6", Value: "value-6"}}}}}
	final := handleContextModal(ui.Context{Context: context.Background(), Services: services, Interaction: finalSubmit})
	if final.Task == nil {
		t.Fatalf("expected completed wizard to create case, got %+v", final)
	}
	responder := &fakeResponder{}
	if err := final.Task(context.Background(), responder); err != nil || !responder.deleted {
		t.Fatalf("wizard did not create public case result: responder=%+v err=%v", responder, err)
	}
}

func TestCaseCommandHasNoLegacyDirectPunishmentCommands(t *testing.T) {
	definition := CaseCommandDefinition()
	for _, option := range definition.Options {
		switch option.Name {
		case "warn", "timeout", "kick", "ban":
			t.Fatalf("legacy direct punishment command remains: %s", option.Name)
		}
	}
}
