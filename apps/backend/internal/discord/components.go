package discord

import "github.com/bwmarrin/discordgo"

// Button returns an interactive button routed by customID.
func Button(customID, label string, style discordgo.ButtonStyle, disabled bool) discordgo.Button {
	return discordgo.Button{CustomID: customID, Label: Truncate(label, 80), Style: style, Disabled: disabled}
}

// LinkButton returns a button that opens url instead of sending an
// interaction.
func LinkButton(url, label string) discordgo.Button {
	return discordgo.Button{URL: url, Label: Truncate(label, 80), Style: discordgo.LinkButton}
}

// Row puts up to five components in one action row; extras are dropped.
func Row(components ...discordgo.MessageComponent) discordgo.ActionsRow {
	if len(components) > 5 {
		components = components[:5]
	}
	return discordgo.ActionsRow{Components: components}
}

// Pagination returns Prev and Next buttons routed to namespace:prefix_prev
// and namespace:prefix_next with payload, each disabled at its end.
func Pagination(namespace, prefix, payload string, page, totalPages int) ([]discordgo.MessageComponent, error) {
	prevID, err := EncodeCustomID(CustomID{Namespace: namespace, Action: prefix + "_prev", Version: "v1", Payload: payload})
	if err != nil {
		return nil, err
	}
	nextID, err := EncodeCustomID(CustomID{Namespace: namespace, Action: prefix + "_next", Version: "v1", Payload: payload})
	if err != nil {
		return nil, err
	}
	return []discordgo.MessageComponent{Row(
		Button(prevID, "Prev", discordgo.SecondaryButton, page <= 1),
		Button(nextID, "Next", discordgo.PrimaryButton, page >= totalPages),
	)}, nil
}

// ModalValue returns the value of the text input named customID in a modal
// submission. discordgo decodes rows as pointers, but tests and older
// payloads use values, so both are accepted.
func ModalValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, component := range data.Components {
		var row *discordgo.ActionsRow
		switch value := component.(type) {
		case *discordgo.ActionsRow:
			row = value
		case discordgo.ActionsRow:
			row = &value
		default:
			continue
		}
		for _, child := range row.Components {
			switch input := child.(type) {
			case *discordgo.TextInput:
				if input.CustomID == customID {
					return input.Value
				}
			case discordgo.TextInput:
				if input.CustomID == customID {
					return input.Value
				}
			}
		}
	}
	return ""
}
