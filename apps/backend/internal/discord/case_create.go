package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// add handles /case add. It resolves and authorizes the moderator once, then
// creates the case in a deferred task and publishes the receipt in place.
// A template with required context fields first collects them in a modal;
// optional context is added afterwards with Edit context.
func (c *cases) add(ctx context.Context, i *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) Result {
	if i.GuildID == "" {
		return Immediate(Error(caseCreateErrorMessage(errNotInGuild)))
	}
	templateOption, userOption := add.GetOption("template"), add.GetOption("user")
	if templateOption == nil || userOption == nil {
		return Immediate(Error(caseCreateErrorMessage(quack.ErrCaseValidation)))
	}
	staff, err := c.staff(ctx, i)
	if err == nil {
		err = staff.Authorize(quack.PermissionActionCaseCreate)
	}
	if err != nil {
		return Immediate(Error(caseCreateErrorMessage(err)))
	}
	templateID, template, err := c.template(ctx, staff, optionString(templateOption))
	if err != nil {
		return Immediate(Error(caseCreateErrorMessage(err)))
	}
	targetID := optionString(userOption)
	link := strings.TrimSpace(optionString(add.GetOption("message_link")))
	var values []quack.CaseContextValueInput
	if template != nil {
		values = messageLinkValues(template, link)
	}
	files := interactionFiles(i, add.GetOption("file"))
	if template != nil && !requiredContextFilled(template, values) {
		modal, err := c.startContext(i, template, targetID, link, i.ChannelID, "", values, files)
		if err != nil {
			return Immediate(Error(err.Error()))
		}
		return Immediate(modal)
	}
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		created, err := c.services.Cases.Create(ctx, staff, quack.CaseInput{
			TemplateID:              templateID,
			TargetDiscordUserID:     targetID,
			Source:                  quack.CaseSourceDiscord,
			ContextChannelDiscordID: i.ChannelID,
			ContextValues:           values,
			EvidenceLinks:           nonEmpty(link),
			Attachments:             files,
			IdempotencyKey:          i.ID,
		})
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		c.publishInPlace(ctx, responder, i, created)
		return nil
	})
}

// messageCommand handles the "Add case" message action. It answers
// privately: with one active template it creates the case straight away,
// with several it asks which rule applies. The receipt replaces the private
// reply, since the message's channel is likely public.
func (c *cases) messageCommand(_ context.Context, i *discordgo.InteractionCreate) Result {
	if i.GuildID == "" {
		return Immediate(Error("Select a server message to create a case."))
	}
	data := i.ApplicationCommandData()
	var message *discordgo.Message
	if data.Resolved != nil {
		message = data.Resolved.Messages[data.TargetID]
	}
	if message == nil || message.Author == nil {
		return Immediate(Error("The selected message is unavailable."))
	}
	target := caseTarget{
		userID:    message.Author.ID,
		channelID: message.ChannelID,
		messageID: message.ID,
	}
	return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
		return c.startContextMenu(ctx, i, responder, target)
	})
}

// userCommand handles the "Add case for member" user action like
// messageCommand, without message evidence. Its receipt is posted to the
// channel and the private reply removed.
func (c *cases) userCommand(_ context.Context, i *discordgo.InteractionCreate) Result {
	if i.GuildID == "" {
		return Immediate(Error("Select a server member to create a case."))
	}
	data := i.ApplicationCommandData()
	if data.TargetID == "" || data.Resolved == nil || data.Resolved.Users[data.TargetID] == nil {
		return Immediate(Error("The selected member is unavailable."))
	}
	return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
		return c.startContextMenu(ctx, i, responder, caseTarget{userID: data.TargetID})
	})
}

// caseTarget is who a context-menu case is about and, for the message
// action, the message it started from.
type caseTarget struct {
	userID, channelID, messageID string
}

// kind is the picker kind carried in template page payloads: "m" for a
// message, "u" for a member.
func (t caseTarget) kind() string {
	if t.messageID != "" {
		return "m"
	}
	return "u"
}

// payload is the target as the picker's select menu carries it:
// "user|channel|message" for a message, the user ID for a member.
func (t caseTarget) payload() string {
	if t.messageID != "" {
		return strings.Join([]string{t.userID, t.channelID, t.messageID}, "|")
	}
	return t.userID
}

// parseCaseTarget reverses payload for the given picker kind.
func parseCaseTarget(kind, payload string) (caseTarget, bool) {
	if kind == "u" {
		return caseTarget{userID: payload}, payload != "" && !strings.Contains(payload, "|")
	}
	parts := strings.Split(payload, "|")
	if kind != "m" || len(parts) != 3 || slices.Contains(parts, "") {
		return caseTarget{}, false
	}
	return caseTarget{userID: parts[0], channelID: parts[1], messageID: parts[2]}, true
}

// startContextMenu authorizes the moderator and either creates the case
// with the guild's only template or shows the rule picker.
func (c *cases) startContextMenu(ctx context.Context, i *discordgo.InteractionCreate, responder Responder, target caseTarget) error {
	staff, err := c.staff(ctx, i)
	if err == nil {
		err = staff.Authorize(quack.PermissionActionCaseCreate)
	}
	if err != nil {
		_, err = responder.EditOriginal(ErrorEdit(caseCreateErrorMessage(err)))
		return err
	}
	templates, err := c.services.Templates.ListActive(ctx, staff)
	if err != nil || len(templates) == 0 {
		_, err = responder.EditOriginal(ErrorEdit("No active case template is available."))
		return err
	}
	if len(templates) > 1 {
		_, err = responder.EditOriginal(EditMessage(templatePicker(templates, target, 0)))
		return err
	}
	return c.createFromContextMenu(ctx, i, responder, staff, &templates[0], target, i.ID)
}

// templatePage handles the picker's Previous and Next buttons. Its payload
// is "kind|page|target". Templates are reloaded so the list is current.
func (c *cases) templatePage(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	parts := strings.SplitN(id.Payload, "|", 3)
	if err != nil || len(parts) != 3 {
		return Immediate(Error("That template page is unavailable."))
	}
	target, ok := parseCaseTarget(parts[0], parts[2])
	page, pageErr := parseCount(parts[1])
	if !ok || pageErr != nil {
		return Immediate(Error("That template page is unavailable."))
	}
	return Async(DeferUpdate(), func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			_, err = responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		templates, err := c.services.Templates.ListActive(ctx, staff)
		if err != nil {
			_, err = responder.EditOriginal(ErrorEdit("Could not load case templates. Try again."))
			return err
		}
		_, err = responder.EditOriginal(EditMessage(templatePicker(templates, target, page)))
		return err
	})
}

// messageTemplate handles the rule picked for a message. Its payload is
// "target|channel|message".
func (c *cases) messageTemplate(_ context.Context, i *discordgo.InteractionCreate) Result {
	return c.pickTemplate(i, "m", "That message case flow is invalid.")
}

// userTemplate handles the rule picked for a member. Its payload is the
// member's user ID.
func (c *cases) userTemplate(_ context.Context, i *discordgo.InteractionCreate) Result {
	return c.pickTemplate(i, "u", "That case selection is unavailable.")
}

// pickTemplate creates the case for the rule chosen in a picker of kind,
// checking the moderator's authority again since the picker may be stale.
func (c *cases) pickTemplate(i *discordgo.InteractionCreate, kind, invalid string) Result {
	data := i.MessageComponentData()
	id, err := DecodeCustomID(data.CustomID)
	target, ok := parseCaseTarget(kind, id.Payload)
	if err != nil || !ok || len(data.Values) != 1 {
		return Immediate(Error(invalid))
	}
	return Async(DeferUpdate(), func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err == nil {
			err = staff.Authorize(quack.PermissionActionCaseCreate)
		}
		if err != nil {
			_, err = responder.EditOriginal(ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		_, template, err := c.template(ctx, staff, data.Values[0])
		if err != nil || template == nil {
			_, err = responder.EditOriginal(ErrorEdit("That case template is not available."))
			return err
		}
		return c.createFromContextMenu(ctx, i, responder, staff, template, target, selectionKey(i))
	})
}

// createFromContextMenu creates a case for target under template. A
// member's case posts its receipt to the channel; a message's case keeps it
// private, since that channel is likely public. Required context the
// message cannot supply sends the moderator to /case add instead.
func (c *cases) createFromContextMenu(
	ctx context.Context, i *discordgo.InteractionCreate, responder Responder,
	staff *quack.GuildStaffContext, template *quack.TemplateResponse, target caseTarget, key string,
) error {
	input := quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: target.userID,
		Source:              quack.CaseSourceDiscord,
		IdempotencyKey:      key,
	}
	if target.messageID != "" {
		link := messageLink(i.GuildID, target.channelID, target.messageID)
		input.ContextChannelDiscordID = target.channelID
		input.ContextMessageDiscordID = target.messageID
		input.ContextValues = messageLinkValues(template, link)
		input.EvidenceLinks = []string{link}
	}
	if !requiredContextFilled(template, input.ContextValues) {
		_, err := responder.EditOriginal(ErrorEdit("Use `/case add` to fill in this rule’s required context."))
		return err
	}
	created, err := c.services.Cases.Create(ctx, staff, input)
	if err != nil {
		_, err = responder.EditOriginal(ErrorEdit(caseCreateErrorMessage(err)))
		return err
	}
	if target.messageID != "" {
		c.publishPrivately(ctx, responder, i, created)
	} else {
		c.publishToChannel(ctx, responder, i, created)
	}
	return nil
}

// templatePicker asks which rule target broke, 25 templates per page with
// Previous and Next buttons when there are more.
func templatePicker(templates []quack.TemplateResponse, target caseTarget, page int) Message {
	if len(templates) == 0 {
		return Signal("error", "There are no rules yet. Create one with `/template create`.", true)
	}
	pages := (len(templates) + choiceLimit - 1) / choiceLimit
	page = max(0, min(page, pages-1))
	action := "user_template"
	if target.messageID != "" {
		action = "message_template"
	}
	id, err := EncodeCustomID(CustomID{Namespace: "case", Action: action, Version: "v1", Payload: target.payload()})
	if err != nil {
		return Signal("error", "Use `/case add` to choose a template for this target.", true)
	}
	options := make([]discordgo.SelectMenuOption, 0, choiceLimit)
	for _, template := range templates[page*choiceLimit : min((page+1)*choiceLimit, len(templates))] {
		options = append(options, discordgo.SelectMenuOption{Label: templateLabel(template), Value: template.ID})
	}
	minValues := 1
	menu := discordgo.SelectMenu{CustomID: id, Placeholder: "Select a rule", MinValues: &minValues, MaxValues: 1, Options: options}
	message := Signal("case", "Which rule did they break?", true)
	message.Components = []discordgo.MessageComponent{Row(menu)}
	if pages > 1 {
		button := func(label string, next int, disabled bool) discordgo.MessageComponent {
			payload := fmt.Sprintf("%s|%d|%s", target.kind(), max(0, next), target.payload())
			id, err := EncodeCustomID(CustomID{Namespace: "case", Action: "template_page", Version: "v1", Payload: payload})
			if err != nil {
				return Button("case:template_page:v1:invalid", label, discordgo.SecondaryButton, true)
			}
			return Button(id, label, discordgo.SecondaryButton, disabled)
		}
		message.Content += fmt.Sprintf("\n-# Page %d of %d", page+1, pages)
		message.Components = append(message.Components, Row(
			button("Previous", page-1, page == 0),
			button("Next", page+1, page == pages-1),
		))
	}
	return message
}

// selectionKey makes a picker single-use: every click on the same private
// picker message shares one idempotency key, even if removing the picker
// failed or two clicks arrived together.
func selectionKey(i *discordgo.InteractionCreate) string {
	if i.Message != nil && i.Message.ID != "" {
		return "case-selector:" + i.Message.ID
	}
	return i.ID
}

// requiredContextFilled reports whether values cover every required context
// field of template.
func requiredContextFilled(template *quack.TemplateResponse, values []quack.CaseContextValueInput) bool {
	for _, field := range template.ContextFields {
		if field.Required && !slices.ContainsFunc(values, func(v quack.CaseContextValueInput) bool { return v.Key == field.Key }) {
			return false
		}
	}
	return true
}

// interactionFiles returns the attachment an option refers to, as Discord
// resolved it. Only Discord's own metadata is trusted.
func interactionFiles(i *discordgo.InteractionCreate, option *discordgo.ApplicationCommandInteractionDataOption) []quack.DiscordAttachmentSnapshot {
	if option == nil {
		return nil
	}
	resolved := i.ApplicationCommandData().Resolved
	if resolved == nil {
		return nil
	}
	file := resolved.Attachments[optionString(option)]
	if file == nil {
		return nil
	}
	return []quack.DiscordAttachmentSnapshot{{
		ID:          file.ID,
		Filename:    file.Filename,
		ContentType: file.ContentType,
		URL:         file.URL,
		SizeBytes:   int64(file.Size),
	}}
}

// messageLink returns the jump URL of a guild message.
func messageLink(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}

// messageLinkValues fills every message-link field of template with link,
// or returns nil when there is no link.
func messageLinkValues(template *quack.TemplateResponse, link string) []quack.CaseContextValueInput {
	if link == "" {
		return nil
	}
	raw, _ := json.Marshal(link)
	var values []quack.CaseContextValueInput
	for _, field := range template.ContextFields {
		if field.FieldType == quack.ContextFieldMessageLink {
			values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
		}
	}
	return values
}

// nonEmpty returns value as a one-element list, or nil when it is empty.
func nonEmpty(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}
