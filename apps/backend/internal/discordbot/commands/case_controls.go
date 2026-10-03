package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

func handleRetryComponent(ctx ui.Context) ui.HandlerResult {
	return actionControlComponent(ctx, "retry")
}

func handleDismissComponent(ctx ui.Context) ui.HandlerResult {
	return actionControlComponent(ctx, "dismiss")
}

func actionControlComponent(ctx ui.Context, operation string) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That action control is invalid."))
	}
	return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		var controlErr error
		if operation == "retry" {
			_, controlErr = ctx.Services.Actions.Retry(taskCtx, guildContext, parsed.Payload)
		} else {
			_, controlErr = ctx.Services.Actions.Dismiss(taskCtx, guildContext, parsed.Payload)
		}
		if controlErr != nil {
			return controlErr
		}
		result, listErr := ctx.Services.Actions.ListFailures(taskCtx, guildContext, 10, 0)
		if listErr != nil {
			return listErr
		}
		_, editErr := responder.UpdateMessage(ui.EditMessage(views.FailedActionMessage(result, 1)))
		return editErr
	})
}

func handleVoidComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || strings.TrimSpace(parsed.Payload) == "" {
		return ui.Immediate(ui.Error("That case control is invalid."))
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "void_submit", Version: "v1", Payload: parsed.Payload})
	return ui.Immediate(ui.Modal("Void case", customID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "reason", Label: "Required correction reason", Style: discordgo.TextInputParagraph, Required: true, MinLength: 3, MaxLength: 500})}))
}

func handleVoidModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case control is invalid."))
	}
	reason := modalTextValue(ctx.Interaction.ModalSubmitData(), "reason")
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		item, voidErr := ctx.Services.Cases.Void(taskCtx, guildContext, parsed.Payload, reason, nil)
		if voidErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(voidErr)))
			return editErr
		}
		_, editErr := ui.Publish(responder, ui.Content("**Case voided**\n"+fmt.Sprintf("Case #%d remains in history and no longer contributes to escalation.", item.CaseNumber), false))
		return editErr
	})
}

func handleReverseComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || len(strings.Split(parsed.Payload, "|")) != 3 {
		return ui.Immediate(ui.Error("That reversal control is invalid."))
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "reverse_submit", Version: "v1", Payload: parsed.Payload})
	return ui.Immediate(ui.Modal("Confirm reversal", customID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "confirm", Label: "Type REVERSE to confirm", Style: discordgo.TextInputShort, Required: true, MinLength: 7, MaxLength: 7})}))
}

func handleReverseModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	parts := strings.Split(parsed.Payload, "|")
	if err != nil || len(parts) != 3 || modalTextValue(ctx.Interaction.ModalSubmitData(), "confirm") != "REVERSE" {
		return ui.Immediate(ui.Error("Reversal confirmation did not match."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		_, reverseErr := ctx.Services.Actions.Reverse(taskCtx, guildContext, parts[0], parts[1], quack.ActionType(parts[2]))
		if reverseErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(reverseErr)))
			return editErr
		}
		_, editErr := ui.Publish(responder, ui.Content("**Reversal queued**\nThe original action remains visible in case history.", false))
		return editErr
	})
}
