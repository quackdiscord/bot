package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

// Handler answers one interaction. It must return within Discord's
// three-second window, so slow work goes in the Result's Task.
type Handler func(ctx context.Context, interaction *discordgo.InteractionCreate) Result

// Task runs after the interaction has been acknowledged and finishes the
// response through responder.
type Task func(ctx context.Context, responder Responder) error

// Result is how a handler answers: Response is sent at once, and Task, if
// set, runs afterwards in its own goroutine.
type Result struct {
	Response *discordgo.InteractionResponse
	Task     Task
}

// Immediate answers with response and nothing more.
func Immediate(response *discordgo.InteractionResponse) Result {
	return Result{Response: response}
}

// Async acknowledges with response, usually a defer, and then runs task.
func Async(response *discordgo.InteractionResponse, task Task) Result {
	return Result{Response: response, Task: task}
}

// Responder completes an acknowledged interaction from a Task.
type Responder interface {
	EditOriginal(Edit) (*discordgo.Message, error)
	Followup(Message) (*discordgo.Message, error)
	EditFollowup(messageID string, edit Edit) (*discordgo.Message, error)
	DeleteOriginal() error
}

// Deduper claims interaction IDs. Discord can deliver an interaction more
// than once, and a moderation interaction must not run twice.
type Deduper interface {
	Claim(ctx context.Context, interactionID string) bool
}

// RedisDeduper claims interaction IDs in Redis so a claim survives restarts
// and is shared by every process.
type RedisDeduper struct {
	client redis.UniversalClient
}

// NewRedisDeduper returns a Deduper backed by client.
func NewRedisDeduper(client redis.UniversalClient) *RedisDeduper {
	return &RedisDeduper{client: client}
}

// Claim reports whether this is the first delivery of interactionID in the
// last 15 minutes. It fails closed: when Redis is unavailable the interaction
// is dropped, since running a moderation action twice is worse than Discord
// showing the user a failure.
func (d *RedisDeduper) Claim(ctx context.Context, interactionID string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	claimed, err := d.client.SetNX(ctx, "discord:interaction:"+interactionID, "claimed", 15*time.Minute).Result()
	if err != nil {
		slog.ErrorContext(ctx, "Discord interaction claim unavailable", "error", err)
	}
	return err == nil && claimed
}

// interactionClient is the part of discordgo.Session the router uses to
// answer interactions.
type interactionClient interface {
	InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse, ...discordgo.RequestOption) error
	InteractionResponseEdit(*discordgo.Interaction, *discordgo.WebhookEdit, ...discordgo.RequestOption) (*discordgo.Message, error)
	FollowupMessageCreate(*discordgo.Interaction, bool, *discordgo.WebhookParams, ...discordgo.RequestOption) (*discordgo.Message, error)
	FollowupMessageEdit(*discordgo.Interaction, string, *discordgo.WebhookEdit, ...discordgo.RequestOption) (*discordgo.Message, error)
	InteractionResponseDelete(*discordgo.Interaction, ...discordgo.RequestOption) error
}

// Router dispatches interactions: application commands by name, and
// components and modals by the namespace and action in their custom ID. It
// drops duplicate deliveries, recovers handler panics, and runs deferred
// tasks.
type Router struct {
	client     interactionClient
	deduper    Deduper
	commands   map[string]Handler
	components map[string]Handler
	modals     map[string]Handler
}

// NewRouter returns a router for bot's interactions with the /case command,
// its components, and the appeal reversal control already installed. Modules
// add their own components with HandleComponent and HandleModal before the
// gateway opens.
func NewRouter(bot *Bot, services *quack.Services, deduper Deduper) *Router {
	r := newRouter(bot.Session, deduper)
	newCases(services).register(r)
	r.HandleComponent(appealNamespace, appealReverseAction, appealReversal(services))
	bot.Session.AddHandler(r.handle)
	return r
}

// newRouter returns a router with no routes, answering through client.
func newRouter(client interactionClient, deduper Deduper) *Router {
	return &Router{
		client:     client,
		deduper:    deduper,
		commands:   map[string]Handler{},
		components: map[string]Handler{},
		modals:     map[string]Handler{},
	}
}

// Custom ID errors.
var (
	ErrCustomIDInvalid = errors.New("custom id is invalid")
	ErrCustomIDTooLong = errors.New("custom id exceeds Discord limit")
)

// CustomID is the routing identity Quack puts in a button, select menu, or
// modal: "namespace:action:version:payload". The router dispatches on
// namespace and action; payload carries the IDs the handler needs. Custom
// IDs live on messages already posted in Discord, so existing ones must keep
// decoding to the same route.
type CustomID struct {
	Namespace string
	Action    string
	Version   string
	Payload   string
}

// EncodeCustomID formats id, rejecting missing parts, separators inside the
// routing parts, and values over Discord's 100-character limit.
func EncodeCustomID(id CustomID) (string, error) {
	namespace := strings.TrimSpace(id.Namespace)
	action := strings.TrimSpace(id.Action)
	version := strings.TrimSpace(id.Version)
	if namespace == "" || action == "" || version == "" {
		return "", ErrCustomIDInvalid
	}
	if strings.Contains(namespace, ":") || strings.Contains(action, ":") || strings.Contains(version, ":") {
		return "", ErrCustomIDInvalid
	}
	value := namespace + ":" + action + ":" + version + ":" + strings.TrimSpace(id.Payload)
	if len([]rune(value)) > customIDLimit {
		return "", ErrCustomIDTooLong
	}
	return value, nil
}

// DecodeCustomID parses a custom ID produced by EncodeCustomID.
func DecodeCustomID(value string) (CustomID, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 4)
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return CustomID{}, ErrCustomIDInvalid
	}
	if len([]rune(value)) > customIDLimit {
		return CustomID{}, ErrCustomIDTooLong
	}
	return CustomID{Namespace: parts[0], Action: parts[1], Version: parts[2], Payload: parts[3]}, nil
}

// MustCustomID is EncodeCustomID for IDs built from code, where an invalid
// ID is a programming error.
func MustCustomID(id CustomID) string {
	value, err := EncodeCustomID(id)
	if err != nil {
		panic(fmt.Sprintf("invalid custom id: %v", err))
	}
	return value
}

// HandleComponent routes buttons and select menus whose custom ID has the
// given namespace and action. Like http.ServeMux, it panics on an empty or
// duplicate route, since routes are fixed at startup.
func (r *Router) HandleComponent(namespace, action string, handler Handler) {
	addRoute(r.components, namespace, action, handler)
}

// HandleModal routes modal submissions the same way HandleComponent routes
// components.
func (r *Router) HandleModal(namespace, action string, handler Handler) {
	addRoute(r.modals, namespace, action, handler)
}

// addRoute registers handler under "namespace:action", panicking on a route
// that is empty, ambiguous, or already taken.
func addRoute(routes map[string]Handler, namespace, action string, handler Handler) {
	namespace, action = strings.TrimSpace(namespace), strings.TrimSpace(action)
	key := namespace + ":" + action
	switch {
	case handler == nil:
		panic("discord: nil handler for " + key)
	case namespace == "" || action == "" || strings.Count(key, ":") != 1:
		panic("discord: invalid route " + key)
	}
	if _, exists := routes[key]; exists {
		panic("discord: duplicate route " + key)
	}
	routes[key] = handler
}

// lookup finds the handler for a component or modal custom ID.
func lookup(routes map[string]Handler, customID string) (Handler, bool) {
	id, err := DecodeCustomID(customID)
	if err != nil {
		return nil, false
	}
	handler, ok := routes[id.Namespace+":"+id.Action]
	return handler, ok
}

// handle is the gateway handler for interaction events.
func (r *Router) handle(_ *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if interaction == nil || interaction.Interaction == nil {
		return
	}
	switch interaction.Type {
	case discordgo.InteractionApplicationCommand, discordgo.InteractionApplicationCommandAutocomplete:
		name := interaction.ApplicationCommandData().Name
		if handler, ok := r.commands[name]; ok {
			r.run(interaction, name, handler)
		}
	case discordgo.InteractionMessageComponent:
		customID := interaction.MessageComponentData().CustomID
		handler, ok := lookup(r.components, customID)
		if !ok {
			_ = r.client.InteractionRespond(interaction.Interaction, Error("That component is not available."))
			return
		}
		r.run(interaction, "component:"+customID, handler)
	case discordgo.InteractionModalSubmit:
		customID := interaction.ModalSubmitData().CustomID
		handler, ok := lookup(r.modals, customID)
		if !ok {
			_ = r.client.InteractionRespond(interaction.Interaction, Error("That modal is not available."))
			return
		}
		r.run(interaction, "modal:"+customID, handler)
	}
}

// run claims the interaction, calls the handler, sends its response, and
// starts its task. name identifies the interaction in logs.
func (r *Router) run(interaction *discordgo.InteractionCreate, name string, handler Handler) {
	started := time.Now()
	ctx := quack.ContextWithAuditSource(traceContext(interaction), quack.AuditSourceDiscord)
	if interaction.ID == "" || !r.deduper.Claim(ctx, interaction.ID) {
		return
	}
	result := r.call(ctx, interaction, name, handler)
	if result.Response == nil {
		return
	}
	if err := r.client.InteractionRespond(interaction.Interaction, result.Response); err != nil {
		attrs := append(errorAttrs(err),
			"interaction", name,
			"interaction_type", int(interaction.Type),
			"response_type", int(result.Response.Type),
			"elapsed_ms", time.Since(started).Milliseconds(),
		)
		if created, err := discordgo.SnowflakeTimestamp(interaction.ID); err == nil {
			attrs = append(attrs, "interaction_age_ms", time.Since(created).Milliseconds())
		}
		slog.ErrorContext(ctx, "failed to respond to Discord interaction", attrs...)
		return
	}
	if result.Task != nil {
		go r.runTask(ctx, interaction, name, result.Task, result.Response.Type)
	}
}

// call runs handler, turning a panic into a private error reply.
func (r *Router) call(ctx context.Context, interaction *discordgo.InteractionCreate, name string, handler Handler) (result Result) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logPanic(ctx, "Discord interaction handler panicked", name, recovered)
			result = Immediate(Error("Quack could not handle that interaction."))
		}
	}()
	return handler(ctx, interaction)
}

// runTask runs a deferred task and reports a failure or panic to the user.
func (r *Router) runTask(
	ctx context.Context, interaction *discordgo.InteractionCreate, name string,
	task Task, responseType discordgo.InteractionResponseType,
) {
	responder := responder{client: r.client, interaction: interaction.Interaction}
	defer func() {
		if recovered := recover(); recovered != nil {
			logPanic(ctx, "Discord interaction task panicked", name, recovered)
			responder.fail(responseType)
		}
	}()
	if err := task(ctx, responder); err != nil {
		slog.Error("Discord interaction task failed",
			"error_type", fmt.Sprintf("%T", err),
			"interaction", name,
			"request_id", quack.RequestIDFromContext(ctx),
			"correlation_id", quack.CorrelationIDFromContext(ctx),
		)
		responder.fail(responseType)
	}
}

// logPanic logs a recovered panic by type only, since its value may contain
// an interaction token.
func logPanic(ctx context.Context, message, name string, recovered any) {
	slog.Error(message,
		"interaction", name,
		"request_id", quack.RequestIDFromContext(ctx),
		"correlation_id", quack.CorrelationIDFromContext(ctx),
		"panic_type", fmt.Sprintf("%T", recovered),
		"stack", debug.Stack(),
	)
}

// traceContext gives each interaction a request and correlation ID derived
// from its Discord ID, so audit entries can be traced back to it.
func traceContext(interaction *discordgo.InteractionCreate) context.Context {
	id := quack.NewTraceID()
	if interaction.ID != "" {
		id = "discord:" + interaction.ID
	}
	return quack.ContextWithTrace(context.Background(), id, id)
}

// errorAttrs keeps Discord's status and error code but never the request
// URL, interaction token, or response body, which can echo case content.
func errorAttrs(err error) []any {
	attrs := []any{"error_type", fmt.Sprintf("%T", err)}
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) {
		if restErr.Response != nil {
			attrs = append(attrs, "http_status", restErr.Response.StatusCode)
		}
		if restErr.Message != nil {
			attrs = append(attrs, "discord_code", restErr.Message.Code)
		}
	}
	return attrs
}

// responder implements Responder for one interaction.
type responder struct {
	client      interactionClient
	interaction *discordgo.Interaction
}

// EditOriginal edits the interaction's first response.
func (r responder) EditOriginal(edit Edit) (*discordgo.Message, error) {
	return r.client.InteractionResponseEdit(r.interaction, edit.webhookEdit())
}

// Followup posts another message through the interaction's webhook,
// waiting for Discord to return it.
func (r responder) Followup(message Message) (*discordgo.Message, error) {
	return r.client.FollowupMessageCreate(r.interaction, true, message.webhookParams())
}

// EditFollowup edits a message posted by Followup.
func (r responder) EditFollowup(messageID string, edit Edit) (*discordgo.Message, error) {
	return r.client.FollowupMessageEdit(r.interaction, messageID, edit.webhookEdit())
}

// DeleteOriginal deletes the interaction's first response.
func (r responder) DeleteOriginal() error {
	return r.client.InteractionResponseDelete(r.interaction)
}

// fail tells the user a task failed. A deferred component update gets a
// private followup so the shared message everyone sees stays intact.
func (r responder) fail(responseType discordgo.InteractionResponseType) {
	const message = "Quack could not finish that interaction."
	if responseType == discordgo.InteractionResponseDeferredMessageUpdate {
		_, _ = r.Followup(embedMessage(errorEmbed(message), true))
		return
	}
	_, _ = r.EditOriginal(ErrorEdit(message))
}
