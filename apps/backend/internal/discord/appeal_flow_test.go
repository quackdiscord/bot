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

// appealHarness is a case harness with an appealable rule and appeal
// handlers.
type appealHarness struct {
	*caseHarness
	appeals discord.Appeals
	ruleID  string
}

func newAppealHarness(t *testing.T, liveActorBits uint64) *appealHarness {
	t.Helper()
	h := &appealHarness{caseHarness: newCaseHarness(t, liveActorBits)}
	h.appeals = discord.NewAppeals(h.services)
	h.ruleID = h.template(t, quack.TemplateInput{
		Slug: "appealable", Name: "Repeated spam", ReasonTemplate: "Spam", Appealable: true,
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true, NotifyUser: true}},
	}).ID
	return h
}

// openCase creates a case against target-1 under the appealable rule.
func (h *appealHarness) openCase(t *testing.T) *quack.CaseResponse {
	t.Helper()
	created, err := h.services.Cases.Create(context.Background(), h.owner, quack.CaseInput{TemplateID: h.ruleID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	return created
}

// appeal files an appeal for a new case.
func (h *appealHarness) appeal(t *testing.T, statement string) *quack.AppealResponse {
	t.Helper()
	appeal, err := h.services.Appeals.Submit(context.Background(), h.openCase(t).ID, "target-1", quack.AppealSubmissionInput{
		Answers: []quack.AppealAnswer{{QuestionID: "reason", Value: statement}},
	})
	if err != nil {
		t.Fatalf("submit appeal: %v", err)
	}
	return appeal
}

// dmComponent is a button click in a DM, where Discord sets User and no
// guild.
func dmComponent(userID, customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "dm-click", AppID: "app-1", Type: discordgo.InteractionMessageComponent, User: &discordgo.User{ID: userID},
		Data: discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

// staffComponent is a click by mod-1 in guild-1.
func staffComponent(customID string, message *discordgo.Message) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "staff-click", AppID: "app-1", Type: discordgo.InteractionMessageComponent, GuildID: "guild-1",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "mod-1"}},
		Message: message,
		Data:    discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

// reasonModal is a submitted form with one "reason" input.
func reasonModal(customID, value string) discordgo.ModalSubmitInteractionData {
	return discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "reason", Value: value}}},
	}}
}

// content is what a task told the user: the edit, or a private followup.
func (f *fakeResponder) content() string {
	if f.followup.Content != "" {
		return f.followup.Content
	}
	if f.edit.Content != nil {
		return *f.edit.Content
	}
	return ""
}

func run(t *testing.T, result discord.Result) *fakeResponder {
	t.Helper()
	if result.Task == nil {
		t.Fatalf("expected a deferred task: %+v", result.Response)
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	return responder
}

// TestAppealDMFormOwnershipAndSingleSubmission drives the DM button and form
// with no guild member, as for a banned member.
func TestAppealDMFormOwnershipAndSingleSubmission(t *testing.T) {
	h := newAppealHarness(t, 0)
	ctx := context.Background()
	created := h.openCase(t)
	id := "appeal:submit:v1:" + created.ID
	click := func(user string) *discordgo.InteractionResponse {
		return h.appeals.OpenForm()(ctx, dmComponent(user, id)).Response
	}
	if response := click("other"); response.Type == discordgo.InteractionResponseModal || !strings.Contains(response.Data.Content, "not available") {
		t.Fatalf("other member opened the form: %+v", response)
	}
	opened := click("target-1")
	if opened.Type != discordgo.InteractionResponseModal || opened.Data.CustomID != id {
		t.Fatalf("owner could not open the form: %+v", opened)
	}
	if row := opened.Data.Components[0].(discordgo.ActionsRow); len(opened.Data.Components) != 1 || len(row.Components) != 1 {
		t.Fatal("form is not one statement")
	}
	submit := func(user string) string {
		i := dmComponent(user, "")
		i.Type = discordgo.InteractionModalSubmit
		i.Data = reasonModal(id, "I understand the rule and am sorry.")
		return run(t, h.appeals.Submit()(ctx, i)).content()
	}
	if text := submit("other"); !strings.Contains(text, "not available") {
		t.Fatalf("forged form accepted: %s", text)
	}
	if text := submit("target-1"); !strings.Contains(text, "Your appeal was submitted") {
		t.Fatalf("submission failed: %s", text)
	}
	if text := submit("target-1"); !strings.Contains(text, "already submitted") {
		t.Fatalf("duplicate lost feedback: %s", text)
	}
	saved, err := h.store.GetAppealByCaseID(ctx, created.ID)
	if err != nil || saved == nil || !strings.Contains(saved.AnswersJSON, "sorry") {
		t.Fatalf("statement not saved: %+v %v", saved, err)
	}
	if response := click("target-1"); response.Type == discordgo.InteractionResponseModal {
		t.Fatal("duplicate appeal reopened the form")
	}

	// A form left open while staff void the case is refused on submit.
	stale := h.openCase(t)
	id = "appeal:submit:v1:" + stale.ID
	if click("target-1").Type != discordgo.InteractionResponseModal {
		t.Fatal("second case form did not open")
	}
	if _, err := h.services.Cases.Void(ctx, h.owner, stale.ID, "Mistake"); err != nil {
		t.Fatal(err)
	}
	if text := submit("target-1"); !strings.Contains(text, "cannot be appealed") {
		t.Fatalf("stale form ignored the void: %s", text)
	}
}

// TestAppealQueueDecisionChecksLivePermissions denies, then accepts, then
// refuses a competing click without touching the shared queue post.
func TestAppealQueueDecisionChecksLivePermissions(t *testing.T) {
	h := newAppealHarness(t, 0)
	ctx := context.Background()
	appeal := h.appeal(t, "Please reconsider.")
	click := func(action string) *fakeResponder {
		result := h.appeals.Decide(action)(ctx, staffComponent("appeal:"+action+":v1:"+appeal.ID, &discordgo.Message{ID: "queue"}))
		if result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
			t.Fatalf("decision did not update the queue post: %+v", result.Response)
		}
		return run(t, result)
	}
	denied := click("accept")
	if denied.editCount != 0 || !denied.followup.Ephemeral || !strings.Contains(denied.followup.Content, "Moderate Members") {
		t.Fatalf("denial was not private: %+v", denied)
	}
	if stored, _ := h.store.GetAppealByID(ctx, appeal.ID); stored.Status != quack.AppealStatusPending {
		t.Fatalf("denial changed the appeal: %+v", stored)
	}
	h.directory.actorBits = uint64(discordgo.PermissionModerateMembers)
	accepted := click("accept")
	if accepted.editCount != 1 || !strings.Contains(*accepted.edit.Content, "Appeal accepted") || len(*accepted.edit.Components) != 0 {
		t.Fatalf("acceptance did not update the post once: %+v", accepted)
	}
	if competing := click("reject"); competing.editCount != 0 || !strings.Contains(competing.followup.Content, "already been decided") {
		t.Fatalf("competing decision: %+v", competing)
	}
	item, err := h.store.GetCaseByID(ctx, appeal.CaseID)
	if err != nil || item.Validity != quack.CaseValidityVoided {
		t.Fatalf("acceptance did not void: %+v %v", item, err)
	}
	member, err := h.services.Appeals.GetMember(ctx, appeal.ID, "target-1")
	if err != nil || member.ReviewedByDiscordUserID != "" {
		t.Fatalf("member saw the reviewer: %+v %v", member, err)
	}
}

// TestAppealQueueDecisionRequiresReason opens the reason form when the
// guild requires one, rejects a blank reason, and rechecks permissions on
// submit.
func TestAppealQueueDecisionRequiresReason(t *testing.T) {
	h := newAppealHarness(t, 0)
	ctx := context.Background()
	if _, err := h.services.Guilds.BootstrapDiscordGuild(ctx, quack.DiscordGuildLifecycleInput{
		DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1",
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := h.store.GetGuildSettings(ctx, h.owner.Guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.AppealReviewReasonRequired = true
	if _, err := h.store.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{Settings: *settings}); err != nil {
		t.Fatal(err)
	}
	appeal := h.appeal(t, "Please reconsider.")
	open := func(action string) string {
		result := h.appeals.Decide(action)(ctx, staffComponent("appeal:"+action+":v1:"+appeal.ID, nil))
		if result.Task != nil || result.Response.Type != discordgo.InteractionResponseModal {
			t.Fatalf("%s did not open a reason form: %+v", action, result)
		}
		input := result.Response.Data.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.TextInput)
		if !input.Required || input.MaxLength != 2000 || input.Label != "Reason sent to the member" {
			t.Fatalf("%s reason input: %+v", action, input)
		}
		return result.Response.Data.CustomID
	}
	acceptForm, rejectForm := open("accept"), open("reject")
	submit := func(action, customID, reason string) discord.Result {
		i := staffComponent("", nil)
		i.Type = discordgo.InteractionModalSubmit
		i.Data = reasonModal(customID, reason)
		return h.appeals.DecideReason(action)(ctx, i)
	}
	if result := submit("accept", acceptForm, "   "); result.Task != nil || !strings.Contains(result.Response.Data.Content, "Write a reason") {
		t.Fatalf("blank reason accepted: %+v", result)
	}
	if denied := run(t, submit("accept", acceptForm, "The evidence supports voiding this case.")); !strings.Contains(denied.content(), "Moderate Members") {
		t.Fatalf("submit did not recheck permissions: %q", denied.content())
	}
	h.directory.actorBits = uint64(discordgo.PermissionModerateMembers)
	if accepted := run(t, submit("accept", acceptForm, "  The evidence supports voiding this case.  ")); !strings.Contains(accepted.content(), "accepted") {
		t.Fatalf("reasoned acceptance failed: %q", accepted.content())
	}
	stored, err := h.store.GetAppealByID(ctx, appeal.ID)
	if err != nil || stored.DecisionReason != "The evidence supports voiding this case." {
		t.Fatalf("reason was not stored: %+v %v", stored, err)
	}
	if competing := run(t, submit("reject", rejectForm, "Opened before acceptance.")); !strings.Contains(competing.content(), "already been decided") {
		t.Fatalf("stale form changed the decision: %q", competing.content())
	}
}

// TestStatementBrowsingRechecksPermissions opens shared queue pages
// publicly, turns private copies in place, and shows nothing of the appeal
// to a moderator who lost access.
func TestStatementBrowsingRechecksPermissions(t *testing.T) {
	h := newAppealHarness(t, 0)
	appeal := h.appeal(t, "Please reconsider.")
	for _, private := range []bool{false, true} {
		message := &discordgo.Message{ID: "queue", Content: "PRIVATE STATEMENT"}
		if private {
			message.Flags = discordgo.MessageFlagsEphemeral
		}
		result := h.appeals.StatementPage(1)(context.Background(), staffComponent("appeal:statement_next:v1:1|"+appeal.ID, message))
		if private != (result.Response.Type == discordgo.InteractionResponseDeferredMessageUpdate) {
			t.Fatalf("private=%v acknowledged with %d", private, result.Response.Type)
		}
		if !private && (result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data != nil) {
			t.Fatal("shared queue browsing was hidden")
		}
		responder := run(t, result)
		if private {
			if !responder.edit.PrivateError || !strings.Contains(*responder.edit.Content, "Moderate Members") || len(*responder.edit.Components) != 0 {
				t.Fatalf("private page kept the appeal: %+v", responder.edit)
			}
		} else if !responder.deleted || !responder.followup.Ephemeral || !strings.Contains(responder.followup.Content, "Moderate Members") {
			t.Fatalf("public placeholder not replaced by a private error: %+v", responder)
		}
		if strings.Contains(responder.content(), "Please reconsider") || message.Content != "PRIVATE STATEMENT" {
			t.Fatal("statement leaked or the queue post changed")
		}
	}
	h.directory.actorBits = uint64(discordgo.PermissionModerateMembers)
	page := run(t, h.appeals.StatementPage(1)(context.Background(), staffComponent("appeal:statement_next:v1:1|"+appeal.ID, &discordgo.Message{})))
	if !strings.Contains(*page.edit.Content, "Please reconsider") {
		t.Fatalf("moderator could not read the statement: %+v", page.edit)
	}
}

// TestAppealsCommandFindsUndeliveredSubmissions reads the queue from
// storage, survives appeals decided between pages, and stops at lost
// permissions.
func TestAppealsCommandFindsUndeliveredSubmissions(t *testing.T) {
	h := newAppealHarness(t, uint64(discordgo.PermissionModerateMembers))
	ctx := context.Background()
	for n := range 2 {
		h.appeal(t, fmt.Sprintf("Statement %d", n))
	}
	command := interaction(discordgo.InteractionApplicationCommand, 0, nil)
	result := h.appeals.Command()(ctx, command)
	if result.Response.Data != nil {
		t.Fatal("staff appeal queue was hidden")
	}
	first := run(t, result)
	if !strings.Contains(*first.edit.Content, "Statement") || !strings.Contains(*first.edit.Content, "Pending appeal 1 of 2") {
		t.Fatalf("undelivered submission missing: %s", *first.edit.Content)
	}
	pending, err := h.services.Appeals.ListStaff(ctx, h.owner, quack.AppealStatusPending, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.services.Appeals.Reject(ctx, h.owner, pending.Appeals[0].ID, "The case stands."); err != nil {
		t.Fatal(err)
	}
	next := run(t, h.appeals.QueuePage()(ctx, staffComponent("appeal:page:v1:2", &discordgo.Message{})))
	if !strings.Contains(*next.edit.Content, "Pending appeal 1 of 1") {
		t.Fatalf("queue shift did not recover: %s", *next.edit.Content)
	}
	h.directory.actorBits = 0
	revoked := run(t, h.appeals.QueuePage()(ctx, staffComponent("appeal:page:v1:1", &discordgo.Message{Flags: discordgo.MessageFlagsEphemeral})))
	if strings.Contains(revoked.content(), "Statement") || !strings.Contains(revoked.content(), "Moderate Members") {
		t.Fatalf("navigation leaked after permission loss: %s", revoked.content())
	}
}
