package discord_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/quack"
)

// templateCommand is /template <subcommand> from mod-1 with options.
func templateCommand(subcommand string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	i := interaction(discordgo.InteractionApplicationCommand, uint64(discordgo.PermissionManageGuild), nil)
	i.Data = discordgo.ApplicationCommandInteractionData{Name: "template", Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: subcommand, Type: discordgo.ApplicationCommandOptionSubCommand, Options: options},
	}}
	return i
}

func stringOption(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionString, Value: value}
}

func intOption(name string, value int) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionInteger, Value: float64(value)}
}

func boolOption(name string, value bool) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionBoolean, Value: value}
}

// runTemplate runs a /template subcommand, which always answers publicly,
// with errors going privately to the invoker.
func runTemplate(t *testing.T, h *caseHarness, subcommand string, options ...*discordgo.ApplicationCommandInteractionDataOption) *fakeResponder {
	t.Helper()
	result := discord.NewTemplates(h.services).Command()(context.Background(), templateCommand(subcommand, options...))
	if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data != nil {
		t.Fatalf("expected a public deferred acknowledgement: %+v", result.Response)
	}
	return run(t, result)
}

func (h *caseHarness) templateByID(t *testing.T, id string) *quack.TemplateResponse {
	t.Helper()
	template, err := h.services.Templates.Get(context.Background(), h.owner, id)
	if err != nil {
		t.Fatal(err)
	}
	return template
}

// TestTemplateCreateFormCreatesRule runs the command and its form through
// real storage for each outcome, and refuses a manager who lost access.
func TestTemplateCreateFormCreatesRule(t *testing.T) {
	for _, scenario := range []struct {
		outcome string
		minutes int
		action  quack.ActionType
		allowed bool
	}{
		{"warning", 0, "", true}, {"timeout", 60, quack.ActionTimeoutUser, true}, {"kick", 0, quack.ActionKickUser, true},
		{"ban", 0, quack.ActionBanUser, true}, {"ban", 0, quack.ActionBanUser, false},
	} {
		name := scenario.outcome
		if !scenario.allowed {
			name += "-revoked"
		}
		t.Run(name, func(t *testing.T) {
			bits := uint64(discordgo.PermissionManageGuild)
			if !scenario.allowed {
				bits = uint64(discordgo.PermissionModerateMembers)
			}
			h := newCaseHarness(t, bits)
			templates := discord.NewTemplates(h.services)
			i := templateCommand("create", stringOption("outcome", scenario.outcome), intOption("minutes", scenario.minutes))
			result := templates.Command()(context.Background(), i)
			if result.Response.Type != discordgo.InteractionResponseModal {
				t.Fatalf("missing form: %+v", result)
			}
			i.Type = discordgo.InteractionModalSubmit
			i.Data = discordgo.ModalSubmitInteractionData{CustomID: result.Response.Data.CustomID, Components: []discordgo.MessageComponent{
				discord.Row(discordgo.TextInput{CustomID: "name", Value: "New rule"}),
				discord.Row(discordgo.TextInput{CustomID: "reason", Value: "Keep chat appropriate."}),
			}}
			responder := run(t, templates.CreateSubmit()(context.Background(), i))
			if scenario.allowed != !responder.deleted {
				t.Fatalf("feedback: %q", responder.content())
			}
			active, err := h.services.Templates.ListActive(context.Background(), h.owner)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, template := range active {
				if template.Name != "New rule" {
					continue
				}
				found = true
				if template.ReasonTemplate != "Keep chat appropriate." || !template.Appealable || len(template.Levels) != 1 ||
					!template.Levels[0].NotifyUser || !template.Levels[0].IsDefault {
					t.Fatalf("wrong rule: %+v", template)
				}
				actions := template.Levels[0].Actions
				if scenario.action == "" && len(actions) != 0 {
					t.Fatal("warning punishes")
				}
				if scenario.action != "" && (len(actions) != 1 || actions[0].ActionType != scenario.action || actions[0].TimeoutDurationSeconds != scenario.minutes*60) {
					t.Fatalf("wrong action: %+v", actions)
				}
			}
			if found != scenario.allowed {
				t.Fatalf("live permission boundary failed: created=%v", found)
			}
		})
	}
}

// TestTemplateLevelUsesCaseCountAndKeepsOtherLevels replaces the third-case
// step in place and leaves the rule and default level alone.
func TestTemplateLevelUsesCaseCountAndKeepsOtherLevels(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionManageGuild))
	for _, outcome := range []string{"ban", "kick"} {
		responder := runTemplate(t, h, "level", stringOption("template", h.spamID), intOption("case", 3), stringOption("outcome", outcome))
		if responder.deleted || !strings.Contains(responder.content(), "after **3** cases") {
			t.Fatalf("feedback: %q", responder.content())
		}
		template := h.templateByID(t, h.spamID)
		if len(template.Levels) != 2 || template.Name != "Spam" || template.ReasonTemplate != "Spam" {
			t.Fatalf("edit lost the rule: %+v", template)
		}
		want := map[string]quack.ActionType{"ban": quack.ActionBanUser, "kick": quack.ActionKickUser}[outcome]
		for _, level := range template.Levels {
			if level.IsDefault {
				if len(level.Actions) != 0 || !level.NotifyUser {
					t.Fatal("default outcome changed")
				}
			} else if level.TriggerCaseCount != 3 || len(level.Actions) != 1 || level.Actions[0].ActionType != want {
				t.Fatalf("third-case step: %+v", level)
			}
		}
	}
}

// TestTemplateManagementLifecycle follows edit, level, view, remove-level,
// archive, and restore on one rule.
func TestTemplateManagementLifecycle(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionManageGuild))
	id := h.spamID
	runTemplate(t, h, "edit", stringOption("template", id), stringOption("name", "Chat rules"),
		stringOption("reason", "Keep chat readable."), boolOption("appeals", false))
	if template := h.templateByID(t, id); template.Name != "Chat rules" || template.ReasonTemplate != "Keep chat readable." || template.Appealable || len(template.Levels) != 1 {
		t.Fatalf("edit lost rule fields: %+v", template)
	}
	runTemplate(t, h, "level", stringOption("template", id), intOption("case", 3), stringOption("outcome", "ban"), boolOption("notify", false))
	for _, level := range h.templateByID(t, id).Levels {
		if !level.IsDefault && level.NotifyUser {
			t.Fatal("DM choice ignored")
		}
	}
	view := runTemplate(t, h, "view", stringOption("template", id))
	for _, want := range []string{"**3+ times:** Ban · DM off", "Keep chat readable.", "Counting all cases for this rule."} {
		if !strings.Contains(view.content(), want) {
			t.Fatalf("view missing %q: %s", want, view.content())
		}
	}
	if missing := runTemplate(t, h, "remove-level", stringOption("template", id), intOption("case", 4)); !strings.Contains(missing.content(), "There is no step at that count") {
		t.Fatalf("missing step: %q", missing.content())
	}
	runTemplate(t, h, "remove-level", stringOption("template", id), intOption("case", 3))
	if levels := h.templateByID(t, id).Levels; len(levels) != 1 || !levels[0].IsDefault {
		t.Fatalf("removal lost the default: %+v", levels)
	}
	runTemplate(t, h, "archive", stringOption("template", id))
	if h.templateByID(t, id).ArchivedAt == nil {
		t.Fatal("not archived")
	}
	if view := runTemplate(t, h, "view", stringOption("template", id)); !strings.Contains(view.content(), "Archived") {
		t.Fatal("archived rule cannot be inspected")
	}
	runTemplate(t, h, "restore", stringOption("template", id))
	if h.templateByID(t, id).ArchivedAt != nil {
		t.Fatal("not restored")
	}
}

// TestTemplateDecayCanBeSetAndCleared sets and clears the counting window.
func TestTemplateDecayCanBeSetAndCleared(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionManageGuild))
	for _, days := range []int{30, 0} {
		runTemplate(t, h, "edit", stringOption("template", h.spamID), intOption("decay-days", days))
		if got := h.templateByID(t, h.spamID).CaseDecayDays; got != days {
			t.Fatalf("decay = %d, want %d", got, days)
		}
		want := "Counting all cases for this rule."
		if days != 0 {
			want = "Counting cases from the last **30 days**."
		}
		if view := runTemplate(t, h, "view", stringOption("template", h.spamID)); !strings.Contains(view.content(), want) {
			t.Fatalf("missing %q: %s", want, view.content())
		}
	}
}

// TestTemplateManagementRejectsRevokedManager checks live permissions, not
// the bits the interaction claims.
func TestTemplateManagementRejectsRevokedManager(t *testing.T) {
	h := newCaseHarness(t, 0)
	for _, subcommand := range []string{"view", "edit", "remove-level", "archive", "restore", "level"} {
		options := []*discordgo.ApplicationCommandInteractionDataOption{stringOption("template", h.spamID)}
		if subcommand == "level" {
			options = append(options, intOption("case", 2), stringOption("outcome", "kick"))
		}
		responder := runTemplate(t, h, subcommand, options...)
		if !responder.deleted || !strings.Contains(responder.content(), "Manage Server") {
			t.Fatalf("%s allowed a revoked manager: %q", subcommand, responder.content())
		}
	}
}

// TestTemplateLevelMatchesCreatedCases counts real cases, so a conversion
// mistake cannot punish a case early.
func TestTemplateLevelMatchesCreatedCases(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionManageGuild))
	runTemplate(t, h, "level", stringOption("template", h.spamID), intOption("case", 3), stringOption("outcome", "ban"))
	for number := 1; number <= 3; number++ {
		created, err := h.services.Cases.Create(context.Background(), h.owner, quack.CaseInput{TemplateID: h.spamID, TargetDiscordUserID: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if created.SelectedLevel == nil || created.SelectedLevel.MatchedCaseCount != int64(number) {
			t.Fatalf("case %d count: %+v", number, created.SelectedLevel)
		}
		if escalated := !created.SelectedLevel.IsDefault && len(created.Actions) == 1; escalated != (number == 3) {
			t.Fatalf("case %d escalation: %+v", number, created)
		}
	}
}

// TestTemplateAutocompleteFiltersBySubcommand offers archived rules only to
// restore, view, and edit.
func TestTemplateAutocompleteFiltersBySubcommand(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionManageGuild))
	archived := h.template(t, quack.TemplateInput{
		Slug: "old", Name: "Old rule", ReasonTemplate: "Old",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	if _, err := h.services.Templates.Archive(context.Background(), h.owner, archived.ID); err != nil {
		t.Fatal(err)
	}
	names := func(subcommand string) []string {
		i := templateCommand(subcommand, &discordgo.ApplicationCommandInteractionDataOption{
			Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: "", Focused: true,
		})
		i.Type = discordgo.InteractionApplicationCommandAutocomplete
		var got []string
		for _, choice := range discord.NewTemplates(h.services).Command()(context.Background(), i).Response.Data.Choices {
			got = append(got, choice.Value.(string))
		}
		return got
	}
	for subcommand, want := range map[string][]string{
		"restore": {archived.ID}, "archive": {h.spamID}, "level": {h.spamID},
		"view": {h.spamID, archived.ID}, "edit": {h.spamID, archived.ID},
	} {
		got := names(subcommand)
		if len(got) != len(want) {
			t.Fatalf("%s offered %v, want %v", subcommand, got, want)
		}
		for _, id := range want {
			if !strings.Contains(strings.Join(got, ","), id) {
				t.Fatalf("%s offered %v, want %v", subcommand, got, want)
			}
		}
	}
}
