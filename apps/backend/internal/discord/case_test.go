package discord_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

// fakeDirectory answers live authorization with fixed permissions and
// counts how often Discord would have been asked.
type fakeDirectory struct {
	// guildID is the one server Quack is in.
	guildID   string
	actorBits uint64
	// mfaRequired makes the guild require 2FA for moderation.
	mfaRequired bool
	calls       atomic.Int64
}

func (f *fakeDirectory) UserGuilds(context.Context, string) ([]quack.DiscordUserGuild, error) {
	return nil, nil
}

func (f *fakeDirectory) BotGuilds(context.Context) ([]quack.DiscordBotGuild, error) {
	return []quack.DiscordBotGuild{{ID: f.guildID, Name: "Guild", OwnerID: "owner-1"}}, nil
}

func (f *fakeDirectory) GuildAuthorization(_ context.Context, _, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
	f.calls.Add(1)
	return &quack.DiscordGuildAuthorization{
		Guild:  quack.DiscordBotGuild{ID: f.guildID, Name: "Guild", OwnerID: "owner-1", MFARequired: f.mfaRequired},
		Actor:  quack.DiscordMemberAuthorization{DiscordUserID: actorID, Present: true, PermissionBits: f.actorBits, TopRolePosition: 10},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 20, Bot: true},
		Target: &quack.DiscordMemberAuthorization{DiscordUserID: targetID, Present: targetID != "", TopRolePosition: 1},
	}, nil
}

// fakePoster records channel posts made with bot credentials.
type fakePoster struct {
	sent []discord.Message
}

func (f *fakePoster) Send(_ context.Context, channelID string, message discord.Message) (*discordgo.Message, error) {
	f.sent = append(f.sent, message)
	return &discordgo.Message{ID: fmt.Sprintf("posted-%d", len(f.sent)), ChannelID: channelID}, nil
}

// caseHarness is a case handler backed by SQLite and a fake Discord, with a
// "spam" template already created.
type caseHarness struct {
	store     *store.Store
	cases     discord.Cases
	services  *quack.Services
	directory *fakeDirectory
	poster    *fakePoster
	owner     *quack.GuildStaffContext
	spamID    string
}

func newCaseHarness(t *testing.T, liveActorBits uint64) *caseHarness {
	t.Helper()
	return newCaseHarnessIn(t, "guild-1", liveActorBits)
}

// newCaseHarnessIn is newCaseHarness in the Discord server guildID.
func newCaseHarnessIn(t *testing.T, guildID string, liveActorBits uint64) *caseHarness {
	t.Helper()
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	directory := &fakeDirectory{guildID: guildID, actorBits: liveActorBits}
	services := quack.New(quack.Deps{Store: repository, Guilds: directory, Evidence: messageEvidence{}})
	owner, err := services.Guilds.ResolveDiscordStaffContext(context.Background(), quack.DiscordStaffContextInput{
		DiscordGuildID: guildID, DiscordUserID: "owner-1", DisplayName: "Owner",
	})
	if err != nil {
		t.Fatalf("resolve owner: %v", err)
	}
	poster := &fakePoster{}
	h := &caseHarness{
		store: repository, services: services, directory: directory, poster: poster, owner: owner,
		cases: discord.NewCases(services, poster, "https://dash.example"),
	}
	h.spamID = h.template(t, quack.TemplateInput{
		Slug: "spam", Name: "Spam", Description: "Unwanted repeated messages", ReasonTemplate: "Spam",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true, NotifyUser: true}},
	}).ID
	directory.calls.Store(0)
	return h
}

// messageEvidence serves any linked message as target-1's.
type messageEvidence struct{}

func (messageEvidence) FetchMessageEvidence(_ context.Context, ref quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	return &quack.DiscordMessageSnapshot{
		GuildID: ref.GuildID, ChannelID: ref.ChannelID, MessageID: ref.MessageID,
		AuthorDiscordUserID: "target-1", URL: ref.URL, Content: "spam", CreatedAt: time.Now().UTC(),
	}, nil
}

func (messageEvidence) PreserveEvidenceAttachment(context.Context, string, string, quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	return nil, errors.New("not preserved")
}

func (messageEvidence) EvidenceAttachmentURL(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (messageEvidence) RefreshAttachmentURL(_ context.Context, original string) (string, error) {
	return original, nil
}

func (h *caseHarness) template(t *testing.T, input quack.TemplateInput) *quack.TemplateResponse {
	t.Helper()
	created, err := h.services.Templates.Create(context.Background(), h.owner, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

// publications returns the receipts recorded for refresh.
func (h *caseHarness) publications(t *testing.T) []quack.CasePublication {
	t.Helper()
	due, err := h.services.Publications.Due(context.Background(), time.Now().Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	return due
}

// interaction returns a /case interaction from moderator mod-1, whose
// interaction payload claims permissions.
func interaction(
	kind discordgo.InteractionType, permissions uint64, options []*discordgo.ApplicationCommandInteractionDataOption,
) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction-1", AppID: "app-1", Type: kind, GuildID: "guild-1", ChannelID: "channel-1",
		Member: &discordgo.Member{
			User:        &discordgo.User{ID: "mod-1", Username: "mod", GlobalName: "Moderator"},
			Permissions: int64(permissions),
		},
		Data: discordgo.ApplicationCommandInteractionData{Name: "case", Options: options},
	}}
}

// component returns a button or select interaction for customID.
func component(customID string, values ...string) *discordgo.InteractionCreate {
	i := interaction(discordgo.InteractionMessageComponent, 0, nil)
	i.Data = discordgo.MessageComponentInteractionData{CustomID: customID, Values: values}
	return i
}

// subcommand wraps options in the named /case subcommand.
func subcommand(name string, options ...*discordgo.ApplicationCommandInteractionDataOption) []*discordgo.ApplicationCommandInteractionDataOption {
	return []*discordgo.ApplicationCommandInteractionDataOption{{
		Name:    name,
		Type:    discordgo.ApplicationCommandOptionSubCommand,
		Options: options,
	}}
}

func caseAdd(templateID, targetID string, permissions uint64) *discordgo.InteractionCreate {
	return interaction(discordgo.InteractionApplicationCommand, permissions, subcommand("add",
		&discordgo.ApplicationCommandInteractionDataOption{
			Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: templateID,
		},
		&discordgo.ApplicationCommandInteractionDataOption{
			Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: targetID,
		},
	))
}

func templateAutocomplete(query string) *discordgo.InteractionCreate {
	permissions := uint64(discordgo.PermissionModerateMembers)
	return interaction(discordgo.InteractionApplicationCommandAutocomplete, permissions, subcommand("add",
		&discordgo.ApplicationCommandInteractionDataOption{
			Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: query, Focused: true,
		},
	))
}

// userMenu returns an "Add case for member" interaction on targetID.
func userMenu(targetID string) *discordgo.InteractionCreate {
	i := interaction(discordgo.InteractionApplicationCommand, 0, nil)
	i.ID = "user-menu"
	i.Data = discordgo.ApplicationCommandInteractionData{
		Name: discord.UserCaseCommandName, CommandType: discordgo.UserApplicationCommand, TargetID: targetID,
		Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{targetID: {ID: targetID}}},
	}
	return i
}

// messageMenu returns an "Add case" interaction on a message by target-1.
func messageMenu() *discordgo.InteractionCreate {
	i := interaction(discordgo.InteractionApplicationCommand, 0, nil)
	i.ID = "message-menu"
	i.Data = discordgo.ApplicationCommandInteractionData{
		Name: discord.MessageCaseCommandName, TargetID: "message-1",
		Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Messages: map[string]*discordgo.Message{
			"message-1": {ID: "message-1", ChannelID: "channel-1", Author: &discordgo.User{ID: "target-1"}},
		}},
	}
	return i
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
	return &discordgo.Message{ID: "message-1", ChannelID: "channel-1"}, nil
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

// run runs result's task and returns what it sent.
func run(t *testing.T, result discord.Result) *fakeResponder {
	t.Helper()
	if result.Task == nil {
		t.Fatalf("expected a deferred task, got %+v", result.Response)
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatalf("run deferred task: %v", err)
	}
	return responder
}

func TestCaseAddCreatesCaseAndResolvesStaffOnce(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	ctx := context.Background()
	result := h.cases.Command(ctx, caseAdd(h.spamID, "target-1", uint64(discordgo.PermissionModerateMembers)))
	if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource ||
		result.Response.Data != nil || result.Task == nil {
		t.Fatalf("expected public deferred acknowledgement, got %+v", result)
	}
	if calls := h.directory.calls.Load(); calls != 1 {
		t.Fatalf("staff context resolved %d times before the task, want 1", calls)
	}
	responder := run(t, result)
	// The task reuses the resolved context; the only extra call is the
	// case preflight's deliberate re-check.
	if calls := h.directory.calls.Load(); calls != 2 {
		t.Fatalf("Discord asked %d times for one /case add, want 2", calls)
	}
	if responder.deleted || responder.editCount != 1 || len(*responder.edit.Embeds) != 0 || responder.followup.Content != "" {
		t.Fatalf("expected the public placeholder to become the result: %+v", responder)
	}
	for _, want := range []string{"{{quack:warn}} Case #1 · <@target-1> · **Spam**", "Warning recorded.", "Moderator: <@mod-1>", "-# Case #1"} {
		if !strings.Contains(*responder.edit.Content, want) {
			t.Fatalf("missing %q in %q", want, *responder.edit.Content)
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
	publications := h.publications(t)
	if len(publications) != 1 || publications[0].MessageID != "message-1" || publications[0].ChannelID != "channel-1" ||
		publications[0].CaseID != created[0].ID {
		t.Fatalf("receipt not recorded for refresh: %+v", publications)
	}
}

// TestCaseAddActsImmediatelyWithOptionalContext checks that optional
// context never delays a case: staff add it afterwards with Edit context.
func TestCaseAddActsImmediatelyWithOptionalContext(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	template := h.template(t, quack.TemplateInput{
		Slug: "abuse", Name: "Abuse", ReasonTemplate: "Abusive behavior",
		ContextFields: []quack.TemplateContextFieldInput{{Key: "details", Label: "What happened?", FieldType: quack.ContextFieldLongText, Position: 1}},
		Levels:        []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	result := h.cases.Command(context.Background(), caseAdd(template.ID, "target-2", 0))
	if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("case creation waited for optional context: %+v", result.Response)
	}
	responder := run(t, result)
	for _, want := range []string{"Case #1", "<@target-2>", "**Abuse**", "case:edit_context:v1:"} {
		if !strings.Contains(*responder.edit.Content+fmt.Sprint(*responder.edit.Components), want) {
			t.Fatalf("missing %q: %s", want, *responder.edit.Content)
		}
	}
}

// TestMessageTemplateExplainsCreateFailure checks that the rule picker
// answers a rejected case with the same specific message as /case add,
// rather than failing the task with a generic error.
func TestMessageTemplateExplainsCreateFailure(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	// The moderator cannot open a case against themselves.
	result := h.cases.MessageTemplate(context.Background(), component("case:message_template:v1:mod-1|channel-1|message-1", h.spamID))
	if result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("selection was not deferred: %+v", result.Response)
	}
	responder := run(t, result)
	if !responder.edit.PrivateError || !strings.Contains(*responder.edit.Content, "No case was created.") || len(h.poster.sent) != 0 {
		t.Fatalf("want the case error privately, got %+v", responder)
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
		if result.Task != nil || !strings.Contains(response.Data.Content, "No case was created.") ||
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
		ContextFields: []quack.TemplateContextFieldInput{{
			Key: "details", Label: "What happened?", FieldType: quack.ContextFieldLongText, Position: 1, Required: true,
		}},
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	result := h.cases.Command(context.Background(), caseAdd(template.ID, "target-2", uint64(discordgo.PermissionModerateMembers)))
	if result.Response.Type != discordgo.InteractionResponseModal || len(result.Response.Data.Components) != 1 {
		t.Fatalf("expected required context modal, got %+v", result.Response)
	}
	submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
	submit.ID = "modal-interaction-2"
	submit.Data = discordgo.ModalSubmitInteractionData{CustomID: result.Response.Data.CustomID, Components: []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.TextInput{CustomID: "context_details", Value: "Repeated abusive replies"}}},
	}}
	modal := h.cases.ContextModal(context.Background(), submit)
	if modal.Response.Data != nil {
		t.Fatalf("expected public acknowledgement, got %+v", modal)
	}
	responder := run(t, modal)
	if responder.deleted || responder.editCount != 1 {
		t.Fatalf("expected public result, got %+v", responder)
	}
	// The result quotes the staff context, as the owner's layout does, but
	// never evidence.
	for _, want := range []string{"<@target-2>", "**Abuse**", "Moderator: <@mod-1>", "> What happened? — Repeated abusive replies"} {
		if !strings.Contains(*responder.edit.Content, want) {
			t.Fatalf("missing %q: %q", want, *responder.edit.Content)
		}
	}
	if strings.Contains(*responder.edit.Content, "View message") {
		t.Fatal("public result leaked evidence")
	}
}

// TestCaseInteractionsMatchGolden replays the /case flows and compares
// every response with testdata/commands.golden.json, so modal fields,
// custom IDs, select menus, and copy cannot drift unnoticed.
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

	out["void_button"] = c.VoidButton(ctx, component("case:void:v1:case-1")).Response
	out["reverse_button"] = c.ReverseButton(ctx, component("case:reverse:v1:case-1|exec-1|unban_user")).Response

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
		input := discordgo.TextInput{CustomID: fmt.Sprintf("context_field_%d", index), Value: value}
		page = append(page, discordgo.ActionsRow{Components: []discordgo.MessageComponent{input}})
	}
	submit := interaction(discordgo.InteractionModalSubmit, 0, nil)
	submit.ID = "modal-many-1"
	submit.Data = discordgo.ModalSubmitInteractionData{CustomID: first.Response.Data.CustomID, Components: page}
	out["context_continue"] = c.ContextModal(ctx, submit).Response

	next := component("case:context_next:v1:interaction-many")
	next.ID = "component-many"
	out["context_modal_next"] = c.ContextNext(ctx, next).Response

	empty := interaction(discordgo.InteractionModalSubmit, 0, nil)
	empty.Data = discordgo.ModalSubmitInteractionData{CustomID: first.Response.Data.CustomID}
	out["context_missing_required"] = c.ContextModal(ctx, empty).Response

	message := c.MessageCommand(ctx, messageMenu())
	out["message_menu_ack"] = message.Response
	out["message_menu_picker"] = run(t, message).edit
	out["user_menu_picker"] = run(t, c.UserCommand(ctx, userMenu("target-2"))).edit
	out["edit_context_modal_missing_case"] = c.Component("edit_context")(ctx, component("case:edit_context:v1:missing")).Response
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
	if responder := run(t, c.ContextModal(ctx, final)); responder.editCount != 1 || responder.followup.Content != "" {
		t.Fatalf("completed form did not publish: responder=%+v", responder)
	}
}

// TestRepeatedReverseCommandReportsCompletedRemoval checks the command's
// receipt when idempotency returns the already-completed reversal.
func TestRepeatedReverseCommandReportsCompletedRemoval(t *testing.T) {
	h := newCaseHarness(t, uint64(discordgo.PermissionModerateMembers))
	ctx := context.Background()
	template := h.template(t, quack.TemplateInput{
		Slug: "receipt-timeout", Name: "Timeout", ReasonTemplate: "Repeated spam",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true,
			Actions: []quack.TemplateActionInput{{ActionType: quack.ActionTimeoutUser, TimeoutDurationSeconds: 60}}}},
	})
	created, err := h.services.Cases.Create(ctx, h.owner, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := h.store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil || len(actions) != 1 {
		t.Fatalf("actions = %+v, %v", actions, err)
	}
	if err := h.store.DB().Model(&quack.CaseActionExecution{}).Where("id = ?", actions[0].ID).Update("status", quack.ActionExecutionSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	command := func() *discordgo.InteractionCreate {
		return interaction(discordgo.InteractionApplicationCommand, 0, subcommand("reverse",
			&discordgo.ApplicationCommandInteractionDataOption{Name: "case", Type: discordgo.ApplicationCommandOptionString, Value: created.ID},
			&discordgo.ApplicationCommandInteractionDataOption{Name: "execution", Type: discordgo.ApplicationCommandOptionString, Value: actions[0].ID},
			&discordgo.ApplicationCommandInteractionDataOption{Name: "action", Type: discordgo.ApplicationCommandOptionString, Value: string(quack.ActionRemoveTimeout)},
			&discordgo.ApplicationCommandInteractionDataOption{Name: "confirm", Type: discordgo.ApplicationCommandOptionBoolean, Value: true},
		))
	}
	first := run(t, h.cases.Command(ctx, command()))
	if !strings.Contains(*first.edit.Content, "Remove timeout queued.") {
		t.Fatalf("first receipt = %s", *first.edit.Content)
	}
	queued, err := h.store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil || len(queued) != 2 {
		t.Fatalf("queued = %+v, %v", queued, err)
	}
	for _, execution := range queued {
		if execution.ReversalOfExecutionID != nil {
			if err := h.store.DB().Model(&quack.CaseActionExecution{}).Where("id = ?", execution.ID).Update("status", quack.ActionExecutionSucceeded).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	second := run(t, h.cases.Command(ctx, command()))
	if !strings.Contains(*second.edit.Content, "Member is no longer timed out.") || strings.Contains(*second.edit.Content, "queued") {
		t.Fatalf("repeated receipt = %s", *second.edit.Content)
	}
	final, err := h.store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil || len(final) != 2 {
		t.Fatalf("repeat created another reversal: %+v, %v", final, err)
	}
}
