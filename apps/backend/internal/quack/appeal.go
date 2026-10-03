package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// AppealStatus is where an appeal sits in review.
type AppealStatus string

// Appeal statuses.
const (
	AppealStatusPending          AppealStatus = "pending"
	AppealStatusNeedsInformation AppealStatus = "needs_information"
	AppealStatusAccepted         AppealStatus = "accepted"
	AppealStatusRejected         AppealStatus = "rejected"
	AppealStatusClosed           AppealStatus = "closed"
)

func validAppealStatus(status AppealStatus) bool {
	switch status {
	case AppealStatusPending, AppealStatusNeedsInformation, AppealStatusAccepted, AppealStatusRejected, AppealStatusClosed:
		return true
	default:
		return false
	}
}

// AppealEventType names a step in an appeal timeline.
type AppealEventType string

// Appeal event types.
const (
	AppealEventSubmitted        AppealEventType = "submitted"
	AppealEventInformationAsked AppealEventType = "information_requested"
	AppealEventInformationAdded AppealEventType = "information_submitted"
	AppealEventReopened         AppealEventType = "reopened"
	AppealEventAccepted         AppealEventType = "accepted"
	AppealEventRejected         AppealEventType = "rejected"
	AppealEventClosed           AppealEventType = "closed"
)

// AppealNotificationAudience says who an appeal notification is for.
type AppealNotificationAudience string

// Appeal notification audiences.
const (
	AppealNotificationMember AppealNotificationAudience = "member"
	AppealNotificationStaff  AppealNotificationAudience = "staff"
)

// AppealNotificationStatus tracks delivery of an appeal notification.
type AppealNotificationStatus string

// Appeal notification statuses. Sending marks a delivery that may have
// reached Discord: it is never reclaimed, so a crash cannot send twice.
const (
	AppealNotificationPending AppealNotificationStatus = "pending"
	AppealNotificationClaimed AppealNotificationStatus = "claimed"
	AppealNotificationSending AppealNotificationStatus = "sending"
	AppealNotificationSent    AppealNotificationStatus = "sent"
	AppealNotificationFailed  AppealNotificationStatus = "failed"
)

// Appeal is a member's request to reconsider one of their cases. Each case
// has at most one appeal; reopening reuses it.
type Appeal struct {
	ULIDModel
	GuildID                 string
	CaseID                  *string
	TargetDiscordUserID     string
	Status                  AppealStatus
	Content                 string
	QuestionSnapshotJSON    string
	AnswersJSON             string
	Version                 uint64
	DecisionReason          string
	ReviewedByDiscordUserID string
	ReviewedAt              *time.Time
	ReviewMessageDiscordID  string
	MetadataJSON            string
}

// AppealEvent is one entry in an appeal timeline.
type AppealEvent struct {
	ULIDModel
	AppealID           string
	GuildID            string
	EventType          string
	ActorDiscordUserID string
	ActorType          string
	Body               string
	MetadataJSON       string
}

// AppealNotification is an outbox row written in the same transaction as the
// appeal change it announces. Member-facing bodies never name staff.
type AppealNotification struct {
	ULIDModel
	AppealID            string
	EventID             string
	GuildID             string
	TargetDiscordUserID string
	Audience            AppealNotificationAudience
	Status              AppealNotificationStatus
	// Body is plain wording for notifiers that do not render appeals
	// themselves.
	Body string
	// DecisionIntentJSON is the AppealDecisionIntent of a member decision
	// notice, frozen when the decision was made.
	DecisionIntentJSON string
	// DeliveryChannelID and DeliveryMessageID locate what was delivered.
	// For staff rows they are the appeal's queue post, which later changes
	// edit in place.
	DeliveryChannelID string
	DeliveryMessageID string
	// RefreshRequested asks for the queue post to be edited again after an
	// in-flight delivery finishes.
	RefreshRequested bool
	LastErrorCode    string
	LeaseToken       string
	LeaseExpiresAt   *time.Time
}

// AppealDecisionIntent is what a member's decision notice says, frozen when
// staff decide so the notice renders the same after a restart or a later
// settings change. Version identifies the payload format.
type AppealDecisionIntent struct {
	Version    int          `json:"version"`
	Status     AppealStatus `json:"status"`
	Reason     string       `json:"reason"`
	CaseID     string       `json:"case_id,omitempty"`
	CaseNumber uint64       `json:"case_number,omitempty"`
	GuildName  string       `json:"guild_name,omitempty"`
	// RejoinURL is the guild's invite, only on accepted appeals.
	RejoinURL string `json:"rejoin_url,omitempty"`
}

// AppealService handles appeals: members submit and follow up, staff ask for
// more information, accept, reject, close, or reopen. Members never see
// which staff member acted.
type AppealService struct {
	store AppealStore
}

// NewAppealService returns an AppealService backed by store.
func NewAppealService(store AppealStore) *AppealService {
	return &AppealService{store: store}
}

// AppealSubmissionInput is a member's answers to the guild's appeal form.
type AppealSubmissionInput struct {
	Answers []AppealAnswer `json:"answers"`
}

// AppealInformationInput is a member's reply to a request for more
// information.
type AppealInformationInput struct {
	Body string `json:"body"`
}

// AppealEventResponse is an appeal timeline entry.
type AppealEventResponse struct {
	ID                 string          `json:"id"`
	Type               AppealEventType `json:"type"`
	ActorType          string          `json:"actor_type"`
	ActorDiscordUserID string          `json:"actor_discord_user_id,omitempty"`
	Body               string          `json:"body"`
	CreatedAt          time.Time       `json:"created_at"`
}

// AppealReversalOffer is an enforcement staff may reverse after accepting
// an appeal. Accepting queues reversals of succeeded timeouts and bans
// itself, so offers only remain for actions that have none yet, such as one
// that succeeded after the appeal was accepted.
type AppealReversalOffer struct {
	OriginalExecutionID string     `json:"original_execution_id"`
	ActionType          ActionType `json:"action_type"`
}

// AppealResponse is an appeal with its form, answers, and timeline.
type AppealResponse struct {
	ID         string `json:"id"`
	GuildID    string `json:"guild_id"`
	CaseID     string `json:"case_id"`
	CaseNumber uint64 `json:"case_number"`
	// TemplateName is the rule name frozen in the case snapshot.
	TemplateName            string                `json:"template_name"`
	TargetDiscordUserID     string                `json:"target_discord_user_id"`
	Status                  AppealStatus          `json:"status"`
	Questions               []AppealQuestion      `json:"questions"`
	Answers                 []AppealAnswer        `json:"answers"`
	DecisionReason          string                `json:"decision_reason,omitempty"`
	ReviewedByDiscordUserID string                `json:"reviewed_by_discord_user_id,omitempty"`
	Events                  []AppealEventResponse `json:"events"`
	ReversalOffers          []AppealReversalOffer `json:"reversal_offers,omitempty"`
	CreatedAt               time.Time             `json:"created_at"`
	UpdatedAt               time.Time             `json:"updated_at"`
}

// Submit files an appeal for a case targeting the member. Each case can be
// appealed once.
func (s *AppealService) Submit(ctx context.Context, caseID, memberDiscordUserID string, input AppealSubmissionInput) (*AppealResponse, error) {
	const action = string(AuditActionAppealSubmit)
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	item, err := s.eligibleCase(ctx, caseID, memberDiscordUserID)
	if err != nil {
		return nil, err
	}
	settings, err := s.GetSettings(ctx, item.GuildID)
	if err != nil {
		return nil, err
	}
	answers, err := validateAnswers(settings.Questions, input.Answers)
	if err != nil {
		return nil, err
	}
	questionJSON, _ := json.Marshal(settings.Questions)
	answersJSON, _ := json.Marshal(answers)
	created, err := s.store.CreateAppeal(ctx, CreateAppealParams{
		Appeal: Appeal{
			GuildID:              item.GuildID,
			CaseID:               &item.ID,
			TargetDiscordUserID:  memberDiscordUserID,
			Status:               AppealStatusPending,
			QuestionSnapshotJSON: string(questionJSON),
			AnswersJSON:          string(answersJSON),
			Version:              1,
			MetadataJSON:         "{}",
		},
		Event: AppealEvent{
			EventType:          string(AppealEventSubmitted),
			ActorDiscordUserID: memberDiscordUserID,
			ActorType:          "member",
			Body:               "Appeal submitted",
			MetadataJSON:       "{}",
		},
		CaseEvent: CaseEvent{
			EventType:          CaseEventAppealCreated,
			ActorDiscordUserID: memberDiscordUserID,
			ActorType:          "member",
			Visibility:         EventVisibilityPublic,
			Body:               "Appeal submitted",
			MetadataJSON:       "{}",
		},
		Audit: webAudit(ctx, item.GuildID, memberDiscordUserID, 0, action, "appeal", "", AuditResultSuccess),
		Notification: AppealNotification{
			TargetDiscordUserID: memberDiscordUserID,
			Audience:            AppealNotificationStaff,
			Status:              AppealNotificationPending,
			Body:                fmt.Sprintf("A new appeal was submitted for case #%d.", item.CaseNumber),
		},
	})
	if errors.Is(err, ErrAppealAlreadyExists) {
		return nil, ErrAppealConflict
	}
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "Appeal submitted", "guild_id", created.GuildID, "case_id", created.CaseID, "appeal_id", created.ID)
	return s.response(ctx, created, true)
}

// CanSubmit checks, without creating anything, that the member could appeal
// the case now, so a Discord form is only opened for an eligible case. It
// returns the same errors Submit would, and Submit checks again.
func (s *AppealService) CanSubmit(ctx context.Context, caseID, memberDiscordUserID string) error {
	_, err := s.eligibleCase(ctx, caseID, memberDiscordUserID)
	return err
}

// eligibleCase loads a case the member may appeal now: it targets them, is
// valid, was appealable when created, and has no appeal yet. Denials are
// audited.
func (s *AppealService) eligibleCase(ctx context.Context, caseID, memberDiscordUserID string) (*Case, error) {
	const action = string(AuditActionAppealSubmit)
	caseID = strings.TrimSpace(caseID)
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	if caseID == "" || memberDiscordUserID == "" {
		return nil, appealValidationError("case and member identity are required")
	}
	item, err := s.store.GetCaseByID(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrAppealNotFound
	}
	if item.TargetDiscordUserID != memberDiscordUserID {
		_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, action, item.ID, AuditResultDenied)
		return nil, ErrAppealNotFound
	}
	if !canAppeal(*item, nil) {
		_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, action, item.ID, AuditResultDenied)
		return nil, ErrAppealCaseIneligible
	}
	existing, err := s.store.GetAppealByCaseID(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrAppealConflict
	}
	return item, nil
}

// ReviewReasonRequired reports whether the guild makes staff write a reason
// for each appeal decision. Discord reads it before a decision button is
// handled, because a form can only open as the first response.
func (s *AppealService) ReviewReasonRequired(ctx context.Context, discordGuildID string) (bool, error) {
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return false, err
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil {
		return false, err
	}
	return settings != nil && settings.AppealReviewReasonRequired, nil
}

// GetMember returns an appeal to the member who filed it.
func (s *AppealService) GetMember(ctx context.Context, appealID, memberDiscordUserID string) (*AppealResponse, error) {
	const action = string(AuditActionAppealRead)
	item, err := s.store.GetAppealByID(ctx, strings.TrimSpace(appealID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrAppealNotFound
	}
	if item.TargetDiscordUserID != strings.TrimSpace(memberDiscordUserID) {
		_ = s.auditMember(ctx, item.GuildID, memberDiscordUserID, action, item.ID, AuditResultDenied)
		return nil, ErrAppealNotFound
	}
	if err := s.auditMember(ctx, item.GuildID, memberDiscordUserID, action, item.ID, AuditResultSuccess); err != nil {
		return nil, err
	}
	return s.response(ctx, item, true)
}

// SubmitInformation adds the member's reply to a request for more
// information and puts the appeal back in the staff queue.
func (s *AppealService) SubmitInformation(ctx context.Context, appealID, memberDiscordUserID string, input AppealInformationInput) (*AppealResponse, error) {
	body := strings.TrimSpace(input.Body)
	if body == "" || len([]rune(body)) > 4000 {
		return nil, appealValidationError("information must be between 1 and 4000 characters")
	}
	item, err := s.store.GetAppealByID(ctx, strings.TrimSpace(appealID))
	if err != nil {
		return nil, err
	}
	if item == nil || item.TargetDiscordUserID != strings.TrimSpace(memberDiscordUserID) {
		return nil, ErrAppealNotFound
	}
	updated, err := s.store.AppendAppealInformation(ctx, AppendAppealInformationParams{
		AppealID:            item.ID,
		TargetDiscordUserID: memberDiscordUserID,
		Body:                body,
		Event: AppealEvent{
			EventType:          string(AppealEventInformationAdded),
			ActorDiscordUserID: memberDiscordUserID,
			ActorType:          "member",
			Body:               body,
			MetadataJSON:       "{}",
		},
		Audit: webAudit(ctx, item.GuildID, memberDiscordUserID, 0,
			string(AuditActionAppealInformationSubmit), "appeal", item.ID, AuditResultSuccess),
		Notification: AppealNotification{
			TargetDiscordUserID: memberDiscordUserID,
			Audience:            AppealNotificationStaff,
			Status:              AppealNotificationPending,
			Body:                "A member submitted additional appeal information.",
		},
	})
	if errors.Is(err, ErrAppealStateConflict) {
		return nil, ErrAppealConflict
	}
	if err != nil {
		return nil, err
	}
	return s.response(ctx, updated, true)
}

// canAppeal reports whether a member can appeal item now: its template
// allowed appeals when the case was created, the case is still valid, and it
// has no appeal yet.
func canAppeal(item Case, existing *Appeal) bool {
	return existing == nil && item.Validity == CaseValidityValid && snapshotAppealable(item.TemplateSnapshotJSON)
}

// response builds an appeal response. For members, staff identities are
// removed. For staff viewing an accepted appeal, it lists the timeouts and
// bans that could be reversed.
func (s *AppealService) response(ctx context.Context, item *Appeal, member bool) (*AppealResponse, error) {
	questions, err := decodeQuestions(item.QuestionSnapshotJSON)
	if err != nil {
		return nil, err
	}
	var answers []AppealAnswer
	if err := json.Unmarshal([]byte(item.AnswersJSON), &answers); err != nil {
		return nil, fmt.Errorf("decode appeal answers: %w", err)
	}
	events, err := s.store.ListAppealEvents(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	responseEvents := make([]AppealEventResponse, 0, len(events))
	for _, event := range events {
		actorID := event.ActorDiscordUserID
		if member && event.ActorType == "staff" {
			actorID = ""
		}
		responseEvents = append(responseEvents, AppealEventResponse{
			ID:                 event.ID,
			Type:               AppealEventType(event.EventType),
			ActorType:          event.ActorType,
			ActorDiscordUserID: actorID,
			Body:               event.Body,
			CreatedAt:          event.CreatedAt,
		})
	}
	reviewedBy := item.ReviewedByDiscordUserID
	if member {
		reviewedBy = ""
	}
	caseID := ""
	var caseNumber uint64
	var templateName string
	if item.CaseID != nil {
		caseID = *item.CaseID
		appealed, err := s.store.GetCaseByID(ctx, caseID)
		if err != nil {
			return nil, err
		}
		if appealed != nil {
			caseNumber, templateName = appealed.CaseNumber, snapshotRuleName(appealed.TemplateSnapshotJSON)
		}
	}
	response := &AppealResponse{
		ID:                      item.ID,
		GuildID:                 item.GuildID,
		CaseID:                  caseID,
		CaseNumber:              caseNumber,
		TemplateName:            templateName,
		TargetDiscordUserID:     item.TargetDiscordUserID,
		Status:                  item.Status,
		Questions:               questions,
		Answers:                 answers,
		DecisionReason:          item.DecisionReason,
		ReviewedByDiscordUserID: reviewedBy,
		Events:                  responseEvents,
		CreatedAt:               item.CreatedAt,
		UpdatedAt:               item.UpdatedAt,
	}
	if member || item.Status != AppealStatusAccepted || caseID == "" {
		return response, nil
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, caseID)
	if err != nil {
		return nil, err
	}
	reversed := map[string]bool{}
	for _, action := range actions {
		if action.ReversalOfExecutionID != nil {
			reversed[*action.ReversalOfExecutionID] = true
		}
	}
	for _, action := range actions {
		if action.Status != ActionExecutionSucceeded || action.ReversalOfExecutionID != nil || reversed[action.ID] {
			continue
		}
		if reversal, ok := reversalOf(action.ActionType); ok {
			response.ReversalOffers = append(response.ReversalOffers, AppealReversalOffer{
				OriginalExecutionID: action.ID,
				ActionType:          reversal,
			})
		}
	}
	return response, nil
}

func (s *AppealService) auditMember(ctx context.Context, guildID, memberID, action, appealID string, result AuditResult) error {
	entry := webAudit(ctx, guildID, memberID, 0, action, "appeal", appealID, result)
	return recordAudit(ctx, s.store, &entry)
}
