package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

func newAppealTestStore(t *testing.T) (*Store, *quack.Guild) {
	t.Helper()
	db := openSQLiteMigrationDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open appeal test connection: %v", err)
	}
	// SQLite ignores SELECT FOR UPDATE. Keep one connection so concurrent
	// transition tests model MySQL's row-lock serialization instead of failing
	// both transactions with shared-cache table-lock errors.
	sqlDB.SetMaxOpenConns(1)
	migrations := registeredMigrations()
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("migrate appeals schema: %v", err)
	}
	repository := New(db, nil)
	guild, err := repository.UpsertGuild(context.Background(), quack.UpsertGuildParams{DiscordGuildID: "appeal-guild", Name: "Appeal Guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatalf("create guild: %v", err)
	}
	return repository, guild
}

func createAppealableCase(t *testing.T, repository *Store, guildID, target string, appealable bool) *quack.Case {
	t.Helper()
	snapshot := `{"template":{"appealable":true}}`
	if !appealable {
		snapshot = `{"template":{"appealable":false}}`
	}
	created, err := repository.CreateCase(context.Background(), quack.CreateCaseParams{
		Case:  quack.Case{GuildID: guildID, TemplateVersion: 1, TemplateSnapshotJSON: snapshot, TargetDiscordUserID: target, ModeratorDiscordUserID: "moderator", Reason: "Official reason", Validity: quack.CaseValidityValid, Source: quack.CaseSourceDashboard, MetadataJSON: "{}", ContextValuesJSON: "[]"},
		Event: quack.CaseEvent{EventType: quack.CaseEventCreated, ActorDiscordUserID: "moderator", ActorType: "staff", Visibility: quack.EventVisibilityPublic, Body: "Case created", MetadataJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	return &created.Case
}

func TestLogical0200MigrationCreatesAppealContracts(t *testing.T) {
	repository, _ := newAppealTestStore(t)
	for _, table := range []any{&appealV5Record{}, &appealEventV5Record{}, &GuildAppealSettingsRecord{}, &AppealNotificationRecord{}} {
		if !repository.db.Migrator().HasTable(table) {
			t.Fatalf("missing appeal table for %T", table)
		}
	}
	if !repository.db.Migrator().HasIndex(&appealV5Record{}, "CaseID") {
		t.Fatal("missing one-appeal-per-case unique index")
	}
}

func TestLogical0200MigrationPreservesPlaceholderAppealsSafely(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()[:8]); err != nil {
		t.Fatalf("migrate baseline: %v", err)
	}
	repository := New(db, nil)
	guild, err := repository.UpsertGuild(context.Background(), quack.UpsertGuildParams{DiscordGuildID: "legacy-appeal", Name: "Legacy Appeal", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatalf("create guild: %v", err)
	}
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	now := time.Now().UTC()
	legacy := migration0200LegacyAppeal{ID: "01KXLEGACYAPPEAL0000000001", GuildID: guild.ID, CaseID: &item.ID, TargetDiscordUserID: "target", Status: string(quack.AppealStatusPending), Content: "legacy content", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := insertLegacyAppeal(db, &legacy); err != nil {
		t.Fatalf("insert legacy appeal: %v", err)
	}
	legacyEvent := AppealEventRecord{ULIDModelRecord: ULIDModelRecord{ID: "01KXLEGACYAPPEALEVENT0001", CreatedAt: now, UpdatedAt: now}, AppealID: legacy.ID, GuildID: guild.ID, EventType: "reviewed", ActorDiscordUserID: "legacy-moderator", Body: "legacy review", MetadataJSON: "{}"}
	if err := db.Create(&legacyEvent).Error; err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	migrations := registeredMigrations()
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("upgrade legacy appeal: %v", err)
	}
	var upgraded appealV5Record
	if err := db.First(&upgraded, "id = ?", legacy.ID).Error; err != nil {
		t.Fatalf("read upgraded appeal: %v", err)
	}
	if upgraded.Content != legacy.Content || !strings.Contains(upgraded.QuestionSnapshotJSON, "legacy_content") || !strings.Contains(upgraded.AnswersJSON, legacy.Content) || upgraded.Version != 1 {
		t.Fatalf("legacy appeal was not preserved and backfilled: %+v", upgraded)
	}
	legacyResponse, err := quack.NewAppealService(repository).GetMember(context.Background(), legacy.ID, "target")
	if err != nil || len(legacyResponse.Questions) != 1 || len(legacyResponse.Answers) != 1 {
		t.Fatalf("upgraded legacy appeal is not readable: %+v err=%v", legacyResponse, err)
	}
	var upgradedEvent appealEventV5Record
	if err := db.First(&upgradedEvent, "id = ?", legacyEvent.ID).Error; err != nil || upgradedEvent.ActorType != "staff" {
		t.Fatalf("legacy staff identity was not safely classified: %+v err=%v", upgradedEvent, err)
	}
}

func TestMySQLLogical0200AppealMigrationAndAcceptance(t *testing.T) {
	db := openMySQLMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()[:8]); err != nil {
		t.Fatalf("migrate MySQL baseline: %v", err)
	}
	repository := New(db, nil)
	guild, err := repository.UpsertGuild(context.Background(), quack.UpsertGuildParams{DiscordGuildID: "mysql-appeal", Name: "MySQL Appeal", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatalf("create MySQL guild: %v", err)
	}
	legacyCase := createAppealableCase(t, repository, guild.ID, "legacy-target", true)
	now := time.Now().UTC()
	legacy := migration0200LegacyAppeal{ID: "01KXMYSQLLEGACYAPPEAL00001", GuildID: guild.ID, CaseID: &legacyCase.ID, TargetDiscordUserID: "legacy-target", Status: string(quack.AppealStatusPending), Content: "preserved MySQL content", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := insertLegacyAppeal(db, &legacy); err != nil {
		t.Fatalf("insert MySQL legacy appeal: %v", err)
	}
	migrations := registeredMigrations()
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("migrate MySQL appeal schema: %v", err)
	}
	var upgraded appealV5Record
	if err := db.First(&upgraded, "id = ?", legacy.ID).Error; err != nil || upgraded.Content != legacy.Content || upgraded.Version != 1 {
		t.Fatalf("MySQL legacy appeal was not preserved: %+v err=%v", upgraded, err)
	}
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	service := quack.NewAppealService(repository)
	appeal, err := service.Submit(context.Background(), item.ID, "target", quack.AppealSubmissionInput{Answers: []quack.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatalf("submit MySQL appeal: %v", err)
	}
	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[quack.PermissionAction]bool{quack.PermissionActionAppealReview: true}}
	if _, err := service.Accept(context.Background(), moderator, appeal.ID, "Accepted after review."); err != nil {
		t.Fatalf("accept MySQL appeal: %v", err)
	}
	persisted, err := repository.GetCaseByID(context.Background(), item.ID)
	if err != nil || persisted.Validity != quack.CaseValidityVoided {
		t.Fatalf("MySQL acceptance did not atomically void case: %+v err=%v", persisted, err)
	}
}

func TestAppealServiceOwnershipSnapshotTimelineAndAtomicAcceptance(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	caseModel := createAppealableCase(t, repository, guild.ID, "target", true)
	now := time.Now().UTC()
	originalAction := quack.CaseActionExecution{ULIDModel: quack.ULIDModel{ID: "01KXAPPEALACTION0000000001", CreatedAt: now, UpdatedAt: now}, CaseID: caseModel.ID, Position: 0, ActionType: quack.ActionBanUser, Status: quack.ActionExecutionSucceeded, IdempotencyKey: "appeal-original-ban", ConfigSnapshotJSON: "{}", SafeForRetry: false}
	if err := repository.db.Create(&originalAction).Error; err != nil {
		t.Fatalf("create original enforcement: %v", err)
	}
	queuedAction := quack.CaseActionExecution{ULIDModel: quack.ULIDModel{ID: "01KXAPPEALACTION0000000002", CreatedAt: now, UpdatedAt: now}, CaseID: caseModel.ID, Position: 1, ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionPending, IdempotencyKey: "appeal-pending-timeout", ConfigSnapshotJSON: "{}", SafeForRetry: true}
	if err := repository.db.Create(&queuedAction).Error; err != nil {
		t.Fatalf("create queued enforcement: %v", err)
	}
	queuedCaseNotification := quack.CaseNotification{ULIDModel: quack.ULIDModel{ID: "01KXAPPEALNOTICE00000000001", CreatedAt: now, UpdatedAt: now}, CaseID: caseModel.ID, Status: quack.NotificationPending}
	if err := repository.db.Create(&queuedCaseNotification).Error; err != nil {
		t.Fatalf("create queued case notification: %v", err)
	}
	service := quack.NewAppealService(repository)

	settings, err := service.GetSettings(ctx, guild.ID)
	if err != nil || !settings.Default || len(settings.Questions) == 0 {
		t.Fatalf("default settings: %+v err=%v", settings, err)
	}
	manager := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "manager"}, Permissions: map[quack.PermissionAction]bool{quack.PermissionActionGuildSettingsWrite: true}}
	configuredQuestions := []quack.AppealQuestion{{ID: "explanation", Prompt: "Explain your appeal", Type: quack.AppealQuestionLongText, Required: true, Position: 0}, {ID: "contact", Prompt: "May staff contact you?", Type: quack.AppealQuestionBoolean, Position: 1}}
	configured, err := service.UpdateSettings(ctx, manager, configuredQuestions)
	if err != nil || configured.Default || len(configured.Questions) != 2 {
		t.Fatalf("configure appeal form: %+v err=%v", configured, err)
	}
	answers := []quack.AppealAnswer{{QuestionID: "explanation", Value: "The decision should be reconsidered."}, {QuestionID: "contact", Value: true}}
	appeal, err := service.Submit(ctx, caseModel.ID, "target", quack.AppealSubmissionInput{Answers: answers})
	if err != nil {
		t.Fatalf("submit appeal: %v", err)
	}
	if appeal.Status != quack.AppealStatusPending || len(appeal.Questions) != len(configuredQuestions) || len(appeal.Events) != 1 {
		t.Fatalf("unexpected submitted appeal: %+v", appeal)
	}
	if _, err := service.UpdateSettings(ctx, manager, []quack.AppealQuestion{{ID: "replacement", Prompt: "Replacement future question", Type: quack.AppealQuestionShortText, Required: true, Position: 0}}); err != nil {
		t.Fatalf("replace future appeal form: %v", err)
	}
	snapshotted, err := service.GetMember(ctx, appeal.ID, "target")
	if err != nil || len(snapshotted.Questions) != 2 || snapshotted.Questions[0].ID != "explanation" {
		t.Fatalf("appeal form was not snapshotted: %+v err=%v", snapshotted, err)
	}
	if _, err := service.Submit(ctx, caseModel.ID, "target", quack.AppealSubmissionInput{Answers: answers}); !errors.Is(err, quack.ErrAppealConflict) {
		t.Fatalf("expected one appeal per case, got %v", err)
	}
	if _, err := service.GetMember(ctx, appeal.ID, "other"); !errors.Is(err, quack.ErrAppealNotFound) {
		t.Fatalf("unrelated member read should be hidden, got %v", err)
	}

	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, ActorDiscordUserID: "moderator", Permissions: map[quack.PermissionAction]bool{quack.PermissionActionAppealReview: true}}
	requested, err := service.RequestInformation(ctx, moderator, appeal.ID, "Please clarify the timeline.")
	if err != nil || requested.Status != quack.AppealStatusNeedsInformation {
		t.Fatalf("request information: %+v err=%v", requested, err)
	}
	memberView, err := service.GetMember(ctx, appeal.ID, "target")
	if err != nil {
		t.Fatalf("member read: %v", err)
	}
	for _, event := range memberView.Events {
		if event.ActorType == "staff" && event.ActorDiscordUserID != "" {
			t.Fatalf("member timeline leaked staff identity: %+v", event)
		}
	}
	if _, err := service.SubmitInformation(ctx, appeal.ID, "target", quack.AppealInformationInput{Body: "Additional timeline context."}); err != nil {
		t.Fatalf("submit information: %v", err)
	}
	accepted, err := service.Accept(ctx, moderator, appeal.ID, "The added context changes the decision.")
	if err != nil || accepted.Status != quack.AppealStatusAccepted || len(accepted.ReversalOffers) != 1 || accepted.ReversalOffers[0].ActionType != quack.ActionUnbanUser {
		t.Fatalf("accept appeal: %+v err=%v", accepted, err)
	}
	actionsBeforeReversal, err := repository.ListCaseActionExecutions(ctx, caseModel.ID)
	if err != nil || len(actionsBeforeReversal) != 2 || actionsBeforeReversal[1].Status != quack.ActionExecutionCancelled || actionsBeforeReversal[1].LastErrorCode != "case_voided" {
		t.Fatalf("acceptance silently queued a reversal: actions=%+v err=%v", actionsBeforeReversal, err)
	}
	var cancelledNotification quack.CaseNotification
	if err := repository.db.First(&cancelledNotification, "id = ?", queuedCaseNotification.ID).Error; err != nil || cancelledNotification.Status != quack.NotificationFailed || cancelledNotification.LastErrorCode != "case_voided" {
		t.Fatalf("appeal acceptance did not cancel queued case notification: %+v err=%v", cancelledNotification, err)
	}
	appealID := appeal.ID
	queued, err := repository.QueueCaseReversal(ctx, quack.QueueCaseReversalParams{GuildID: guild.ID, CaseID: caseModel.ID, ActorDiscordUserID: "moderator", OriginalExecutionID: originalAction.ID, ActionType: quack.ActionUnbanUser, AppealID: &appealID})
	if err != nil || queued == nil || queued.ReversalAppealID == nil || *queued.ReversalAppealID != appeal.ID {
		t.Fatalf("explicit accepted-appeal reversal was not linked: %+v err=%v", queued, err)
	}
	persistedCase, err := repository.GetCaseByID(ctx, caseModel.ID)
	if err != nil || persistedCase.Validity != quack.CaseValidityVoided {
		t.Fatalf("accepted appeal did not atomically void case: %+v err=%v", persistedCase, err)
	}
	if _, err := service.Reject(ctx, moderator, appeal.ID, "late competing decision"); !errors.Is(err, quack.ErrAppealConflict) {
		t.Fatalf("accepted appeal allowed competing decision: %v", err)
	}
	caseService := quack.NewCaseService(repository, nil, nil, nil)
	memberDetail, err := caseService.GetMemberCase(ctx, caseModel.ID, "target")
	if err != nil || memberDetail.Validity != quack.CaseValidityVoided || memberDetail.AppealStatus != quack.AppealStatusAccepted || memberDetail.Appealable {
		t.Fatalf("member case projection did not retain voided accepted appeal: %+v err=%v", memberDetail, err)
	}
	memberCases, err := caseService.ListMemberCases(ctx, guild.ID, "target", quack.CaseListInput{})
	if err != nil || len(memberCases.Cases) != 1 || memberCases.Cases[0].Validity != quack.CaseValidityVoided {
		t.Fatalf("member history omitted voided case: %+v err=%v", memberCases, err)
	}
	encodedMember, _ := json.Marshal(memberDetail)
	if strings.Contains(string(encodedMember), "moderator") || strings.Contains(string(encodedMember), "worker") || strings.Contains(string(encodedMember), "last_error") {
		t.Fatalf("member case projection exposed staff or internal fields: %s", encodedMember)
	}
	var notificationRecords []AppealNotificationRecord
	err = repository.db.Where("status = ?", quack.AppealNotificationPending).Order("created_at ASC").Find(&notificationRecords).Error
	notifications := make([]quack.AppealNotification, 0, len(notificationRecords))
	for _, record := range notificationRecords {
		notifications = append(notifications, appealNotificationModel(record))
	}
	if err != nil || len(notifications) < 2 {
		t.Fatalf("expected staff and member notifications, got %+v err=%v", notifications, err)
	}
	for _, notification := range notifications {
		if notification.Audience == quack.AppealNotificationMember && (notification.Body == "" || strings.Contains(notification.Body, "moderator")) {
			t.Fatalf("member notification leaked staff identity: %+v", notification)
		}
	}
	client := &appealNotificationClientStub{}
	var dispatchErrors [2]error
	var dispatchWait sync.WaitGroup
	for index := range dispatchErrors {
		dispatchWait.Add(1)
		go func(index int) {
			defer dispatchWait.Done()
			dispatchErrors[index] = quack.NewAppealNotificationDispatcher(repository, client).DispatchPending(ctx, 10)
		}(index)
	}
	dispatchWait.Wait()
	for _, dispatchErr := range dispatchErrors {
		if dispatchErr != nil {
			t.Fatalf("dispatch appeal notifications: %v", dispatchErr)
		}
	}
	var remaining int64
	err = repository.db.Model(&AppealNotificationRecord{}).Where("status IN ?", []quack.AppealNotificationStatus{quack.AppealNotificationPending, quack.AppealNotificationClaimed}).Count(&remaining).Error
	memberSends, staffSends := client.counts()
	if err != nil || remaining != 0 || memberSends == 0 || staffSends == 0 || memberSends+staffSends != len(notifications) {
		t.Fatalf("notification adapter did not deliver each item once: remaining=%d member=%d staff=%d expected=%d err=%v", remaining, memberSends, staffSends, len(notifications), err)
	}
}

func TestAppealServiceRejectsIneligibleCasesAndConcurrentDecisions(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	service := quack.NewAppealService(repository)
	nonAppealable := createAppealableCase(t, repository, guild.ID, "target", false)
	if _, err := service.Submit(ctx, nonAppealable.ID, "target", quack.AppealSubmissionInput{}); !errors.Is(err, quack.ErrAppealCaseIneligible) {
		t.Fatalf("non-appealable case accepted: %v", err)
	}
	eligible := createAppealableCase(t, repository, guild.ID, "target", true)
	appeal, err := service.Submit(ctx, eligible.ID, "target", quack.AppealSubmissionInput{Answers: []quack.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatalf("submit eligible appeal: %v", err)
	}
	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[quack.PermissionAction]bool{quack.PermissionActionAppealReview: true}}
	var successes int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, accept := range []bool{true, false} {
		accept := accept
		wait.Add(1)
		go func() {
			defer wait.Done()
			var transitionErr error
			if accept {
				_, transitionErr = service.Accept(ctx, moderator, appeal.ID, "accepted concurrently")
			} else {
				_, transitionErr = service.Reject(ctx, moderator, appeal.ID, "rejected concurrently")
			}
			if transitionErr == nil {
				mutex.Lock()
				successes++
				mutex.Unlock()
			}
		}()
	}
	wait.Wait()
	if successes != 1 {
		t.Fatalf("expected exactly one concurrent decision, got %d", successes)
	}
}

func TestAppealNotificationClaimRecoversExpiredLeaseAndRejectsStaleCompletion(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	service := quack.NewAppealService(repository)
	if _, err := service.Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []quack.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}}); err != nil {
		t.Fatalf("submit appeal: %v", err)
	}
	first, err := repository.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(first) != 1 || first[0].Status != quack.AppealNotificationClaimed || first[0].LeaseToken == "" {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	if err := repository.db.Model(&AppealNotificationRecord{}).Where("id = ?", first[0].ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatalf("expire first claim: %v", err)
	}
	second, err := repository.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID || second[0].LeaseToken == first[0].LeaseToken {
		t.Fatalf("reclaimed notification: first=%+v second=%+v err=%v", first, second, err)
	}
	if err := repository.CompleteAppealNotification(ctx, quack.CompleteAppealNotificationParams{NotificationID: first[0].ID, LeaseToken: first[0].LeaseToken, Status: quack.AppealNotificationSent}); !errors.Is(err, quack.ErrAppealStateConflict) {
		t.Fatalf("stale lease completed reclaimed notification: %v", err)
	}
	if err := repository.CompleteAppealNotification(ctx, quack.CompleteAppealNotificationParams{NotificationID: second[0].ID, LeaseToken: second[0].LeaseToken, Status: quack.AppealNotificationSent}); err != nil {
		t.Fatalf("current lease completion: %v", err)
	}
}

func TestAppealRejectedReopenedAndClosedTimeline(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	service := quack.NewAppealService(repository)
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	appeal, err := service.Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []quack.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[quack.PermissionAction]bool{quack.PermissionActionAppealReview: true}}
	if rejected, err := service.Reject(ctx, moderator, appeal.ID, "Insufficient context."); err != nil || rejected.Status != quack.AppealStatusRejected {
		t.Fatalf("reject: %+v err=%v", rejected, err)
	}
	if reopened, err := service.Reopen(ctx, moderator, appeal.ID, "One final clarification is needed."); err != nil || reopened.Status != quack.AppealStatusNeedsInformation {
		t.Fatalf("reopen: %+v err=%v", reopened, err)
	}
	if _, err := service.SubmitInformation(ctx, appeal.ID, "target", quack.AppealInformationInput{Body: "Final clarification."}); err != nil {
		t.Fatalf("submit reopened information: %v", err)
	}
	closed, err := service.Close(ctx, moderator, appeal.ID, "Review is complete.")
	if err != nil || closed.Status != quack.AppealStatusClosed || len(closed.Events) != 5 {
		t.Fatalf("close timeline: %+v err=%v", closed, err)
	}
	persisted, err := repository.GetCaseByID(ctx, item.ID)
	if err != nil || persisted.Validity != quack.CaseValidityValid {
		t.Fatalf("reject/reopen/close changed case validity: %+v err=%v", persisted, err)
	}
}

func TestAppealAcceptanceAndDirectVoidCannotProduceAcceptedValidCase(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	service := quack.NewAppealService(repository)
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	appeal, err := service.Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []quack.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[quack.PermissionAction]bool{quack.PermissionActionAppealReview: true}}
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = service.Accept(ctx, moderator, appeal.ID, "Accepted concurrently.")
	}()
	go func() {
		defer wait.Done()
		_, _ = repository.VoidCase(ctx, quack.VoidCaseParams{GuildID: guild.ID, CaseID: item.ID, ActorDiscordUserID: "other-moderator", Reason: "Direct correction"})
	}()
	wait.Wait()
	persistedAppeal, err := repository.GetAppealByID(ctx, appeal.ID)
	if err != nil {
		t.Fatalf("read appeal: %v", err)
	}
	persistedCase, err := repository.GetCaseByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	if persistedAppeal.Status == quack.AppealStatusAccepted && persistedCase.Validity != quack.CaseValidityVoided {
		t.Fatalf("race produced accepted appeal with valid case: appeal=%+v case=%+v", persistedAppeal, persistedCase)
	}
}

type appealNotificationClientStub struct {
	mutex  sync.Mutex
	member int
	staff  int
}

func insertLegacyAppeal(db *gorm.DB, appeal *migration0200LegacyAppeal) error {
	return db.Select("id", "guild_id", "case_id", "target_discord_user_id", "status", "content", "decision_reason", "reviewed_by_discord_user_id", "reviewed_at", "review_message_discord_id", "metadata_json", "created_at", "updated_at").Create(appeal).Error
}

func (c *appealNotificationClientStub) SendAppealMemberNotification(context.Context, string, string) (string, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.member++
	return "member-message", nil
}

func (c *appealNotificationClientStub) SendAppealStaffNotification(context.Context, string, string) (string, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.staff++
	return "staff-message", nil
}

func (c *appealNotificationClientStub) counts() (int, int) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.member, c.staff
}
