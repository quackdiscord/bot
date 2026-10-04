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

// Labels of dashboard link buttons. Staff views use the generic label unless
// a more specific one says what the page is for.
const (
	dashboardLabel       = "Open in dashboard"
	dashboardCaseLabel   = "Open case"
	dashboardAppealLabel = "View appeal"
	dashboardReplyLabel  = "Reply on the dashboard"
	dashboardRuleLabel   = "Open rule"
	dashboardSetupLabel  = "Open settings"
)

// withLink returns message with a link button to url, labelled label, in a
// row of its own below its other components. An empty url, from a
// DashboardLinks without a dashboard or with an unsafe ID, or a message
// already at Discord's five rows, returns message unchanged. The
// components are copied, so message itself is never modified.
func withLink(message Message, url, label string) Message {
	if url == "" || len(message.Components) >= 5 {
		return message
	}
	message.Components = append(append([]discordgo.MessageComponent(nil), message.Components...),
		Row(LinkButton(url, label)))
	return message
}

// appendLink returns buttons with a link button to url added at the end, or
// buttons unchanged when url is empty or a row could not hold another.
func appendLink(buttons []discordgo.MessageComponent, url, label string) []discordgo.MessageComponent {
	if url == "" || len(buttons) >= 5 {
		return buttons
	}
	return append(buttons, LinkButton(url, label))
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
