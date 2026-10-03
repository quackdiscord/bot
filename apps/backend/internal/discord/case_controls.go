package discord

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// The case controls on receipts and details: Edit context, View evidence,
// History, and View case. Every click resolves the moderator's authority
// again; payloads carry only IDs and page numbers.

// maxPage bounds page numbers read from custom IDs.
const maxPage = 1_000_000

// editContextButton opens the free-text context form, prefilled with the
// case's current context.
func (c *cases) editContextButton(ctx context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	if err != nil {
		return Immediate(Error("That case button is invalid."))
	}
	staff, err := c.staff(ctx, i)
	if err != nil {
		return Immediate(Error(caseErrorMessage(err)))
	}
	detail, err := c.services.Cases.GetCompact(ctx, staff, id.Payload)
	if err != nil {
		return Immediate(Error(caseErrorMessage(err)))
	}
	var parts []string
	for _, value := range detail.ContextValues {
		if value.Value != nil {
			parts = append(parts, fmt.Sprint(value.Value))
		}
	}
	text := strings.Join(parts, "\n\n")
	if len([]rune(text)) > 4000 {
		return Immediate(Error("This case has too much context for one Discord form."))
	}
	customID := MustCustomID(CustomID{Namespace: "case", Action: "edit_context_submit", Version: "v1", Payload: detail.ID})
	input := discordgo.TextInput{
		CustomID:    "context",
		Label:       "What happened?",
		Placeholder: "Describe what happened or paste a Discord message link.",
		Style:       discordgo.TextInputParagraph,
		MaxLength:   4000,
		Value:       text,
	}
	return Immediate(Modal(fmt.Sprintf("Context for case #%d", detail.CaseNumber), customID, []discordgo.MessageComponent{Row(input)}))
}

// editContextModal saves the new context without touching the decision or
// its enforcement, and shows the updated case in the channel. Message links
// in the text are captured as evidence; if that fails the text is still
// saved and the moderator is told how to retry.
func (c *cases) editContextModal(_ context.Context, i *discordgo.InteractionCreate) Result {
	data := i.ModalSubmitData()
	id, err := DecodeCustomID(data.CustomID)
	if err != nil {
		return Immediate(Error("That context form is invalid."))
	}
	text := ModalValue(data, "context")
	return Async(DeferPublic(), func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			return err
		}
		detail, err := c.services.Cases.UpdateContext(ctx, staff, id.Payload, text)
		if err != nil {
			return err
		}
		lead := fmt.Sprintf("Context saved for case #%d.", detail.CaseNumber)
		if detail.EvidenceIncomplete && quack.ContextContainsMessageLinks(text) {
			lead += fmt.Sprintf(" Some evidence could not be saved. Use `/case evidence case:%d` with the message link to try again.", detail.CaseNumber)
		}
		result := caseDetailPage(detail, 1, i.AppID)
		result.Content = lead + "\n\n" + result.Content
		_, err = responder.EditOriginal(EditMessage(result))
		return err
	})
}

// viewButton opens the full staff view of the case in the payload, from a
// receipt that was cut short or has outlived its refresh.
func (c *cases) viewButton(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	if err != nil {
		return Immediate(Error("That case button is invalid."))
	}
	return Async(DeferPublic(), func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			return err
		}
		detail, err := c.services.Cases.GetCompact(ctx, staff, id.Payload)
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(EditMessage(c.webLink(caseDetailPage(detail, 1, i.AppID), i.GuildID, "cases", detail.ID)))
		return err
	})
}

// pageDetail handles the Prev and Next buttons of a long case detail. The
// payload is "page|case".
func (c *cases) pageDetail(delta int) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		parts := strings.SplitN(id.Payload, "|", 2)
		if err != nil || len(parts) != 2 || parts[1] == "" {
			return Immediate(Error("That case page is no longer available."))
		}
		page, err := parseCount(parts[0])
		if err != nil || page < 1 {
			return Immediate(Error("That case page is no longer available."))
		}
		return Async(DeferUpdate(), func(ctx context.Context, responder Responder) error {
			staff, err := c.staff(ctx, i)
			if err != nil {
				return err
			}
			detail, err := c.services.Cases.GetCompact(ctx, staff, parts[1])
			if err != nil {
				return err
			}
			message := c.webLink(caseDetailPage(detail, page+delta, i.AppID), i.GuildID, "cases", detail.ID)
			_, err = responder.EditOriginal(EditMessage(message))
			return err
		})
	}
}

// evidenceButton shows the case's first evidence item and its context in
// the channel.
func (c *cases) evidenceButton(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	if err != nil {
		return Immediate(Error("That case button is invalid."))
	}
	return Async(DeferPublic(), func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			return err
		}
		evidence, err := c.services.Cases.EvidencePage(ctx, staff, id.Payload, 1)
		if err != nil {
			return err
		}
		message := c.webLink(evidencePageMessage(evidence, 1, i.AppID), i.GuildID, "cases", evidence.CaseID)
		_, err = responder.EditOriginal(EditMessage(message))
		return err
	})
}

// pageEvidence handles Previous and Next on an evidence view. The payload is
// "position:page|case": the evidence item and its text page. Stepping past
// the first or last text page moves to the neighboring item.
func (c *cases) pageEvidence(delta int) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		parts := strings.SplitN(id.Payload, "|", 2)
		if err != nil || len(parts) != 2 || parts[1] == "" {
			return Immediate(Error("That case page is no longer available."))
		}
		position, page := 1, 1
		coordinates := strings.SplitN(parts[0], ":", 2)
		if len(coordinates) == 1 {
			page, err = parseCount(coordinates[0])
		} else if position, err = parseCount(coordinates[0]); err == nil {
			page, err = parseCount(coordinates[1])
		}
		if err != nil || position < 1 || page < 1 {
			return Immediate(Error("That case page is no longer available."))
		}
		caseID := parts[1]
		return Async(DeferUpdate(), func(ctx context.Context, responder Responder) error {
			staff, err := c.staff(ctx, i)
			if err != nil {
				return err
			}
			evidence, err := c.services.Cases.EvidencePage(ctx, staff, caseID, position)
			if err != nil {
				return err
			}
			last := len(evidencePages(evidence, i.AppID))
			page = min(page, last) + delta
			switch {
			case page < 1 && evidence.Position > 1:
				evidence, err = c.services.Cases.EvidencePage(ctx, staff, caseID, evidence.Position-1)
				page = maxPage
			case page > last && int64(evidence.Position) < evidence.Total:
				evidence, err = c.services.Cases.EvidencePage(ctx, staff, caseID, evidence.Position+1)
				page = 1
			}
			if err != nil {
				return err
			}
			message := c.webLink(evidencePageMessage(evidence, page, i.AppID), i.GuildID, "cases", evidence.CaseID)
			_, err = responder.EditOriginal(EditMessage(message))
			return err
		})
	}
}

// historyButton shows the case history of the member in the payload in the
// channel.
func (c *cases) historyButton(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	if err != nil {
		return Immediate(Error("That user button is invalid."))
	}
	return Async(DeferPublic(), func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			return err
		}
		profile, err := c.services.Cases.UserHistory(ctx, staff, id.Payload, quack.CaseListInput{Limit: strconv.Itoa(casePageSize)})
		if err != nil {
			return err
		}
		message := c.webLink(caseProfileMessage(profile, 1, id.Payload), i.GuildID, "members", id.Payload)
		_, err = responder.EditOriginal(EditMessage(message))
		return err
	})
}

// webLink adds an "Open on web" button linking the dashboard page for
// resource ("cases" or "members") and recordID, when a dashboard is
// configured and the message has room for another row. Dashboard access is
// checked by the dashboard; the link carries only IDs, never evidence or
// context.
func (c *cases) webLink(message Message, guildID, resource, recordID string) Message {
	if c.dashboardURL == "" || len(message.Components) >= 5 {
		return message
	}
	destination, err := url.Parse(c.dashboardURL)
	if err != nil || destination.Scheme != "https" || destination.Hostname() == "" || destination.User != nil ||
		destination.RawQuery != "" || destination.ForceQuery || destination.Fragment != "" || destination.Opaque != "" {
		return message
	}
	if !webSegment(guildID) || (recordID != "" && !webSegment(recordID)) ||
		(resource != "cases" && resource != "members") || (resource == "members" && recordID == "") {
		return message
	}
	destination.Path = strings.TrimRight(destination.Path, "/") + "/guilds/" + guildID + "/" + resource
	if recordID != "" {
		destination.Path += "/" + recordID
	}
	destination.RawPath = ""
	if len(destination.String()) > 512 {
		return message
	}
	message.Components = append(append([]discordgo.MessageComponent(nil), message.Components...),
		Row(LinkButton(destination.String(), "Open on web")))
	return message
}

// webSegment reports whether value is an opaque ID safe to put in a URL
// path, so a crafted payload cannot add URL syntax or traverse paths.
func webSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

// parseCount parses a non-negative page or position number from a custom
// ID, rejecting values past maxPage.
func parseCount(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n > maxPage {
		return 0, fmt.Errorf("invalid page number %q", value)
	}
	return n, nil
}
