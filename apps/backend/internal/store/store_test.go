package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestWithGuildCaseLockSerializesCaseNumbering(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			errs <- s.WithGuildCaseLock(ctx, guildID, func(cases quack.CaseStore) error {
				_, err := cases.CreateCase(ctx, quack.CreateCaseParams{Case: newCase(guildID, nil), Event: newCaseEvent()})
				return err
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cases, err := s.ListCases(ctx, guildID)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if c.CaseNumber != uint64(i+1) {
			t.Fatalf("case numbers are not 1..8: %+v", cases)
		}
	}
}

func TestWithGuildCaseLockRejectsUnknownGuild(t *testing.T) {
	s, _ := newTestStore(t)
	called := false
	err := s.WithGuildCaseLock(context.Background(), "missing", func(quack.CaseStore) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("WithGuildCaseLock(missing) = %v, called = %v; want an error and no call", err, called)
	}
}

func TestRedisMethodsFailWithoutRedis(t *testing.T) {
	s := testutil.NewSQLiteStore(t)
	ctx := context.Background()
	checks := map[string]error{
		"PingRedis":          s.PingRedis(ctx),
		"SaveSession":        s.SaveSession(ctx, &quack.AuthSession{ID: "id", DiscordUserID: "user"}, time.Minute),
		"RevokeUserSessions": s.RevokeUserSessions(ctx, "user"),
	}
	_, checks["GetSession"] = s.GetSession(ctx, "id")
	_, checks["HashGet"] = s.HashGet(ctx, "key", "field")
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s without Redis succeeded", name)
		}
	}
	if err := s.PingDatabase(ctx); err != nil {
		t.Errorf("PingDatabase: %v", err)
	}
}

// newTestStore returns a migrated SQLite store with one guild.
func newTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	s := testutil.NewSQLiteStore(t)
	return s, addGuild(t, s, "guild-1")
}

// addGuild creates a guild and returns its internal ID.
func addGuild(t *testing.T, s *store.Store, discordID string) string {
	t.Helper()
	guild, err := s.UpsertGuild(context.Background(), quack.UpsertGuildParams{
		DiscordGuildID:     discordID,
		Name:               "Guild " + discordID,
		OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}
	return guild.ID
}

func newTemplate(guildID, slug string) quack.CaseTemplate {
	return quack.CaseTemplate{
		GuildID:                guildID,
		Slug:                   slug,
		Name:                   "Spam",
		Description:            "Spam template",
		ReasonTemplate:         "No spam",
		CreatedByDiscordUserID: "moderator-1",
		UpdatedByDiscordUserID: "moderator-1",
	}
}

// newLevels is a default level with no action and a timeout at three cases.
func newLevels() []quack.ExpandedCaseTemplateLevel {
	return []quack.ExpandedCaseTemplateLevel{
		{Level: quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true}},
		{
			Level: quack.CaseTemplateLevel{Position: 2, Name: "Escalated", TriggerCaseCount: 3},
			Actions: []quack.CaseTemplateLevelAction{
				{ActionType: quack.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
			},
		},
	}
}

func createTemplate(t *testing.T, s *store.Store, guildID, slug string) *quack.ExpandedCaseTemplate {
	t.Helper()
	created, err := s.CreateCaseTemplate(context.Background(), quack.CreateCaseTemplateParams{
		Template: newTemplate(guildID, slug),
		Levels:   newLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

func newCase(guildID string, templateID *string) quack.Case {
	return quack.Case{
		GuildID:                guildID,
		TemplateID:             templateID,
		TemplateVersion:        1,
		TemplateSnapshotJSON:   "{}",
		TargetDiscordUserID:    "target-1",
		ModeratorDiscordUserID: "moderator-1",
		Reason:                 "No spam",
		Validity:               quack.CaseValidityValid,
		Source:                 quack.CaseSourceDashboard,
		MetadataJSON:           "{}",
	}
}

func newCaseEvent() quack.CaseEvent {
	return quack.CaseEvent{
		EventType:          quack.CaseEventCreated,
		ActorDiscordUserID: "moderator-1",
		Body:               "Case created",
	}
}

// createCase creates a case in guildID with the given executions and
// optional notification.
func createCase(t *testing.T, s *store.Store, guildID string, executions []quack.CaseActionExecution, notification *quack.CaseNotification) *quack.CreatedCase {
	t.Helper()
	created, err := s.CreateCase(context.Background(), quack.CreateCaseParams{
		Case:             newCase(guildID, nil),
		Event:            newCaseEvent(),
		ActionExecutions: executions,
		Notification:     notification,
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	return created
}

// timeout is one pending timeout execution at position.
func timeout(position int) []quack.CaseActionExecution {
	return []quack.CaseActionExecution{{
		Position:           position,
		ActionType:         quack.ActionTimeoutUser,
		ConfigSnapshotJSON: `{"duration_seconds":60}`,
		SafeForRetry:       true,
	}}
}

// pendingNotification is a notification as case creation queues it.
func pendingNotification() *quack.CaseNotification {
	return &quack.CaseNotification{RenderedMessage: "You were timed out"}
}

func claim(t *testing.T, s *store.Store, caseID string) *quack.ClaimedCaseAction {
	t.Helper()
	claimed, err := s.ClaimNextCaseAction(context.Background(), quack.ClaimCaseActionParams{CaseID: caseID, WorkerID: "worker"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return claimed
}

// complete records an outcome for a claimed execution.
func complete(t *testing.T, s *store.Store, claimed *quack.ClaimedCaseAction, status quack.ActionExecutionStatus) {
	t.Helper()
	attempt := quack.ActionAttemptSucceeded
	if status != quack.ActionExecutionSucceeded {
		attempt = quack.ActionAttemptFailed
	}
	if err := s.CompleteCaseAction(context.Background(), quack.CompleteCaseActionParams{
		ExecutionID:     claimed.Execution.ID,
		LeaseToken:      claimed.Execution.LeaseToken,
		AttemptNumber:   claimed.Execution.AttemptCount,
		WorkerID:        "worker",
		AttemptStatus:   attempt,
		ExecutionStatus: status,
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

// notFound checks for the store's not-found result, (nil, nil), and
// describes anything else.
func notFound[T any](got *T, err error) error {
	if err != nil || got != nil {
		return fmt.Errorf("got %+v, %v; want nil, nil", got, err)
	}
	return nil
}
