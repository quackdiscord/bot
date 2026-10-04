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
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestCaseNotificationCopy pins the member's wording and forbids claiming a
// failed action succeeded or inventing a timeout's end.
func TestCaseNotificationCopy(t *testing.T) {
	until := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	request := quack.CaseNotificationRequest{
		GuildName: "The Pond", RuleName: "Repeated spam", Reason: "Please stop repeating messages.", CaseNumber: 12, Appealable: true,
		Outcomes: []quack.CaseNotificationOutcome{{ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionSucceeded, TimeoutUntil: &until}},
	}
	want := discordtext.Conversation("timeout", "You’ve been timed out in **The Pond** for **Repeated spam**.", request.Reason,
		fmt.Sprintf("You can chat again <t:%d:R> — <t:%d:f>.\n\nUse the Appeal decision button below to ask the moderators to review this case.", until.Unix(), until.Unix()),
		"Case #12")
	if body := caseNotificationBody(request); body != want {
		t.Fatalf("copy changed\n%s\n%s", body, want)
	}
	request.Outcomes[0].Status = quack.ActionExecutionFailed
	if body := caseNotificationBody(request); strings.Contains(body, "You’ve been timed out") || strings.Contains(body, "You can chat again") {
		t.Fatal("false success", body)
	}
	request.Outcomes[0].Status = quack.ActionExecutionSucceeded
	request.Outcomes[0].TimeoutUntil = nil
	if body := caseNotificationBody(request); strings.Contains(body, "You can chat again") {
		t.Fatal("invented expiry", body)
	}
	request.Reason = "@everyone **quoted**"
	request.Introduction = "intro **literal**"
	request.Footer = "footer @here"
	body := caseNotificationBody(request)
	for _, literal := range []string{request.Reason, request.Introduction, request.Footer} {
		if !strings.Contains(body, discordtext.Plain(literal)) {
			t.Fatal("escaping changed", body)
		}
	}
}

// TestCaseNotificationRemovalDescribesResultingState stays accurate after a
// timeout or ban is lifted.
func TestCaseNotificationRemovalDescribesResultingState(t *testing.T) {
	for _, scenario := range []struct {
		action     quack.ActionType
		icon, lead string
	}{
		{quack.ActionRemoveTimeout, "untimeout", "Your timeout in **The Pond** has ended."},
		{quack.ActionUnbanUser, "unban", "You’re no longer banned from **The Pond**."},
	} {
		request := quack.CaseNotificationRequest{
			GuildName: "The Pond", RuleName: "Repeated spam", Reason: "Appeal accepted", CaseNumber: 12,
			Outcomes: []quack.CaseNotificationOutcome{{ActionType: scenario.action, Status: quack.ActionExecutionSucceeded}},
		}
		want := discordtext.Conversation(scenario.icon, scenario.lead, request.Reason, "This updates your case for **Repeated spam**.", "Case #12")
		if got := caseNotificationBody(request); got != want {
			t.Fatalf("inaccurate removal copy: %s; want %s", got, want)
		}
	}
}

// TestDeliverCaseNotification sends through the prepared channel with the
// appeal button, and keeps the rendered text when sending fails.
func TestDeliverCaseNotification(t *testing.T) {
	var bot *Bot
	var sent struct {
		Components []struct {
			Components []struct {
				CustomID string `json:"custom_id"`
				Label    string `json:"label"`
			} `json:"components"`
		} `json:"components"`
	}
	opens, sends := 0, 0
	failSend := false
	bot = testBot(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/users/@me/channels") {
			opens++
			return jsonResponse(request, discordgo.Channel{ID: "dm"}), nil
		}
		if !strings.HasSuffix(request.URL.Path, "/channels/dm/messages") {
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
		sends++
		if failSend {
			return nil, errors.New("connection interrupted")
		}
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &sent)
		return jsonResponse(request, discordgo.Message{ID: "dm-message", ChannelID: "dm"}), nil
	})
	request := quack.CaseNotificationRequest{TargetDiscordUserID: "member", CaseID: "case-1", Reason: "saved reason", CaseNumber: 3, Appealable: true}
	receipt, err := bot.DeliverCaseNotification(context.Background(), request)
	if err != nil || receipt.MessageID != "dm-message" || receipt.RenderedMessage != caseNotificationBody(request) || opens != 1 {
		t.Fatalf("delivery = %+v, %v (opens=%d)", receipt, err, opens)
	}
	if len(sent.Components) != 1 {
		t.Fatalf("components = %+v", sent.Components)
	}
	button := sent.Components[0].Components[0]
	if button.CustomID != "appeal:submit:v1:case-1" || button.Label != "Appeal decision" {
		t.Fatalf("unexpected appeal control: %+v", button)
	}

	failSend = true
	request.PreparedChannelDiscordID = "dm"
	request.Appealable = false
	receipt, err = bot.DeliverCaseNotification(context.Background(), request)
	classified, ok := errors.AsType[quack.DiscordError](err)
	if !ok || !classified.OutcomeUncertain || classified.Retryable {
		t.Fatalf("an uncertain send was classified as safe to repeat: %v", err)
	}
	if receipt.RenderedMessage != caseNotificationBody(request) || receipt.MessageID != "" || opens != 1 || sends != 2 {
		t.Fatalf("lost the failure receipt or reopened the DM: %+v opens=%d sends=%d", receipt, opens, sends)
	}
}

// TestCaseNotificationContext preserves visible labels and values without
// sending message-link evidence or interpreting member text as Markdown.
func TestCaseNotificationContext(t *testing.T) {
	request := quack.CaseNotificationRequest{
		Reason: "Official reason", CaseNumber: 12,
		ContextValues: []quack.CaseContextValueResponse{
			{Label: "Summary **literal**", FieldType: quack.ContextFieldShortText, Value: "@everyone {{quack:ban}}"},
			{Label: "Details", FieldType: quack.ContextFieldLongText, Value: "line one\n# line two 🦆"},
			{Label: "Confirmed", FieldType: quack.ContextFieldBoolean, Value: false},
			{Label: "Count", FieldType: quack.ContextFieldNumber, Value: -2.5},
			{Label: "Message", FieldType: quack.ContextFieldMessageLink, Value: "https://discord.com/channels/staff/evidence/private"},
			{Label: "Empty", FieldType: quack.ContextFieldShortText},
		},
	}
	body := caseNotificationBody(request)
	for _, want := range []string{
		"> " + discordtext.Plain("Summary **literal** — @everyone {{quack:ban}}"),
		"> Details — line one\n> " + discordtext.Plain("# line two 🦆"),
		"> Confirmed — No", "> Count — -2.5",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing context %q in %s", want, body)
		}
	}
	for _, forbidden := range []string{"private", "> Message", "> Empty", "Moderator:", "{{quack:ban}}"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unexpected private or interpreted context %q in %s", forbidden, body)
		}
	}
	request.ContextValues[2].Value = true
	if body := caseNotificationBody(request); !strings.Contains(body, "> Confirmed — Yes") {
		t.Fatal("true context missing", body)
	}
}

// TestLongCaseNotificationContext keeps the full member context in the
// attachment fallback, with appeal access intact and mentions suppressed.
func TestLongCaseNotificationContext(t *testing.T) {
	text := strings.Repeat("long visible context 🦆 ", 200)
	request := quack.CaseNotificationRequest{
		Reason: "Official reason", CaseNumber: 12, Appealable: true,
		ContextValues: []quack.CaseContextValueResponse{
			{Label: "Details", FieldType: quack.ContextFieldLongText, Value: text},
			{Label: "Message", FieldType: quack.ContextFieldMessageLink, Value: "private evidence link"},
		},
	}
	message := Signal("message", caseNotificationBody(request), false)
	message.Components = []discordgo.MessageComponent{Row(Button("appeal:submit:v1:case-1", "Appeal decision", discordgo.PrimaryButton, false))}
	message = message.ForApplication("")
	if utf16Len(message.Content) > contentLimit || len(message.Files) != 1 || len(message.Components) != 1 {
		t.Fatalf("notification overflow lost text or appeal access: %+v", message)
	}
	full, err := io.ReadAll(message.Files[0].Reader)
	if err != nil || !strings.Contains(string(full), discordtext.Plain(text)) || !strings.Contains(string(full), "Case #12") || strings.Contains(string(full), "private evidence") {
		t.Fatalf("unsafe or incomplete member attachment: %s, %v", full, err)
	}
	if mentions := message.sendParams().AllowedMentions; mentions == nil || len(mentions.Parse) != 0 {
		t.Fatalf("member context enables mentions: %+v", mentions)
	}
}
