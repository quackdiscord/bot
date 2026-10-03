package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

type fakeDiscordClient struct {
	botGuild                *quack.DiscordBotGuild
	liveActorPermissionBits *uint64
}

func (f fakeDiscordClient) UserGuilds(ctx context.Context, accessToken string) ([]quack.DiscordUserGuild, error) {
	return nil, nil
}

func (f fakeDiscordClient) BotGuilds(ctx context.Context) ([]quack.DiscordBotGuild, error) {
	if f.botGuild == nil {
		return nil, nil
	}
	return []quack.DiscordBotGuild{*f.botGuild}, nil
}

func (f fakeDiscordClient) BotGuild(ctx context.Context, discordGuildID string) (*quack.DiscordBotGuild, error) {
	return f.botGuild, nil
}

func (f fakeDiscordClient) GuildAuthorization(ctx context.Context, guildID, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
	permissionBits := uint64(discordgo.PermissionModerateMembers)
	if f.liveActorPermissionBits != nil {
		permissionBits = *f.liveActorPermissionBits
	}
	target := &quack.DiscordMemberAuthorization{DiscordUserID: targetID, Present: targetID != "", TopRolePosition: 1}
	return &quack.DiscordGuildAuthorization{
		Guild:  *f.botGuild,
		Actor:  quack.DiscordMemberAuthorization{DiscordUserID: actorID, Present: true, PermissionBits: permissionBits, TopRolePosition: 10},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 20, Bot: true},
		Target: target,
	}, nil
}

func TestCommandDefinitionDefinesCaseAdd(t *testing.T) {
	command := CommandDefinition()
	if command.Name != "case" || command.DMPermission == nil || *command.DMPermission {
		t.Fatalf("unexpected command definition: %+v", command)
	}
	if command.DefaultMemberPermissions == nil || *command.DefaultMemberPermissions != int64(discordgo.PermissionModerateMembers) {
		t.Fatalf("expected moderate members default permission, got %+v", command.DefaultMemberPermissions)
	}
	if len(command.Options) < 9 || command.Options[0].Name != "add" {
		t.Fatalf("expected add subcommand, got %+v", command.Options)
	}

	add := command.Options[0]
	if len(add.Options) != 4 {
		t.Fatalf("expected template/user/context/evidence options, got %+v", add.Options)
	}
	if !add.Options[0].Autocomplete {
		t.Fatalf("expected template option to support autocomplete")
	}
	for position, option := range add.Options {
		if position < 2 && !option.Required {
			t.Fatalf("expected required options first, got %+v", add.Options)
		}
		if position >= 2 && option.Required {
			t.Fatalf("required option follows optional options: %+v", add.Options)
		}
	}
}

func TestHandleCaseInteractionCreatesCase(t *testing.T) {
	ctx := context.Background()
	store, services, templateID := newCaseCommandHarness(t)

	result := HandleCaseInteraction(ui.Context{
		Context:     ctx,
		Services:    services,
		Interaction: caseAddInteraction(templateID, "target-1", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred success response, got %v", response.Type)
	}
	if response.Data == nil || response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected private acknowledgement before public success, got %+v", response.Data)
	}
	if result.Task == nil {
		t.Fatalf("expected deferred case creation task")
	}
	responder := &fakeResponder{}
	if err := result.Task(ctx, responder); err != nil {
		t.Fatalf("run deferred task: %v", err)
	}
	if !responder.deleted || len(responder.followup.Embeds) != 0 || responder.followup.Ephemeral || responder.editCount != 1 {
		t.Fatalf("expected permanent text after completing private acknowledgement: %+v", responder)
	}
	for _, want := range []string{"**Case #1 created**", "<@target-1>", "Spam", "Default", "No Discord action configured"} {
		if !strings.Contains(responder.followup.Content, want) {
			t.Fatalf("missing %q in %q", want, responder.followup.Content)
		}
	}

	cases, err := store.ListCases(ctx, storeGuildID(t, store, "guild-1"))
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected one case, got %+v", cases)
	}
	if cases[0].Source != quack.CaseSourceDiscord || cases[0].TargetDiscordUserID != "target-1" {
		t.Fatalf("unexpected case: %+v", cases[0])
	}
	if cases[0].Reason != "Spam" {
		t.Fatalf("expected immutable template reason, got %q", cases[0].Reason)
	}
}

func TestHandleCaseInteractionDoesNotTrustStaleInteractionPermissionBits(t *testing.T) {
	_, services, templateID := newCaseCommandHarness(t)

	result := HandleCaseInteraction(ui.Context{
		Context:     context.Background(),
		Services:    services,
		Interaction: caseAddInteraction(templateID, "target-1", 0),
	})
	response := result.Response
	if response == nil || response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("unexpected response: %+v", response)
	}
	if result.Task == nil {
		t.Fatalf("expected live Discord permission to authorize despite stale interaction bits")
	}
}

func TestHandleCaseInteractionDeniesRevokedLivePermissionDespiteInteractionSnapshot(t *testing.T) {
	_, services, templateID := newCaseCommandHarnessWithLivePermissions(t, 0)
	result := HandleCaseInteraction(ui.Context{
		Context: context.Background(), Services: services,
		Interaction: caseAddInteraction(templateID, "target-1", ^uint64(0)),
	})
	response := result.Response
	if response == nil || response.Data == nil || len(response.Data.Embeds) != 1 || !strings.Contains(response.Data.Embeds[0].Description, "do not have permission") {
		t.Fatalf("unexpected live permission denial: %+v", response)
	}
	if result.Task != nil || response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected immediate private denial, result=%+v", result)
	}
}

func TestHandleTemplateAutocompleteReturnsUsableTemplates(t *testing.T) {
	_, services, templateID := newCaseCommandHarness(t)

	result := HandleCaseInteraction(ui.Context{
		Context:     context.Background(),
		Services:    services,
		Interaction: templateAutocompleteInteraction("repeated", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil || response.Data == nil || len(response.Data.Choices) != 1 {
		t.Fatalf("unexpected autocomplete response: %+v", response)
	}
	if result.Task != nil {
		t.Fatalf("expected autocomplete to be immediate")
	}
	if response.Data.Choices[0].Value != templateID {
		t.Fatalf("expected template id choice, got %+v", response.Data.Choices[0])
	}
	if response.Data.Choices[0].Name != "Spam - Unwanted repeated messages" {
		t.Fatalf("expected name and description choice label, got %q", response.Data.Choices[0].Name)
	}
}

func TestHandleTemplateAutocompleteFiltersArchivedTemplates(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	ctx := context.Background()
	guildContext := caseCommandGuildContext(t, services)
	template := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{
		Slug:           "ghost",
		Name:           "Ghost",
		Description:    "Hidden moderation workflow",
		ReasonTemplate: "Hidden",
		Levels: []quack.TemplateLevelInput{
			{
				Name:      "Default",
				Position:  1,
				IsDefault: true,
			},
		},
	})
	if _, err := services.Templates.Archive(ctx, guildContext, template.ID); err != nil {
		t.Fatalf("archive template: %v", err)
	}

	result := HandleCaseInteraction(ui.Context{
		Context:     ctx,
		Services:    services,
		Interaction: templateAutocompleteInteraction("hidden", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil || response.Data == nil {
		t.Fatalf("unexpected autocomplete response: %+v", response)
	}
	if len(response.Data.Choices) != 0 {
		t.Fatalf("expected disabled template to be filtered, got %+v", response.Data.Choices)
	}
}

func TestTemplateAutocompleteLabelTruncatesToDiscordLimit(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	ctx := context.Background()
	guildContext := caseCommandGuildContext(t, services)
	longTemplate := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{
		Slug:           "longdesc",
		Name:           "Long Description",
		Description:    strings.Repeat("description ", 20),
		ReasonTemplate: "Long description",
		Levels: []quack.TemplateLevelInput{
			{
				Name:      "Default",
				Position:  1,
				IsDefault: true,
			},
		},
	})

	result := HandleCaseInteraction(ui.Context{
		Context:     ctx,
		Services:    services,
		Interaction: templateAutocompleteInteraction("longdesc", uint64(discordgo.PermissionModerateMembers)),
	})
	response := result.Response
	if response == nil || response.Data == nil || len(response.Data.Choices) != 1 {
		t.Fatalf("unexpected autocomplete response: %+v", response)
	}
	if response.Data.Choices[0].Value != longTemplate.ID {
		t.Fatalf("expected long template id choice, got %+v", response.Data.Choices[0])
	}
	if len([]rune(response.Data.Choices[0].Name)) != 100 {
		t.Fatalf("expected label length 100, got %d: %q", len([]rune(response.Data.Choices[0].Name)), response.Data.Choices[0].Name)
	}
}

type fakeResponder struct {
	edit      ui.Edit
	followup  ui.Message
	deleted   bool
	updated   ui.Edit
	editCount int
}

func (f *fakeResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	f.edit = edit
	f.editCount++
	return &discordgo.Message{ID: "message-1"}, nil
}

func (f *fakeResponder) Followup(message ui.Message) (*discordgo.Message, error) {
	if f.editCount == 0 {
		message.Ephemeral = true
	}
	f.followup = message
	return &discordgo.Message{ID: "followup-1"}, nil
}

func (f *fakeResponder) EditFollowup(messageID string, edit ui.Edit) (*discordgo.Message, error) {
	f.updated = edit
	return &discordgo.Message{ID: messageID}, nil
}

func (f *fakeResponder) DeleteOriginal() error {
	f.deleted = true
	return nil
}

func (f *fakeResponder) UpdateMessage(edit ui.Edit) (*discordgo.Message, error) {
	f.updated = edit
	return &discordgo.Message{ID: "message-1"}, nil
}

func newCaseCommandHarness(t *testing.T) (*store.Store, *quack.Services, string) {
	return newCaseCommandHarnessWithLivePermissions(t, uint64(discordgo.PermissionModerateMembers))
}

func newCaseCommandHarnessWithLivePermissions(t *testing.T, permissionBits uint64) (*store.Store, *quack.Services, string) {
	t.Helper()

	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	services := quack.New(quack.Deps{Store: store, Guilds: fakeDiscordClient{
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"}, liveActorPermissionBits: &permissionBits,
	}})
	guildContext, err := services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "owner-1",
		DisplayName:    "Owner",
	})
	if err != nil {
		t.Fatalf("resolve guild context: %v", err)
	}

	created := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{
		Slug:           "spam",
		Name:           "Spam",
		Description:    "Unwanted repeated messages",
		ReasonTemplate: "Spam",
		Levels: []quack.TemplateLevelInput{
			{
				Name:       "Default",
				Position:   1,
				IsDefault:  true,
				NotifyUser: true,
			},
		},
	})

	return store, services, created.ID
}

func createCaseCommandTemplate(t *testing.T, services *quack.Services, guildContext *quack.GuildStaffContext, input quack.TemplateInput) *quack.TemplateResponse {
	t.Helper()

	created, err := services.Templates.Create(context.Background(), guildContext, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

func caseCommandGuildContext(t *testing.T, services *quack.Services) *quack.GuildStaffContext {
	t.Helper()

	guildContext, err := services.Guilds.ResolveDiscordStaffContext(context.Background(), quack.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "owner-1",
		DisplayName:    "Owner",
	})
	if err != nil {
		t.Fatalf("resolve guild context: %v", err)
	}
	return guildContext
}

func caseAddInteraction(templateID, targetID string, permissions uint64) *discordgo.InteractionCreate {
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

func storeGuildID(t *testing.T, store *store.Store, discordGuildID string) string {
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

func embedFields(embed *discordgo.MessageEmbed) map[string]string {
	fields := map[string]string{}
	for _, field := range embed.Fields {
		fields[field.Name] = field.Value
	}
	return fields
}
