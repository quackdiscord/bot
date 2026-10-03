package honeypot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

type caseCreatorFake struct {
	guildID string
	input   quack.CaseInput
	err     error
}

func (f *caseCreatorFake) CreateSystemHoneypot(_ context.Context, guildID string, input quack.CaseInput) (*quack.CaseResponse, error) {
	f.guildID, f.input = guildID, input
	if f.err != nil {
		return nil, f.err
	}
	return &quack.CaseResponse{ID: "case-1"}, nil
}

// coreStoreFake returns one template with a single default level running
// action, and the saved case, if any.
type coreStoreFake struct {
	action    quack.ActionType
	err       error
	saved     *quack.Case
	lookupKey string
}

func (f *coreStoreFake) GetCaseTemplateExpanded(context.Context, string, string) (*quack.ExpandedCaseTemplate, error) {
	if f.err != nil {
		return nil, f.err
	}
	level := quack.ExpandedCaseTemplateLevel{Level: quack.CaseTemplateLevel{IsDefault: true}}
	if f.action != "" {
		level.Actions = []quack.CaseTemplateLevelAction{{ActionType: f.action}}
	}
	return &quack.ExpandedCaseTemplate{Levels: []quack.ExpandedCaseTemplateLevel{level}}, nil
}

func (f *coreStoreFake) GetCaseByIdempotencyKey(_ context.Context, _, key string) (*quack.Case, error) {
	f.lookupKey = key
	return f.saved, f.err
}

// roundTripper serves a fake Discord API.
type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeSession returns a session whose REST calls go to serve.
func fakeSession(serve func(*http.Request) (int, any)) *discordgo.Session {
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "quack", Bot: true}
	session.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		status, body := serve(r)
		raw, ok := body.(string)
		if !ok {
			encoded, _ := json.Marshal(body)
			raw = string(encoded)
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(raw)),
			Request:    r,
		}, nil
	})}
	return session
}

func TestTemplateValidatorAcceptsOnlyEnforcementActions(t *testing.T) {
	for action, valid := range map[quack.ActionType]bool{
		"":                      true,
		quack.ActionTimeoutUser: true,
		quack.ActionKickUser:    true,
		quack.ActionBanUser:     true,
		quack.ActionSendDM:      false,
	} {
		err := templateValidator{store: &coreStoreFake{action: action}}.ValidateHoneypotTemplate(context.Background(), "guild", "template")
		if (err == nil) != valid {
			t.Errorf("action %q: err = %v, want valid %t", action, err, valid)
		}
		if err != nil && !errors.Is(err, ErrTemplateUnavailable) {
			t.Errorf("action %q: err = %v, want ErrTemplateUnavailable", action, err)
		}
	}
}

// A storage failure must not look like policy drift, which turns the
// honeypot off.
func TestTemplateValidatorKeepsStorageFailuresDistinct(t *testing.T) {
	storageErr := errors.New("database unavailable")
	err := templateValidator{store: &coreStoreFake{err: storageErr}}.ValidateHoneypotTemplate(context.Background(), "guild", "template")
	if !errors.Is(err, storageErr) || errors.Is(err, ErrTemplateUnavailable) {
		t.Fatalf("err = %v, want the storage error only", err)
	}
}

func TestCaseApplierKeepsTheNormalCaseEnvelope(t *testing.T) {
	creator := &caseCreatorFake{}
	applier := caseApplier{cases: creator}
	request := ApplyRequest{
		GuildID: "guild", TemplateID: "template", TargetDiscordUserID: "target",
		ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
		ContextURL: "https://discord.com/channels/1/2/3", IdempotencyKey: "honeypot:guild:message",
		Source: SourceHoneypot, ActorType: ActorTypeSystem,
	}
	result, err := applier.ApplyHoneypotCase(context.Background(), request)
	if err != nil || result.CaseID != "case-1" {
		t.Fatalf("apply = %+v, %v", result, err)
	}
	want := quack.CaseInput{
		TemplateID: "template", TargetDiscordUserID: "target", Source: quack.CaseSourceHoneypot,
		ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
		ContextURL: request.ContextURL, IdempotencyKey: request.IdempotencyKey,
	}
	if creator.guildID != "guild" || creator.input.TemplateID != want.TemplateID ||
		creator.input.TargetDiscordUserID != want.TargetDiscordUserID || creator.input.Source != want.Source ||
		creator.input.ContextChannelDiscordID != want.ContextChannelDiscordID ||
		creator.input.ContextMessageDiscordID != want.ContextMessageDiscordID ||
		creator.input.ContextURL != want.ContextURL || creator.input.IdempotencyKey != want.IdempotencyKey {
		t.Fatalf("case input = %q %+v, want %+v", creator.guildID, creator.input, want)
	}
	request.ActorDiscordUserID = "fabricated-staff"
	if _, err := applier.ApplyHoneypotCase(context.Background(), request); err == nil {
		t.Fatal("accepted a staff attribution")
	}
}

// Opening a case never deletes the bait; only the cleanup queue does, after
// the case is saved.
func TestCaseApplicationNeverDeletesTheMessage(t *testing.T) {
	session := fakeSession(func(r *http.Request) (int, any) {
		t.Fatalf("case application called Discord: %s %s", r.Method, r.URL.Path)
		return 0, nil
	})
	for _, caseErr := range []error{nil, errors.New("not saved")} {
		applier := caseApplier{cases: &caseCreatorFake{err: caseErr}, session: session}
		_, err := applier.ApplyHoneypotCase(context.Background(), ApplyRequest{
			GuildID: "guild", TemplateID: "template", TargetDiscordUserID: "target",
			ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
			ContextURL: "https://discord.com/channels/guild/channel/message", IdempotencyKey: "incident",
			Source: SourceHoneypot, ActorType: ActorTypeSystem,
		})
		if (err != nil) != (caseErr != nil) {
			t.Fatalf("err = %v, want %v", err, caseErr)
		}
	}
}

// A message or channel that is already gone counts as deleted; permission
// and server errors stay due for retry.
func TestDeleteReceipts(t *testing.T) {
	for _, scenario := range []struct {
		status, code int
		failure      bool
	}{{204, 0, false}, {404, 10008, false}, {404, 10003, false}, {403, 50013, true}, {500, 0, true}} {
		session := fakeSession(func(r *http.Request) (int, any) {
			if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/channels/channel/messages/message") {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			return scenario.status, fmt.Sprintf(`{"code":%d}`, scenario.code)
		})
		err := caseApplier{session: session}.DeleteHoneypotMessage(context.Background(), "channel", "message")
		if (err != nil) != scenario.failure {
			t.Fatalf("status %d code %d: %v", scenario.status, scenario.code, err)
		}
	}
}

func TestFindHoneypotCaseAdoptsOnlyTheIncidentsCase(t *testing.T) {
	request := ApplyRequest{GuildID: "guild", TargetDiscordUserID: "target", IdempotencyKey: "honeypot:guild:message"}
	store := &coreStoreFake{}
	applier := caseApplier{store: store}
	if result, err := applier.FindHoneypotCase(context.Background(), request); err != nil || result.CaseID != "" {
		t.Fatalf("no saved case: %+v, %v", result, err)
	}
	if store.lookupKey != request.IdempotencyKey {
		t.Fatalf("looked up %q", store.lookupKey)
	}
	store.saved = &quack.Case{ULIDModel: quack.ULIDModel{ID: "saved"}, Source: quack.CaseSourceHoneypot, TargetDiscordUserID: "target"}
	if result, err := applier.FindHoneypotCase(context.Background(), request); err != nil || result.CaseID != "saved" {
		t.Fatalf("saved case: %+v, %v", result, err)
	}
	store.saved.TargetDiscordUserID = "someone-else"
	if _, err := applier.FindHoneypotCase(context.Background(), request); err == nil {
		t.Fatal("adopted another member's case")
	}
}

// Recovery re-checks the author live: staff and Quack are exempt, ordinary
// bots are not, and a deleted source cannot become a case.
func TestPrepareRecoveryRefreshesAuthor(t *testing.T) {
	for _, scenario := range []struct {
		name, id      string
		bot           bool
		permissions   int64
		messageStatus int
		want          error
	}{
		{name: "ordinary bot", id: "member", bot: true},
		{name: "moderator bot", id: "member", bot: true, permissions: discordgo.PermissionModerateMembers, want: ErrExempt},
		{name: "human administrator", id: "member", permissions: discordgo.PermissionAdministrator, want: ErrExempt},
		{name: "Quack", id: "quack", bot: true, want: ErrExempt},
		{name: "missing source", id: "member", messageStatus: 404, want: ErrNotTrigger},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session := fakeSession(func(r *http.Request) (int, any) {
				if r.Method != http.MethodGet {
					t.Fatal("recovery preparation changed Discord")
				}
				switch path := r.URL.Path; {
				case strings.HasSuffix(path, "/channels/trap/messages/message"):
					if scenario.messageStatus != 0 {
						return scenario.messageStatus, map[string]any{"code": 10008}
					}
					return 200, &discordgo.Message{ID: "message", ChannelID: "trap", Author: &discordgo.User{ID: scenario.id, Bot: scenario.bot}}
				case strings.HasSuffix(path, "/channels/trap"):
					return 200, &discordgo.Channel{ID: "trap", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
				case strings.HasSuffix(path, "/guilds/guild"):
					return 200, &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "role", Permissions: scenario.permissions}}}
				case strings.Contains(path, "/guilds/guild/members/"):
					return 200, &discordgo.Member{User: &discordgo.User{ID: scenario.id, Bot: scenario.bot}, Roles: []string{"role"}}
				}
				t.Fatalf("unexpected lookup %s", r.URL.Path)
				return 0, nil
			})
			request := ApplyRequest{GuildID: "internal", TemplateID: "template", TargetDiscordUserID: scenario.id,
				ContextChannelDiscordID: "trap", ContextMessageDiscordID: "message"}
			prepared, err := caseApplier{session: session}.PrepareHoneypotRecovery(context.Background(), request)
			if !errors.Is(err, scenario.want) {
				t.Fatalf("err = %v, want %v", err, scenario.want)
			}
			if scenario.want == nil && prepared.ContextURL != "https://discord.com/channels/guild/trap/message" {
				t.Fatalf("missing evidence link: %+v", prepared)
			}
		})
	}
}
