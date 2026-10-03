package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestAppealLifecycleAndAtomicAcceptance(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	guild, err := s.GetGuildByID(ctx, guildID)
	if err != nil {
		t.Fatal(err)
	}
	c := createAppealableCase(t, s, guildID, true,
		quack.CaseActionExecution{
			Position:           0,
			ActionType:         quack.ActionBanUser,
			Status:             quack.ActionExecutionSucceeded,
			ConfigSnapshotJSON: "{}",
		},
		quack.CaseActionExecution{
			Position:           1,
			ActionType:         quack.ActionTimeoutUser,
			Status:             quack.ActionExecutionPending,
			ConfigSnapshotJSON: "{}",
			SafeForRetry:       true,
		},
	)
	service := quack.NewAppealService(s)

	settings, err := service.GetSettings(ctx, guildID)
	if err != nil || !settings.Default || len(settings.Questions) == 0 {
		t.Fatalf("default settings: %+v err=%v", settings, err)
	}
	manager := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guildID, DiscordUserID: "manager"},
		Permissions: map[quack.PermissionAction]bool{quack.PermissionActionGuildSettingsWrite: true}}
	questions := []quack.AppealQuestion{
		{ID: "explanation", Prompt: "Explain your appeal", Type: quack.AppealQuestionLongText, Required: true, Position: 0},
		{ID: "contact", Prompt: "May staff contact you?", Type: quack.AppealQuestionBoolean, Position: 1},
	}
	if configured, err := service.UpdateSettings(ctx, manager, questions); err != nil || configured.Default || len(configured.Questions) != 2 {
		t.Fatalf("configure appeal form: %+v err=%v", configured, err)
	}
	answers := []quack.AppealAnswer{
		{QuestionID: "explanation", Value: "The decision should be reconsidered."},
		{QuestionID: "contact", Value: true},
	}
	appeal, err := service.Submit(ctx, c.ID, "target", quack.AppealSubmissionInput{Answers: answers})
	if err != nil {
		t.Fatalf("submit appeal: %v", err)
	}
	if appeal.Status != quack.AppealStatusPending || len(appeal.Questions) != 2 || len(appeal.Events) != 1 {
		t.Fatalf("unexpected submitted appeal: %+v", appeal)
	}
	replacement := []quack.AppealQuestion{{ID: "replacement", Prompt: "Replacement", Type: quack.AppealQuestionShortText, Required: true}}
	if _, err := service.UpdateSettings(ctx, manager, replacement); err != nil {
		t.Fatalf("replace appeal form: %v", err)
	}
	if got, err := service.GetMember(ctx, appeal.ID, "target"); err != nil || len(got.Questions) != 2 || got.Questions[0].ID != "explanation" {
		t.Fatalf("appeal kept no snapshot of its form: %+v err=%v", got, err)
	}
	if _, err := service.Submit(ctx, c.ID, "target", quack.AppealSubmissionInput{Answers: answers}); !errors.Is(err, quack.ErrAppealConflict) {
		t.Fatalf("second appeal for one case = %v, want ErrAppealConflict", err)
	}
	if _, err := service.GetMember(ctx, appeal.ID, "other"); !errors.Is(err, quack.ErrAppealNotFound) {
		t.Fatalf("another member read the appeal: %v", err)
	}

	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &quack.StaffMember{GuildID: guildID, DiscordUserID: "moderator"},
		ActorDiscordUserID: "moderator", Permissions: map[quack.PermissionAction]bool{quack.PermissionActionAppealReview: true}}
	requested, err := service.RequestInformation(ctx, moderator, appeal.ID, "Please clarify.")
	if err != nil || requested.Status != quack.AppealStatusNeedsInformation {
		t.Fatalf("request information: %+v err=%v", requested, err)
	}
	memberView, err := service.GetMember(ctx, appeal.ID, "target")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range memberView.Events {
		if event.ActorType == "staff" && event.ActorDiscordUserID != "" {
			t.Fatalf("member timeline leaked staff identity: %+v", event)
		}
	}
	if _, err := service.SubmitInformation(ctx, appeal.ID, "target", quack.AppealInformationInput{Body: "More context."}); err != nil {
		t.Fatalf("submit information: %v", err)
	}
	accepted, err := service.Accept(ctx, moderator, appeal.ID, "The added context changes the decision.")
	if err != nil || accepted.Status != quack.AppealStatusAccepted || len(accepted.ReversalOffers) != 1 ||
		accepted.ReversalOffers[0].ActionType != quack.ActionUnbanUser {
		t.Fatalf("accept appeal: %+v err=%v", accepted, err)
	}

	// Acceptance voids the case and cancels unstarted work, but queues no
	// reversal on its own.
	executions, err := s.ListCaseActionExecutions(ctx, c.ID)
	if err != nil || len(executions) != 2 ||
		executions[1].Status != quack.ActionExecutionCancelled || executions[1].LastErrorCode != "case_voided" {
		t.Fatalf("executions after acceptance: %+v err=%v", executions, err)
	}
	if n, err := s.GetCaseNotification(ctx, c.ID); err != nil || n.Status != quack.NotificationFailed || n.LastErrorCode != "case_voided" {
		t.Fatalf("notification after acceptance: %+v err=%v", n, err)
	}
	if persisted, err := s.GetCaseByID(ctx, c.ID); err != nil || persisted.Validity != quack.CaseValidityVoided {
		t.Fatalf("case after acceptance: %+v err=%v", persisted, err)
	}
	appealID := appeal.ID
	queued, err := s.QueueCaseReversal(ctx, quack.QueueCaseReversalParams{GuildID: guildID, CaseID: c.ID, ActorDiscordUserID: "moderator",
		OriginalExecutionID: executions[0].ID, ActionType: quack.ActionUnbanUser, AppealID: &appealID})
	if err != nil || queued == nil || queued.ReversalAppealID == nil || *queued.ReversalAppealID != appeal.ID || queued.SafeForRetry {
		t.Fatalf("reversal: %+v err=%v", queued, err)
	}
	if _, err := service.Reject(ctx, moderator, appeal.ID, "late competing decision"); !errors.Is(err, quack.ErrAppealConflict) {
		t.Fatalf("decided appeal took a second decision: %v", err)
	}

	cases := quack.NewCaseService(s, nil, nil, nil)
	detail, err := cases.GetMemberCase(ctx, c.ID, "target")
	if err != nil || detail.Validity != quack.CaseValidityVoided || detail.AppealStatus != quack.AppealStatusAccepted || detail.Appealable {
		t.Fatalf("member case after acceptance: %+v err=%v", detail, err)
	}
	encoded, _ := json.Marshal(detail)
	for _, secret := range []string{"moderator", "worker", "last_error"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("member case exposes %q: %s", secret, encoded)
		}
	}

	// Two dispatchers racing still deliver each notification once.
	client := &appealNotifier{}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { errs[i] = quack.NewAppealNotificationDispatcher(s, client).DispatchPending(ctx, 10) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var total, open int64
	db := s.DB().Table("appeal_notifications")
	if err := db.Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Table("appeal_notifications").Where("status IN ?", []quack.AppealNotificationStatus{
		quack.AppealNotificationPending, quack.AppealNotificationClaimed}).Count(&open).Error; err != nil {
		t.Fatal(err)
	}
	member, staff := client.counts()
	if open != 0 || member == 0 || staff == 0 || int64(member+staff) != total {
		t.Fatalf("delivered member=%d staff=%d of %d, %d still open", member, staff, total, open)
	}
}

func TestAppealRejectsIneligibleCasesAndConcurrentDecisions(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	guild, _ := s.GetGuildByID(ctx, guildID)
	service := quack.NewAppealService(s)
	closed := createAppealableCase(t, s, guildID, false)
	if _, err := service.Submit(ctx, closed.ID, "target", quack.AppealSubmissionInput{}); !errors.Is(err, quack.ErrAppealCaseIneligible) {
		t.Fatalf("non-appealable case = %v, want ErrAppealCaseIneligible", err)
	}
	open := createAppealableCase(t, s, guildID, true)
	appeal, err := service.Submit(ctx, open.ID, "target", reasonAnswer())
	if err != nil {
		t.Fatal(err)
	}
	moderator := reviewer(guild)
	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Go(func() { _, results[0] = service.Accept(ctx, moderator, appeal.ID, "accepted concurrently") })
	wg.Go(func() { _, results[1] = service.Reject(ctx, moderator, appeal.ID, "rejected concurrently") })
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) {
		t.Fatalf("want exactly one decision to win, got %v and %v", results[0], results[1])
	}
}

func TestAppealAcceptanceRacingDirectVoid(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	guild, _ := s.GetGuildByID(ctx, guildID)
	service := quack.NewAppealService(s)
	c := createAppealableCase(t, s, guildID, true)
	appeal, err := service.Submit(ctx, c.ID, "target", reasonAnswer())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = service.Accept(ctx, reviewer(guild), appeal.ID, "Accepted.") })
	wg.Go(func() {
		_, _ = s.VoidCase(ctx, quack.VoidCaseParams{GuildID: guildID, CaseID: c.ID, ActorDiscordUserID: "other", Reason: "Direct correction"})
	})
	wg.Wait()
	gotAppeal, _ := s.GetAppealByID(ctx, appeal.ID)
	gotCase, _ := s.GetCaseByID(ctx, c.ID)
	if gotAppeal.Status == quack.AppealStatusAccepted && gotCase.Validity != quack.CaseValidityVoided {
		t.Fatalf("accepted appeal left its case valid: appeal=%+v case=%+v", gotAppeal, gotCase)
	}
}

func TestAppealRejectReopenClose(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	guild, _ := s.GetGuildByID(ctx, guildID)
	service := quack.NewAppealService(s)
	c := createAppealableCase(t, s, guildID, true)
	appeal, err := service.Submit(ctx, c.ID, "target", reasonAnswer())
	if err != nil {
		t.Fatal(err)
	}
	moderator := reviewer(guild)
	if got, err := service.Reject(ctx, moderator, appeal.ID, "Insufficient context."); err != nil || got.Status != quack.AppealStatusRejected {
		t.Fatalf("reject: %+v err=%v", got, err)
	}
	got, err := service.Reopen(ctx, moderator, appeal.ID, "One more question.")
	if err != nil || got.Status != quack.AppealStatusNeedsInformation {
		t.Fatalf("reopen: %+v err=%v", got, err)
	}
	if _, err := service.SubmitInformation(ctx, appeal.ID, "target", quack.AppealInformationInput{Body: "Answer."}); err != nil {
		t.Fatalf("submit information: %v", err)
	}
	closedAppeal, err := service.Close(ctx, moderator, appeal.ID, "Review complete.")
	if err != nil || closedAppeal.Status != quack.AppealStatusClosed || len(closedAppeal.Events) != 5 {
		t.Fatalf("close: %+v err=%v", closedAppeal, err)
	}
	if got, err := s.GetCaseByID(ctx, c.ID); err != nil || got.Validity != quack.CaseValidityValid {
		t.Fatalf("non-accepting decisions changed the case: %+v err=%v", got, err)
	}
	listed, err := s.ListAppeals(ctx, quack.AppealListParams{GuildID: guildID, Status: quack.AppealStatusClosed})
	if err != nil || listed.Total != 1 {
		t.Fatalf("ListAppeals(closed) = %+v, %v", listed, err)
	}
}

func TestAppealNotificationLeaseFencing(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	c := createAppealableCase(t, s, guildID, true)
	if _, err := quack.NewAppealService(s).Submit(ctx, c.ID, "target", reasonAnswer()); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(first) != 1 || first[0].Status != quack.AppealNotificationClaimed || first[0].LeaseToken == "" {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	if again, err := s.ClaimPendingAppealNotifications(ctx, 1); err != nil || len(again) != 0 {
		t.Fatalf("claimed a leased notification: %+v err=%v", again, err)
	}
	expire(t, s, "appeal_notifications", first[0].ID)
	second, err := s.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID || second[0].LeaseToken == first[0].LeaseToken {
		t.Fatalf("reclaim: %+v err=%v", second, err)
	}
	sent := func(n quack.AppealNotification) quack.CompleteAppealNotificationParams {
		return quack.CompleteAppealNotificationParams{
			NotificationID: n.ID,
			LeaseToken:     n.LeaseToken,
			Status:         quack.AppealNotificationSent,
		}
	}
	if err := s.CompleteAppealNotification(ctx, sent(first[0])); !errors.Is(err, quack.ErrAppealStateConflict) {
		t.Fatalf("stale completion = %v, want ErrAppealStateConflict", err)
	}
	if err := s.CompleteAppealNotification(ctx, sent(second[0])); err != nil {
		t.Fatalf("current completion: %v", err)
	}
}

// createAppealableCase creates a case for member "target" whose template
// snapshot allows or forbids appeals, with a pending notification.
func createAppealableCase(t *testing.T, s *store.Store, guildID string, appealable bool, executions ...quack.CaseActionExecution) *quack.Case {
	t.Helper()
	c := newCase(guildID, nil)
	c.TargetDiscordUserID = "target"
	c.ModeratorDiscordUserID = "moderator"
	c.TemplateSnapshotJSON = `{"template":{"appealable":false}}`
	if appealable {
		c.TemplateSnapshotJSON = `{"template":{"appealable":true}}`
	}
	created, err := s.CreateCase(context.Background(), quack.CreateCaseParams{
		Case:             c,
		Event:            newCaseEvent(),
		ActionExecutions: executions,
		Notification:     pendingNotification(),
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	return &created.Case
}

// createAppeal saves an appeal directly, bypassing the service's checks.
func createAppeal(t *testing.T, s *store.Store, guildID, caseID string) *quack.Appeal {
	t.Helper()
	c, err := s.GetCaseByID(context.Background(), caseID)
	if err != nil || c == nil {
		t.Fatalf("get appealed case: %+v, %v", c, err)
	}
	appeal, err := s.CreateAppeal(context.Background(), quack.CreateAppealParams{
		Appeal: quack.Appeal{
			GuildID: guildID, CaseID: &caseID, TargetDiscordUserID: c.TargetDiscordUserID, Status: quack.AppealStatusPending,
			Content: "Please reconsider", QuestionSnapshotJSON: "[]", AnswersJSON: "[]", Version: 1, MetadataJSON: "{}",
		},
		Event:     quack.AppealEvent{EventType: "submitted", ActorType: "member", Body: "Submitted"},
		CaseEvent: quack.CaseEvent{EventType: quack.CaseEventAppealCreated, Visibility: quack.EventVisibilityPublic, Body: "Appeal submitted"},
		Audit: quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: "appeal.create",
			ResourceType: "appeal", Result: quack.AuditResultSuccess},
		Notification: quack.AppealNotification{TargetDiscordUserID: "target-1", Audience: quack.AppealNotificationStaff,
			Status: quack.AppealNotificationPending, Body: "New appeal"},
	})
	if err != nil {
		t.Fatalf("create appeal: %v", err)
	}
	return appeal
}

func reasonAnswer() quack.AppealSubmissionInput {
	return quack.AppealSubmissionInput{Answers: []quack.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}}
}

func reviewer(guild *quack.Guild) *quack.GuildStaffContext {
	return &quack.GuildStaffContext{
		Guild:       guild,
		Staff:       &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"},
		Permissions: map[quack.PermissionAction]bool{quack.PermissionActionAppealReview: true},
	}
}

// expire moves a row's lease into the past.
func expire(t *testing.T, s *store.Store, table, id string) {
	t.Helper()
	if err := s.DB().Table(table).Where("id = ?", id).Update("lease_expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire %s lease: %v", table, err)
	}
}

// appealNotifier counts deliveries by audience.
type appealNotifier struct {
	mu            sync.Mutex
	member, staff int
}

func (n *appealNotifier) SendAppealMemberNotification(context.Context, string, string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.member++
	return "member-message", nil
}

func (n *appealNotifier) SendAppealStaffNotification(context.Context, string, string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.staff++
	return "staff-message", nil
}

func (n *appealNotifier) counts() (int, int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.member, n.staff
}
