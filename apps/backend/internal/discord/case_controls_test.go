package discord_test

import (
	"context"
	"fmt"
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
			if !strings.Contains(*responder.edit.Content, "permission") || len(h.poster.sent) != 0 {
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
