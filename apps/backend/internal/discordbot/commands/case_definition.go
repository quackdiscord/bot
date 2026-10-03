package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

const caseCommandName = "case"

const messageCaseCommandName = "Create moderation case"

// CaseCommandSpec binds the /case definition to its interaction handler for explicit runtime registration.
func CaseCommandSpec() CommandSpec {
	return CommandSpec{
		Definition: CaseCommandDefinition(),
		Handler:    HandleCaseInteraction,
	}
}

// MessageCaseCommandSpec starts the same evidence-backed flow from a live Discord message.
func MessageCaseCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionModerateMembers)
	dm := false
	return CommandSpec{Definition: &discordgo.ApplicationCommand{Type: discordgo.MessageApplicationCommand, Name: messageCaseCommandName, DefaultMemberPermissions: &permissions, DMPermission: &dm}, Handler: HandleMessageCaseInteraction}
}

// CaseCommandDefinition returns a fresh /case definition so Discord-side mutation cannot alter registry state.
func CaseCommandDefinition() *discordgo.ApplicationCommand {
	defaultPermissions := int64(discordgo.PermissionModerateMembers)
	dmPermission := false
	add := &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "add", Description: "Create a moderation case from a template.", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionString, Name: "template", Description: "Case template to apply.", Required: true, Autocomplete: true}, {Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to moderate.", Required: true}, {Type: discordgo.ApplicationCommandOptionString, Name: "context", Description: "Visible context values as a JSON object."}, {Type: discordgo.ApplicationCommandOptionString, Name: "message_link", Description: "Discord message link to capture as evidence."}}}
	options := append([]*discordgo.ApplicationCommandOption{add}, caseStaffCommandOptions()...)
	return &discordgo.ApplicationCommand{
		Name:                     caseCommandName,
		Description:              "Create and manage moderation cases.",
		DefaultMemberPermissions: &defaultPermissions,
		DMPermission:             &dmPermission,
		Options:                  options,
	}
}

// caseStaffCommandOptions defines privacy-safe browsing and explicit recovery controls.
func caseStaffCommandOptions() []*discordgo.ApplicationCommandOption {
	stringOption := func(name, description string, required bool) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: name, Description: description, Required: required}
	}
	sub := func(name, description string, options ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: name, Description: description, Options: options}
	}
	confirm := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionBoolean, Name: "confirm", Description: "Confirm this irreversible control.", Required: true}
	}
	return []*discordgo.ApplicationCommandOption{sub("view", "View authorized case detail.", stringOption("case", "Case number or ID.", true)), sub("list", "List recent guild cases."), sub("user", "View a member's case history.", &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "Member to review.", Required: true}), sub("failures", "Review failed Discord actions."), sub("retry", "Retry the same failed action.", stringOption("execution", "Failed execution ID.", true)), sub("dismiss", "Dismiss a failure from active review.", stringOption("execution", "Failed execution ID.", true)), sub("void", "Void an incorrect case.", stringOption("case", "Case number or ID.", true), stringOption("reason", "Required correction reason.", true), confirm()), sub("reverse", "Remove a timeout or unban.", stringOption("case", "Case ID.", true), stringOption("execution", "Original execution ID.", true), &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: "action", Description: "Reversal action.", Required: true, Choices: []*discordgo.ApplicationCommandOptionChoice{{Name: "Remove timeout", Value: string(quack.ActionRemoveTimeout)}, {Name: "Unban", Value: string(quack.ActionUnbanUser)}}}, confirm())}
}

// CommandDefinition returns a fresh compatibility command definition derived from the explicit case specification.
func CommandDefinition() *discordgo.ApplicationCommand {
	return CaseCommandDefinition()
}
