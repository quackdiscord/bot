package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// resolveInteractionGuildContext resolves interaction guild context from authoritative request and repository data.
func resolveInteractionGuildContext(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate) (*quack.GuildStaffContext, error) {
	userID, displayName, permissionBits := interactionMemberFields(interaction)
	return services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: interaction.GuildID,
		DiscordUserID:  userID,
		DisplayName:    displayName,
		PermissionBits: permissionBits,
		LastActiveAt:   time.Now().UTC(),
	})
}

// resolveTemplate resolves template from authoritative request and repository data.
func resolveTemplate(ctx context.Context, services *quack.Services, guildContext *quack.GuildStaffContext, templateInput string) (string, *quack.TemplateResponse, error) {
	value := strings.TrimSpace(templateInput)
	if value == "" {
		return "", nil, quack.ErrCaseValidation
	}

	templates, err := services.Templates.ListActive(ctx, guildContext)
	if err != nil {
		return "", nil, err
	}
	for _, template := range templates {
		if template.ID == value || strings.EqualFold(template.Slug, value) {
			matched := template
			return template.ID, &matched, nil
		}
	}

	return value, nil, nil
}

// handleTemplateAutocomplete handles template autocomplete and translates it into the package's application or response contract.
func handleTemplateAutocomplete(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil || services.Guilds.Authorize(ctx, guildContext, quack.PermissionActionCaseCreate, quack.AuditSourceDiscord) != nil {
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
	templates, err := services.Templates.ListActive(ctx, guildContext)
	if err != nil {
		slog.Error("failed to list templates for case autocomplete", "error", err)
		return autocompleteResponse(nil)
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, 25)
	for _, template := range templates {
		search := strings.ToLower(template.Slug + " " + template.Name + " " + template.Description)
		if query != "" && !strings.Contains(search, query) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  templateAutocompleteLabel(template),
			Value: template.ID,
		})
		if len(choices) == 25 {
			break
		}
	}

	return autocompleteResponse(choices)
}

// interactionMemberFields encapsulates the interaction member fields rule so callers share one consistent package implementation.
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

// optionStringValue encapsulates the option string value rule so callers share one consistent package implementation.
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

// templateAutocompleteLabel encapsulates the template autocomplete label rule so callers share one consistent package implementation.
func templateAutocompleteLabel(template quack.TemplateResponse) string {
	name := strings.TrimSpace(template.Name)
	if name == "" {
		name = strings.TrimSpace(template.Slug)
	}
	description := strings.TrimSpace(template.Description)
	if description != "" {
		name = fmt.Sprintf("%s - %s", name, description)
	}
	return truncateDiscordChoiceName(name)
}

// truncateDiscordChoiceName encapsulates the truncate discord choice name rule so callers share one consistent package implementation.
func truncateDiscordChoiceName(value string) string {
	runes := []rune(value)
	if len(runes) <= 100 {
		return value
	}
	return string(runes[:100])
}

// caseCommandErrorMessage maps expected moderation failures to concise private Discord replies.
func caseCommandErrorMessage(err error) string {
	switch {
	case errors.Is(err, quack.ErrCasePermissionDenied), errors.Is(err, quack.ErrAuthorizationDenied):
		return "You do not have permission to create that case."
	case errors.Is(err, quack.ErrCaseTemplateNotAvailable):
		return "That case template is not available."
	case errors.Is(err, quack.ErrCaseValidation):
		return "That case request is invalid."
	case errors.Is(err, quack.ErrBotNotInGuild):
		return "Quack is not active in this server."
	default:
		slog.Error("case command failed", "error", err)
		return "Quack could not create that case."
	}
}

// autocompleteResponse converts autocomplete response into its transport presentation without leaking transport types into the core.
func autocompleteResponse(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return ui.Autocomplete(choices)
}
