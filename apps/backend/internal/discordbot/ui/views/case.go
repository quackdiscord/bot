package views

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// CaseCreated groups the case created state used to keep this package's responsibilities explicit.
type CaseCreated struct {
	Case     *quack.CaseResponse
	Template *quack.TemplateResponse
}

// CaseCreatedMessage converts case created message into its transport presentation without leaking transport types into the core.
func CaseCreatedMessage(result CaseCreated) ui.Message {
	return ui.Content(ui.TruncateRunes(FormatCaseCreated(result), 2000), false)
}

// CaseCreatedEmbed converts case created embed into its transport presentation without leaking transport types into the core.
func CaseCreatedEmbed(result CaseCreated) *discordgo.MessageEmbed {
	if result.Case == nil {
		return ui.SuccessEmbed("Case Created", "Created case.")
	}

	created := result.Case
	embed := ui.NewSuccessEmbed(fmt.Sprintf("Case #%d Created", created.CaseNumber), "").
		AddField("Target", fmt.Sprintf("<@%s>", created.TargetDiscordUserID), true)

	templateName := caseTemplateDisplayName(result.Template)
	if templateName != "" {
		embed.AddField("Template", templateName, false)
	}

	if created.SelectedLevel != nil {
		levelName := strings.TrimSpace(created.SelectedLevel.Name)
		if levelName == "" {
			levelName = fmt.Sprintf("Level %d", created.SelectedLevel.Position)
		}
		embed.AddField("Level", levelName, true)
	}

	embed.AddField("Action Status", publicActionStatus(created.Actions), false)
	return embed.Build()
}

// FormatCaseCreated encapsulates the format case created rule so callers share one consistent package implementation.
func FormatCaseCreated(result CaseCreated) string {
	if result.Case == nil {
		return "Created case."
	}

	created := result.Case
	lines := []string{
		fmt.Sprintf("**Case #%d created** · <@%s>", created.CaseNumber, created.TargetDiscordUserID),
	}

	templateName := caseTemplateDisplayName(result.Template)
	if templateName != "" {
		lines = append(lines, "**Template:** "+ui.TruncateRunes(templateName, 256))
	}

	if created.SelectedLevel != nil {
		levelName := strings.TrimSpace(created.SelectedLevel.Name)
		if levelName == "" {
			levelName = fmt.Sprintf("Level %d", created.SelectedLevel.Position)
		}
		lines = append(lines, fmt.Sprintf("**Level:** %s", ui.TruncateRunes(levelName, 256)))
	}

	lines = append(lines, "**Action:** "+publicActionStatus(created.Actions))
	return strings.Join(lines, "\n")
}

func publicActionStatus(actions []quack.CaseActionResponse) string {
	if len(actions) == 0 {
		return "No Discord action configured"
	}
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, fmt.Sprintf("%s · %s", action.ActionType.Label(), action.Status.Label()))
	}
	return strings.Join(parts, ", ")
}

// caseTemplateDisplayName prefers the admin-provided rule name, falling back to its slug.
func caseTemplateDisplayName(template *quack.TemplateResponse) string {
	if template == nil {
		return ""
	}
	name := strings.TrimSpace(template.Name)
	slug := strings.TrimSpace(template.Slug)
	switch {
	case name != "":
		return name
	default:
		return slug
	}
}

// actionSummary encapsulates the action summary rule so callers share one consistent package implementation.
func actionSummary(actions []quack.CaseActionResponse) string {
	if len(actions) == 0 {
		return " (none)"
	}

	counts := map[quack.ActionType]int{}
	order := make([]quack.ActionType, 0, len(actions))
	for _, action := range actions {
		if counts[action.ActionType] == 0 {
			order = append(order, action.ActionType)
		}
		counts[action.ActionType]++
	}

	parts := make([]string, 0, len(order))
	for _, actionType := range order {
		count := counts[actionType]
		if count == 1 {
			parts = append(parts, string(actionType))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s x%d", actionType, count))
	}
	return ": " + strings.Join(parts, ", ")
}
