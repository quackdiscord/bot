package discord

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestMessagesMatchGolden renders every case view and the appeal entry and
// compares them with testdata/views.golden.json (appeal and template views
// are in TestAppealViewsMatchGolden), so copy, layout, and custom IDs cannot
// drift unnoticed. Regenerate the golden file only for a deliberate change.
func TestMessagesMatchGolden(t *testing.T) {
	level := func(name string, position int) *quack.CaseSelectedLevel {
		return &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: name, Position: position}}
	}
	until := time.Unix(1700086400, 0)
	receipt := &quack.CaseReceipt{
		CaseID: "case-1", CaseNumber: 7, TargetDiscordUserID: "target", ModeratorDiscordUserID: "moderator",
		RuleName: "Spam", Reason: "Keep chat readable.", CreatedAt: time.Unix(1700000000, 0),
		ContextValues: []quack.CaseContextValueResponse{{Key: "context", Label: "Context", Value: "Posted six links"}},
		Actions: []quack.CaseReceiptAction{
			{ID: "a1", ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionSucceeded, TimeoutUntil: &until},
			{ID: "a2", ActionType: quack.ActionBanUser, Status: quack.ActionExecutionPending},
		},
		EvidenceIncomplete: true,
	}
	voidedReceipt := *receipt
	voidedReceipt.Validity = quack.CaseValidityVoided
	voidedReceipt.Actions = nil
	detail := &quack.CaseDetailResponse{
		CaseResponse: quack.CaseResponse{
			ID: "case-1", CaseNumber: 7, TargetDiscordUserID: "target", Reason: "Official **reason**",
			ModeratorDiscordUserID: "moderator", CreatedAt: time.Unix(1700000000, 0),
			Validity: quack.CaseValidityValid, Source: quack.CaseSourceDiscord, SelectedLevel: level("Timeout", 0),
			ContextValues: []quack.CaseContextValueResponse{{Key: "details", Label: "Details", Value: "Visible context"}},
		},
		Actions: []quack.CaseActionDetailResponse{{
			CaseActionResponse: quack.CaseActionResponse{
				ID: "action-1", ActionType: quack.ActionBanUser, Status: quack.ActionExecutionSucceeded,
			},
			AttemptCount:  2,
			LastErrorCode: "permission_denied",
		}},
		Evidence: []quack.CaseEvidenceResponse{
			{
				MessageURL:     "https://discord.com/channels/1/2/3",
				CaptureOutcome: "captured",
				Attachments: []quack.CaseEvidenceAttachmentResponse{
					{Filename: "a.png", OriginalURL: "https://cdn/a.png", CopyOutcome: "copied"},
				},
			},
			{CaptureOutcome: "deleted"},
		},
		Events:       []quack.CaseEventResponse{{EventType: quack.CaseEventCreated, Body: "Case created", CreatedAt: time.Unix(1700000000, 0)}},
		Notification: &quack.CaseNotificationResponse{Status: "sent"},
	}
	list := &quack.CaseListResponse{Total: 21, Cases: []quack.CaseResponse{
		{CaseNumber: 9, TargetDiscordUserID: "member", Validity: quack.CaseValidityVoided, RuleName: "Spam"},
		{CaseNumber: 8, TargetDiscordUserID: "other", Validity: quack.CaseValidityValid, CreatedAt: time.Unix(1700000000, 0)},
	}}
	failed := &quack.FailedCaseActionResult{Total: 12, Executions: []quack.CaseActionExecution{
		{ID: "exec-1", CaseID: "case-1", ActionType: quack.ActionKickUser, LastErrorCode: "kick_permission_or_hierarchy_denied"},
		{ID: "exec-2", CaseID: "case-2", ActionType: quack.ActionBanUser},
	}}
	entry, err := appealEntryMessage("https://dash.example/base/", "guild", "case-1")
	if err != nil {
		t.Fatal(err)
	}
	profile := &quack.CaseProfileResponse{CaseListResponse: *list, Summary: quack.CaseProfileSummary{
		Total: 21, ByValidity: map[string]int64{"valid": 20, "voided": 1},
	}}
	voided := &quack.CaseResponse{CaseNumber: 7, Actions: []quack.CaseActionResponse{
		{ActionType: quack.ActionUnbanUser, Status: quack.ActionExecutionPending},
	}}
	evidence := &quack.CaseEvidencePage{
		CaseID: "case-1", CaseNumber: 7, Position: 2, Total: 3,
		ContextValues: []quack.CaseContextValueResponse{{Key: "context", Label: "Context", Value: "Moderator note"}},
		Evidence:      &detail.Evidence[0],
	}
	audit := func(message quack.AuditMirrorMessage) Message {
		message.OccurredAt = time.Unix(1700000000, 0)
		message.DiscordGuildID = "discord-guild"
		return auditMirrorMessage(message, quack.NewDashboardLinks("https://dash.example"))
	}
	out := map[string]any{
		"case_receipt":          caseReceiptMessage(receipt, "https://dash.example/guilds/discord-guild/cases/case-1"),
		"case_receipt_voided":   caseReceiptMessage(&voidedReceipt, ""),
		"case_receipt_nil":      caseReceiptMessage(nil, ""),
		"case_voided":           caseVoidedMessage(voided),
		"case_detail":           caseDetailPage(detail, 1, ""),
		"case_detail_nil":       caseDetailMessage(nil),
		"case_evidence":         evidencePageMessage(evidence, 1, ""),
		"case_list_user":        caseListMessage(list, 2, "member"),
		"case_list_empty":       caseListMessage(nil, 0, ""),
		"case_profile":          caseProfileMessage(profile, 1, "member"),
		"failures":              failedActionMessage(failed, 1),
		"failures_empty":        failedActionMessage(nil, 1),
		"template_picker":       templatePicker([]quack.TemplateResponse{{ID: "t1", Name: "Spam", Description: "Repeated messages"}}, caseTarget{userID: "member"}, 0),
		"appeal_entry":          entry,
		"audit_case_create":     audit(quack.AuditMirrorMessage{ActorDiscordUserID: "moderator", Action: "case.create", Result: quack.AuditResultSuccess, CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "member", RuleName: "Spam", SelectedLevelName: "Third case", SelectedOutcome: "Timeout (24h)"}),
		"audit_action_failed":   audit(quack.AuditMirrorMessage{ActorDiscordUserID: "quack-system", Action: "case_action.failed", Result: quack.AuditResultFailure, FailureReason: "ban_permission_denied", CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "member", RuleName: "Spam", ActionType: quack.ActionBanUser, RetryExecutionID: "execution"}),
		"audit_reversal_noop":   audit(quack.AuditMirrorMessage{Action: "case_action.succeeded", Result: quack.AuditResultSuccess, ActionType: quack.ActionRemoveTimeout, ReversalNoop: true}),
		"audit_unknown":         audit(quack.AuditMirrorMessage{ActorDiscordUserID: "moderator", Action: "evidence.capture", Result: quack.AuditResultSuccess}),
		"case_notification_dm":  caseNotificationBody(quack.CaseNotificationRequest{GuildName: "The Pond", RuleName: "Spam", Reason: "Keep chat readable.", CaseNumber: 7, CreatedAt: time.Unix(1700000000, 0), Appealable: true, Outcomes: []quack.CaseNotificationOutcome{{ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionSucceeded, TimeoutUntil: &until}}}),
		"error_response":        Error("Nope"),
		"case_detail_long_page": caseDetailPage(&quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case-2", CaseNumber: 8, ContextValues: []quack.CaseContextValueResponse{{Label: "Context", Value: strings.Repeat("long context ", 200)}}}}, 2, ""),
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	AssertGolden(t, "testdata/views.golden.json", string(body))
}

func TestAppealEntryLinksOnlySafeDestinations(t *testing.T) {
	for _, test := range []struct{ base, guild, caseID string }{
		{"", "guild", "case"},
		{"ftp://dashboard.example", "guild", "case"},
		{"https://dashboard.example/base?secret=drop", "guild", "case"},
		{"https://dashboard.example", "guild id", "case"},
		{"https://dashboard.example", "guild", "../case"},
	} {
		if _, err := appealEntryMessage(test.base, test.guild, test.caseID); err == nil {
			t.Fatalf("unsafe appeal entry accepted: %+v", test)
		}
	}
	message, err := appealEntryMessage("https://dashboard.example/base/", "guild", "case")
	if err != nil {
		t.Fatal(err)
	}
	button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if button.Style != discordgo.LinkButton || button.URL != "https://dashboard.example/base/guilds/guild/cases/case/appeal" {
		t.Fatalf("unexpected appeal link: %+v", button)
	}
}

func TestCaseDetailOffersReverseOrRecovery(t *testing.T) {
	primary := []string{"case:edit_context:", "case:evidence:", "case:user_detail:", "case:void:"}
	for status, recovery := range map[quack.ActionExecutionStatus][]string{
		quack.ActionExecutionFailed:    {"case:retry:", "case:dismiss:"},
		quack.ActionExecutionSucceeded: {"case:reverse:v1:case-1|action-1|remove_timeout"},
	} {
		detail := &quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case-1", TargetDiscordUserID: "member"}, Actions: []quack.CaseActionDetailResponse{
			{CaseActionResponse: quack.CaseActionResponse{ID: "action-1", ActionType: quack.ActionTimeoutUser, Status: status}},
		}}
		var buttons []discordgo.MessageComponent
		for _, row := range caseDetailMessage(detail).Components {
			buttons = append(buttons, row.(discordgo.ActionsRow).Components...)
		}
		want := append(append([]string(nil), primary...), recovery...)
		if len(buttons) != len(want) {
			t.Fatalf("%s: got %d controls, want %d", status, len(buttons), len(want))
		}
		for i, prefix := range want {
			if id := buttons[i].(discordgo.Button).CustomID; !strings.HasPrefix(id, prefix) {
				t.Errorf("%s: control %d is %q, want prefix %q", status, i, id, prefix)
			}
		}
	}
}

// TestReceiptShowsDecisionNotStaffDetails keeps the public receipt to the
// decision, its progress, and staff-written context: never the escalation
// level, failure codes, or DM status.
func TestReceiptShowsDecisionNotStaffDetails(t *testing.T) {
	sent := quack.NotificationFailed
	receipt := &quack.CaseReceipt{
		CaseID: "case", CaseNumber: 4, TargetDiscordUserID: "member", RuleName: "Spam", Reason: "Do not spam",
		SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "STAFF LEVEL"}},
		Actions:       []quack.CaseReceiptAction{{ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionFailed}},
		Notification:  &sent, EvidenceIncomplete: true,
	}
	message := caseReceiptMessage(receipt, "")
	for _, hidden := range []string{"STAFF LEVEL", "DM"} {
		if strings.Contains(message.Content, hidden) {
			t.Fatalf("receipt shows %q: %s", hidden, message.Content)
		}
	}
	for _, want := range []string{"Do not spam", "**Spam**", "{{quack:error}}", "Some evidence couldn’t be saved"} {
		if !strings.Contains(message.Content, want) {
			t.Fatalf("receipt missing %q: %s", want, message.Content)
		}
	}
	receipt.Validity = quack.CaseValidityVoided
	message = caseReceiptMessage(receipt, "")
	if !strings.Contains(strings.SplitN(message.Content, "\n", 2)[0], "**Voided**") {
		t.Fatalf("void not visible in the lead: %s", message.Content)
	}
	if !message.Components[0].(discordgo.ActionsRow).Components[3].(discordgo.Button).Disabled {
		t.Fatal("voided receipt still offers Void case")
	}
}

// TestLongReceiptOffersFullCase cuts a long receipt to one page and links
// the full record.
func TestLongReceiptOffersFullCase(t *testing.T) {
	receipt := &quack.CaseReceipt{CaseID: "case", CaseNumber: 4, ContextValues: []quack.CaseContextValueResponse{
		{Label: "Context", Value: strings.Repeat("context ", 400)},
	}}
	message := caseReceiptMessage(receipt, "").ForApplication("")
	if len(message.Files) != 0 || utf16Len(message.Content) > contentLimit || len(message.Components) != 2 {
		t.Fatalf("long receipt left Discord's limits: %d units, %d files", utf16Len(message.Content), len(message.Files))
	}
	button := message.Components[1].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if button.Label != "View full case" || button.CustomID != "case:view:v1:case" {
		t.Fatalf("unexpected full case control: %+v", button)
	}
}

// TestLongCaseDetailRetainsContextAndRecovery checks that the complete staff
// record can be read without downloading a file or losing its retry controls.
func TestLongCaseDetailRetainsContextAndRecovery(t *testing.T) {
	detail := &quack.CaseDetailResponse{
		CaseResponse: quack.CaseResponse{ID: "case-1", CaseNumber: 9, Reason: "Spam", ContextValues: []quack.CaseContextValueResponse{{Label: "Context", Value: strings.Repeat("🦆 long context\n", 500) + "FINAL CONTEXT"}}},
		Actions:      []quack.CaseActionDetailResponse{{CaseActionResponse: quack.CaseActionResponse{ID: "failed-1", ActionType: quack.ActionBanUser, Status: quack.ActionExecutionFailed}}},
	}
	var contents strings.Builder
	for page := 1; ; page++ {
		message := caseDetailPage(detail, page, "819019613371236432").ForApplication("819019613371236432")
		if message.Ephemeral || len(message.Files) != 0 || utf16Len(message.Content) > contentLimit || len(message.Components) != 3 {
			t.Fatalf("page %d lost native content or controls: %+v", page, message)
		}
		contents.WriteString(message.Content)
		recovery := message.Components[1].(discordgo.ActionsRow)
		if recovery.Components[0].(discordgo.Button).Label != "Retry" {
			t.Fatal("retry control missing")
		}
		navigation := message.Components[2].(discordgo.ActionsRow)
		if navigation.Components[1].(discordgo.Button).Disabled {
			break
		}
		if page > 100 {
			t.Fatal("case detail has no last page")
		}
	}
	if !strings.Contains(contents.String(), "FINAL CONTEXT") {
		t.Fatal("long context was truncated")
	}
}

// TestVoidedCaseDoesNotInviteAnotherAppeal keeps the staff detail consistent
// with the void an accepted appeal makes.
func TestVoidedCaseDoesNotInviteAnotherAppeal(t *testing.T) {
	detail := &quack.CaseDetailResponse{
		CaseResponse:     quack.CaseResponse{ID: "case-1", CaseNumber: 1, Validity: quack.CaseValidityValid},
		TemplateSnapshot: &quack.CaseTemplateSnapshotResponse{},
	}
	detail.TemplateSnapshot.Template.Appealable = true
	if !strings.Contains(caseDetailMessage(detail).Content, "The member can appeal this case.") {
		t.Fatal("valid appealable case lost its appeal guidance")
	}
	detail.Validity = quack.CaseValidityVoided
	message := caseDetailMessage(detail)
	if strings.Contains(message.Content, "The member can appeal this case.") || !strings.Contains(message.Content, "This case was voided") {
		t.Fatalf("voided case has misleading guidance: %s", message.Content)
	}
	if !message.Components[0].(discordgo.ActionsRow).Components[3].(discordgo.Button).Disabled {
		t.Fatal("voided case still offers an enabled void control")
	}
}

// TestCaseHistoryNamesTheRule keeps a case recognizable by its rule rather
// than an internal escalation label.
func TestCaseHistoryNamesTheRule(t *testing.T) {
	message := caseListMessage(&quack.CaseListResponse{Total: 1, Cases: []quack.CaseResponse{{
		CaseNumber: 4, RuleName: "Spam", TargetDiscordUserID: "member", CreatedAt: time.Unix(1700000000, 0),
		SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "Case 2 onward"}},
	}}}, 1, "")
	if !strings.Contains(message.Content, "Spam") || strings.Contains(message.Content, "Case 2 onward") || !strings.Contains(message.Content, "\n-# <t:1700000000:R>") {
		t.Fatal(message.Content)
	}
}

// TestCaseHistoryWorstCaseLabelsStayNative bounds a page after icon
// expansion while keeping every row, tag, date, summary, and page control.
func TestCaseHistoryWorstCaseLabelsStayNative(t *testing.T) {
	for _, name := range []string{strings.Repeat("😀", 100), strings.Repeat("_*~`", 25), strings.Repeat("{{quack:history}}", 6)} {
		profile := &quack.CaseProfileResponse{CaseListResponse: quack.CaseListResponse{Total: 100, Limit: 10}, Summary: quack.CaseProfileSummary{Total: 100, ByValidity: map[string]int64{"valid": 90, "voided": 10}}}
		for i := range 10 {
			profile.Cases = append(profile.Cases, quack.CaseResponse{
				CaseNumber: uint64(18446744073709551600) + uint64(i), TargetDiscordUserID: "12345678901234567890",
				Validity: quack.CaseValidityVoided, CreatedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), RuleName: name,
			})
		}
		for _, message := range []Message{caseListMessage(&profile.CaseListResponse, 1, ""), caseProfileMessage(profile, 1, "12345678901234567890")} {
			for _, app := range []string{"", "968198214450831370", "819019613371236432"} {
				prepared := message.ForApplication(app)
				if utf16Len(prepared.Content) > contentLimit || len(prepared.Files) != 0 || len(prepared.Components) == 0 {
					t.Fatalf("history left native text: units=%d files=%d", utf16Len(prepared.Content), len(prepared.Files))
				}
				for _, item := range profile.Cases {
					if !strings.Contains(prepared.Content, fmt.Sprintf("**#%d**", item.CaseNumber)) {
						t.Fatal("history row truncated away")
					}
				}
				if strings.Count(prepared.Content, "**Voided**") != 10 || !strings.Contains(prepared.Content, "Page 1/10") {
					t.Fatal("row tags or pagination lost")
				}
			}
		}
	}
}

// TestImportedCaseViewsPreserveHistoricalOutcome ensures imported history
// never reads as a new warning.
func TestImportedCaseViewsPreserveHistoricalOutcome(t *testing.T) {
	for action, label := range map[string]string{"warning": "Warning", "ban": "Ban", "kick": "Kick", "unban": "Unban", "timeout": "Timeout", "message_delete": "Message deletion"} {
		item := quack.CaseResponse{CaseNumber: 9, TargetDiscordUserID: "member", Source: quack.CaseSourceV4Import, Reason: "Original reason", ContextURL: "https://discord.com/channels/1/2/3", Metadata: map[string]any{"v4": map[string]any{"action_type": action}}}
		detail := caseDetailMessage(&quack.CaseDetailResponse{CaseResponse: item})
		list := caseListMessage(&quack.CaseListResponse{Cases: []quack.CaseResponse{item}, Total: 1}, 1, "member")
		for _, content := range []string{detail.Content, list.Content} {
			if !strings.Contains(content, label) || !strings.Contains(content, "Imported v4") || strings.Contains(content, "Warning recorded.") {
				t.Fatalf("misleading imported history: %s", content)
			}
		}
	}
	for _, unsafe := range []string{"javascript:alert(1)", "https://name:secret@example.com/", "//example.com"} {
		if historicalContextLink(unsafe) != "" {
			t.Fatalf("unsafe context rendered: %q", unsafe)
		}
	}
}

// TestEvidencePagesStayNative pages long evidence and context without
// losing text or spilling into a file, and steps into neighboring items.
func TestEvidencePagesStayNative(t *testing.T) {
	page := &quack.CaseEvidencePage{
		CaseID: "case", CaseNumber: 7, Position: 1, Total: 2,
		ContextValues: []quack.CaseContextValueResponse{{Label: "Context", Value: strings.Repeat("Moderator note. ", 300) + "FINAL NOTE"}},
		Evidence:      &quack.CaseEvidenceResponse{CaptureOutcome: "uploaded", Attachments: []quack.CaseEvidenceAttachmentResponse{{Filename: "proof.png", PreservedURL: "https://discord.com/channels/g/c/m", CopyOutcome: "preserved"}}},
	}
	pages := evidencePages(page, "")
	var content string
	for i := range pages {
		message := evidencePageMessage(page, i+1, "").ForApplication("")
		if len(message.Files) != 0 || utf16Len(message.Content) > contentLimit {
			t.Fatal("evidence left a native page")
		}
		next := message.Components[0].(discordgo.ActionsRow).Components[1].(discordgo.Button)
		if next.Disabled {
			t.Fatal("Next disabled before the last evidence item")
		}
		content += message.Content
	}
	for _, want := range []string{"FINAL NOTE", "https://discord.com/channels/g/c/m", "Evidence 1 of 2"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q", want)
		}
	}
	empty := evidencePageMessage(&quack.CaseEvidencePage{CaseID: "case", CaseNumber: 7, Position: 1}, 1, "")
	if !strings.Contains(empty.Content, "No evidence has been added yet.") || len(empty.Components) != 0 {
		t.Fatalf("empty evidence view: %+v", empty)
	}
}

// TestEvidenceSummaryCaptureLabels keeps headings readable while retaining
// captured content, links, attachment outcomes, and warnings.
func TestEvidenceSummaryCaptureLabels(t *testing.T) {
	for outcome, label := range map[string]string{
		"uploaded": "Uploaded file", "captured": "Captured message",
		"unavailable": "Capture unavailable", "deleted": "Message deleted or missing",
		"inaccessible": "Message inaccessible", "": "Evidence", "future_status": "Evidence",
	} {
		item := quack.CaseEvidenceResponse{CaptureOutcome: outcome, Content: "Captured text", CaptureWarning: "Capture warning", Attachments: []quack.CaseEvidenceAttachmentResponse{{Filename: "proof.png", PreservedURL: "https://example.com/proof.png", CopyOutcome: "preserved", Warning: "Attachment warning"}}}
		text := evidenceSummary([]quack.CaseEvidenceResponse{item})
		if !strings.HasPrefix(text, label+"\n") {
			t.Fatalf("wrong capture heading: %s", text)
		}
		for _, retained := range []string{"Captured text", "Capture warning", "[proof.png](https://example.com/proof.png) · Saved copy", "Attachment warning"} {
			if !strings.Contains(text, retained) {
				t.Fatalf("lost evidence detail %q: %s", retained, text)
			}
		}
	}
}

func TestAuditMirrorEntries(t *testing.T) {
	failed := auditMirrorMessage(quack.AuditMirrorMessage{
		ActorDiscordUserID: "quack-system", Action: "case_action.failed", Result: quack.AuditResultFailure,
		CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "123", RuleName: "Spam", ActionType: quack.ActionBanUser,
		RetryExecutionID: "execution", CorrelationID: "internal-correlation", MetadataJSON: `{"private":"not-for-display"}`,
	}, quack.DashboardLinks{})
	for _, want := range []string{"Ban action failed.", "-# Case #42 · <@123> · Spam", "-# By Quack"} {
		if !strings.Contains(failed.Content, want) {
			t.Fatalf("missing %q: %s", want, failed.Content)
		}
	}
	for _, hidden := range []string{"<@quack-system>", "internal-correlation", "not-for-display"} {
		if strings.Contains(failed.Content, hidden) {
			t.Fatalf("leaked %s", hidden)
		}
	}
	button := failed.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if button.Label != "Retry action" || button.CustomID != "case:retry:v1:execution" {
		t.Fatalf("wrong recovery control: %+v", button)
	}

	created := auditMirrorMessage(quack.AuditMirrorMessage{
		ActorDiscordUserID: "moderator", Action: "case.create", Result: quack.AuditResultSuccess, CaseID: "case",
		CaseNumber: 42, TargetDiscordUserID: "member", RuleName: "Spam", SelectedLevelName: "Third case",
		SelectedOutcome: "Timeout (24h)", OccurredAt: time.Unix(1700000000, 0),
	}, quack.DashboardLinks{})
	for _, want := range []string{"<@moderator> added a case.", "Level: Third case", "Outcome: Timeout (24h)", "<t:1700000000:R>"} {
		if !strings.Contains(created.Content, want) {
			t.Fatalf("missing %q: %s", want, created.Content)
		}
	}
	// Every detail is subtext directly under the event.
	for _, line := range strings.Split(created.Content, "\n")[1:] {
		if !strings.HasPrefix(line, "-# ") {
			t.Fatalf("detail is not adjacent subtext: %q", created.Content)
		}
	}
	noop := auditMirrorMessage(quack.AuditMirrorMessage{Action: "case_action.succeeded", ActionType: quack.ActionRemoveTimeout, Result: quack.AuditResultSuccess, ReversalNoop: true}, quack.DashboardLinks{})
	if !strings.Contains(noop.Content, "had already ended") || strings.Contains(noop.Content, "completed") {
		t.Fatal(noop.Content)
	}
	settings := auditMirrorMessage(quack.AuditMirrorMessage{ActorDiscordUserID: "moderator", Action: "guild_settings.update", Result: quack.AuditResultSuccess, ResourceType: "guild_settings", ResourceID: "internal-settings-id"}, quack.DashboardLinks{})
	if !strings.Contains(settings.Content, "updated Quack settings") || strings.Contains(settings.Content, "internal-settings-id") {
		t.Fatal(settings.Content)
	}
}

func TestWebLinkAcceptsOnlySafeDestinations(t *testing.T) {
	message := Message{Content: "case"}
	for _, base := range []string{"", "ftp://dash.example", "https://user:pw@dash.example", "https://dash.example?x=1", "https://dash.example#frag"} {
		if got := (&cases{dashboard: quack.NewDashboardLinks(base)}).webLink(message, "guild", "cases", "case"); len(got.Components) != 0 {
			t.Fatalf("%q produced a link", base)
		}
	}
	c := &cases{dashboard: quack.NewDashboardLinks("https://dash.example/app/")}
	if got := c.webLink(message, "guild", "cases", "../evil"); len(got.Components) != 0 {
		t.Fatal("path traversal produced a link")
	}
	if got := c.webLink(message, "guild", "members", ""); len(got.Components) != 0 {
		t.Fatal("member link without a member")
	}
	full := Message{Components: make([]discordgo.MessageComponent, 5)}
	if got := c.webLink(full, "guild", "cases"); len(got.Components) != 5 {
		t.Fatal("link added past Discord's five rows")
	}
	got := c.webLink(message, "guild", "members", "member")
	button := got.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if button.URL != "https://dash.example/app/guilds/guild/members/member" || button.Label != "Open in dashboard" || len(message.Components) != 0 {
		t.Fatalf("unexpected link: %+v", button)
	}
}

// TestAuditMirrorLinksDashboardPages links each entry to the page it is
// about, next to the Retry action control when there is one.
func TestAuditMirrorLinksDashboardPages(t *testing.T) {
	dashboard := quack.NewDashboardLinks("https://dash.example")
	const guild = "https://dash.example/guilds/discord-guild"
	for _, test := range []struct {
		entry      quack.AuditMirrorMessage
		url, label string
	}{
		{quack.AuditMirrorMessage{Action: "appeal.accepted", ResourceType: "appeal", ResourceID: "appeal-1", CaseID: "case-1"}, guild + "/appeals/appeal-1", "View appeal"},
		{quack.AuditMirrorMessage{Action: "appeal.submit", ResourceType: "appeal"}, guild + "/appeals", "Open in dashboard"},
		{quack.AuditMirrorMessage{Action: "case.void", ResourceType: "case", ResourceID: "case-1", CaseID: "case-1"}, guild + "/cases/case-1", "Open case"},
		{quack.AuditMirrorMessage{Action: "case_template.update", ResourceType: "case_template", ResourceID: "rule-1"}, guild + "/rules/rule-1", "Open rule"},
		{quack.AuditMirrorMessage{Action: "case_template.import", ResourceType: "case_template"}, guild + "/rules", "Open in dashboard"},
		{quack.AuditMirrorMessage{Action: "guild_settings.update", ResourceType: "guild_settings", ResourceID: "settings-1"}, guild + "/settings", "Open settings"},
		{quack.AuditMirrorMessage{Action: "ticket.open", ResourceType: "ticket", ResourceID: "ticket-1"}, guild + "/modules/tickets", "Open in dashboard"},
		{quack.AuditMirrorMessage{Action: "general_logging.settings.update"}, guild + "/modules/logging", "Open settings"},
		{quack.AuditMirrorMessage{Action: "honeypot.settings.update"}, guild + "/modules/honeypot", "Open settings"},
		{quack.AuditMirrorMessage{Action: "guild.lifecycle.bootstrap", ResourceType: "guild"}, "", ""},
	} {
		test.entry.DiscordGuildID, test.entry.Result = "discord-guild", quack.AuditResultSuccess
		message := auditMirrorMessage(test.entry, dashboard)
		if test.url == "" {
			if len(message.Components) != 0 {
				t.Fatalf("%s: unexpected link %+v", test.entry.Action, message.Components)
			}
			continue
		}
		button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
		if button.URL != test.url || button.Label != test.label || button.Style != discordgo.LinkButton {
			t.Fatalf("%s: got %+v", test.entry.Action, button)
		}
	}
	failed := auditMirrorMessage(quack.AuditMirrorMessage{
		DiscordGuildID: "discord-guild", Action: "case_action.failed", Result: quack.AuditResultFailure,
		ResourceType: "case_action_execution", CaseID: "case-1", RetryExecutionID: "execution",
	}, dashboard)
	row := failed.Components[0].(discordgo.ActionsRow)
	if len(failed.Components) != 1 || len(row.Components) != 2 ||
		row.Components[0].(discordgo.Button).CustomID != "case:retry:v1:execution" ||
		row.Components[1].(discordgo.Button).URL != guild+"/cases/case-1" {
		t.Fatalf("failed action controls: %+v", failed.Components)
	}
}

func TestTemplatePickerReachesEveryTemplate(t *testing.T) {
	templates := make([]quack.TemplateResponse, 51)
	for i := range templates {
		templates[i] = quack.TemplateResponse{ID: fmt.Sprintf("template-%d", i), Name: fmt.Sprintf("Rule %d", i)}
	}
	for _, target := range []caseTarget{{userID: "489264179472236557"}, {userID: "489264179472236557", channelID: "1005778938108325970", messageID: "1005778938108325971"}} {
		seen := map[string]bool{}
		for page := range 3 {
			message := templatePicker(templates, target, page)
			menu := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
			id, err := DecodeCustomID(menu.CustomID)
			if err != nil || id.Payload != target.payload() {
				t.Fatalf("selection lost target: %+v, %v", id, err)
			}
			for _, option := range menu.Options {
				if seen[option.Value] {
					t.Fatalf("duplicate template %s", option.Value)
				}
				seen[option.Value] = true
			}
			for i, component := range message.Components[1].(discordgo.ActionsRow).Components {
				button := component.(discordgo.Button)
				next := max(0, page-1)
				if i == 1 {
					next = page + 1
				}
				id, err := DecodeCustomID(button.CustomID)
				if err != nil || id.Payload != fmt.Sprintf("%s|%d|%s", target.kind(), next, target.payload()) {
					t.Fatalf("navigation lost target: %+v, %v", id, err)
				}
				if button.Disabled != ((i == 0 && page == 0) || (i == 1 && page == 2)) {
					t.Fatal("incorrect boundary button")
				}
				parsed, ok := parseCaseTarget(target.kind(), target.payload())
				if !ok || parsed != target {
					t.Fatalf("target did not round trip: %+v", parsed)
				}
			}
		}
		if len(seen) != len(templates) {
			t.Fatalf("reached %d of %d templates", len(seen), len(templates))
		}
	}
	stale := templatePicker(templates[:1], caseTarget{userID: "u"}, 99)
	if len(stale.Components) != 1 || len(stale.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu).Options) != 1 {
		t.Fatal("stale page did not clamp to the remaining template")
	}
	if empty := templatePicker(nil, caseTarget{userID: "u"}, 0); len(empty.Components) != 0 {
		t.Fatal("empty list rendered a select menu")
	}
}
