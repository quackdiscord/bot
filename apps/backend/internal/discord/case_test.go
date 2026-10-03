package discord_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

// fakeDirectory answers live authorization with fixed permissions and
// counts how often Discord would have been asked.
type fakeDirectory struct {
	actorBits uint64
	calls     atomic.Int64
}

func (f *fakeDirectory) UserGuilds(context.Context, string) ([]quack.DiscordUserGuild, error) {
	return nil, nil
}

func (f *fakeDirectory) BotGuilds(context.Context) ([]quack.DiscordBotGuild, error) {
	return []quack.DiscordBotGuild{{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"}}, nil
}

func (f *fakeDirectory) GuildAuthorization(_ context.Context, _, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
	f.calls.Add(1)
	return &quack.DiscordGuildAuthorization{
		Guild:  quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
		Actor:  quack.DiscordMemberAuthorization{DiscordUserID: actorID, Present: true, PermissionBits: f.actorBits, TopRolePosition: 10},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 20, Bot: true},
		Target: &quack.DiscordMemberAuthorization{DiscordUserID: targetID, Present: targetID != "", TopRolePosition: 1},
	}, nil
}

// caseHarness is a case handler backed by SQLite and a fake Discord, with a
// "spam" template already created.
type caseHarness struct {
	store     *store.Store
	cases     discord.Cases
	services  *quack.Services
	directory *fakeDirectory
	owner     *quack.GuildStaffContext
	spamID    string
}

func newCaseHarness(t *testing.T, liveActorBits uint64) *caseHarness {
	t.Helper()
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	directory := &fakeDirectory{actorBits: liveActorBits}
	services := quack.New(quack.Deps{Store: repository, Guilds: directory})
	owner, err := services.Guilds.ResolveDiscordStaffContext(context.Background(), quack.DiscordStaffContextInput{
		DiscordGuildID: "guild-1", DiscordUserID: "owner-1", DisplayName: "Owner",
	})
	if err != nil {
		t.Fatalf("resolve owner: %v", err)
	}
	h := &caseHarness{store: repository, cases: discord.NewCases(services), services: services, directory: directory, owner: owner}
	h.spamID = h.template(t, quack.TemplateInput{
		Slug: "spam", Name: "Spam", Description: "Unwanted repeated messages", ReasonTemplate: "Spam",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true, NotifyUser: true}},
	}).ID
	directory.calls.Store(0)
	return h
}

func (h *caseHarness) template(t *testing.T, input quack.TemplateInput) *quack.TemplateResponse {
	t.Helper()
	created, err := h.services.Templates.Create(context.Background(), h.owner, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

func interaction(kind discordgo.InteractionType, permissions uint64, options []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction-1", AppID: "app-1", Type: kind, GuildID: "guild-1", ChannelID: "channel-1",
		Member: &discordgo.Member{
			User:        &discordgo.User{ID: "mod-1", Username: "mod", GlobalName: "Moderator"},
			Permissions: int64(permissions),
		},
		Data: discordgo.ApplicationCommandInteractionData{Name: "case", Options: options},
	}}
}

func caseAdd(templateID, targetID string, permissions uint64) *discordgo.InteractionCreate {
	return interaction(discordgo.InteractionApplicationCommand, permissions, []*discordgo.ApplicationCommandInteractionDataOption{{
		Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: templateID},
			{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: targetID},
		},
	}})
}

func templateAutocomplete(query string) *discordgo.InteractionCreate {
	return interaction(discordgo.InteractionApplicationCommandAutocomplete, uint64(discordgo.PermissionModerateMembers), []*discordgo.ApplicationCommandInteractionDataOption{{
		Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: query, Focused: true},
		},
	}})
}

// fakeResponder records a task's output. Like Discord, a followup before
// the deferred response is edited stays private.
type fakeResponder struct {
	edit      discord.Edit
	followup  discord.Message
	updated   discord.Edit
	deleted   bool
	editCount int
}

func (f *fakeResponder) EditOriginal(edit discord.Edit) (*discordgo.Message, error) {
	f.edit = edit
	f.editCount++
	return &discordgo.Message{ID: "message-1"}, nil
}

func (f *fakeResponder) Followup(message discord.Message) (*discordgo.Message, error) {
	if f.editCount == 0 {
		message.Ephemeral = true
	}
	f.followup = message
	return &discordgo.Message{ID: "followup-1"}, nil
}

func (f *fakeResponder) EditFollowup(messageID string, edit discord.Edit) (*discordgo.Message, error) {
	f.updated = edit
	return &discordgo.Message{ID: messageID}, nil
}

func (f *fakeResponder) DeleteOriginal() error {
	f.deleted = true
	return nil
}

func TestCaseAddCreatesCaseAndResolvesStaffOnce(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	ctx := context.Background()
	result := h.cases.Command(ctx, caseAdd(h.spamID, "target-1", uint64(discordgo.PermissionModerateMembers)))
	if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource ||
		result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || result.Task == nil {
		t.Fatalf("expected private deferred acknowledgement, got %+v", result)
	}
	if calls := h.directory.calls.Load(); calls != 1 {
		t.Fatalf("staff context resolved %d times before the task, want 1", calls)
	}
	responder := &fakeResponder{}
	if err := result.Task(ctx, responder); err != nil {
		t.Fatalf("run deferred task: %v", err)
	}
	// The task reuses the resolved context; the only extra call is the
	// case preflight's deliberate re-check.
	if calls := h.directory.calls.Load(); calls != 2 {
		t.Fatalf("Discord asked %d times for one /case add, want 2", calls)
	}
	if !responder.deleted || len(responder.followup.Embeds) != 0 || responder.followup.Ephemeral || responder.editCount != 1 {
		t.Fatalf("expected public text after completing private acknowledgement: %+v", responder)
	}
	for _, want := range []string{"**Case #1 created**", "<@target-1>", "Spam", "Default", "No Discord action configured"} {
		if !strings.Contains(responder.followup.Content, want) {
			t.Fatalf("missing %q in %q", want, responder.followup.Content)
		}
	}
	guild, err := h.store.GetGuildByDiscordID(ctx, "guild-1")
	if err != nil {
		t.Fatal(err)
	}
	created, err := h.store.ListCases(ctx, guild.ID)
	if err != nil || len(created) != 1 {
		t.Fatalf("list cases: %+v, %v", created, err)
	}
	if created[0].Source != quack.CaseSourceDiscord || created[0].TargetDiscordUserID != "target-1" || created[0].Reason != "Spam" {
		t.Fatalf("unexpected case: %+v", created[0])
	}
}

func TestCaseAddUsesLivePermissionsNotInteractionBits(t *testing.T) {
	t.Run("stale denial", func(t *testing.T) {
		h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
		result := h.cases.Command(context.Background(), caseAdd(h.spamID, "target-1", 0))
		if result.Task == nil {
			t.Fatalf("live permission did not authorize despite stale interaction bits: %+v", result.Response)
		}
	})
	t.Run("revoked grant", func(t *testing.T) {
		h := newCaseHarness(t, 0)
		result := h.cases.Command(context.Background(), caseAdd(h.spamID, "target-1", ^uint64(0)))
		response := result.Response
		if result.Task != nil || len(response.Data.Embeds) != 1 || !strings.Contains(response.Data.Embeds[0].Description, "do not have permission") ||
			response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
			t.Fatalf("expected immediate private denial, got %+v", result)
		}
	})
}

func TestTemplateAutocomplete(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	archived := h.template(t, quack.TemplateInput{
		Slug: "ghost", Name: "Ghost", Description: "Hidden moderation workflow", ReasonTemplate: "Hidden",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	if _, err := h.services.Templates.Archive(context.Background(), h.owner, archived.ID); err != nil {
		t.Fatal(err)
	}
	long := h.template(t, quack.TemplateInput{
		Slug: "longdesc", Name: "Long Description", Description: strings.Repeat("description ", 20), ReasonTemplate: "Long",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	tests := []struct {
		query     string
		wantValue string
		wantName  string
	}{
		{"repeated", h.spamID, "Spam - Unwanted repeated messages"},
		{"hidden", "", ""},
		{"longdesc", long.ID, discord.Truncate("Long Description - "+strings.Repeat("description ", 20), 100)},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			result := h.cases.Command(context.Background(), templateAutocomplete(test.query))
			choices := result.Response.Data.Choices
			if result.Task != nil || result.Response.Type != discordgo.InteractionApplicationCommandAutocompleteResult {
				t.Fatalf("expected immediate autocomplete, got %+v", result)
			}
			if test.wantValue == "" {
				if len(choices) != 0 {
					t.Fatalf("archived template offered: %+v", choices)
				}
				return
			}
			if len(choices) != 1 || choices[0].Value != test.wantValue || choices[0].Name != test.wantName {
				t.Fatalf("choices = %+v", choices)
			}
			if len([]rune(choices[0].Name)) > 100 {
				t.Fatalf("choice name over Discord's limit: %q", choices[0].Name)
			}
		})
	}
}

func TestCaseAddContextModalKeepsPublicSummaryLimited(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	template := h.template(t, quack.TemplateInput{
		Slug: "abuse", Name: "Abuse", ReasonTemplate: "Abusive behavior",
		ContextFields: []quack.TemplateContextFieldInput{{Key: "details", Label: "What happened?", FieldType: quack.ContextFieldLongText, Position: 1, Required: true}},
		Levels:        []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	result := h.cases.Command(context.Background(), caseAdd(template.ID, "target-2", uint64(discordgo.PermissionModerateMembers)))
	if result.Response.Type != discordgo.InteractionResponseModal || len(result.Response.Data.Components) != 1 {
		t.Fatalf("expected structured context modal, got %+v", result.Response)
	}
	submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
	submit.ID = "modal-interaction-2"
	submit.Data = discordgo.ModalSubmitInteractionData{CustomID: result.Response.Data.CustomID, Components: []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "context_details", Value: "Repeated abusive replies"}}},
	}}
	modal := h.cases.ContextModal(context.Background(), submit)
	if modal.Task == nil || modal.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected private acknowledgement, got %+v", modal)
	}
	responder := &fakeResponder{}
	if err := modal.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if !responder.deleted || responder.followup.Ephemeral || responder.editCount != 1 {
		t.Fatalf("expected public result, got %+v", responder)
	}
	for _, want := range []string{"<@target-2>", "Abuse", "Default"} {
		if !strings.Contains(responder.followup.Content, want) {
			t.Fatalf("missing %q: %+v", want, responder.followup)
		}
	}
	for _, hidden := range []string{"Moderator", "Visible context", "Evidence", "Repeated abusive replies"} {
		if strings.Contains(responder.followup.Content, hidden) {
			t.Fatalf("public result leaked %s", hidden)
		}
	}
}

// TestCaseInteractionsMatchGolden replays the /case flows and compares
// every response with what the pre-rewrite adapter produced, so modal
// fields, custom IDs, select menus, and copy stay exactly as they were.
func TestCaseInteractionsMatchGolden(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	fields := make([]quack.TemplateContextFieldInput, 0, 6)
	for index := 1; index <= 6; index++ {
		fields = append(fields, quack.TemplateContextFieldInput{
			Key: fmt.Sprintf("field_%d", index), Label: fmt.Sprintf("Field %d", index),
			FieldType: quack.ContextFieldShortText, Position: index, Required: index%2 == 0,
		})
	}
	fields[1].FieldType = quack.ContextFieldLongText
	fields[2].FieldType = quack.ContextFieldBoolean
	many := h.template(t, quack.TemplateInput{
		Slug: "many-fields", Name: "Many Fields", Description: "Lots", ReasonTemplate: "Many fields",
		ContextFields: fields, Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	ctx := context.Background()
	c := h.cases
	out := map[string]any{}

	void := interaction(discordgo.InteractionMessageComponent, 0, nil)
	void.Data = discordgo.MessageComponentInteractionData{CustomID: "case:void:v1:case-1"}
	out["void_button"] = c.VoidButton(ctx, void).Response

	reverse := interaction(discordgo.InteractionMessageComponent, 0, nil)
	reverse.Data = discordgo.MessageComponentInteractionData{CustomID: "case:reverse:v1:case-1|exec-1|unban_user"}
	out["reverse_button"] = c.ReverseButton(ctx, reverse).Response

	command := caseAdd(many.ID, "target-many", uint64(discordgo.PermissionModerateMembers))
	command.ID = "interaction-many"
	first := c.Command(ctx, command)
	out["context_modal_first"] = first.Response

	var page []discordgo.MessageComponent
	for index := 1; index <= 5; index++ {
		value := fmt.Sprintf("value-%d", index)
		if index == 3 {
			value = "true"
		}
		page = append(page, discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: fmt.Sprintf("context_field_%d", index), Value: value}}})
	}
	submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
	submit.ID = "modal-many-1"
	submit.Data = discordgo.ModalSubmitInteractionData{CustomID: first.Response.Data.CustomID, Components: page}
	out["context_continue"] = c.ContextModal(ctx, submit).Response

	next := interaction(discordgo.InteractionMessageComponent, 0, nil)
	next.ID = "component-many"
	next.Data = discordgo.MessageComponentInteractionData{CustomID: "case:context_next:v1:interaction-many"}
	out["context_modal_next"] = c.ContextNext(ctx, next).Response

	empty := interaction(discordgo.InteractionModalSubmit, 0, nil)
	empty.Data = discordgo.ModalSubmitInteractionData{CustomID: first.Response.Data.CustomID}
	missing := c.ContextModal(ctx, empty).Response
	missing.Data.Embeds[0].Timestamp = ""
	out["context_missing_required"] = missing

	message := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "message-command", Type: discordgo.InteractionApplicationCommand, GuildID: "guild-1", ChannelID: "channel-1",
		Member: &discordgo.Member{User: &discordgo.User{ID: "mod-1", Username: "mod"}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: discord.MessageCaseCommandName, TargetID: "message-1",
			Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Messages: map[string]*discordgo.Message{
				"message-1": {ID: "message-1", ChannelID: "channel-1", Author: &discordgo.User{ID: "target-1"}},
			}},
		},
	}}
	out["message_select"] = c.MessageCommand(ctx, message).Response
	out["autocomplete_all"] = c.Command(ctx, templateAutocomplete("")).Response

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(body), many.ID, "TEMPLATE_MANY")
	got = strings.ReplaceAll(got, h.spamID, "TEMPLATE_SPAM")
	discord.AssertGolden(t, "testdata/commands.golden.json", got)

	// The last page creates the case from all six fields.
	final := interaction(discordgo.InteractionModalSubmit, 0, nil)
	final.ID = "modal-many-2"
	final.Data = discordgo.ModalSubmitInteractionData{CustomID: first.Response.Data.CustomID, Components: []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "context_field_6", Value: "value-6"}}},
	}}
	created := c.ContextModal(ctx, final)
	responder := &fakeResponder{}
	if created.Task == nil {
		t.Fatalf("completed form did not create a case: %+v", created.Response)
	}
	if err := created.Task(ctx, responder); err != nil || !responder.deleted {
		t.Fatalf("completed form did not publish: responder=%+v err=%v", responder, err)
	}
}
