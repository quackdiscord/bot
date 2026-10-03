package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

func handleContextModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	draft, ok := caseContextDrafts.get(parsed.Payload)
	if err != nil || !ok || !caseDraftMatchesInteraction(draft, ctx.Interaction) {
		return ui.Immediate(ui.Error("That case context form is invalid."))
	}
	fields := contextDraftPageFields(draft)
	values, evidence, valuesErr := contextValuesFromModalFields(ctx.Interaction.ModalSubmitData(), fields)
	if valuesErr != nil {
		return ui.Immediate(ui.Error(valuesErr.Error()))
	}
	for _, value := range values {
		draft.Values[value.Key] = value.Value
	}
	draft.EvidenceLinks = appendUniqueStrings(draft.EvidenceLinks, evidence...)
	draft.Page++
	if draft.Page*5 < len(draft.Template.ContextFields) {
		caseContextDrafts.save(draft)
		continueID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "context_next", Version: "v1", Payload: draft.Token})
		return ui.Immediate(ui.Ephemeral(ui.Message{Content: fmt.Sprintf("Saved context page %d. Continue to page %d.", draft.Page, draft.Page+1), Components: []discordgo.MessageComponent{ui.Row(ui.Button(continueID, "Continue context", discordgo.PrimaryButton, false))}, Ephemeral: true}))
	}
	caseContextDrafts.delete(draft.Token)
	orderedValues := make([]quack.CaseContextValueInput, 0, len(draft.Values))
	for _, field := range draft.Template.ContextFields {
		if value, exists := draft.Values[field.Key]; exists {
			orderedValues = append(orderedValues, quack.CaseContextValueInput{Key: field.Key, Value: value})
		}
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		_, template, resolveTemplateErr := resolveTemplate(taskCtx, ctx.Services, guildContext, draft.Template.ID)
		if resolveTemplateErr != nil || template == nil {
			return quack.ErrCaseTemplateNotAvailable
		}
		created, createErr := ctx.Services.Cases.Create(taskCtx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: draft.TargetDiscordUserID, Source: quack.CaseSourceDiscord, ContextChannelDiscordID: draft.ContextChannelDiscordID, ContextMessageDiscordID: draft.ContextMessageDiscordID, ContextValues: orderedValues, EvidenceLinks: draft.EvidenceLinks, IdempotencyKey: draft.Token})
		if createErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(createErr)))
			return editErr
		}
		message, followErr := ui.Publish(responder, views.CaseCreatedMessage(views.CaseCreated{Case: created, Template: template}))
		if followErr == nil && message != nil {
			updatePublicCaseResult(taskCtx, responder, ctx.Services, created, message.ID, template)
		}
		return followErr
	})
}

func handleContextNextComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	draft, ok := caseContextDrafts.get(parsed.Payload)
	if err != nil || !ok || !caseDraftMatchesInteraction(draft, ctx.Interaction) {
		return ui.Immediate(ui.Error("That case context form expired."))
	}
	modal, modalErr := contextDraftModal(draft)
	if modalErr != nil {
		return ui.Immediate(ui.Error("That case context form is unavailable."))
	}
	return ui.Immediate(modal)
}

func handleMessageTemplateComponent(ctx ui.Context) ui.HandlerResult {
	data := ctx.Interaction.MessageComponentData()
	parsed, err := ui.DecodeCustomID(data.CustomID)
	parts := strings.Split(parsed.Payload, "|")
	if err != nil || len(parts) != 3 || len(data.Values) != 1 {
		return ui.Immediate(ui.Error("That message case flow is invalid."))
	}
	guildContext, resolveErr := resolveInteractionGuildContext(ctx.Context, ctx.Services, ctx.Interaction)
	if resolveErr != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(resolveErr)))
	}
	_, template, templateErr := resolveTemplate(ctx.Context, ctx.Services, guildContext, data.Values[0])
	if templateErr != nil || template == nil {
		return ui.Immediate(ui.Error("That case template is not available."))
	}
	link := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", ctx.Interaction.GuildID, parts[1], parts[2])
	values := messageLinkContext(template, link)
	if len(values) != len(template.ContextFields) {
		modal, modalErr := contextModalWithPrefill(ctx.Interaction, template, parts[0], link, parts[1], parts[2], values)
		if modalErr != nil {
			return ui.Immediate(ui.Error("That case context form is unavailable."))
		}
		return ui.Immediate(modal)
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		created, createErr := ctx.Services.Cases.Create(taskCtx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: parts[0], Source: quack.CaseSourceDiscord, ContextChannelDiscordID: parts[1], ContextMessageDiscordID: parts[2], ContextValues: values, EvidenceLinks: []string{link}, IdempotencyKey: ctx.Interaction.ID})
		if createErr != nil {
			return createErr
		}
		message, followErr := ui.Publish(responder, views.CaseCreatedMessage(views.CaseCreated{Case: created, Template: template}))
		if followErr == nil && message != nil {
			updatePublicCaseResult(taskCtx, responder, ctx.Services, created, message.ID, template)
		}
		return followErr
	})
}

func contextValuesFromModalFields(data discordgo.ModalSubmitInteractionData, fields []quack.TemplateContextFieldResponse) ([]quack.CaseContextValueInput, []string, error) {
	values := make([]quack.CaseContextValueInput, 0, len(fields))
	evidence := []string{}
	for _, field := range fields {
		value := strings.TrimSpace(modalTextValue(data, "context_"+field.Key))
		if value == "" {
			if field.Required {
				return nil, nil, fmt.Errorf("%s is required", field.Label)
			}
			continue
		}
		var raw json.RawMessage
		switch field.FieldType {
		case quack.ContextFieldBoolean:
			parsed, err := strconv.ParseBool(strings.ToLower(value))
			if err != nil {
				return nil, nil, fmt.Errorf("%s must be true or false", field.Label)
			}
			raw, _ = json.Marshal(parsed)
		case quack.ContextFieldNumber:
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return nil, nil, fmt.Errorf("%s must be a number", field.Label)
			}
			raw = json.RawMessage(value)
		default:
			raw, _ = json.Marshal(value)
		}
		if field.FieldType == quack.ContextFieldMessageLink {
			evidence = append(evidence, value)
		}
		values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
	}
	return values, evidence, nil
}

func messageLinkContext(template *quack.TemplateResponse, link string) []quack.CaseContextValueInput {
	values := []quack.CaseContextValueInput{}
	for _, field := range template.ContextFields {
		if field.FieldType != quack.ContextFieldMessageLink {
			continue
		}
		raw, _ := json.Marshal(link)
		values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
	}
	return values
}

func modalTextValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, component := range data.Components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			if value, valueOK := component.(discordgo.ActionsRow); valueOK {
				row = &value
			} else {
				continue
			}
		}
		for _, child := range row.Components {
			input, ok := child.(*discordgo.TextInput)
			if ok && input.CustomID == customID {
				return input.Value
			}
			if input, ok := child.(discordgo.TextInput); ok && input.CustomID == customID {
				return input.Value
			}
		}
	}
	return ""
}

func contextModal(interaction *discordgo.InteractionCreate, template *quack.TemplateResponse, targetID, evidenceLink string) (*discordgo.InteractionResponse, error) {
	return contextModalWithPrefill(interaction, template, targetID, evidenceLink, interaction.ChannelID, "", nil)
}

func contextModalWithPrefill(interaction *discordgo.InteractionCreate, template *quack.TemplateResponse, targetID, evidenceLink, channelID, messageID string, prefilled []quack.CaseContextValueInput) (*discordgo.InteractionResponse, error) {
	if interaction == nil || template == nil || len(template.ContextFields) == 0 || strings.TrimSpace(interaction.ID) == "" {
		return nil, errors.New("template context is empty")
	}
	actorID, _, _ := interactionMemberFields(interaction)
	values := map[string]json.RawMessage{}
	for _, value := range prefilled {
		values[value.Key] = value.Value
	}
	draft := caseContextDraft{Token: interaction.ID, ActorDiscordUserID: actorID, GuildID: interaction.GuildID, ContextChannelDiscordID: channelID, ContextMessageDiscordID: messageID, TargetDiscordUserID: targetID, Template: *template, Values: values, EvidenceLinks: appendUniqueStrings(nil, evidenceLink), ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}
	caseContextDrafts.put(draft)
	return contextDraftModal(draft)
}

func contextDraftModal(draft caseContextDraft) (*discordgo.InteractionResponse, error) {
	customID, err := ui.EncodeCustomID(ui.CustomID{Namespace: "case", Action: "context_submit", Version: "v1", Payload: draft.Token})
	if err != nil {
		return nil, err
	}
	fields := contextDraftPageFields(draft)
	rows := make([]discordgo.MessageComponent, 0, len(fields))
	for _, field := range fields {
		style := discordgo.TextInputShort
		maxLength := 1000
		if field.FieldType == quack.ContextFieldLongText {
			style = discordgo.TextInputParagraph
			maxLength = 4000
		}
		placeholder := "Enter a value"
		if field.FieldType == quack.ContextFieldBoolean {
			placeholder = "true or false"
		}
		value := ""
		if raw, exists := draft.Values[field.Key]; exists {
			_ = json.Unmarshal(raw, &value)
		}
		rows = append(rows, ui.Row(discordgo.TextInput{CustomID: "context_" + field.Key, Label: field.Label, Style: style, Required: field.Required, MaxLength: maxLength, Placeholder: placeholder, Value: value}))
	}
	return ui.Modal(fmt.Sprintf("Case context (%d/%d)", draft.Page+1, (len(draft.Template.ContextFields)+4)/5), customID, rows), nil
}

func contextDraftPageFields(draft caseContextDraft) []quack.TemplateContextFieldResponse {
	start := draft.Page * 5
	if start >= len(draft.Template.ContextFields) {
		return nil
	}
	end := start + 5
	if end > len(draft.Template.ContextFields) {
		end = len(draft.Template.ContextFields)
	}
	return draft.Template.ContextFields[start:end]
}

func caseDraftMatchesInteraction(draft caseContextDraft, interaction *discordgo.InteractionCreate) bool {
	if interaction == nil || draft.GuildID != interaction.GuildID {
		return false
	}
	actorID, _, _ := interactionMemberFields(interaction)
	return actorID != "" && actorID == draft.ActorDiscordUserID
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		values = append(values, value)
		seen[value] = struct{}{}
	}
	return values
}
