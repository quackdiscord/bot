package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

type fakeDiscordClient struct {
	botGuild *app.DiscordBotGuild
}

func (f fakeDiscordClient) UserGuilds(ctx context.Context, accessToken string) ([]app.DiscordUserGuild, error) {
	return nil, nil
}

func (f fakeDiscordClient) BotGuilds(ctx context.Context) ([]app.DiscordBotGuild, error) {
	if f.botGuild == nil {
		return nil, nil
	}
	return []app.DiscordBotGuild{*f.botGuild}, nil
}

func (f fakeDiscordClient) BotGuild(ctx context.Context, discordGuildID string) (*app.DiscordBotGuild, error) {
	return f.botGuild, nil
}

func TestCommandDefinitionDefinesCaseAdd(t *testing.T) {
	command := CommandDefinition()
	if command.Name != "case" || command.DMPermission == nil || *command.DMPermission {
		t.Fatalf("unexpected command definition: %+v", command)
	}
	if command.DefaultMemberPermissions == nil || *command.DefaultMemberPermissions != int64(discordgo.PermissionModerateMembers) {
		t.Fatalf("expected moderate members default permission, got %+v", command.DefaultMemberPermissions)
	}
	if len(command.Options) != 1 || command.Options[0].Name != "add" {
		t.Fatalf("expected add subcommand, got %+v", command.Options)
	}

	add := command.Options[0]
	if len(add.Options) != 3 {
		t.Fatalf("expected template/user/reason options, got %+v", add.Options)
	}
	if !add.Options[0].Autocomplete {
		t.Fatalf("expected template option to support autocomplete")
	}
}

func TestHandleCaseInteractionCreatesCase(t *testing.T) {
	ctx := context.Background()
	store, services, templateID := newCaseCommandHarness(t)

	response := HandleCaseInteraction(ctx, services, nil, caseAddInteraction(templateID, "target-1", "manual reason", uint64(discordgo.PermissionModerateMembers)))
	if response == nil || response.Data == nil || !strings.Contains(response.Data.Content, "Created case #1") {
		t.Fatalf("unexpected response: %+v", response)
	}

	cases, err := store.ListCases(ctx, storeGuildID(t, store, "guild-1"))
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected one case, got %+v", cases)
	}
	if cases[0].Source != structs.CaseSourceDiscordCommand || cases[0].TargetDiscordUserID != "target-1" {
		t.Fatalf("unexpected case: %+v", cases[0])
	}
	if cases[0].Reason != "manual reason" {
		t.Fatalf("expected reason override, got %q", cases[0].Reason)
	}
}

func TestHandleCaseInteractionDeniesMissingPermission(t *testing.T) {
	_, services, templateID := newCaseCommandHarness(t)

	response := HandleCaseInteraction(context.Background(), services, nil, caseAddInteraction(templateID, "target-1", "", 0))
	if response == nil || response.Data == nil || !strings.Contains(response.Data.Content, "do not have permission") {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestHandleTemplateAutocompleteReturnsUsableTemplates(t *testing.T) {
	_, services, templateID := newCaseCommandHarness(t)

	response := HandleCaseInteraction(context.Background(), services, nil, templateAutocompleteInteraction("sp", uint64(discordgo.PermissionModerateMembers)))
	if response == nil || response.Data == nil || len(response.Data.Choices) != 1 {
		t.Fatalf("unexpected autocomplete response: %+v", response)
	}
	if response.Data.Choices[0].Value != templateID {
		t.Fatalf("expected template id choice, got %+v", response.Data.Choices[0])
	}
}

func newCaseCommandHarness(t *testing.T) (*storage.Store, *app.Services, string) {
	t.Helper()

	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	services := app.NewWithDiscordClient(store, fakeDiscordClient{
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})
	guildContext, err := services.Guilds.ResolveDiscordStaffContext(ctx, app.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "owner-1",
		DisplayName:    "Owner",
	})
	if err != nil {
		t.Fatalf("resolve guild context: %v", err)
	}

	created, err := services.Templates.Create(ctx, guildContext, app.TemplateInput{
		Slug:           "spam",
		Name:           "Spam",
		ReasonTemplate: "Spam",
		Actions: []app.TemplateActionInput{
			{ActionType: structs.ActionRecordWarning},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	return store, services, created.ID
}

func caseAddInteraction(templateID, targetID, reason string, permissions uint64) *discordgo.InteractionCreate {
	options := []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name:  "template",
			Type:  discordgo.ApplicationCommandOptionString,
			Value: templateID,
		},
		{
			Name:  "user",
			Type:  discordgo.ApplicationCommandOptionUser,
			Value: targetID,
		},
	}
	if reason != "" {
		options = append(options, &discordgo.ApplicationCommandInteractionDataOption{
			Name:  "reason",
			Type:  discordgo.ApplicationCommandOptionString,
			Value: reason,
		})
	}

	return interaction(discordgo.InteractionApplicationCommand, permissions, []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name:    "add",
			Type:    discordgo.ApplicationCommandOptionSubCommand,
			Options: options,
		},
	})
}

func templateAutocompleteInteraction(query string, permissions uint64) *discordgo.InteractionCreate {
	return interaction(discordgo.InteractionApplicationCommandAutocomplete, permissions, []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name: "add",
			Type: discordgo.ApplicationCommandOptionSubCommand,
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{
					Name:    "template",
					Type:    discordgo.ApplicationCommandOptionString,
					Value:   query,
					Focused: true,
				},
			},
		},
	})
}

func interaction(interactionType discordgo.InteractionType, permissions uint64, options []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "interaction-1",
			AppID:     "app-1",
			Type:      interactionType,
			GuildID:   "guild-1",
			ChannelID: "channel-1",
			Member: &discordgo.Member{
				User:        &discordgo.User{ID: "mod-1", Username: "mod", GlobalName: "Moderator"},
				Permissions: int64(permissions),
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:    "case",
				Options: options,
			},
		},
	}
}

func storeGuildID(t *testing.T, store *storage.Store, discordGuildID string) string {
	t.Helper()

	guild, err := store.GetGuildByDiscordID(context.Background(), discordGuildID)
	if err != nil {
		t.Fatalf("get guild: %v", err)
	}
	if guild == nil {
		t.Fatalf("expected guild %s", discordGuildID)
	}
	return guild.ID
}
