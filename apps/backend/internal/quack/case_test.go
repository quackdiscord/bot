package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
)

func TestCaseServiceCreateFromTemplate(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	service := quack.NewCaseService(store, nil, nil, nil)

	created, err := service.Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
		Metadata:            json.RawMessage(`{"message_id":"123"}`),
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.ID == "" || created.CaseNumber != 1 {
		t.Fatalf("unexpected case response: %+v", created)
	}
	if created.Reason != "No spam" || created.Validity != quack.CaseValidityValid || created.Source != quack.CaseSourceDashboard {
		t.Fatalf("unexpected case fields: %+v", created)
	}
	if len(created.Actions) != 0 {
		t.Fatalf("expected notification to be separate from enforcement actions, got %+v", created.Actions)
	}
	if created.SelectedLevel == nil || !created.SelectedLevel.IsDefault || created.SelectedLevel.MatchedCaseCount != 1 {
		t.Fatalf("expected selected default level, got %+v", created.SelectedLevel)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != quack.NotificationPending {
		t.Fatalf("expected pending case notification, got %+v err=%v", notification, err)
	}

	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	var snapshot struct {
		Template struct {
			ID             string `json:"id"`
			ReasonTemplate string `json:"reason_template"`
		} `json:"template"`
		Actions []struct {
			ActionType quack.ActionType `json:"action_type"`
		} `json:"actions"`
		SelectedLevel struct {
			ID               string `json:"id"`
			IsDefault        bool   `json:"is_default"`
			MatchedCaseCount int64  `json:"matched_case_count"`
		} `json:"selected_level"`
	}
	if err := json.Unmarshal([]byte(cases[0].TemplateSnapshotJSON), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Template.ID != template.ID || snapshot.Template.ReasonTemplate != created.Reason || len(snapshot.Actions) != 0 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.SelectedLevel.ID == "" || !snapshot.SelectedLevel.IsDefault || snapshot.SelectedLevel.MatchedCaseCount != 1 {
		t.Fatalf("unexpected selected level snapshot: %+v", snapshot.SelectedLevel)
	}
}

func TestCaseServiceRejectsUnavailableTemplates(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	templateService := quack.NewTemplateService(store)
	caseService := quack.NewCaseService(store, nil, nil, nil)

	archivedTemplate := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("archived"))
	if _, err := templateService.Archive(ctx, adminContext, archivedTemplate.ID); err != nil {
		t.Fatalf("archive template: %v", err)
	}

	tests := []struct {
		name       string
		templateID string
	}{
		{name: "missing", templateID: "missing-template"},
		{name: "archived", templateID: archivedTemplate.ID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := caseService.Create(ctx, modContext, quack.CaseInput{
				TemplateID:          tt.templateID,
				TargetDiscordUserID: "target-1",
			})
			if !errors.Is(err, quack.ErrCaseTemplateNotAvailable) {
				t.Fatalf("expected unavailable template error, got %v", err)
			}
		})
	}
}

func TestCaseServiceValidationFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	service := quack.NewCaseService(store, nil, nil, nil)

	tests := []struct {
		name  string
		input quack.CaseInput
	}{
		{name: "missing template", input: quack.CaseInput{TargetDiscordUserID: "target-1"}},
		{name: "missing target", input: quack.CaseInput{TemplateID: template.ID}},
		{name: "invalid source", input: quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", Source: "invalid"}},
		{name: "invalid metadata", input: quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", Metadata: json.RawMessage(`[]`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Create(ctx, modContext, tt.input)
			if !errors.Is(err, quack.ErrCaseValidation) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func TestCaseServiceRejectsEmptyFinalReason(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	created, err := store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: quack.CaseTemplate{
			GuildID:                guildContext.Guild.ID,
			Slug:                   "empty-reason",
			Name:                   "Empty Reason",
			ReasonTemplate:         " ",
			CreatedByDiscordUserID: "admin-1",
			UpdatedByDiscordUserID: "admin-1",
		},
		Levels: []quack.ExpandedCaseTemplateLevel{
			{
				Level: quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = quack.NewCaseService(store, nil, nil, nil).Create(ctx, guildContext, quack.CaseInput{
		TemplateID:          created.Template.ID,
		TargetDiscordUserID: "target-1",
	})
	if !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestCaseServiceCreatesActionlessWarningCase(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)

	input := validTemplateInput("silent-warning")
	input.Levels[0].NotifyUser = false
	template := createAppTemplate(t, ctx, store, adminContext, input)

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if len(created.Actions) != 0 {
		t.Fatalf("expected no action rows for silent warning case, got %+v", created.Actions)
	}
	events, err := store.ListCaseEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("list case events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != quack.CaseEventCreated {
		t.Fatalf("expected only case created event, got %+v", events)
	}
	audits, err := store.ListAuditLogEntries(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if audits[len(audits)-1].Action != "case.create" {
		t.Fatalf("expected case.create as warning audit trail, got %+v", audits)
	}
}

func TestCaseServicePermissionFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)

	noCreateContext := *modContext
	noCreateContext.Permissions = map[quack.PermissionAction]bool{quack.PermissionActionCaseCreate: false}
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	_, err := service.Create(ctx, &noCreateContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("expected case.create permission error, got %v", err)
	}
}

func TestCaseServiceTraceIDsPropagateToCaseActionsAndAudit(t *testing.T) {
	ctx := quack.ContextWithTrace(context.Background(), "req-case-1", "corr-case-1")
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("trace-spam"))

	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create traced case: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected created case id")
	}

	caseModel, err := store.GetCaseByIDOrNumber(ctx, modContext.Guild.ID, created.ID)
	if err != nil {
		t.Fatalf("get traced case: %v", err)
	}
	if caseModel == nil || caseModel.CorrelationID != "corr-case-1" {
		t.Fatalf("expected case correlation id, got %+v", caseModel)
	}

	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list traced actions: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("expected case-level notification instead of a synthetic action, got %+v", actions)
	}

	audits, err := store.ListAuditLogEntries(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	var foundCaseAudit bool
	for _, audit := range audits {
		if audit.Action == "case.create" {
			foundCaseAudit = true
			if audit.RequestID != "req-case-1" || audit.CorrelationID != "corr-case-1" {
				t.Fatalf("expected traced case audit, got %+v", audit)
			}
		}
	}
	if !foundCaseAudit {
		t.Fatalf("expected case.create audit in %+v", audits)
	}
}

func createAppTemplate(t *testing.T, ctx context.Context, store *storage.Store, guildContext *quack.GuildStaffContext, input quack.TemplateInput) *quack.TemplateResponse {
	t.Helper()

	created, err := quack.NewTemplateService(store).Create(ctx, guildContext, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

func TestCaseContextEvidenceVoidReplacementAndMemberProjection(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildDiscordID := "111111111111111111"
	admin := templateGuildContext(t, store, guildDiscordID, "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, guildDiscordID, "mod-1", uint64(discordgo.PermissionModerateMembers))
	settings := quack.GuildSettings{GuildID: admin.Guild.ID, ManagedEvidenceChannelDiscordID: "999999999999999999"}
	if err := store.DB().Create(&settings).Error; err != nil {
		t.Fatalf("create evidence settings: %v", err)
	}
	input := validTemplateInput("evidence-policy")
	input.ContextFields = []quack.TemplateContextFieldInput{{Key: "summary", Label: "Summary", FieldType: quack.ContextFieldShortText, Position: 1, Required: true}, {Key: "message", Label: "Message", FieldType: quack.ContextFieldMessageLink, Position: 2, Required: true}, {Key: "details", Label: "Details", FieldType: quack.ContextFieldLongText, Position: 3}}
	template := createAppTemplate(t, ctx, store, admin, input)
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	evidenceClient := &fakeEvidenceClient{message: quack.DiscordMessageSnapshot{GuildID: guildDiscordID, ChannelID: "222222222222222222", MessageID: "333333333333333333", AuthorDiscordUserID: "target-1", URL: link, Content: "original text", CreatedAt: time.Now().UTC(), Attachments: []quack.DiscordAttachmentSnapshot{{ID: "a1", Filename: "proof.png", ContentType: "image/png", SizeBytes: 100, URL: "https://cdn.discordapp.com/proof"}}}, preserved: quack.PreservedDiscordAttachment{URL: "https://cdn.discordapp.com/copy", MessageID: "copy-message", AttachmentID: "copy-attachment"}}
	service := quack.NewCaseService(store, nil, quack.NewEvidenceService(store, evidenceClient), nil)
	summary, _ := json.Marshal("visible summary")
	message, _ := json.Marshal(link)
	created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", ContextValues: []quack.CaseContextValueInput{{Key: "summary", Value: summary}, {Key: "message", Value: message}, {Key: "details", Value: json.RawMessage("null")}}})
	if err != nil {
		t.Fatalf("create evidence case: %v", err)
	}
	detail, err := service.Get(ctx, moderator, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.ContextValues) != 3 || detail.ContextValues[2].Value != nil || len(detail.Evidence) != 1 || len(detail.Evidence[0].Attachments) != 1 || detail.Evidence[0].Attachments[0].CopyOutcome != "preserved" {
		t.Fatalf("case snapshot incomplete: %+v", detail)
	}
	voided, err := service.Void(ctx, moderator, created.ID, "wrong policy")
	if err != nil || voided.Validity != quack.CaseValidityVoided {
		t.Fatalf("void failed: %+v err=%v", voided, err)
	}
	replacement, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", ReplacesCaseID: created.ID, ContextValues: []quack.CaseContextValueInput{{Key: "summary", Value: summary}, {Key: "message", Value: message}, {Key: "details", Value: json.RawMessage("null")}}})
	if err != nil {
		t.Fatalf("replacement: %v", err)
	}
	if replacement.ReplacesCaseID == nil || *replacement.ReplacesCaseID != created.ID {
		t.Fatalf("replacement link missing: %+v", replacement)
	}
	member, err := service.GetMemberCase(ctx, replacement.ID, "target-1")
	if err != nil {
		t.Fatalf("member detail: %v", err)
	}
	if member.Reason != "No spam" || len(member.ContextValues) != 3 || len(member.Evidence) != 1 {
		t.Fatalf("member projection incomplete: %+v", member)
	}
	if _, err := service.GetMemberCase(ctx, replacement.ID, "other-user"); err != quack.ErrCaseNotFound {
		t.Fatalf("cross-user enumeration was not hidden: %v", err)
	}
}

func TestCaseCreationIdempotencyPreventsDuplicateWork(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, admin, validTemplateInput("idempotent-policy"))
	service := quack.NewCaseService(store, nil, nil, nil)
	input := quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", IdempotencyKey: "discord-interaction-1"}
	first, err := service.Create(ctx, moderator, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, moderator, input)
	if err != nil || second.ID != first.ID {
		t.Fatalf("duplicate request created another result: first=%s second=%+v err=%v", first.ID, second, err)
	}
	cases, err := store.ListCases(ctx, moderator.Guild.ID)
	if err != nil || len(cases) != 1 {
		t.Fatalf("duplicate case rows: %+v err=%v", cases, err)
	}
	input.TargetDiscordUserID = "target-2"
	if _, err := service.Create(ctx, moderator, input); !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("idempotency key collision accepted: %v", err)
	}
}

func TestSystemHoneypotCaseUsesNormalPathWithoutFabricatedStaff(t *testing.T) {
	ctx := quack.ContextWithTrace(context.Background(), "req-honeypot", "corr-honeypot")
	repository := newMigratedStore(t)
	guild, err := repository.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "111111111111111111", Name: "Guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	template, err := repository.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: quack.CaseTemplate{GuildID: guild.ID, Slug: "honeypot", Name: "Honeypot", ReasonTemplate: "Trap channel activity", CreatedByDiscordUserID: "admin", UpdatedByDiscordUserID: "admin"},
		Levels: []quack.ExpandedCaseTemplateLevel{{
			Level:   quack.CaseTemplateLevel{Name: "Default", Position: 1, IsDefault: true, NotifyUser: true},
			Actions: []quack.CaseTemplateLevelAction{{ActionType: quack.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":60}`, MaxRetries: 2}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &quack.DiscordGuildAuthorization{
		Guild:  quack.DiscordBotGuild{ID: "111111111111111111", Name: "Guild", OwnerID: "owner"},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", PermissionBits: uint64(discordgo.PermissionAdministrator), TopRolePosition: 20, Present: true, Bot: true},
		Target: &quack.DiscordMemberAuthorization{DiscordUserID: "target", Present: true, TopRolePosition: 1},
	}
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	services := quack.New(quack.Deps{
		Store:  repository,
		Guilds: fakeDiscordClient{botGuild: &snapshot.Guild, authorization: snapshot},
		Evidence: &fakeEvidenceClient{message: quack.DiscordMessageSnapshot{
			GuildID: "111111111111111111", ChannelID: "222222222222222222", MessageID: "333333333333333333",
			AuthorDiscordUserID: "target", URL: link, Content: "evidence", CreatedAt: time.Now().UTC(),
		}},
	})
	input := quack.CaseInput{
		TemplateID: template.Template.ID, TargetDiscordUserID: "target",
		Source: quack.CaseSourceHoneypot, ContextChannelDiscordID: "222222222222222222",
		ContextMessageDiscordID: "333333333333333333", ContextURL: link,
		IdempotencyKey: "honeypot:111111111111111111:333333333333333333",
	}
	created, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, input)
	if err != nil {
		t.Fatalf("create system honeypot case: %v", err)
	}
	replayed, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, input)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("idempotent replay changed result: got=%+v err=%v", replayed, err)
	}
	cases, err := repository.ListCases(ctx, guild.ID)
	if err != nil || len(cases) != 1 {
		t.Fatalf("expected one case: %+v err=%v", cases, err)
	}
	if cases[0].Source != quack.CaseSourceHoneypot || cases[0].ModeratorDiscordUserID != "" || cases[0].ContextMessageDiscordID != "333333333333333333" || cases[0].CorrelationID != "corr-honeypot" {
		t.Fatalf("system attribution or context changed: %+v", cases[0])
	}
	events, _ := repository.ListCaseEvents(ctx, created.ID)
	actions, _ := repository.ListCaseActionExecutions(ctx, created.ID)
	evidence, _, _ := repository.ListCaseEvidence(ctx, created.ID)
	notification, _ := repository.GetCaseNotification(ctx, created.ID)
	if len(events) != 1 || events[0].ActorType != "system" || events[0].ActorDiscordUserID != "" || len(actions) != 1 || actions[0].Status != quack.ActionExecutionPending || len(evidence) != 1 || notification == nil || notification.Status != quack.NotificationPending {
		t.Fatalf("normal path parity missing: events=%+v actions=%+v evidence=%+v notification=%+v", events, actions, evidence, notification)
	}
	audits, _ := repository.ListAuditLogEntries(ctx, guild.ID)
	if len(audits) < 2 || audits[len(audits)-2].Source != quack.AuditSourceHoneypot || audits[len(audits)-2].ActorDiscordUserID != "" || audits[len(audits)-2].Action != "case.create" {
		t.Fatalf("case audit fabricated staff identity: %+v", audits)
	}

	if _, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, quack.CaseInput{TemplateID: template.Template.ID, TargetDiscordUserID: "target", Source: quack.CaseSourceDashboard}); !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("non-honeypot system misuse accepted: %v", err)
	}

	snapshot.Bot.PermissionBits = 0
	input.ContextMessageDiscordID = "444444444444444444"
	input.IdempotencyKey = "honeypot:111111111111111111:444444444444444444"
	input.ContextURL = ""
	if _, err := services.Cases.CreateSystemHoneypot(ctx, guild.ID, input); !errors.Is(err, quack.ErrAuthorizationDenied) {
		t.Fatalf("unsafe bot capability accepted: %v", err)
	}
	cases, _ = repository.ListCases(ctx, guild.ID)
	if len(cases) != 1 {
		t.Fatalf("failed system preflight partially committed: %+v", cases)
	}
	audits, _ = repository.ListAuditLogEntries(ctx, guild.ID)
	last := audits[len(audits)-1]
	if last.Source != quack.AuditSourceHoneypot || last.ActorDiscordUserID != "" || last.Action != "authorization.denied" || last.Result != quack.AuditResultDenied {
		t.Fatalf("system denial audit is incorrect: %+v", last)
	}
}
