package interactions

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// CommandLookup groups the command lookup state used to keep this package's responsibilities explicit.
type CommandLookup interface {
	LookupCommand(name string) (ui.Handler, bool)
}

// Client defines the external operations needed by this package, keeping the concrete client at the adapter boundary.
type Client interface {
	InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse) error
	InteractionResponseEdit(*discordgo.Interaction, *discordgo.WebhookEdit) (*discordgo.Message, error)
	FollowupMessageCreate(*discordgo.Interaction, bool, *discordgo.WebhookParams) (*discordgo.Message, error)
	FollowupMessageEdit(*discordgo.Interaction, string, *discordgo.WebhookEdit) (*discordgo.Message, error)
	InteractionResponseDelete(*discordgo.Interaction) error
}

// Dispatcher routes Discord interactions to registered handlers and applies the required response lifecycle.
type Dispatcher struct {
	Services   *quack.Services
	Commands   CommandLookup
	Components *ComponentRegistry
	Client     Client
	Deduper    *InteractionDeduper
	dedupeMu   sync.Mutex
}

// NewDispatcher constructs dispatcher with required dependencies explicit so callers control lifecycle and substitution.
func NewDispatcher(services *quack.Services, commands CommandLookup) *Dispatcher {
	return &Dispatcher{
		Services:   services,
		Commands:   commands,
		Components: NewComponentRegistry(),
		Deduper:    NewInteractionDeduper(15*time.Minute, 10000),
	}
}

// Handle handles handle and translates it into the package's application or response contract.
func (d *Dispatcher) Handle(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if interaction == nil || interaction.Interaction == nil {
		return
	}
	if d.Client == nil {
		d.Client = sessionClient{session: session}
	}

	switch interaction.Type {
	case discordgo.InteractionApplicationCommand, discordgo.InteractionApplicationCommandAutocomplete:
		d.handleCommand(session, interaction)
	case discordgo.InteractionMessageComponent:
		d.handleComponent(session, interaction)
	case discordgo.InteractionModalSubmit:
		d.handleModal(session, interaction)
	}
}

// handleCommand handles command and translates it into the package's application or response contract.
func (d *Dispatcher) handleCommand(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if d.Commands == nil {
		return
	}
	data := interaction.ApplicationCommandData()
	handler, ok := d.Commands.LookupCommand(data.Name)
	if !ok {
		return
	}
	d.execute(session, interaction, data.Name, handler)
}

// handleComponent handles component and translates it into the package's application or response contract.
func (d *Dispatcher) handleComponent(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if d.Components == nil {
		_ = d.respond(interaction, ui.Error("That component is not available."))
		return
	}
	data := interaction.MessageComponentData()
	handler, ok, err := d.Components.LookupComponent(data.CustomID)
	if err != nil || !ok {
		_ = d.respond(interaction, ui.Error("That component is not available."))
		return
	}
	d.execute(session, interaction, "component:"+data.CustomID, handler)
}

// handleModal handles modal and translates it into the package's application or response contract.
func (d *Dispatcher) handleModal(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	if d.Components == nil {
		_ = d.respond(interaction, ui.Error("That modal is not available."))
		return
	}
	data := interaction.ModalSubmitData()
	handler, ok, err := d.Components.LookupModal(data.CustomID)
	if err != nil || !ok {
		_ = d.respond(interaction, ui.Error("That modal is not available."))
		return
	}
	d.execute(session, interaction, "modal:"+data.CustomID, handler)
}

// execute processes execute according to persisted state and retry policy.
func (d *Dispatcher) execute(session *discordgo.Session, interaction *discordgo.InteractionCreate, name string, handler ui.Handler) {
	started := time.Now()
	if interaction != nil && !d.interactionDeduper().Claim(interaction.ID) {
		return
	}
	ctx := quack.ContextWithAuditSource(interactionTraceContext(interaction), quack.AuditSourceDiscord)
	result := d.safeHandle(ctx, session, interaction, name, handler)
	if result.Response == nil {
		return
	}
	if err := d.respond(interaction, result.Response); err != nil {
		attrs := discordErrorAttrs(err)
		attrs = append(attrs, "interaction", name, "interaction_type", int(interaction.Type), "response_type", int(result.Response.Type), "elapsed_ms", time.Since(started).Milliseconds())
		if created, timestampErr := discordgo.SnowflakeTimestamp(interaction.ID); timestampErr == nil {
			attrs = append(attrs, "interaction_age_ms", time.Since(created).Milliseconds())
		}
		slog.ErrorContext(ctx, "failed to respond to Discord interaction", attrs...)
		return
	}
	if result.Task == nil {
		return
	}

	go d.runTask(ctx, interaction, name, result.Task, result.Response.Type)
}

func (d *Dispatcher) interactionDeduper() *InteractionDeduper {
	d.dedupeMu.Lock()
	defer d.dedupeMu.Unlock()
	if d.Deduper == nil {
		d.Deduper = NewInteractionDeduper(15*time.Minute, 10000)
	}
	return d.Deduper
}

// safeHandle encapsulates the safe handle rule so callers share one consistent package implementation.
func (d *Dispatcher) safeHandle(ctx context.Context, session *discordgo.Session, interaction *discordgo.InteractionCreate, name string, handler ui.Handler) (result ui.HandlerResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("Discord interaction handler panicked", "interaction", name, "request_id", quack.RequestIDFromContext(ctx), "correlation_id", quack.CorrelationIDFromContext(ctx), "panic_type", fmt.Sprintf("%T", recovered), "stack", debug.Stack())
			result = ui.Immediate(ui.Error("Quack could not handle that interaction."))
		}
	}()
	return handler(ui.Context{
		Context:     ctx,
		Services:    d.Services,
		Session:     session,
		Interaction: interaction,
	})
}

// runTask encapsulates the run task rule so callers share one consistent package implementation.
func (d *Dispatcher) runTask(ctx context.Context, interaction *discordgo.InteractionCreate, name string, task ui.Task, responseType discordgo.InteractionResponseType) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("Discord interaction task panicked", "interaction", name, "request_id", quack.RequestIDFromContext(ctx), "correlation_id", quack.CorrelationIDFromContext(ctx), "panic_type", fmt.Sprintf("%T", recovered), "stack", debug.Stack())
			d.taskError(interaction, responseType)
		}
	}()
	if err := task(ctx, d.responder(interaction)); err != nil {
		slog.Error("Discord interaction task failed", "error_type", fmt.Sprintf("%T", err), "interaction", name, "request_id", quack.RequestIDFromContext(ctx), "correlation_id", quack.CorrelationIDFromContext(ctx))
		d.taskError(interaction, responseType)
	}
}

// interactionTraceContext encapsulates the interaction trace context rule so callers share one consistent package implementation.
func interactionTraceContext(interaction *discordgo.InteractionCreate) context.Context {
	requestID := quack.NewTraceID()
	correlationID := requestID
	if interaction != nil && interaction.ID != "" {
		requestID = "discord:" + interaction.ID
		correlationID = requestID
	}
	return quack.ContextWithTrace(context.Background(), requestID, correlationID)
}

// respond encapsulates the respond rule so callers share one consistent package implementation.
func (d *Dispatcher) respond(interaction *discordgo.InteractionCreate, response *discordgo.InteractionResponse) error {
	if d.Client == nil {
		return fmt.Errorf("discord interaction client is not configured")
	}
	return d.Client.InteractionRespond(interaction.Interaction, response)
}

// responder encapsulates the responder rule so callers share one consistent package implementation.
func (d *Dispatcher) responder(interaction *discordgo.InteractionCreate) ui.Responder {
	return responder{client: d.Client, interaction: interaction.Interaction}
}

// responder groups the responder state used to keep this package's responsibilities explicit.
type responder struct {
	client      Client
	interaction *discordgo.Interaction
}

// EditOriginal encapsulates the edit original rule so callers share one consistent package implementation.
func (r responder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	return r.client.InteractionResponseEdit(r.interaction, edit.WebhookEdit())
}

// Followup encapsulates the followup rule so callers share one consistent package implementation.
func (r responder) Followup(message ui.Message) (*discordgo.Message, error) {
	return r.client.FollowupMessageCreate(r.interaction, true, message.WebhookParams())
}

// EditFollowup updates a previously published public result after asynchronous work reaches a terminal state.
func (r responder) EditFollowup(messageID string, edit ui.Edit) (*discordgo.Message, error) {
	return r.client.FollowupMessageEdit(r.interaction, messageID, edit.WebhookEdit())
}

// DeleteOriginal encapsulates the delete original rule so callers share one consistent package implementation.
func (r responder) DeleteOriginal() error {
	return r.client.InteractionResponseDelete(r.interaction)
}

// UpdateMessage updates message while retaining validation, compatibility, and audit requirements.
func (r responder) UpdateMessage(edit ui.Edit) (*discordgo.Message, error) {
	return r.EditOriginal(edit)
}

// sessionClient defines the external operations needed by this package, keeping the concrete client at the adapter boundary.
type sessionClient struct {
	session *discordgo.Session
}

// InteractionRespond encapsulates the interaction respond rule so callers share one consistent package implementation.
func (c sessionClient) InteractionRespond(interaction *discordgo.Interaction, response *discordgo.InteractionResponse) error {
	return c.session.InteractionRespond(interaction, response)
}

// InteractionResponseEdit encapsulates the interaction response edit rule so callers share one consistent package implementation.
func (c sessionClient) InteractionResponseEdit(interaction *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
	return c.session.InteractionResponseEdit(interaction, edit)
}

// FollowupMessageCreate encapsulates the followup message create rule so callers share one consistent package implementation.
func (c sessionClient) FollowupMessageCreate(interaction *discordgo.Interaction, wait bool, params *discordgo.WebhookParams) (*discordgo.Message, error) {
	return c.session.FollowupMessageCreate(interaction, wait, params)
}

// FollowupMessageEdit updates an interaction followup through the application webhook token.
func (c sessionClient) FollowupMessageEdit(interaction *discordgo.Interaction, messageID string, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
	return c.session.WebhookMessageEdit(interaction.AppID, interaction.Token, messageID, edit)
}

// InteractionResponseDelete encapsulates the interaction response delete rule so callers share one consistent package implementation.
func (c sessionClient) InteractionResponseDelete(interaction *discordgo.Interaction) error {
	return c.session.InteractionResponseDelete(interaction)
}

// Key encapsulates the key rule so callers share one consistent package implementation.
func Key(namespace, action string) string {
	return strings.TrimSpace(namespace) + ":" + strings.TrimSpace(action)
}

// taskError preserves a shared component message when an action fails, reporting
// the error only to the person who clicked it. Private defers remain private.
func (d *Dispatcher) taskError(interaction *discordgo.InteractionCreate, responseType discordgo.InteractionResponseType) {
	const message = "Quack could not finish that interaction."
	responder := d.responder(interaction)
	if responseType == discordgo.InteractionResponseDeferredMessageUpdate {
		_, _ = responder.Followup(ui.EmbedMessage(ui.ErrorEmbed(message), true))
		return
	}
	_, _ = responder.EditOriginal(ui.ErrorEdit(message))
}

// discordErrorAttrs retains Discord's numeric rejection details without logging
// request URLs, interaction tokens, response bodies, or submitted case content.
func discordErrorAttrs(err error) []any {
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
