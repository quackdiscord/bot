package discord

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

// memoryDeduper is the in-process Deduper tests use in place of Redis.
type memoryDeduper struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (d *memoryDeduper) Claim(_ context.Context, id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	if d.seen[id] {
		return false
	}
	d.seen[id] = true
	return true
}

// fakeClient records what the router sends to Discord.
type fakeClient struct {
	mu         sync.Mutex
	responses  []*discordgo.InteractionResponse
	edits      []*discordgo.WebhookEdit
	followups  []*discordgo.WebhookParams
	respondErr error
	done       chan struct{}
}

func (f *fakeClient) InteractionRespond(_ *discordgo.Interaction, response *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, response)
	return f.respondErr
}

func (f *fakeClient) InteractionResponseEdit(_ *discordgo.Interaction, edit *discordgo.WebhookEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.mu.Lock()
	f.edits = append(f.edits, edit)
	f.mu.Unlock()
	f.signal()
	return &discordgo.Message{ID: "message-1"}, nil
}

func (f *fakeClient) FollowupMessageCreate(_ *discordgo.Interaction, _ bool, params *discordgo.WebhookParams, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.mu.Lock()
	f.followups = append(f.followups, params)
	f.mu.Unlock()
	f.signal()
	return &discordgo.Message{ID: "followup-1"}, nil
}

func (f *fakeClient) FollowupMessageEdit(_ *discordgo.Interaction, messageID string, edit *discordgo.WebhookEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, edit)
	return &discordgo.Message{ID: messageID}, nil
}

func (f *fakeClient) InteractionResponseDelete(*discordgo.Interaction, ...discordgo.RequestOption) error {
	return nil
}

func (f *fakeClient) signal() {
	if f.done != nil {
		f.done <- struct{}{}
	}
}

func (f *fakeClient) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async task")
	}
}

func testRouter(client *fakeClient) *Router {
	return newRouter(client, &memoryDeduper{})
}

func commandInteraction(id, name string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: id, Type: discordgo.InteractionApplicationCommand, GuildID: "guild-1",
		Data: discordgo.ApplicationCommandInteractionData{Name: name},
	}}
}

func componentInteraction(id, customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: id, Type: discordgo.InteractionMessageComponent, GuildID: "guild-1",
		Data: discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

func modalInteraction(id, customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: id, Type: discordgo.InteractionModalSubmit, GuildID: "guild-1",
		Data: discordgo.ModalSubmitInteractionData{CustomID: customID},
	}}
}

func TestRouterSendsImmediateResponse(t *testing.T) {
	client := &fakeClient{}
	router := testRouter(client)
	router.commands["ping"] = func(context.Context, *discordgo.InteractionCreate) Result {
		return Immediate(Ephemeral(Content("pong", false)))
	}
	router.handle(nil, commandInteraction("interaction-1", "ping"))
	if len(client.responses) != 1 || client.responses[0].Data.Content != "pong" || len(client.edits) != 0 {
		t.Fatalf("unexpected responses %+v edits %+v", client.responses, client.edits)
	}
}

func TestRouterDropsDuplicateDelivery(t *testing.T) {
	client := &fakeClient{}
	router := testRouter(client)
	calls := 0
	router.commands["ping"] = func(context.Context, *discordgo.InteractionCreate) Result {
		calls++
		return Immediate(Ephemeral(Content("pong", false)))
	}
	router.handle(nil, commandInteraction("interaction-1", "ping"))
	router.handle(nil, commandInteraction("interaction-1", "ping"))
	if calls != 1 || len(client.responses) != 1 {
		t.Fatalf("duplicate delivery ran again: calls=%d responses=%d", calls, len(client.responses))
	}
}

func TestRouterDefersThenEditsWithTraceContext(t *testing.T) {
	client := &fakeClient{done: make(chan struct{}, 1)}
	router := testRouter(client)
	checkTrace := func(ctx context.Context) {
		if quack.RequestIDFromContext(ctx) != "discord:interaction-1" || quack.CorrelationIDFromContext(ctx) != "discord:interaction-1" {
			t.Errorf("missing discord trace: request=%q correlation=%q", quack.RequestIDFromContext(ctx), quack.CorrelationIDFromContext(ctx))
		}
	}
	router.commands["slow"] = func(ctx context.Context, _ *discordgo.InteractionCreate) Result {
		checkTrace(ctx)
		return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
			checkTrace(ctx)
			_, err := responder.EditOriginal(EditMessage(Content("finished", false)))
			return err
		})
	}
	router.handle(nil, commandInteraction("interaction-1", "slow"))
	client.wait(t)
	if len(client.responses) != 1 || client.responses[0].Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected one deferred response, got %+v", client.responses)
	}
	if len(client.edits) != 1 || *client.edits[0].Content != "finished" {
		t.Fatalf("expected final edit, got %+v", client.edits)
	}
}

func TestRouterTaskErrors(t *testing.T) {
	tests := []struct {
		name          string
		ack           *discordgo.InteractionResponse
		wantEdits     int
		wantFollowups int
	}{
		// A private defer is replaced by the standard error.
		{"deferred reply", DeferEphemeral(), 1, 0},
		// A shared component message is left alone; only the clicker hears.
		{"deferred update", DeferUpdate(), 0, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{done: make(chan struct{}, 1)}
			router := testRouter(client)
			router.HandleComponent("case", "list_next", func(context.Context, *discordgo.InteractionCreate) Result {
				return Async(test.ack, func(context.Context, Responder) error { return errors.New("boom") })
			})
			router.handle(nil, componentInteraction("interaction-1", "case:list_next:v1:1"))
			client.wait(t)
			if len(client.edits) != test.wantEdits || len(client.followups) != test.wantFollowups {
				t.Fatalf("edits=%d followups=%d", len(client.edits), len(client.followups))
			}
			var embeds []*discordgo.MessageEmbed
			if test.wantEdits == 1 {
				embeds = *client.edits[0].Embeds
			} else {
				embeds = client.followups[0].Embeds
				if client.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 {
					t.Fatal("component failure was not private")
				}
			}
			if len(embeds) != 1 || embeds[0].Description != "Quack could not finish that interaction." {
				t.Fatalf("expected standard error, got %+v", embeds)
			}
		})
	}
}

func TestRouterRoutesComponentsAndModals(t *testing.T) {
	client := &fakeClient{}
	router := testRouter(client)
	router.HandleComponent("case", "next", func(context.Context, *discordgo.InteractionCreate) Result {
		return Immediate(&discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage})
	})
	router.HandleModal("case", "note", func(context.Context, *discordgo.InteractionCreate) Result {
		return Immediate(Ephemeral(Content("saved", true)))
	})
	router.handle(nil, componentInteraction("interaction-1", "case:next:v1:user=1"))
	router.handle(nil, modalInteraction("interaction-2", "case:note:v1:user=1"))
	router.handle(nil, componentInteraction("interaction-3", "case:missing:v1:"))
	if len(client.responses) != 3 {
		t.Fatalf("expected three responses, got %d", len(client.responses))
	}
	if client.responses[0].Type != discordgo.InteractionResponseUpdateMessage || client.responses[1].Data.Content != "saved" {
		t.Fatalf("unexpected routed responses: %+v", client.responses)
	}
	if embeds := client.responses[2].Data.Embeds; len(embeds) != 1 || embeds[0].Description != "That component is not available." {
		t.Fatalf("unknown component was not refused: %+v", client.responses[2])
	}
}

func TestRouterRejectsDuplicateRoutes(t *testing.T) {
	router := testRouter(&fakeClient{})
	handler := func(context.Context, *discordgo.InteractionCreate) Result { return Result{} }
	router.HandleComponent("ticket", "open", handler)
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate route was accepted")
		}
	}()
	router.HandleComponent("ticket", "open", handler)
}

func TestNewRouterInstallsCoreRoutes(t *testing.T) {
	bot, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(bot, quack.New(quack.Deps{}), &memoryDeduper{})
	for _, name := range []string{caseCommandName, messageCaseCommandName} {
		if router.commands[name] == nil {
			t.Errorf("command %q not routed", name)
		}
	}
	components := []string{
		"list_prev", "list_next", "user_prev", "user_next", "failures_prev", "failures_next",
		"retry", "dismiss", "void", "reverse", "message_template", "context_next",
	}
	for _, action := range components {
		if _, ok := lookup(router.components, MustCustomID(CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "p"})); !ok {
			t.Errorf("component case:%s not routed", action)
		}
	}
	for _, action := range []string{"void_submit", "reverse_submit", "context_submit"} {
		if _, ok := lookup(router.modals, MustCustomID(CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "p"})); !ok {
			t.Errorf("modal case:%s not routed", action)
		}
	}
	if _, ok := lookup(router.components, "appeal:reverse:v1:appeal,execution,unban_user"); !ok {
		t.Error("appeal reversal not routed")
	}
}

func TestRouterLogsNeverExposeWebhookCredentials(t *testing.T) {
	const secret = "private-interaction-token"
	transportErr := &url.Error{Op: "Post", URL: "https://discord.com/api/v10/webhooks/application/" + secret, Err: errors.New("connection reset")}
	prior := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prior) })
	for _, mode := range []string{"response", "task", "panic"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			client := &fakeClient{done: make(chan struct{}, 1)}
			if mode == "response" {
				client.respondErr = transportErr
			}
			router := testRouter(client)
			router.commands["test"] = func(context.Context, *discordgo.InteractionCreate) Result {
				if mode == "response" {
					return Immediate(Ephemeral(Content("done", false)))
				}
				return Async(DeferEphemeral(), func(context.Context, Responder) error {
					if mode == "panic" {
						panic(secret)
					}
					return transportErr
				})
			}
			router.handle(nil, commandInteraction("interaction-1", "test"))
			if mode != "response" {
				client.wait(t)
			}
			if strings.Contains(output.String(), secret) || output.Len() == 0 {
				t.Fatalf("unsafe or missing operational log: %s", output.String())
			}
		})
	}
}

func TestRouterRejectionLogsIncludeSafeDiagnostics(t *testing.T) {
	const secret = "private-interaction-token"
	var output bytes.Buffer
	prior := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prior) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	client := &fakeClient{respondErr: &discordgo.RESTError{
		Response:     &http.Response{StatusCode: 404},
		Message:      &discordgo.APIErrorMessage{Code: 10062, Message: secret},
		ResponseBody: []byte(secret),
	}}
	router := testRouter(client)
	router.commands["test"] = func(context.Context, *discordgo.InteractionCreate) Result {
		return Immediate(DeferEphemeral())
	}
	router.handle(nil, commandInteraction("interaction-1", "test"))
	logged := output.String()
	for _, want := range []string{`"http_status":404`, `"discord_code":10062`, `"interaction_type":2`, `"response_type":5`, `"elapsed_ms":`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("missing %s in %s", want, logged)
		}
	}
	if strings.Contains(logged, secret) {
		t.Fatalf("logged response secrets: %s", logged)
	}
}

func TestRedisDeduperClaimsOnceSurvivesRestartAndFailsClosed(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	first := NewRedisDeduper(client)
	var claimed atomic.Int64
	var wait sync.WaitGroup
	for range 50 {
		wait.Go(func() {
			if first.Claim(ctx, "interaction-1") {
				claimed.Add(1)
			}
		})
	}
	wait.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("expected exactly one claim, got %d", claimed.Load())
	}
	if NewRedisDeduper(client).Claim(ctx, "interaction-1") {
		t.Fatal("restarted process did not see the earlier claim")
	}
	server.Close()
	if first.Claim(ctx, "interaction-2") {
		t.Fatal("unavailable Redis did not fail closed")
	}
}
