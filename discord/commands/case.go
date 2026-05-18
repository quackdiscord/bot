package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

const caseCommandName = "case"

func init() {
	registerCommand(CommandSpec{
		Definition: CaseCommandDefinition(),
		Handler:    HandleCaseInteraction,
	})
}

func HandleCaseInteraction(ctx context.Context, services *app.Services, session *discordgo.Session, interaction *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	if interaction == nil || interaction.Interaction == nil {
		return nil
	}
	if interaction.Type == discordgo.InteractionApplicationCommandAutocomplete {
		return handleTemplateAutocomplete(ctx, services, interaction)
	}

	data := interaction.ApplicationCommandData()
	add := data.GetOption("add")
	if add == nil {
		return ephemeralResponse("Use `/case add` to create a case from a template.")
	}

	created, err := createCaseFromInteraction(ctx, services, interaction, add)
	if err != nil {
		return ephemeralResponse(caseCommandErrorMessage(err))
	}

	return ephemeralResponse(fmt.Sprintf(
		"Created case #%d for <@%s>. %d action(s) queued.",
		created.CaseNumber,
		created.TargetDiscordUserID,
		len(created.Actions),
	))
}

func CaseCommandDefinition() *discordgo.ApplicationCommand {
	defaultPermissions := int64(discordgo.PermissionModerateMembers)
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     caseCommandName,
		Description:              "Create and manage moderation cases.",
		DefaultMemberPermissions: &defaultPermissions,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Create a moderation case from a template.",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "template",
						Description:  "Case template to apply.",
						Required:     true,
						Autocomplete: true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "User to moderate.",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "reason",
						Description: "Optional reason override.",
						Required:    false,
					},
				},
			},
		},
	}
}

func createCaseFromInteraction(ctx context.Context, services *app.Services, interaction *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) (*app.CaseResponse, error) {
	if services == nil || services.Guilds == nil || services.Cases == nil {
		return nil, errors.New("case command services are not configured")
	}
	if interaction.GuildID == "" {
		return nil, errors.New("case commands must be used in a server")
	}

	templateOption := add.GetOption("template")
	userOption := add.GetOption("user")
	if templateOption == nil || userOption == nil {
		return nil, app.ErrCaseValidation
	}

	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil {
		return nil, err
	}
	if !guildContext.Can(structs.PermissionActionCaseCreate) {
		return nil, app.ErrCasePermissionDenied
	}

	templateID, err := resolveTemplateID(ctx, services, guildContext, templateOption.StringValue())
	if err != nil {
		return nil, err
	}

	reason := ""
	if reasonOption := add.GetOption("reason"); reasonOption != nil {
		reason = reasonOption.StringValue()
	}

	return services.Cases.Create(ctx, guildContext, app.CaseInput{
		TemplateID:              templateID,
		TargetDiscordUserID:     optionStringValue(userOption),
		ReasonOverride:          reason,
		Source:                  structs.CaseSourceDiscordCommand,
		ContextChannelDiscordID: interaction.ChannelID,
	})
}

func resolveInteractionGuildContext(ctx context.Context, services *app.Services, interaction *discordgo.InteractionCreate) (*app.GuildStaffContext, error) {
	userID, displayName, permissionBits := interactionMemberFields(interaction)
	return services.Guilds.ResolveDiscordStaffContext(ctx, app.DiscordStaffContextInput{
		DiscordGuildID: interaction.GuildID,
		DiscordUserID:  userID,
		DisplayName:    displayName,
		PermissionBits: permissionBits,
		LastActiveAt:   time.Now().UTC(),
	})
}

func resolveTemplateID(ctx context.Context, services *app.Services, guildContext *app.GuildStaffContext, templateInput string) (string, error) {
	value := strings.TrimSpace(templateInput)
	if value == "" {
		return "", app.ErrCaseValidation
	}

	templates, err := services.Templates.List(ctx, guildContext)
	if err != nil {
		return "", err
	}
	for _, template := range templates {
		if template.ID == value || strings.EqualFold(template.Slug, value) {
			if !template.Enabled {
				return "", app.ErrCaseTemplateNotAvailable
			}
			return template.ID, nil
		}
	}

	return value, nil
}

func handleTemplateAutocomplete(ctx context.Context, services *app.Services, interaction *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil || !guildContext.Can(structs.PermissionActionCaseCreate) {
		return autocompleteResponse(nil)
	}

	data := interaction.ApplicationCommandData()
	add := data.GetOption("add")
	if add == nil {
		return autocompleteResponse(nil)
	}
	templateOption := add.GetOption("template")
	if templateOption == nil {
		return autocompleteResponse(nil)
	}

	query := strings.ToLower(strings.TrimSpace(templateOption.StringValue()))
	templates, err := services.Templates.List(ctx, guildContext)
	if err != nil {
		log.Error().Err(err).Msg("failed to list templates for case autocomplete")
		return autocompleteResponse(nil)
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, 25)
	for _, template := range templates {
		if !template.Enabled {
			continue
		}
		requiredBits, _ := strconv.ParseUint(template.RequiredPermissionBits, 10, 64)
		if requiredBits != 0 && guildContext.PermissionBits&requiredBits != requiredBits && !guildContext.IsOwner && !guildContext.IsAdministrator {
			continue
		}
		search := strings.ToLower(template.Slug + " " + template.Name)
		if query != "" && !strings.Contains(search, query) {
			continue
		}
		name := template.Slug
		if template.Name != "" && template.Name != template.Slug {
			name = fmt.Sprintf("%s - %s", template.Slug, template.Name)
		}
		if len(name) > 100 {
			name = name[:100]
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  name,
			Value: template.ID,
		})
		if len(choices) == 25 {
			break
		}
	}

	return autocompleteResponse(choices)
}

func interactionMemberFields(interaction *discordgo.InteractionCreate) (string, string, uint64) {
	if interaction == nil || interaction.Member == nil {
		return "", "", 0
	}

	member := interaction.Member
	userID := ""
	username := ""
	if member.User != nil {
		userID = member.User.ID
		username = member.User.Username
	}

	displayName := strings.TrimSpace(member.Nick)
	if displayName == "" && member.User != nil {
		displayName = strings.TrimSpace(member.User.GlobalName)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(username)
	}

	return userID, displayName, uint64(member.Permissions)
}

func optionStringValue(option *discordgo.ApplicationCommandInteractionDataOption) string {
	if option == nil || option.Value == nil {
		return ""
	}
	switch value := option.Value.(type) {
	case string:
		return value
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func caseCommandErrorMessage(err error) string {
	switch {
	case errors.Is(err, app.ErrCasePermissionDenied):
		return "You do not have permission to create that case."
	case errors.Is(err, app.ErrCaseTemplateNotAvailable):
		return "That case template is not available."
	case errors.Is(err, app.ErrCaseValidation):
		return "That case request is invalid."
	case errors.Is(err, app.ErrBotNotInGuild):
		return "Quack is not active in this server."
	case errors.Is(err, app.ErrStaffDisabled):
		return "Your staff access is disabled in this server."
	default:
		log.Error().Err(err).Msg("case command failed")
		return "Quack could not create that case."
	}
}

func ephemeralResponse(content string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}
}

func autocompleteResponse(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	}
}
