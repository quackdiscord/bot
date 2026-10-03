package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestEscalationDecayCountsOnlyRecentCases(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)

	input := validTemplateInput("decaying")
	input.CaseDecayDays = 30
	input.Levels[1].TriggerCaseCount = 2
	template := createAppTemplate(t, ctx, store, admin, input)
	if template.CaseDecayDays != 30 {
		t.Fatalf("template decay = %d, want 30", template.CaseDecayDays)
	}

	old, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	aged := time.Now().UTC().AddDate(0, 0, -31)
	if err := store.DB().Model(&quack.Case{}).Where("id = ?", old.ID).Update("created_at", aged).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SelectedLevel == nil || !fresh.SelectedLevel.IsDefault || fresh.SelectedLevel.MatchedCaseCount != 1 {
		t.Fatalf("a case outside the window counted: %+v", fresh.SelectedLevel)
	}
	escalated, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	if escalated.SelectedLevel == nil || escalated.SelectedLevel.MatchedCaseCount != 2 || escalated.SelectedLevel.IsDefault {
		t.Fatalf("recent cases did not escalate: %+v", escalated.SelectedLevel)
	}
	detail, err := service.Get(ctx, moderator, escalated.ID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Template struct {
			CaseDecayDays int `json:"case_decay_days"`
		} `json:"template"`
	}
	body, _ := json.Marshal(detail.TemplateSnapshot)
	if err := json.Unmarshal(body, &snapshot); err != nil || snapshot.Template.CaseDecayDays != 30 {
		t.Fatalf("snapshot decay = %+v, %v", snapshot, err)
	}
}

func TestTemplateDecayValidationAndPolicyRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(store)

	for _, days := range []int{-1, quack.MaxCaseDecayDays + 1} {
		input := validTemplateInput("invalid-decay")
		input.CaseDecayDays = days
		if _, err := service.Create(ctx, admin, input); !errors.Is(err, quack.ErrTemplateValidation) {
			t.Fatalf("decay %d = %v, want ErrTemplateValidation", days, err)
		}
	}
	input := validTemplateInput("exported")
	input.CaseDecayDays = quack.MaxCaseDecayDays
	created, err := service.Create(ctx, admin, input)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.Export(ctx, admin, created.ID)
	if err != nil || policy.CaseDecayDays != quack.MaxCaseDecayDays {
		t.Fatalf("export = %+v, %v", policy, err)
	}
	policy.Slug = "imported"
	imported, err := service.Import(ctx, admin, quack.TemplateImportInput{Confirm: true, Policy: *policy})
	if err != nil || imported.CaseDecayDays != quack.MaxCaseDecayDays {
		t.Fatalf("import = %+v, %v", imported, err)
	}
}

func TestTemplateUpdateRejectsStaleEdits(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(store)
	created, err := service.Create(ctx, admin, validTemplateInput("edited"))
	if err != nil {
		t.Fatal(err)
	}

	first := created.EditInput()
	second := created.EditInput()
	if first.ExpectedVersion != created.Version || len(first.Levels) != len(created.Levels) {
		t.Fatalf("edit input = %+v", first)
	}
	first.Levels[0].Name = "Changed"
	if created.Levels[0].Name == "Changed" {
		t.Fatal("editing the input changed the response")
	}
	first.Name = "First edit"
	updated, err := service.Update(ctx, admin, created.ID, first)
	if err != nil || updated.Version != created.Version+1 {
		t.Fatalf("first edit = %+v, %v", updated, err)
	}
	second.Name = "Second edit"
	if _, err := service.Update(ctx, admin, created.ID, second); !errors.Is(err, quack.ErrTemplateConflict) {
		t.Fatalf("stale edit = %v, want ErrTemplateConflict", err)
	}
	// Without an expected version the editor accepts whatever is current.
	second.ExpectedVersion = 0
	if _, err := service.Update(ctx, admin, created.ID, second); err != nil {
		t.Fatalf("unguarded edit: %v", err)
	}
}

func TestUnattendedTemplateActionsListDistinctOutcomes(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(store)

	input := validTemplateInput("trap")
	input.Levels = append(input.Levels,
		quack.TemplateLevelInput{Name: "Again", Position: 3, TriggerCaseCount: 4,
			Actions: []quack.TemplateActionInput{{ActionType: quack.ActionTimeoutUser, TimeoutDurationSeconds: 60}}},
		quack.TemplateLevelInput{Name: "Ban", Position: 4, TriggerCaseCount: 5,
			Actions: []quack.TemplateActionInput{{ActionType: quack.ActionBanUser}}})
	created, err := service.Create(ctx, admin, input)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := service.UnattendedTemplateActions(ctx, " "+admin.Guild.ID+" ", created.ID)
	if err != nil || !slices.Equal(actions, []quack.ActionType{"", quack.ActionTimeoutUser, quack.ActionBanUser}) {
		t.Fatalf("actions = %v, %v", actions, err)
	}
	if _, err := service.UnattendedTemplateActions(ctx, "other-guild", created.ID); !errors.Is(err, quack.ErrUnattendedTemplateUnavailable) {
		t.Fatalf("other guild = %v", err)
	}
	if _, err := service.Archive(ctx, admin, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UnattendedTemplateActions(ctx, admin.Guild.ID, created.ID); !errors.Is(err, quack.ErrUnattendedTemplateUnavailable) {
		t.Fatalf("archived = %v", err)
	}
}

func TestEnsureHoneypotTemplateCreatesOnceAndKeepsEdits(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewTemplateService(store)

	if _, err := service.EnsureHoneypotTemplate(ctx, moderator); !errors.Is(err, quack.ErrTemplatePermissionDenied) {
		t.Fatalf("moderator setup = %v", err)
	}
	created, err := service.EnsureHoneypotTemplate(ctx, admin)
	if err != nil || created.Slug != "honeypot" || !created.Appealable || len(created.Levels) != 1 ||
		created.Levels[0].Actions[0].ActionType != quack.ActionBanUser || !created.Levels[0].NotifyUser {
		t.Fatalf("created = %+v, %v", created, err)
	}
	edit := created.EditInput()
	edit.Name = "Trap channel"
	if _, err := service.Update(ctx, admin, created.ID, edit); err != nil {
		t.Fatal(err)
	}
	again, err := service.EnsureHoneypotTemplate(ctx, admin)
	if err != nil || again.ID != created.ID || again.Name != "Trap channel" {
		t.Fatalf("second setup = %+v, %v", again, err)
	}
	if _, err := service.Archive(ctx, admin, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsureHoneypotTemplate(ctx, admin); !errors.Is(err, quack.ErrTemplateValidation) {
		t.Fatalf("archived setup = %v, want ErrTemplateValidation", err)
	}
}
