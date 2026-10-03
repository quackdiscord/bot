package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// add handles /case add. It resolves and authorizes the moderator once.
// Templates with context fields and no "context" option collect their
// values through a modal; everything else creates the case in a deferred
// task.
func (c *cases) add(ctx context.Context, i *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) Result {
	if i.GuildID == "" {
		return Immediate(Error(caseErrorMessage(errNotInGuild)))
	}
	templateOption, userOption := add.GetOption("template"), add.GetOption("user")
	if templateOption == nil || userOption == nil {
		return Immediate(Error(caseErrorMessage(quack.ErrCaseValidation)))
	}
	staff, err := c.staff(ctx, i)
	if err == nil {
		err = c.services.Guilds.Authorize(ctx, staff, quack.PermissionActionCaseCreate, quack.AuditSourceDiscord)
	}
	if err != nil {
		return Immediate(Error(caseErrorMessage(err)))
	}

	contextOption, linkOption := add.GetOption("context"), add.GetOption("message_link")
	templateValue := optionString(templateOption)
	link := strings.TrimSpace(optionString(linkOption))
	var (
		templateID string
		template   *quack.TemplateResponse
		resolved   bool
	)
	if strings.TrimSpace(optionString(contextOption)) == "" {
		templateID, template, err = c.template(ctx, staff, templateValue)
		if err != nil {
			return Immediate(Error(caseErrorMessage(err)))
		}
		if template != nil && len(template.ContextFields) > 0 {
			modal, err := c.startContext(i, template, optionString(userOption), link, i.ChannelID, "", nil)
			if err != nil {
				return Immediate(Error(err.Error()))
			}
			return Immediate(modal)
		}
		resolved = true
	}

	return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
		if !resolved {
			templateID, template, err = c.template(ctx, staff, templateValue)
		}
		var created *quack.CaseResponse
		if err == nil {
			values := contextValuesFromOption(contextOption, template)
			created, err = c.services.Cases.Create(ctx, staff, quack.CaseInput{
				TemplateID:              templateID,
				TargetDiscordUserID:     optionString(userOption),
				Source:                  quack.CaseSourceDiscord,
				ContextChannelDiscordID: i.ChannelID,
				ContextValues:           withMessageLink(values, link, template),
				EvidenceLinks:           nonEmpty(link),
				IdempotencyKey:          i.ID,
			})
		}
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		return c.publish(ctx, responder, created, template)
	})
}

// messageCommand handles the "Create moderation case" message action. With
// one active template that needs at most the message link, it creates the
// case straight away; with several it offers a template picker.
func (c *cases) messageCommand(ctx context.Context, i *discordgo.InteractionCreate) Result {
	if i.GuildID == "" {
		return Immediate(Error("Select a server message to create a case."))
	}
	data := i.ApplicationCommandData()
	var message *discordgo.Message
	if data.Resolved != nil {
		message = data.Resolved.Messages[data.TargetID]
	}
	if message == nil || message.Author == nil {
		return Immediate(Error("The selected message is unavailable."))
	}
	staff, err := c.staff(ctx, i)
	if err != nil {
		return Immediate(Error(caseErrorMessage(err)))
	}
	templates, err := c.services.Templates.ListActive(ctx, staff)
	if err != nil || len(templates) == 0 {
		return Immediate(Error("No active case template is available."))
	}
	if len(templates) > 1 {
		return c.templatePicker(message, templates)
	}
	template := templates[0]
	fields := template.ContextFields
	if len(fields) > 1 || (len(fields) == 1 && fields[0].FieldType != quack.ContextFieldMessageLink) {
		return Immediate(Error("Use `/case add` to complete this template's visible context."))
	}
	link := messageLink(i.GuildID, message.ChannelID, message.ID)
	input := quack.CaseInput{
		TemplateID:              template.ID,
		TargetDiscordUserID:     message.Author.ID,
		Source:                  quack.CaseSourceDiscord,
		ContextChannelDiscordID: message.ChannelID,
		ContextMessageDiscordID: message.ID,
		ContextValues:           messageLinkValues(&template, link),
		EvidenceLinks:           []string{link},
		IdempotencyKey:          i.ID,
	}
	return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
		created, err := c.services.Cases.Create(ctx, staff, input)
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		return c.publish(ctx, responder, created, &template)
	})
}

// templatePicker asks which active template applies to message. The choice
// arrives at messageTemplate.
func (c *cases) templatePicker(message *discordgo.Message, templates []quack.TemplateResponse) Result {
	options := make([]discordgo.SelectMenuOption, 0, choiceLimit)
	for _, template := range templates[:min(len(templates), choiceLimit)] {
		options = append(options, discordgo.SelectMenuOption{Label: templateLabel(template), Value: template.ID})
	}
	payload := strings.Join([]string{message.Author.ID, message.ChannelID, message.ID}, "|")
	customID, err := EncodeCustomID(CustomID{Namespace: "case", Action: "message_template", Version: "v1", Payload: payload})
	if err != nil {
		return Immediate(Error("Use `/case add` to select a template for this message."))
	}
	minValues := 1
	menu := discordgo.SelectMenu{
		CustomID:    customID,
		Placeholder: "Choose an active case template",
		MinValues:   &minValues,
		MaxValues:   1,
		Options:     options,
	}
	return Immediate(Ephemeral(Message{
		Content:    "Choose the template that matches this message.",
		Components: []discordgo.MessageComponent{Row(menu)},
		Ephemeral:  true,
	}))
}

// messageTemplate handles the template picker. Its payload is
// "target|channel|message".
func (c *cases) messageTemplate(ctx context.Context, i *discordgo.InteractionCreate) Result {
	data := i.MessageComponentData()
	id, err := DecodeCustomID(data.CustomID)
	parts := strings.Split(id.Payload, "|")
	if err != nil || len(parts) != 3 || len(data.Values) != 1 {
		return Immediate(Error("That message case flow is invalid."))
	}
	targetID, channelID, messageID := parts[0], parts[1], parts[2]
	staff, err := c.staff(ctx, i)
	if err != nil {
		return Immediate(Error(caseErrorMessage(err)))
	}
	_, template, err := c.template(ctx, staff, data.Values[0])
	if err != nil || template == nil {
		return Immediate(Error("That case template is not available."))
	}
	link := messageLink(i.GuildID, channelID, messageID)
	values := messageLinkValues(template, link)
	if len(values) != len(template.ContextFields) {
		modal, err := c.startContext(i, template, targetID, link, channelID, messageID, values)
		if err != nil {
			return Immediate(Error("That case context form is unavailable."))
		}
		return Immediate(modal)
	}
	input := quack.CaseInput{
		TemplateID:              template.ID,
		TargetDiscordUserID:     targetID,
		Source:                  quack.CaseSourceDiscord,
		ContextChannelDiscordID: channelID,
		ContextMessageDiscordID: messageID,
		ContextValues:           values,
		EvidenceLinks:           []string{link},
		IdempotencyKey:          i.ID,
	}
	return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
		created, err := c.services.Cases.Create(ctx, staff, input)
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		return c.publish(ctx, responder, created, template)
	})
}

// publish posts the public case result and keeps it updated until the
// case's actions finish.
func (c *cases) publish(ctx context.Context, responder Responder, created *quack.CaseResponse, template *quack.TemplateResponse) error {
	message, err := Publish(responder, caseCreatedMessage(created, template))
	if err == nil && message != nil {
		c.followResult(ctx, responder, created, message.ID, template)
	}
	return err
}

// followResult polls the case's actions for up to 30 seconds and edits the
// public result once they all reach a final state. It works on a copy so the
// caller's case is never mutated.
func (c *cases) followResult(
	ctx context.Context, responder Responder, created *quack.CaseResponse, messageID string, template *quack.TemplateResponse,
) {
	if created.ID == "" || messageID == "" || len(created.Actions) == 0 {
		return
	}
	snapshot := *created
	snapshot.Actions = slices.Clone(created.Actions)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			actions, err := c.services.Cases.Actions(ctx, snapshot.ID)
			if err != nil {
				if ctx.Err() == nil {
					slog.ErrorContext(ctx, "Could not refresh public case result", "case_id", snapshot.ID, "error", err)
				}
				return
			}
			status := make(map[string]quack.ActionExecutionStatus, len(actions))
			for _, action := range actions {
				status[action.ID] = action.Status
			}
			done := true
			for i := range snapshot.Actions {
				if current, ok := status[snapshot.Actions[i].ID]; ok {
					snapshot.Actions[i].Status = current
				}
				switch snapshot.Actions[i].Status {
				case quack.ActionExecutionPending, quack.ActionExecutionRunning, quack.ActionExecutionRetrying:
					done = false
				}
			}
			if done {
				if _, err := responder.EditFollowup(messageID, EditMessage(caseCreatedMessage(&snapshot, template))); err != nil {
					slog.WarnContext(ctx, "Could not update public case result", "case_id", snapshot.ID, "error_type", "discord_response")
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// messageLink returns the jump URL of a guild message.
func messageLink(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}

// messageLinkValues fills every message-link field of template with link.
func messageLinkValues(template *quack.TemplateResponse, link string) []quack.CaseContextValueInput {
	raw, _ := json.Marshal(link)
	values := []quack.CaseContextValueInput{}
	for _, field := range template.ContextFields {
		if field.FieldType == quack.ContextFieldMessageLink {
			values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
		}
	}
	return values
}

// withMessageLink fills the template's first message-link field with link
// unless the context option already set it.
func withMessageLink(
	values []quack.CaseContextValueInput, link string, template *quack.TemplateResponse,
) []quack.CaseContextValueInput {
	if link == "" || template == nil {
		return values
	}
	for _, field := range template.ContextFields {
		if field.FieldType != quack.ContextFieldMessageLink {
			continue
		}
		if !slices.ContainsFunc(values, func(v quack.CaseContextValueInput) bool { return v.Key == field.Key }) {
			raw, _ := json.Marshal(link)
			values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
		}
		break
	}
	return values
}

// contextValuesFromOption decodes the "context" option, a JSON object of
// field values. Invalid JSON becomes an unknown key so case validation
// rejects it with the usual message.
func contextValuesFromOption(
	option *discordgo.ApplicationCommandInteractionDataOption, template *quack.TemplateResponse,
) []quack.CaseContextValueInput {
	if option == nil || template == nil {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(optionString(option)), &values) != nil {
		return []quack.CaseContextValueInput{{Key: "__invalid__", Value: json.RawMessage(`null`)}}
	}
	out := make([]quack.CaseContextValueInput, 0, len(values))
	for key, value := range values {
		out = append(out, quack.CaseContextValueInput{Key: key, Value: value})
	}
	return out
}

// nonEmpty returns value as a one-element list, or nil when it is empty.
func nonEmpty(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}
