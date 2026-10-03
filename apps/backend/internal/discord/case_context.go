package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

const (
	// contextPageSize is how many context fields fit in one modal; Discord
	// allows five inputs per modal.
	contextPageSize = 5

	// draftLifetime is how long a moderator has to finish a context form.
	draftLifetime = 15 * time.Minute
)

// contextDraft is a context form in progress. Discord modals hold at most
// five inputs, so templates with more fields are filled across pages.
type contextDraft struct {
	// Token is the ID of the interaction that started the form, and becomes
	// the case's idempotency key.
	Token                   string
	ActorDiscordUserID      string
	GuildID                 string
	ContextChannelDiscordID string
	ContextMessageDiscordID string
	TargetDiscordUserID     string
	Template                quack.TemplateResponse
	Values                  map[string]json.RawMessage
	EvidenceLinks           []string
	Page                    int
	ExpiresAt               time.Time
}

// draftStore keeps context drafts in memory until they expire. Drafts are
// per process, which is fine because Discord routes a modal back within
// seconds of opening it.
type draftStore struct {
	mu     sync.Mutex
	drafts map[string]contextDraft
}

// newDraftStore returns an empty draft store.
func newDraftStore() *draftStore {
	return &draftStore{drafts: map[string]contextDraft{}}
}

// contextModal handles one submitted page of the context form. Earlier pages
// are kept in the draft until the last page creates the case.
func (c *cases) contextModal(ctx context.Context, i *discordgo.InteractionCreate) Result {
	data := i.ModalSubmitData()
	id, err := DecodeCustomID(data.CustomID)
	draft, ok := c.drafts.get(id.Payload)
	if err != nil || !ok || !draft.matches(i) {
		return Immediate(Error("That case context form is invalid."))
	}
	values, evidence, err := contextValuesFromModal(data, draft.page())
	if err != nil {
		return Immediate(Error(err.Error()))
	}
	for _, value := range values {
		draft.Values[value.Key] = value.Value
	}
	draft.EvidenceLinks = appendUnique(draft.EvidenceLinks, evidence...)
	draft.Page++
	if draft.Page*contextPageSize < len(draft.Template.ContextFields) {
		c.drafts.save(draft)
		continueID := MustCustomID(CustomID{
			Namespace: "case",
			Action:    "context_next",
			Version:   "v1",
			Payload:   draft.Token,
		})
		return Immediate(Ephemeral(Message{
			Content:    fmt.Sprintf("Saved context page %d. Continue to page %d.", draft.Page, draft.Page+1),
			Components: []discordgo.MessageComponent{Row(Button(continueID, "Continue context", discordgo.PrimaryButton, false))},
			Ephemeral:  true,
		}))
	}
	c.drafts.delete(draft.Token)
	ordered := make([]quack.CaseContextValueInput, 0, len(draft.Values))
	for _, field := range draft.Template.ContextFields {
		if value, ok := draft.Values[field.Key]; ok {
			ordered = append(ordered, quack.CaseContextValueInput{Key: field.Key, Value: value})
		}
	}
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			return err
		}
		_, template, err := c.template(ctx, staff, draft.Template.ID)
		if err != nil || template == nil {
			return quack.ErrCaseTemplateNotAvailable
		}
		created, err := c.services.Cases.Create(ctx, staff, quack.CaseInput{
			TemplateID:              template.ID,
			TargetDiscordUserID:     draft.TargetDiscordUserID,
			Source:                  quack.CaseSourceDiscord,
			ContextChannelDiscordID: draft.ContextChannelDiscordID,
			ContextMessageDiscordID: draft.ContextMessageDiscordID,
			ContextValues:           ordered,
			EvidenceLinks:           draft.EvidenceLinks,
			IdempotencyKey:          draft.Token,
		})
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		return c.publish(ctx, responder, created, template)
	})
}

// contextNext reopens the context form at the draft's next page.
func (c *cases) contextNext(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	draft, ok := c.drafts.get(id.Payload)
	if err != nil || !ok || !draft.matches(i) {
		return Immediate(Error("That case context form expired."))
	}
	modal, err := draft.modal()
	if err != nil {
		return Immediate(Error("That case context form is unavailable."))
	}
	return Immediate(modal)
}

// startContext starts a context form draft and returns its first page. The
// draft is keyed by the interaction ID, which later becomes the case's
// idempotency key.
func (c *cases) startContext(
	i *discordgo.InteractionCreate, template *quack.TemplateResponse,
	targetID, evidenceLink, channelID, messageID string, prefilled []quack.CaseContextValueInput,
) (*discordgo.InteractionResponse, error) {
	if len(template.ContextFields) == 0 || strings.TrimSpace(i.ID) == "" {
		return nil, errors.New("template context is empty")
	}
	actorID, _ := interactionMember(i)
	values := make(map[string]json.RawMessage, len(prefilled))
	for _, value := range prefilled {
		values[value.Key] = value.Value
	}
	draft := contextDraft{
		Token:                   i.ID,
		ActorDiscordUserID:      actorID,
		GuildID:                 i.GuildID,
		ContextChannelDiscordID: channelID,
		ContextMessageDiscordID: messageID,
		TargetDiscordUserID:     targetID,
		Template:                *template,
		Values:                  values,
		EvidenceLinks:           appendUnique(nil, evidenceLink),
		ExpiresAt:               time.Now().UTC().Add(draftLifetime),
	}
	c.drafts.put(draft)
	return draft.modal()
}

// matches reports whether i comes from the moderator who started the draft,
// in the same guild.
func (d contextDraft) matches(i *discordgo.InteractionCreate) bool {
	actorID, _ := interactionMember(i)
	return d.GuildID == i.GuildID && actorID != "" && actorID == d.ActorDiscordUserID
}

// page returns the fields on the draft's current page.
func (d contextDraft) page() []quack.TemplateContextFieldResponse {
	fields := d.Template.ContextFields
	start := d.Page * contextPageSize
	if start >= len(fields) {
		return nil
	}
	return fields[start:min(start+contextPageSize, len(fields))]
}

// modal renders the draft's current page, prefilled with saved values.
func (d contextDraft) modal() (*discordgo.InteractionResponse, error) {
	customID, err := EncodeCustomID(CustomID{Namespace: "case", Action: "context_submit", Version: "v1", Payload: d.Token})
	if err != nil {
		return nil, err
	}
	fields := d.page()
	rows := make([]discordgo.MessageComponent, 0, len(fields))
	for _, field := range fields {
		style, maxLength := discordgo.TextInputShort, 1000
		if field.FieldType == quack.ContextFieldLongText {
			style, maxLength = discordgo.TextInputParagraph, 4000
		}
		placeholder := "Enter a value"
		if field.FieldType == quack.ContextFieldBoolean {
			placeholder = "true or false"
		}
		value := ""
		if raw, ok := d.Values[field.Key]; ok {
			_ = json.Unmarshal(raw, &value)
		}
		rows = append(rows, Row(discordgo.TextInput{
			CustomID:    "context_" + field.Key,
			Label:       field.Label,
			Style:       style,
			Required:    field.Required,
			MaxLength:   maxLength,
			Placeholder: placeholder,
			Value:       value,
		}))
	}
	pages := (len(d.Template.ContextFields) + contextPageSize - 1) / contextPageSize
	return Modal(fmt.Sprintf("Case context (%d/%d)", d.Page+1, pages), customID, rows), nil
}

// put stores draft and drops expired ones.
func (s *draftStore) put(draft contextDraft) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for token, existing := range s.drafts {
		if !existing.ExpiresAt.After(now) {
			delete(s.drafts, token)
		}
	}
	s.drafts[draft.Token] = draft
}

// get returns a copy of the draft that the caller may modify freely.
func (s *draftStore) get(token string) (contextDraft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.drafts[token]
	if !ok || !draft.ExpiresAt.After(time.Now().UTC()) {
		delete(s.drafts, token)
		return contextDraft{}, false
	}
	values := make(map[string]json.RawMessage, len(draft.Values))
	for key, value := range draft.Values {
		values[key] = slices.Clone(value)
	}
	draft.Values = values
	draft.EvidenceLinks = slices.Clone(draft.EvidenceLinks)
	return draft, true
}

// save writes back a draft obtained from get, keeping its expiry.
func (s *draftStore) save(draft contextDraft) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts[draft.Token] = draft
}

// delete drops a draft once its case has been submitted.
func (s *draftStore) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drafts, token)
}

// contextValuesFromModal reads and type-checks one page of the context form.
// Message-link values are also returned as evidence links.
func contextValuesFromModal(
	data discordgo.ModalSubmitInteractionData, fields []quack.TemplateContextFieldResponse,
) ([]quack.CaseContextValueInput, []string, error) {
	values := make([]quack.CaseContextValueInput, 0, len(fields))
	evidence := []string{}
	for _, field := range fields {
		value := strings.TrimSpace(ModalValue(data, "context_"+field.Key))
		if value == "" {
			if field.Required {
				return nil, nil, fmt.Errorf("%s is required", field.Label)
			}
			continue
		}
		var raw json.RawMessage
		switch field.FieldType {
		case quack.ContextFieldBoolean:
			parsed, err := strconv.ParseBool(strings.ToLower(value))
			if err != nil {
				return nil, nil, fmt.Errorf("%s must be true or false", field.Label)
			}
			raw, _ = json.Marshal(parsed)
		case quack.ContextFieldNumber:
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return nil, nil, fmt.Errorf("%s must be a number", field.Label)
			}
			raw = json.RawMessage(value)
		default:
			raw, _ = json.Marshal(value)
		}
		if field.FieldType == quack.ContextFieldMessageLink {
			evidence = append(evidence, value)
		}
		values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
	}
	return values, evidence, nil
}

// appendUnique appends the non-blank additions not already in values.
func appendUnique(values []string, additions ...string) []string {
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	return values
}
