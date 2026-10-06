package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestCountTemplateCasesForTarget(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	template := createTemplate(t, s, guildID, "spam")
	other := createTemplate(t, s, guildID, "other")
	add := func(c quack.Case) *quack.CreatedCase {
		t.Helper()
		created, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: c, Event: newCaseEvent()})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	add(newCase(guildID, &template.Template.ID))
	add(newCase(guildID, &template.Template.ID))
	voided := add(newCase(guildID, &template.Template.ID))
	if _, err := s.VoidCase(ctx, quack.VoidCaseParams{GuildID: guildID, CaseID: voided.Case.ID, Reason: "mistake"}); err != nil {
		t.Fatal(err)
	}
	imported := newCase(guildID, &template.Template.ID)
	imported.Source = quack.CaseSourceV4Import
	add(imported)
	otherTarget := newCase(guildID, &template.Template.ID)
	otherTarget.TargetDiscordUserID = "target-2"
	add(otherTarget)
	add(newCase(guildID, &other.Template.ID))

	count, err := s.CountTemplateCasesForTarget(ctx, quack.CountTemplateCasesForTargetParams{
		GuildID: guildID, TemplateID: template.Template.ID, TargetDiscordUserID: "target-1",
	})
	if err != nil || count != 2 {
		t.Fatalf("count = %d, %v; want 2 (voided, imported, other members and templates excluded)", count, err)
	}
}

func TestListAndGetCases(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	template := createTemplate(t, s, guildID, "spam")
	otherID := addGuild(t, s, "guild-2")
	key := "request-1"
	firstCase := newCase(guildID, &template.Template.ID)
	firstCase.IdempotencyKey = &key
	first, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: firstCase, Event: newCaseEvent()})
	if err != nil {
		t.Fatal(err)
	}
	secondCase := newCase(guildID, &template.Template.ID)
	secondCase.TargetDiscordUserID = "target-2"
	secondCase.ModeratorDiscordUserID = "moderator-2"
	secondCase.Validity = quack.CaseValidityVoided
	second, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: secondCase, Event: newCaseEvent(), ActionExecutions: timeout(0)})
	if err != nil {
		t.Fatal(err)
	}
	createCase(t, s, otherID, nil, nil)

	tests := []struct {
		name   string
		params quack.ListCasesParams
		want   []string
	}{
		{"guild newest first", quack.ListCasesParams{GuildID: guildID}, []string{second.Case.ID, first.Case.ID}},
		{"all filters", quack.ListCasesParams{GuildID: guildID, TargetDiscordUserID: "target-2", ModeratorDiscordUserID: "moderator-2",
			TemplateID: template.Template.ID, Validity: quack.CaseValidityVoided}, []string{second.Case.ID}},
		{"case number", quack.ListCasesParams{GuildID: guildID, CaseNumber: "1"}, []string{first.Case.ID}},
		{"action result", quack.ListCasesParams{GuildID: guildID, ActionResult: string(quack.ActionExecutionPending)}, []string{second.Case.ID}},
		{"page", quack.ListCasesParams{GuildID: guildID, Limit: 1, Offset: 1}, []string{first.Case.ID}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := s.ListCasesFiltered(ctx, test.params)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, len(got.Cases))
			for i, c := range got.Cases {
				ids[i] = c.ID
			}
			if len(ids) != len(test.want) || (len(ids) > 0 && ids[0] != test.want[0]) {
				t.Fatalf("cases = %v, want %v", ids, test.want)
			}
		})
	}

	if got, err := s.GetCaseByIDOrNumber(ctx, guildID, "1"); err != nil || got.ID != first.Case.ID {
		t.Errorf("by number = %+v, %v", got, err)
	}
	if got, err := s.GetCaseByIDOrNumber(ctx, guildID, second.Case.ID); err != nil || got.CaseNumber != 2 {
		t.Errorf("by id = %+v, %v", got, err)
	}
	if err := notFound(s.GetCaseByIDOrNumber(ctx, otherID, second.Case.ID)); err != nil {
		t.Errorf("by id across guilds: %v", err)
	}
	if err := notFound(s.GetCaseByIDOrNumber(ctx, guildID, "01")); err != nil {
		t.Errorf("non-canonical number: %v", err)
	}
	if got, err := s.GetCaseByIdempotencyKey(ctx, guildID, key); err != nil || got.ID != first.Case.ID {
		t.Errorf("by idempotency key = %+v, %v", got, err)
	}
	if err := notFound(s.GetCaseByIdempotencyKey(ctx, otherID, key)); err != nil {
		t.Errorf("idempotency key across guilds: %v", err)
	}
}

func TestTargetCaseSummary(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	template := createTemplate(t, s, guildID, "spam")
	for _, c := range []quack.Case{newCase(guildID, &template.Template.ID), newCase(guildID, &template.Template.ID), newCase(guildID, nil)} {
		if _, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: c, Event: newCaseEvent()}); err != nil {
			t.Fatal(err)
		}
	}
	cases, _ := s.ListCases(ctx, guildID)
	if _, err := s.VoidCase(ctx, quack.VoidCaseParams{GuildID: guildID, CaseID: cases[0].ID, Reason: "mistake"}); err != nil {
		t.Fatal(err)
	}
	summary, err := s.TargetCaseSummary(ctx, guildID, "target-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 3 || summary.ByValidity[quack.CaseValidityValid] != 2 || summary.ByValidity[quack.CaseValidityVoided] != 1 ||
		summary.ByTemplate[template.Template.ID] != 2 || summary.ByTemplate[""] != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestListCaseActionAttempts(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), nil)
	complete(t, s, claim(t, s, created.Case.ID), quack.ActionExecutionSucceeded)
	id := created.ActionExecutions[0].ID
	attempts, err := s.ListCaseActionAttempts(ctx, []string{id})
	if err != nil || len(attempts) != 1 || attempts[0].ExecutionID != id || attempts[0].Status != quack.ActionAttemptSucceeded {
		t.Fatalf("attempts = %+v, %v", attempts, err)
	}
	if got, err := s.GetCaseActionExecution(ctx, guildID, id); err != nil || got.Status != quack.ActionExecutionSucceeded {
		t.Fatalf("GetCaseActionExecution = %+v, %v", got, err)
	}
	if err := notFound(s.GetCaseActionExecution(ctx, "other-guild", id)); err != nil {
		t.Errorf("execution across guilds: %v", err)
	}
}
