package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
)

const updateGuildDiscordID = "111111111111111111"

// messageEvidenceClient serves any message in the guild written by
// target-1, counts fetches, and preserves every attachment.
type messageEvidenceClient struct {
	mu        sync.Mutex
	fetches   int
	preserves int
}

func (c *messageEvidenceClient) FetchMessageEvidence(_ context.Context, ref quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	c.mu.Lock()
	c.fetches++
	c.mu.Unlock()
	return &quack.DiscordMessageSnapshot{
		GuildID: ref.GuildID, ChannelID: ref.ChannelID, MessageID: ref.MessageID,
		AuthorDiscordUserID: "target-1", URL: ref.URL, Content: "message " + ref.MessageID, CreatedAt: time.Now().UTC(),
	}, nil
}

func (c *messageEvidenceClient) PreserveEvidenceAttachment(_ context.Context, _, _ string, attachment quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	c.mu.Lock()
	c.preserves++
	c.mu.Unlock()
	return &quack.PreservedDiscordAttachment{URL: "https://cdn.discordapp.com/copy/" + attachment.ID, MessageID: "copy", AttachmentID: "copy-" + attachment.ID}, nil
}

func (c *messageEvidenceClient) RefreshAttachmentURL(_ context.Context, original string) (string, error) {
	return original + "?fresh", nil
}

func (c *messageEvidenceClient) EvidenceAttachmentURL(context.Context, string, string, string) (string, error) {
	return "", nil
}

func messageLink(messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/222222222222222222/%s", updateGuildDiscordID, messageID)
}

// updateFixture is a guild with an evidence channel, a template without
// context fields, and one case against target-1.
func updateFixture(t *testing.T) (*storage.Store, *quack.CaseService, *quack.GuildStaffContext, *messageEvidenceClient, *quack.CaseResponse) {
	t.Helper()
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, updateGuildDiscordID, "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, updateGuildDiscordID, "mod-1", uint64(discordgo.PermissionModerateMembers))
	if err := insertGuildSettings(store, quack.GuildSettings{GuildID: admin.Guild.ID, ManagedEvidenceChannelDiscordID: "999999999999999999"}); err != nil {
		t.Fatal(err)
	}
	template := createAppTemplate(t, ctx, store, admin, validTemplateInput("updates"))
	client := &messageEvidenceClient{}
	service := quack.NewCaseService(store, nil, quack.NewEvidenceService(client), nil)
	created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	return store, service, moderator, client, created
}

func TestUpdateContextSavesTextAndCapturesNewLinksOnce(t *testing.T) {
	ctx := context.Background()
	_, service, moderator, client, created := updateFixture(t)

	text := fmt.Sprintf("Spammed links: %s and again <%s?x=1>. Also %s.",
		messageLink("333333333333333333"), messageLink("333333333333333333"), messageLink("444444444444444444"))
	if !quack.ContextContainsMessageLinks(text) || quack.ContextContainsMessageLinks("no links here") {
		t.Fatal("link detection is wrong")
	}
	detail, err := service.UpdateContext(ctx, moderator, fmt.Sprint(created.CaseNumber), text)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.ContextValues) != 1 || detail.ContextValues[0].Key != "context" || detail.ContextValues[0].Value != text {
		t.Fatalf("context = %+v", detail.ContextValues)
	}
	if len(detail.Evidence) != 2 || client.fetches != 2 || detail.EvidenceIncomplete {
		t.Fatalf("evidence = %+v fetches=%d incomplete=%v", detail.Evidence, client.fetches, detail.EvidenceIncomplete)
	}

	// Saving the same links again captures nothing new; removing a link
	// keeps its snapshot.
	detail, err = service.UpdateContext(ctx, moderator, created.ID, "Only "+messageLink("333333333333333333"))
	if err != nil || len(detail.Evidence) != 2 || client.fetches != 2 {
		t.Fatalf("resave = %+v fetches=%d err=%v", detail, client.fetches, err)
	}

	// A link to another guild cannot be captured, but the text still saves.
	foreign := "https://discord.com/channels/555555555555555555/222222222222222222/666666666666666666"
	detail, err = service.UpdateContext(ctx, moderator, created.ID, "See "+foreign)
	if err != nil || !detail.EvidenceIncomplete || detail.ContextValues[0].Value != "See "+foreign {
		t.Fatalf("foreign link = %+v err=%v", detail, err)
	}

	cleared, err := service.UpdateContext(ctx, moderator, created.ID, "  ")
	if err != nil || len(cleared.ContextValues) != 0 {
		t.Fatalf("clear = %+v err=%v", cleared.ContextValues, err)
	}
	if _, err := service.UpdateContext(ctx, moderator, created.ID, strings.Repeat("x", 4001)); !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("long context = %v", err)
	}
	if _, err := service.UpdateContext(ctx, moderator, "999", "text"); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("missing case = %v", err)
	}
}

func TestAddEvidenceAttachesUploadsAndLinksWithoutReenforcing(t *testing.T) {
	ctx := context.Background()
	store, service, moderator, client, created := updateFixture(t)

	files := make([]quack.DiscordAttachmentSnapshot, 11)
	for i := range files {
		files[i] = quack.DiscordAttachmentSnapshot{ID: fmt.Sprint("file-", i), Filename: "proof.png", ContentType: "image/png", SizeBytes: 10, URL: "https://cdn.discordapp.com/proof"}
	}
	detail, err := service.AddEvidence(ctx, moderator, created.ID, []string{messageLink("333333333333333333")}, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Evidence) != 11 || client.preserves != 10 {
		t.Fatalf("evidence = %d items, %d preserved", len(detail.Evidence), client.preserves)
	}
	upload := detail.Evidence[1]
	if upload.CaptureOutcome != "uploaded" || upload.AuthorDiscordUserID != "mod-1" || !strings.Contains(upload.CaptureWarning, "first ten files") {
		t.Fatalf("first upload = %+v", upload)
	}
	if !detail.EvidenceIncomplete {
		t.Fatal("dropping files did not mark evidence incomplete")
	}
	if actions, err := store.ListCaseActionExecutions(ctx, created.ID); err != nil || len(actions) != 0 {
		t.Fatalf("adding evidence queued actions: %+v %v", actions, err)
	}

	if _, err := service.AddEvidence(ctx, moderator, created.ID, nil, nil); !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("empty add = %v", err)
	}
	reader := templateGuildContext(t, store, updateGuildDiscordID, "reader-1", 1<<10)
	if _, err := service.AddEvidence(ctx, reader, created.ID, nil, files[:1]); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("reader add = %v", err)
	}

	page, err := service.EvidencePage(ctx, moderator, created.ID, 99)
	if err != nil || page.Position != 11 || page.Total != 11 || page.Evidence == nil || page.Evidence.CaptureOutcome != "uploaded" {
		t.Fatalf("last page = %+v err=%v", page, err)
	}
	page, err = service.EvidencePage(ctx, moderator, created.ID, 0)
	if err != nil || page.Position != 1 || page.Evidence.CaptureOutcome != "captured" {
		t.Fatalf("first page = %+v err=%v", page, err)
	}
}

func TestCreateCaptureUploadsAndReportsIncompleteEvidence(t *testing.T) {
	ctx := context.Background()
	store, service, moderator, _, _ := updateFixture(t)
	templates, err := quack.NewTemplateService(store).List(ctx, moderator)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, moderator, quack.CaseInput{
		TemplateID:          templates[0].ID,
		TargetDiscordUserID: "target-2",
		Attachments: []quack.DiscordAttachmentSnapshot{
			{ID: "ok", Filename: "proof.png", ContentType: "image/png", SizeBytes: 10, URL: "https://cdn.discordapp.com/ok"},
			{ID: "exe", Filename: "tool.exe", ContentType: "application/octet-stream", SizeBytes: 10, URL: "https://cdn.discordapp.com/exe"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.EvidenceIncomplete {
		t.Fatal("an uncopied upload did not mark evidence incomplete")
	}
	compact, err := service.GetCompact(ctx, moderator, created.ID)
	if err != nil || len(compact.Evidence) != 2 || !compact.EvidenceIncomplete || compact.RuleName != "Spam" {
		t.Fatalf("compact detail = %+v err=%v", compact, err)
	}
}

func TestGetCompactKeepsLatestEventsAndTimeoutEnd(t *testing.T) {
	ctx := context.Background()
	store, service, moderator, _, created := updateFixture(t)
	for i := range 8 {
		if _, err := service.UpdateContext(ctx, moderator, created.ID, fmt.Sprint("edit ", i)); err != nil {
			t.Fatal(err)
		}
	}
	// Give the case a succeeded timeout whose attempt recorded its end.
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	execution := quack.CaseActionExecution{
		ULIDModel: quack.ULIDModel{ID: quack.NewID(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		CaseID:    created.ID, ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionSucceeded,
		IdempotencyKey: "timeout", ConfigSnapshotJSON: "{}",
	}
	if err := store.DB().Create(&execution).Error; err != nil {
		t.Fatal(err)
	}
	attempt := quack.CaseActionAttempt{
		ULIDModel:   quack.ULIDModel{ID: quack.NewID(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		ExecutionID: execution.ID, AttemptNumber: 1, Status: quack.ActionAttemptSucceeded, StartedAt: time.Now().UTC(),
		RequestPayloadJSON: "{}", ResponsePayloadJSON: fmt.Sprintf(`{"timeout_until":%q}`, until.Format(time.RFC3339)),
	}
	if err := store.DB().Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	full, err := service.Get(ctx, moderator, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := service.GetCompact(ctx, moderator, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(compact.Events) != 6 || compact.Events[5].ID != full.Events[len(full.Events)-1].ID {
		t.Fatalf("compact events = %d, want the latest 6 of %d", len(compact.Events), len(full.Events))
	}
	if got := compact.CaseResponse.Actions[0].TimeoutUntil; got == nil || !got.Equal(until) {
		t.Fatalf("timeout until = %v, want %v", got, until)
	}
	receipt, err := service.Receipt(ctx, moderator.Guild.ID, created.ID)
	if err != nil || receipt.Actions[0].TimeoutUntil == nil || receipt.RuleName != "Spam" || receipt.Pending() != true {
		t.Fatalf("receipt = %+v err=%v", receipt, err)
	}
	if _, err := service.Receipt(ctx, "other-guild", created.ID); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("receipt from another guild = %v", err)
	}
}

// TestUpdateStructuredContextUsesFrozenFields validates edits against the case's
// original required fields even when the live rule changes.
func TestUpdateStructuredContextUsesFrozenFields(t *testing.T) {
	ctx := context.Background()
	store, service, moderator, client, _ := updateFixture(t)
	admin := templateGuildContext(t, store, updateGuildDiscordID, "admin-1", uint64(discordgo.PermissionManageGuild))
	input := validTemplateInput("structured-update")
	input.ContextFields = []quack.TemplateContextFieldInput{
		{Key: "summary", Label: "Summary", FieldType: quack.ContextFieldShortText, Required: true, Position: 1},
		{Key: "confirmed", Label: "Confirmed", FieldType: quack.ContextFieldBoolean, Required: true, Position: 2},
		{Key: "count", Label: "Count", FieldType: quack.ContextFieldNumber, Required: true, Position: 3},
		{Key: "message", Label: "Message", FieldType: quack.ContextFieldMessageLink, Position: 4},
	}
	template := createAppTemplate(t, ctx, store, admin, input)
	values := []quack.CaseContextValueInput{
		{Key: "summary", Value: json.RawMessage(`"Original"`)},
		{Key: "confirmed", Value: json.RawMessage(`false`)},
		{Key: "count", Value: json.RawMessage(`-2.5`)},
		{Key: "message", Value: json.RawMessage(fmt.Sprintf("%q", messageLink("333333333333333333")))},
	}
	created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", ContextValues: values})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.Get(ctx, moderator, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	input.ContextFields[0].Label = "Changed label"
	input.ContextFields[0].Required = false
	if _, err := quack.NewTemplateService(store).Update(ctx, admin, template.ID, input); err != nil {
		t.Fatal(err)
	}
	unchanged, err := service.UpdateContextValues(ctx, moderator, created.ID, values)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.ContextValues, unchanged.ContextValues) || !reflect.DeepEqual(before.TemplateSnapshot, unchanged.TemplateSnapshot) || client.fetches != 1 {
		t.Fatalf("unchanged edit rewrote context/evidence: %+v fetches=%d", unchanged, client.fetches)
	}
	if _, err := service.UpdateContext(ctx, moderator, created.ID, "flattened"); !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("free-text bypass: %v", err)
	}
	for _, bad := range []struct {
		index int
		value string
	}{{0, `" "`}, {1, `"false"`}, {2, `false`}, {2, `"2"`}, {2, `2 3`}, {3, `"https://example.com/message"`}} {
		invalid := append([]quack.CaseContextValueInput(nil), values...)
		invalid[bad.index].Value = json.RawMessage(bad.value)
		if _, err := service.UpdateContextValues(ctx, moderator, created.ID, invalid); !errors.Is(err, quack.ErrCaseValidation) {
			t.Fatalf("invalid field %d accepted: %v", bad.index, err)
		}
	}
	values[0].Value = json.RawMessage(`"Edited"`)
	values[1].Value = json.RawMessage(`true`)
	values[2].Value = json.RawMessage(`42.25`)
	values[3].Value = json.RawMessage(fmt.Sprintf("%q", messageLink("444444444444444444")))
	edited, err := service.UpdateContextValues(ctx, moderator, created.ID, values)
	if err != nil {
		t.Fatal(err)
	}
	if edited.ContextValues[0].Label != "Summary" || !edited.ContextValues[0].Required || edited.ContextValues[0].Value != "Edited" || edited.ContextValues[1].Value != true || edited.ContextValues[2].Value != float64(42.25) || len(edited.Evidence) != 2 || client.fetches != 2 {
		t.Fatalf("edited: %+v fetches=%d", edited, client.fetches)
	}
	if !reflect.DeepEqual(before.TemplateSnapshot, edited.TemplateSnapshot) || before.Validity != edited.Validity || before.Reason != edited.Reason {
		t.Fatal("context update changed original decision")
	}
}
