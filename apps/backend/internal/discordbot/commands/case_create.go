package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// caseCommandCreateResult captures the outcome of case command create result for the caller.
type caseCommandCreateResult struct {
	Case     *quack.CaseResponse
	Template *quack.TemplateResponse
}

// createCaseFromInteraction creates case from interaction while preserving validation, authorization, and persistence invariants.
func createCaseFromInteraction(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) (*caseCommandCreateResult, error) {
	if services == nil || services.Guilds == nil || services.Cases == nil {
		return nil, errors.New("case command services are not configured")
	}
	if interaction.GuildID == "" {
		return nil, errors.New("case commands must be used in a server")
	}

	templateOption := add.GetOption("template")
	userOption := add.GetOption("user")
	if templateOption == nil || userOption == nil {
		return nil, quack.ErrCaseValidation
	}

	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil {
		return nil, err
	}
	if err := services.Guilds.Authorize(ctx, guildContext, quack.PermissionActionCaseCreate, quack.AuditSourceDiscord); err != nil {
		return nil, err
	}

	templateID, template, err := resolveTemplate(ctx, services, guildContext, templateOption.StringValue())
	if err != nil {
		return nil, err
	}

	contextValues := contextValuesFromOption(add.GetOption("context"), template)
	contextValues = mergeMessageLinkContext(contextValues, add.GetOption("message_link"), template)
	created, err := services.Cases.Create(ctx, guildContext, quack.CaseInput{
		TemplateID:              templateID,
		TargetDiscordUserID:     optionStringValue(userOption),
		Source:                  quack.CaseSourceDiscord,
		ContextChannelDiscordID: interaction.ChannelID,
		ContextValues:           contextValues, EvidenceLinks: evidenceLinksFromOption(add.GetOption("message_link")), IdempotencyKey: interaction.ID,
	})
	if err != nil {
		return nil, err
	}

	return &caseCommandCreateResult{Case: created, Template: template}, nil
}

// mergeMessageLinkContext binds a pasted link to the first Discord-message-link definition when not already supplied.
func mergeMessageLinkContext(values []quack.CaseContextValueInput, option *discordgo.ApplicationCommandInteractionDataOption, template *quack.TemplateResponse) []quack.CaseContextValueInput {
	if option == nil || template == nil || strings.TrimSpace(option.StringValue()) == "" {
		return values
	}
	existing := map[string]struct{}{}
	for _, value := range values {
		existing[value.Key] = struct{}{}
	}
	for _, field := range template.ContextFields {
		if field.FieldType == quack.ContextFieldMessageLink {
			if _, ok := existing[field.Key]; !ok {
				raw, _ := json.Marshal(strings.TrimSpace(option.StringValue()))
				values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
			}
			break
		}
	}
	return values
}

// contextValuesFromOption decodes visible template context without accepting a reason override.
func contextValuesFromOption(option *discordgo.ApplicationCommandInteractionDataOption, template *quack.TemplateResponse) []quack.CaseContextValueInput {
	if option == nil || template == nil {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(option.StringValue()), &values) != nil {
		return []quack.CaseContextValueInput{{Key: "__invalid__", Value: json.RawMessage(`null`)}}
	}
	out := make([]quack.CaseContextValueInput, 0, len(values))
	for key, value := range values {
		out = append(out, quack.CaseContextValueInput{Key: key, Value: value})
	}
	return out
}

// evidenceLinksFromOption forwards pasted links into the shared capture service.
func evidenceLinksFromOption(option *discordgo.ApplicationCommandInteractionDataOption) []string {
	if option == nil || strings.TrimSpace(option.StringValue()) == "" {
		return nil
	}
	return []string{strings.TrimSpace(option.StringValue())}
}
