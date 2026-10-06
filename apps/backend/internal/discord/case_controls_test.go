package discord_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestUserMenuCreatesCaseForSelectedMember picks a rule from the private
// picker and checks the receipt lands in the channel, the picker goes, and
// the receipt is recorded for refresh.
func TestUserMenuCreatesCaseForSelectedMember(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	profile := h.template(t, quack.TemplateInput{
		Slug: "profile-rule", Name: "Profile rule", ReasonTemplate: "Profile violates the rules",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	result := h.cases.UserCommand(context.Background(), userMenu("target-2"))
	if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource ||
		result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("not privately deferred: %+v", result.Response)
	}
	picker := run(t, result)
	if picker.edit.Components == nil || len(*picker.edit.Components) != 1 {
		t.Fatalf("missing rule picker: %+v", picker.edit)
	}
	menu := (*picker.edit.Components)[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if menu.CustomID != "case:user_template:v1:target-2" || len(menu.Options) != 2 {
		t.Fatalf("unexpected picker: %+v", menu)
	}
	pick := component(menu.CustomID, profile.ID)
	pick.Message = &discordgo.Message{ID: "picker-1", Flags: discordgo.MessageFlagsEphemeral}
	responder := run(t, h.cases.Component("user_template")(context.Background(), pick))
	if !responder.deleted || len(h.poster.sent) != 1 || responder.followup.Content != "" {
		t.Fatalf("want one channel post and the picker removed: %+v, posts=%d", responder, len(h.poster.sent))
	}
	posted := h.poster.sent[0].Content
	if !strings.Contains(posted, "<@target-2>") || !strings.Contains(posted, "Profile rule") {
		t.Fatalf("wrong receipt: %s", posted)
	}
	if publications := h.publications(t); len(publications) != 1 || publications[0].MessageID != "posted-1" {
		t.Fatalf("channel receipt not recorded: %+v", publications)
	}
}

// TestContextMenusDeferAndCheckLivePermissions keeps the menus inside
// Discord's response window and keeps a denial private.
// TestStaffWithout2FAAreToldHowToFixIt checks that a moderator in a server
// requiring 2FA that Quack has not confirmed is refused with instructions.
// The refusal is about the person acting, so it is not audited.
func TestStaffWithout2FAAreToldHowToFixIt(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	h.directory.mfaRequired = true
	responder := run(t, h.cases.UserCommand(context.Background(), userMenu("target")))
	if !strings.Contains(*responder.edit.Content, "two-factor authentication") || !strings.Contains(*responder.edit.Content, "dashboard") {
		t.Fatalf("2FA denial = %q", *responder.edit.Content)
	}
	audits, err := h.store.ListAuditLogEntries(context.Background(), h.owner.Guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if audit.Result == quack.AuditResultDenied {
			t.Fatalf("2FA refusal was audited: %+v", audit)
		}
	}
}

// TestMembersUsingStaffCommandsAreNotAudited checks that a member who is
// not staff can type and run a staff command without writing any audit
// entry or staff record: the refusal is about them, not Quack.
func TestMembersUsingStaffCommandsAreNotAudited(t *testing.T) {
	ctx := context.Background()
	h := newCaseHarness(t, uint64(discordgo.PermissionSendMessages))
	count := func() int {
		t.Helper()
		audits, err := h.store.ListAuditLogEntries(ctx, h.owner.Guild.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(audits)
	}
	before := count()
	for _, query := range []string{"s", "sp", "spa"} {
		if result := h.cases.Command(ctx, templateAutocomplete(query)); len(result.Response.Data.Choices) != 0 {
			t.Fatalf("a member was offered rules: %+v", result.Response.Data.Choices)
		}
	}
	if after := count(); after != before {
		t.Fatalf("autocomplete wrote %d audits, want none", after-before)
	}
	responder := run(t, h.cases.UserCommand(ctx, userMenu("target")))
	if !strings.Contains(*responder.edit.Content, "Only moderators") {
		t.Fatalf("member denial = %q", *responder.edit.Content)
	}
	if after := count(); after != before {
		t.Fatalf("running the command wrote %d audits, want none", after-before)
	}
	if record, err := h.store.GetStaffMember(ctx, h.owner.Guild.ID, "mod-1"); err != nil || record != nil {
		t.Fatalf("a member got a staff record: %+v, %v", record, err)
	}
}

func TestContextMenusDeferAndCheckLivePermissions(t *testing.T) {
	h := newCaseHarness(t, 0)
	for name, result := range map[string]discord.Result{
		"user":    h.cases.UserCommand(context.Background(), userMenu("target")),
		"message": h.cases.MessageCommand(context.Background(), messageMenu()),
	} {
		t.Run(name, func(t *testing.T) {
			if result.Response.Data == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
				t.Fatalf("not privately deferred: %+v", result.Response)
			}
			responder := run(t, result)
			if !strings.Contains(*responder.edit.Content, "Only moderators") || len(h.poster.sent) != 0 {
				t.Fatalf("missing private permission denial: %+v", responder)
			}
		})
	}
}

// TestSingleTemplateMenuPublishesImmediately skips the picker when the
// guild has one rule.
func TestSingleTemplateMenuPublishesImmediately(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	responder := run(t, h.cases.UserCommand(context.Background(), userMenu("target-1")))
	if !responder.deleted || responder.editCount != 0 || len(h.poster.sent) != 1 {
		t.Fatalf("want one public receipt and no private copy: %+v", responder)
	}
	if !strings.Contains(h.poster.sent[0].Content, "<@target-1>") {
		t.Fatalf("wrong receipt: %s", h.poster.sent[0].Content)
	}
}

// TestEditContextSavesFreeText replaces a case's context through the modal
// and shows the updated case.
func TestEditContextSavesFreeText(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	run(t, h.cases.Command(context.Background(), caseAdd(h.spamID, "target-1", 0)))
	caseID := h.publications(t)[0].CaseID
	open := h.cases.Component("edit_context")(context.Background(), component("case:edit_context:v1:"+caseID))
	if open.Response.Type != discordgo.InteractionResponseModal || open.Response.Data.Title != "Context for case #1" {
		t.Fatalf("expected the context form, got %+v", open.Response)
	}
	submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
	submit.Data = discordgo.ModalSubmitInteractionData{CustomID: open.Response.Data.CustomID, Components: []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "context", Value: "Posted the same link six times."}}},
	}}
	responder := run(t, h.cases.Modal("edit_context_submit")(context.Background(), submit))
	content := *responder.edit.Content
	if !strings.HasPrefix(content, "Context saved for case #1.") || !strings.Contains(content, "> Context — Posted the same link six times.") {
		t.Fatalf("unexpected result: %s", content)
	}
	evidence := run(t, h.cases.Component("evidence")(context.Background(), component("case:evidence:v1:"+caseID)))
	if !strings.Contains(*evidence.edit.Content, "Evidence for case #1") || !strings.Contains(*evidence.edit.Content, "Posted the same link six times.") ||
		!strings.Contains(*evidence.edit.Content, "No evidence has been added yet.") {
		t.Fatalf("evidence view lost the context: %s", *evidence.edit.Content)
	}
	viewed := run(t, h.cases.Command(context.Background(), interaction(discordgo.InteractionApplicationCommand, 0, subcommand("evidence",
		&discordgo.ApplicationCommandInteractionDataOption{Name: "case", Type: discordgo.ApplicationCommandOptionString, Value: "1"},
	))))
	if !strings.Contains(*viewed.edit.Content, "Evidence for case #1") {
		t.Fatalf("/case evidence with only a case: %s", *viewed.edit.Content)
	}
	history := run(t, h.cases.Component("user_detail")(context.Background(), component("case:user_detail:v1:target-1")))
	if !strings.Contains(*history.edit.Content, "<@target-1>") || !strings.Contains(fmt.Sprint(*history.edit.Components), "https://dash.example/guilds/guild-1/members/target-1") {
		t.Fatalf("history view: %s %+v", *history.edit.Content, *history.edit.Components)
	}
}

// TestRecoveryControlsKeepTheirSource refreshes a private failure queue in
// place but answers a shared message, such as an audit entry, with a new
// public message.
func TestRecoveryControlsKeepTheirSource(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	shared := component("case:retry:v1:execution")
	shared.Message = &discordgo.Message{ID: "audit-entry"}
	if got := h.cases.Component("retry")(context.Background(), shared).Response.Type; got != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("shared source acknowledged with %v", got)
	}
	private := component("case:dismiss:v1:execution")
	private.Message = &discordgo.Message{ID: "queue", Flags: discordgo.MessageFlagsEphemeral}
	if got := h.cases.Component("dismiss")(context.Background(), private).Response.Type; got != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("private source acknowledged with %v", got)
	}
}

// TestEvidencePagingRechecksTheCase rejects tampered page payloads and
// reloads the case under the moderator's current authority.
func TestEvidencePagingRechecksTheCase(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	run(t, h.cases.Command(context.Background(), caseAdd(h.spamID, "target-1", 0)))
	caseID := h.publications(t)[0].CaseID
	for _, payload := range []string{"x|" + caseID, "1:-1|" + caseID, "1:1|", "2000000:1|" + caseID} {
		result := h.cases.Component("evidence_next")(context.Background(), component("case:evidence_next:v1:"+payload))
		if result.Task != nil || !strings.Contains(result.Response.Data.Content, "no longer available") {
			t.Fatalf("payload %q accepted: %+v", payload, result)
		}
	}
	responder := run(t, h.cases.Component("evidence_prev")(context.Background(), component("case:evidence_prev:v1:1:1|"+caseID)))
	if !strings.Contains(*responder.edit.Content, "Evidence for case #1") {
		t.Fatalf("evidence page not reloaded: %s", *responder.edit.Content)
	}
	denied := newCaseHarness(t, 0)
	result := denied.cases.Component("evidence_next")(context.Background(), component("case:evidence_next:v1:1:1|"+caseID))
	if err := result.Task(context.Background(), &fakeResponder{}); err == nil {
		t.Fatal("evidence paged without current authority")
	}
}

// TestEditStructuredContextRoundTrip keeps named typed values through unchanged
// and edited Discord forms, including clearing optional fields.
func TestEditStructuredContextRoundTrip(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	ctx := context.Background()
	fields := []quack.TemplateContextFieldInput{
		{Key: "summary", Label: "Summary", FieldType: quack.ContextFieldShortText, Required: true, Position: 1},
		{Key: "details", Label: "Details", FieldType: quack.ContextFieldLongText, Position: 2},
		{Key: "confirmed", Label: "Confirmed", FieldType: quack.ContextFieldBoolean, Required: true, Position: 3},
		{Key: "count", Label: "Count", FieldType: quack.ContextFieldNumber, Required: true, Position: 4},
		{Key: "message", Label: "Message", FieldType: quack.ContextFieldMessageLink, Position: 5},
		{Key: "extra", Label: "Extra", FieldType: quack.ContextFieldShortText, Position: 6},
	}
	template := h.template(t, quack.TemplateInput{Slug: "structured", Name: "Structured", ReasonTemplate: "Reason", ContextFields: fields, Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})
	values := []quack.CaseContextValueInput{
		{Key: "summary", Value: json.RawMessage(`"Summary text"`)},
		{Key: "details", Value: json.RawMessage(`"Details text"`)},
		{Key: "confirmed", Value: json.RawMessage(`false`)},
		{Key: "count", Value: json.RawMessage(`-2.5`)},
		{Key: "extra", Value: json.RawMessage(`"Optional extra"`)},
	}
	created, err := h.services.Cases.Create(ctx, h.owner, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", ContextValues: values})
	if err != nil {
		t.Fatal(err)
	}
	// A valid stored message value exercises the link input without an external
	// Discord evidence dependency in this adapter fixture.
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	before, err := h.services.Cases.Get(ctx, h.owner, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	before.ContextValues[4].Value = link
	body, _ := json.Marshal(before.ContextValues)
	if _, err := h.store.UpdateCaseContext(ctx, quack.UpdateCaseContextParams{GuildID: h.owner.Guild.ID, CaseRef: created.ID, ContextValuesJSON: string(body)}); err != nil {
		t.Fatal(err)
	}
	for _, edited := range []bool{false, true} {
		open := component("case:edit_context:v1:" + created.ID)
		open.ID = fmt.Sprintf("edit-%t", edited)
		first := h.cases.Component("edit_context")(ctx, open)
		if first.Response.Type != discordgo.InteractionResponseModal || len(first.Response.Data.Components) != 5 {
			t.Fatalf("modal: %+v", first.Response)
		}
		inputs := first.Response.Data.Components
		for index, component := range inputs {
			row := component.(discordgo.ActionsRow)
			input := row.Components[0].(discordgo.TextInput)
			if input.Label != fields[index].Label || input.Required != fields[index].Required {
				t.Fatalf("field metadata: %+v", input)
			}
			if index == 2 && input.Value != "false" || index == 3 && input.Value != "-2.5" || index == 4 && input.Value != link {
				t.Fatalf("typed prefill: %+v", input)
			}
			if edited && index == 0 {
				input.Value = "Edited summary"
			}
			if edited && index == 1 {
				input.Value = ""
			}
			row.Components[0] = input
			inputs[index] = row
		}
		submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
		submit.Data = discordgo.ModalSubmitInteractionData{CustomID: first.Response.Data.CustomID, Components: inputs}
		next := h.cases.Modal("edit_context_submit")(ctx, submit)
		if next.Task != nil || !strings.Contains(next.Response.Data.Content, "Continue") {
			t.Fatalf("next: %+v", next)
		}
		page := h.cases.ContextNext(ctx, component("case:context_next:v1:"+open.ID))
		submit.Data = discordgo.ModalSubmitInteractionData{CustomID: page.Response.Data.CustomID, Components: page.Response.Data.Components}
		run(t, h.cases.Modal("edit_context_submit")(ctx, submit))
		after, err := h.services.Cases.Get(ctx, h.owner, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !edited && !reflect.DeepEqual(before.ContextValues, after.ContextValues) {
			t.Fatalf("unchanged values: before=%+v after=%+v", before.ContextValues, after.ContextValues)
		}
		if edited && (after.ContextValues[0].Value != "Edited summary" || after.ContextValues[1].Value != nil) {
			t.Fatalf("edited values: %+v", after.ContextValues)
		}
		if !reflect.DeepEqual(before.TemplateSnapshot, after.TemplateSnapshot) {
			t.Fatal("edit changed frozen decision")
		}
	}
	open := component("case:edit_context:v1:" + created.ID)
	open.ID = "invalid-edit"
	modal := h.cases.Component("edit_context")(ctx, open)
	for _, bad := range []struct{ key, value string }{{"summary", " "}, {"confirmed", "maybe"}, {"count", "not a number"}} {
		inputs := make([]discordgo.MessageComponent, 0, 5)
		for _, part := range modal.Response.Data.Components {
			row := part.(discordgo.ActionsRow)
			input := row.Components[0].(discordgo.TextInput)
			if input.CustomID == "context_"+bad.key {
				input.Value = bad.value
			}
			inputs = append(inputs, discordgo.ActionsRow{Components: []discordgo.MessageComponent{input}})
		}
		submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
		submit.Data = discordgo.ModalSubmitInteractionData{CustomID: modal.Response.Data.CustomID, Components: inputs}
		result := h.cases.Modal("edit_context_submit")(ctx, submit)
		if result.Task != nil || result.Response.Data == nil || result.Response.Data.Content == "" {
			t.Fatalf("invalid %s accepted: %+v", bad.key, result)
		}
	}
}

// TestExpiredStructuredEditRequiresReopening cannot fall back to the legacy
// free-text route when its in-memory draft is gone.
func TestExpiredStructuredEditRequiresReopening(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
	submit.Data = discordgo.ModalSubmitInteractionData{CustomID: "case:edit_context_submit:v2:expired"}
	result := h.cases.Modal("edit_context_submit")(context.Background(), submit)
	if result.Task != nil || !strings.Contains(result.Response.Data.Content, "Open Edit context again") {
		t.Fatalf("expired draft: %+v", result)
	}
}
