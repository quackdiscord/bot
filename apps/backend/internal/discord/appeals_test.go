package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestAppealReversalRejectsInvalidControls(t *testing.T) {
	handler := appealReversal(quack.New(quack.Deps{}))
	member := &discordgo.Member{User: &discordgo.User{ID: "mod"}}
	tests := []struct {
		name     string
		customID string
		member   *discordgo.Member
		want     string
	}{
		{"no member", "appeal:reverse:v1:appeal,exec,unban_user", nil, "Open this appeal in the server’s review queue to remove the punishment."},
		{"short payload", "appeal:reverse:v1:appeal,exec", member, "That punishment button is broken. Open the case to try again."},
		{"blank appeal", "appeal:reverse:v1: ,exec,unban_user", member, "That punishment button is broken. Open the case to try again."},
		{"not a reversal", "appeal:reverse:v1:appeal,exec,ban_user", member, "Only bans and timeouts can be removed here."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			i := componentInteraction("interaction-1", test.customID)
			i.Member = test.member
			result := handler(context.Background(), i)
			if result.Task != nil || result.Response.Data.Content != "{{quack:error}} "+test.want {
				t.Fatalf("got %+v", result.Response.Data)
			}
		})
	}
	valid := componentInteraction("interaction-1", "appeal:reverse:v1:appeal,exec,unban_user")
	valid.Member = member
	if result := handler(context.Background(), valid); result.Task == nil || result.Response.Data == nil ||
		result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("valid control was not deferred privately: %+v", result)
	}
}

func TestNewRouterInstallsAppealRoutes(t *testing.T) {
	bot, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(bot, quack.New(quack.Deps{}), &memoryDeduper{})
	for _, name := range []string{appealsCommandName, templateCommandName, helpCommandName, uiPreviewCommandName} {
		if router.commands[name] == nil {
			t.Errorf("command %q not routed", name)
		}
	}
	for _, id := range []string{
		"appeal:submit:v1:case", "appeal:accept:v1:appeal", "appeal:reject:v1:appeal",
		"appeal:statement_prev:v1:2|appeal", "appeal:statement_next:v1:1|appeal", "appeal:page:v1:1",
		"appeal:reverse:v1:appeal,execution,unban_user",
	} {
		if _, ok := lookup(router.components, id); !ok {
			t.Errorf("component %s not routed", id)
		}
	}
	for _, id := range []string{"appeal:submit:v1:case", "appeal:accept_reason:v1:appeal", "appeal:reject_reason:v1:appeal", "template:create:v1:warning|0"} {
		if _, ok := lookup(router.modals, id); !ok {
			t.Errorf("modal %s not routed", id)
		}
	}
}

type appealSettingsStore struct{ channelID string }

func (s appealSettingsStore) GetGuildSettings(context.Context, string) (*quack.GuildSettings, error) {
	return &quack.GuildSettings{AppealQueueChannelDiscordID: s.channelID, AuditMirrorChannelDiscordID: "audit"}, nil
}

func (appealSettingsStore) GetGuildByID(context.Context, string) (*quack.Guild, error) {
	return &quack.Guild{DiscordGuildID: "discord-guild"}, nil
}

type stubStaffChannel struct {
	guildID, channelID string
	err                error
}

func (v *stubStaffChannel) ValidateStaffChannel(_ context.Context, guildID, channelID string) error {
	v.guildID, v.channelID = guildID, channelID
	return v.err
}

func TestAppealQueueChannelIsRevalidated(t *testing.T) {
	validator := &stubStaffChannel{err: errors.New("destination is public")}
	channels := appealChannels{store: appealSettingsStore{channelID: "queue"}, validator: validator}
	channel, discordGuild, err := channels.queueChannel(context.Background(), "internal-guild")
	if !errors.Is(err, quack.ErrAppealDeliveryDeferred) || channel != "" || discordGuild != "" {
		t.Fatalf("public appeal destination accepted: %q, %v", channel, err)
	}
	if validator.guildID != "discord-guild" || validator.channelID != "queue" {
		t.Fatalf("validated the wrong destination: %+v", validator)
	}
	unset := appealChannels{store: appealSettingsStore{}, validator: &stubStaffChannel{}}
	if _, _, err := unset.queueChannel(context.Background(), "internal-guild"); !errors.Is(err, quack.ErrAppealDeliveryDeferred) {
		t.Fatalf("missing queue channel was not deferred: %v", err)
	}
}

func TestQueueSendErrorDefersOnlyClearRefusals(t *testing.T) {
	for _, status := range []int{403, 404, 429, 500} {
		err := queueSendError(&discordgo.RESTError{Response: &http.Response{StatusCode: status}})
		if errors.Is(err, quack.ErrAppealDeliveryDeferred) != (status != 500) {
			t.Fatalf("incorrect retry classification for %d: %v", status, err)
		}
	}
	if err := queueSendError(errors.New("connection reset after writing request")); errors.Is(err, quack.ErrAppealDeliveryDeferred) {
		t.Fatal("uncertain send was retried")
	}
	if err := memberSendError("appeal_dm", &discordgo.RESTError{Response: &http.Response{StatusCode: 403}}); errors.Is(err, quack.ErrAppealDeliveryDeferred) {
		t.Fatal("closed DMs are probed forever")
	}
}

// TestPublishAppealQueueEditsOrRecreatesOnlyMissingMessages keeps one post
// per appeal: an edit that fails for any reason but a deleted message is
// retried rather than posted again, and a decided appeal loses its
// decision buttons.
func TestPublishAppealQueueEditsOrRecreatesOnlyMissingMessages(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			edits, posts := 0, 0
			bot := testBot(t, func(request *http.Request) (*http.Response, error) {
				switch request.Method {
				case http.MethodPatch:
					edits++
					raw, _ := io.ReadAll(request.Body)
					if strings.Contains(string(raw), "appeal:accept") || strings.Contains(string(raw), "appeal:reject") {
						t.Error("decided message still has decision controls")
					}
					switch status {
					case 404:
						return textResponse(request, 404, `{"code":10008,"message":"Unknown Message"}`), nil
					case 500:
						return textResponse(request, 500, `{"code":0,"message":"Server error"}`), nil
					}
					return textResponse(request, 200, `{"id":"original"}`), nil
				case http.MethodPost:
					posts++
					return textResponse(request, 200, `{"id":"replacement"}`), nil
				}
				t.Fatalf("unexpected request %s %s", request.Method, request.URL)
				return nil, nil
			})
			notifier := &AppealNotifier{bot: bot, channels: appealChannels{store: appealSettingsStore{channelID: "queue"}, validator: &stubStaffChannel{}}}
			receipt, err := notifier.PublishAppealQueue(context.Background(), "guild",
				&quack.AppealResponse{ID: "appeal", Status: quack.AppealStatusRejected},
				quack.AppealQueueReceipt{ChannelID: "queue", MessageID: "original"})
			if edits != 1 || posts != map[int]int{200: 0, 404: 1, 500: 0}[status] {
				t.Fatalf("unexpected requests: %d edits, %d posts", edits, posts)
			}
			if status == 500 {
				if !errors.Is(err, quack.ErrAppealDeliveryDeferred) {
					t.Fatalf("edit not retryable: %v", err)
				}
				return
			}
			if err != nil || receipt.ChannelID != "queue" || receipt.MessageID != map[int]string{200: "original", 404: "replacement"}[status] {
				t.Fatalf("receipt: %+v %v", receipt, err)
			}
		})
	}
}

// TestAppealDecisionCopy pins the decision DM wording, with the staff
// reason quoted literally.
func TestAppealDecisionCopy(t *testing.T) {
	for _, item := range []struct {
		status           quack.AppealStatus
		icon, lead, next string
	}{
		{quack.AppealStatusAccepted, "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."},
		{quack.AppealStatusRejected, "decline", "Your appeal was rejected.", ""},
		{quack.AppealStatusNeedsInformation, "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."},
		{quack.AppealStatusClosed, "appeal", "Your appeal was closed.", ""},
	} {
		intent := quack.AppealDecisionIntent{Version: 1, Status: item.status, Reason: "**Reason** @everyone"}
		want := discordtext.Conversation(item.icon, item.lead, discordtext.Plain(intent.Reason), item.next, "")
		if item.status == quack.AppealStatusAccepted {
			intent.RejoinURL = "https://discord.gg/original"
			want += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + intent.RejoinURL
		}
		if got := appealDecisionMessage(intent, "").Content; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

// TestAppealDecisionContext names the case and server but never the
// reviewer.
func TestAppealDecisionContext(t *testing.T) {
	for _, status := range []quack.AppealStatus{quack.AppealStatusAccepted, quack.AppealStatusRejected} {
		intent := quack.AppealDecisionIntent{Version: 1, Status: status, Reason: "Thanks for explaining.", CaseNumber: 42, CaseID: "case-id", GuildName: "Duck Pond"}
		message := appealDecisionMessage(intent, "")
		for _, want := range []string{"Your appeal was " + string(status), "Case #42 · Duck Pond", "Thanks for explaining."} {
			if !strings.Contains(message.Content, want) {
				t.Fatalf("missing %q: %s", want, message.Content)
			}
		}
		if message.Ephemeral {
			t.Fatal("member DM is ephemeral")
		}
		intent.CaseNumber = 0
		if !strings.Contains(appealDecisionMessage(intent, "").Content, "case-id") {
			t.Fatal("case ID fallback missing")
		}
	}
}

// TestAppealDecisionRejoinButton sends the Rejoin Server button only with an
// accepted appeal's invite, and checks what actually goes over the wire.
func TestAppealDecisionRejoinButton(t *testing.T) {
	for _, kind := range []string{"accepted", "unconfigured", "rejected"} {
		t.Run(kind, func(t *testing.T) {
			intent := quack.AppealDecisionIntent{Version: 1, Status: quack.AppealStatusAccepted, Reason: "Reviewed reason", RejoinURL: "https://discord.gg/saved-invite"}
			switch kind {
			case "unconfigured":
				intent.RejoinURL = ""
			case "rejected":
				intent.Status = quack.AppealStatusRejected
			}
			message := appealDecisionMessage(intent, "")
			if kind == "accepted" {
				if len(message.Components) != 1 {
					t.Fatal("button missing")
				}
				button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
				if button.Style != discordgo.LinkButton || button.Label != "Rejoin Server" || button.URL != intent.RejoinURL || button.CustomID != "" {
					t.Fatalf("wrong rejoin button %+v", button)
				}
				if !strings.Contains(message.Content, "once any ban has been removed") {
					t.Fatal("removal uncertainty lost")
				}
			} else if len(message.Components) != 0 {
				t.Fatal("unexpected rejoin control")
			}
			sends := 0
			bot := testBot(t, func(request *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(request.URL.Path, "/messages") {
					return textResponse(request, 200, `{"id":"dm"}`), nil
				}
				sends++
				var payload struct {
					Flags           discordgo.MessageFlags            `json:"flags"`
					Content         string                            `json:"content"`
					Components      []json.RawMessage                 `json:"components"`
					AllowedMentions *discordgo.MessageAllowedMentions `json:"allowed_mentions"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.Flags&discordgo.MessageFlagsEphemeral != 0 {
					t.Errorf("DM carried interaction flags: %v", payload.Flags)
				}
				if payload.Content != message.ForApplication("bot").Content || len(payload.Components) != len(message.Components) ||
					payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
					t.Errorf("REST presentation changed %+v", payload)
				}
				if kind == "accepted" && !strings.Contains(string(payload.Components[0]), "https://discord.gg/saved-invite") {
					t.Error("saved invite lost in transport")
				}
				return textResponse(request, 200, `{"id":"sent"}`), nil
			})
			notifier := NewAppealNotifier(bot, appealSettingsStore{})
			id, err := notifier.SendAppealDecision(context.Background(), "member", quack.AppealDecisionNotice{Intent: intent})
			if err != nil || id != "sent" || sends != 1 {
				t.Fatalf("expected one DM: id=%q sends=%d err=%v", id, sends, err)
			}
		})
	}
}

// TestLongAppealStatementUsesPages keeps a whole Unicode statement readable
// in pages that each keep the decision buttons, never as an attachment.
func TestLongAppealStatementUsesPages(t *testing.T) {
	appeal := &quack.AppealResponse{ID: "appeal", CaseNumber: 12, Status: quack.AppealStatusPending, Statement: strings.Repeat("🦆", 3000) + "FINAL STATEMENT"}
	const app = "968198214450831370"
	var all strings.Builder
	for page := 1; ; page++ {
		message := appealStaffPage(appeal, page, app, "").ForApplication(app)
		if len(message.Files) != 0 || len(utf16.Encode([]rune(message.Content))) > contentLimit || len(message.Components) != 2 {
			t.Fatalf("statement page %d lost paging: %+v", page, message)
		}
		all.WriteString(message.Content)
		decisions := message.Components[0].(discordgo.ActionsRow)
		if decisions.Components[0].(discordgo.Button).CustomID != "appeal:accept:v1:appeal" {
			t.Fatal("statement page lost decision identity")
		}
		if message.Components[1].(discordgo.ActionsRow).Components[1].(discordgo.Button).Disabled {
			break
		}
		if page > 100 {
			t.Fatal("statement never reached its last page")
		}
	}
	if strings.Count(all.String(), "🦆") != 3000 || !strings.Contains(all.String(), "FINAL STATEMENT") {
		t.Fatal("statement pagination lost content")
	}
}

func TestAppealStaffMessageOffersOnlyExplicitReversalControls(t *testing.T) {
	message := appealStaffMessage(&quack.AppealResponse{ID: "appeal", CaseID: "case", TargetDiscordUserID: "target", Status: quack.AppealStatusAccepted, ReversalOffers: []quack.AppealReversalOffer{{OriginalExecutionID: "execution", ActionType: quack.ActionUnbanUser}}}, "")
	if len(message.Components) != 1 || len(message.Embeds) != 0 || !strings.Contains(message.Content, "<@target>") {
		t.Fatalf("expected one explicit reversal offer: %+v", message)
	}
	button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if button.Style != discordgo.DangerButton || button.Label != "Confirm unban" || button.CustomID != "appeal:reverse:v1:appeal,execution,unban_user" {
		t.Fatalf("reversal was not an explicit confirmation control: %+v", button)
	}
}

func TestAppealStaffMessageDecisionControls(t *testing.T) {
	appeal := &quack.AppealResponse{ID: "appeal", CaseID: "case", CaseNumber: 12, TemplateName: "Spam", TargetDiscordUserID: "member", Status: quack.AppealStatusPending, Statement: "I am sorry for repeating messages."}
	pending := appealStaffMessage(appeal, "")
	for _, want := range []string{"Received an appeal from <@member>", "I am sorry", "Case #12 · Spam"} {
		if !strings.Contains(pending.Content, want) {
			t.Fatalf("missing %q: %s", want, pending.Content)
		}
	}
	row := pending.Components[0].(discordgo.ActionsRow)
	accept, reject := row.Components[0].(discordgo.Button), row.Components[1].(discordgo.Button)
	if accept.CustomID != "appeal:accept:v1:appeal" || reject.CustomID != "appeal:reject:v1:appeal" ||
		accept.Style != discordgo.SuccessButton || reject.Style != discordgo.DangerButton {
		t.Fatalf("decision controls: %+v", row)
	}
	for _, status := range []quack.AppealStatus{quack.AppealStatusAccepted, quack.AppealStatusRejected} {
		appeal.Status, appeal.ReviewedByDiscordUserID, appeal.DecisionReason = status, "reviewer", "Thanks for explaining."
		decided := appealStaffMessage(appeal, "")
		for _, want := range []string{"Appeal " + string(status), "Reviewed by <@reviewer>", appeal.DecisionReason} {
			if !strings.Contains(decided.Content, want) {
				t.Fatalf("missing %q: %s", want, decided.Content)
			}
		}
		if len(decided.Components) != 0 {
			t.Fatal("decided appeal retains buttons")
		}
	}
}

func TestAppealViewsMatchGolden(t *testing.T) {
	pending := &quack.AppealResponse{
		ID: "appeal", CaseID: "case", CaseNumber: 12, TemplateName: "Spam", TargetDiscordUserID: "target",
		Status: quack.AppealStatusPending, Statement: "Please *reconsider*.",
	}
	accepted := &quack.AppealResponse{
		ID: "appeal", CaseID: "case", CaseNumber: 12, TemplateName: "Spam", TargetDiscordUserID: "target",
		Status: quack.AppealStatusAccepted, ReviewedByDiscordUserID: "moderator", DecisionReason: "Fair point.",
		ReversalOffers: []quack.AppealReversalOffer{{OriginalExecutionID: "execution", ActionType: quack.ActionUnbanUser}},
	}
	const (
		staffURL  = "https://dash.example/guilds/discord-guild/appeals/appeal"
		memberURL = "https://dash.example/guilds/guild/cases/case/appeal"
	)
	out := map[string]any{
		"appeal_staff_pending":  appealStaffMessage(pending, staffURL),
		"appeal_staff_accepted": appealStaffMessage(accepted, staffURL),
		"appeal_staff_no_link":  appealStaffMessage(pending, ""),
		"appeal_decision_accepted": appealDecisionMessage(quack.AppealDecisionIntent{
			Version: 1, Status: quack.AppealStatusAccepted, Reason: "Fair point.", CaseNumber: 12,
			GuildName: "Duck Pond", RejoinURL: "https://discord.gg/pond",
		}, memberURL),
		"appeal_decision_rejected": appealDecisionMessage(quack.AppealDecisionIntent{
			Version: 1, Status: quack.AppealStatusRejected, Reason: "The case stands.", CaseNumber: 12, GuildName: "Duck Pond",
		}, memberURL),
		"appeal_decision_needs_information": appealDecisionMessage(quack.AppealDecisionIntent{
			Version: 1, Status: quack.AppealStatusNeedsInformation, Reason: "Which message do you mean?", CaseNumber: 12, GuildName: "Duck Pond",
		}, memberURL),
		"appeal_decision_no_link": appealDecisionMessage(quack.AppealDecisionIntent{
			Version: 1, Status: quack.AppealStatusRejected, Reason: "The case stands.", CaseNumber: 12, GuildName: "Duck Pond",
		}, ""),
		"template_view": templatePolicyMessage(quack.TemplateResponse{
			Name: "Spam", ReasonTemplate: "Keep chat readable.", Appealable: true, CaseDecayDays: 30,
			Levels: []quack.TemplateLevelResponse{
				{TemplateLevelDetails: quack.TemplateLevelDetails{TriggerCaseCount: 3, NotifyUser: false},
					Actions: []quack.TemplateActionResponse{{ActionType: quack.ActionBanUser}}},
				{TemplateLevelDetails: quack.TemplateLevelDetails{IsDefault: true, NotifyUser: true}},
				{TemplateLevelDetails: quack.TemplateLevelDetails{TriggerCaseCount: 2, NotifyUser: true},
					Actions: []quack.TemplateActionResponse{{ActionType: quack.ActionTimeoutUser, TimeoutDurationSeconds: 300}}},
			},
		}),
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	AssertGolden(t, "testdata/appeals.golden.json", string(body))
}
