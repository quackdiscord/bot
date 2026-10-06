package tickets

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// buttons returns every button in components, row by row.
func buttons(t *testing.T, components []discordgo.MessageComponent) []discordgo.Button {
	t.Helper()
	var out []discordgo.Button
	for _, row := range components {
		for _, component := range row.(discordgo.ActionsRow).Components {
			out = append(out, component.(discordgo.Button))
		}
	}
	return out
}

func decode(t *testing.T, button discordgo.Button) discord.CustomID {
	t.Helper()
	id, err := discord.DecodeCustomID(button.CustomID)
	if err != nil || id.Namespace != componentNamespace || id.Version != "v1" {
		t.Fatalf("custom ID %q: %+v, %v", button.CustomID, id, err)
	}
	return id
}

func TestPostedMessagesKeepLaptopCopyAndRoutes(t *testing.T) {
	panel := entryPanelMessage()
	if panel.Content != "# Need a hand?\nTalk privately with the mod team. Open a ticket and tell us what’s going on." {
		t.Fatalf("panel = %q", panel.Content)
	}
	if b := buttons(t, panel.Components); len(b) != 1 || b[0].Label != "Open ticket" || decode(t, b[0]).Action != "open" {
		t.Fatalf("panel buttons = %+v", b)
	}

	ticket := &Ticket{ID: "ticket", OwnerDiscordUserID: "owner", ThreadDiscordChannelID: "thread"}
	open := queuePostMessage(ticket, nil)
	if open.Content != "{{quack:ticket}} <@owner> opened a ticket: <#thread>." {
		t.Fatalf("open post = %q", open.Content)
	}
	if b := buttons(t, open.Components); len(b) != 2 || b[0].Label != "Recovery" || decode(t, b[0]).Action != "view" ||
		b[1].Label != "Close" || decode(t, b[1]).Action != "close" {
		t.Fatalf("open post buttons = %+v", b)
	}
	closed := queuePostMessage(ticket, &Transcript{Content: "text"})
	if closed.Content != "{{quack:ticket}} The ticket for <@owner> was closed. The transcript is attached." ||
		len(closed.Files) != 1 || closed.Files[0].Name != "ticket-ticket.txt" {
		t.Fatalf("closed post = %+v", closed)
	}

	notice := closeNoticeMessage("Pond *", "ticket", &Transcript{Content: "text"})
	if notice.Content != "Your ticket in **Pond \\*** is closed. Here’s a copy of the conversation for your records.\n-# Ticket ticket" ||
		len(notice.Files) != 1 {
		t.Fatalf("notice = %+v", notice)
	}
}

// progressResponder stands in for Discord, refusing edits once the thread
// the interaction came from is deleted.
type progressResponder struct {
	discord.Responder
	deleted  bool
	inThread bool
	messages []string
}

func (r *progressResponder) EditOriginal(edit discord.Edit) (*discordgo.Message, error) {
	if r.deleted && r.inThread {
		return nil, errors.New("unknown channel")
	}
	if edit.Content != nil {
		r.messages = append(r.messages, *edit.Content)
	}
	return &discordgo.Message{}, nil
}

// fakeCloser reports progress, then deletes the thread or fails to.
type fakeCloser struct {
	responder  *progressResponder
	failDelete bool
}

func (c fakeCloser) CloseWithProgress(_ context.Context, _ modules.Actor, _ string, beforeDelete func(*Ticket) error) (*Ticket, error) {
	ticket := &Ticket{ID: "ticket", Status: StatusResolved, ThreadDiscordChannelID: "thread", TranscriptURL: "saved"}
	if err := beforeDelete(ticket); err != nil {
		return ticket, err
	}
	if c.failDelete {
		return ticket, errors.New("cannot delete")
	}
	c.responder.deleted = true
	return ticket, nil
}

// TestCloseFeedbackSurvivesOriginDeletion answers inside the thread before
// it goes, confirms outside it, and never claims a failed delete finished.
func TestCloseFeedbackSurvivesOriginDeletion(t *testing.T) {
	for _, scenario := range []struct {
		name, origin string
		failure      bool
		count        int
		want         string
	}{
		{"inside", "thread", false, 1, "is closing"},
		{"outside", "entry", false, 1, "Ticket closed"},
		{"delete_failure", "thread", true, 2, "cleanup did not finish"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			responder := &progressResponder{inThread: scenario.origin == "thread"}
			closer := fakeCloser{responder: responder, failDelete: scenario.failure}
			if err := closeWithFeedback(context.Background(), responder, closer, modules.Actor{}, "ticket", scenario.origin); err != nil {
				t.Fatal(err)
			}
			if len(responder.messages) != scenario.count || !strings.Contains(responder.messages[len(responder.messages)-1], scenario.want) {
				t.Fatalf("messages = %q", responder.messages)
			}
			for _, message := range responder.messages {
				if scenario.failure && strings.Contains(message, "Ticket closed.") {
					t.Fatalf("claimed completion: %q", message)
				}
			}
		})
	}
}

// TestDetailLifecycle never links a deleted thread or offers impossible
// actions on a closed ticket.
func TestDetailLifecycle(t *testing.T) {
	ticket := &Ticket{CloseNoticeDelivered: true, ID: "ticket", OwnerDiscordUserID: "owner", ThreadDiscordChannelID: "deleted-thread",
		Status: StatusResolved, TranscriptURL: "https://discord.com/channels/guild/staff/message"}
	transcript := &Transcript{Content: "private retained content"}
	for _, pending := range []bool{false, true} {
		message := detailMessage(ticket, nil, modules.Actor{DiscordUserID: "owner", CanManage: true}, pending, transcript, 0)
		if strings.Contains(message.Content, "deleted-thread") || strings.Contains(message.Content, "https://discord.com") {
			t.Fatalf("owner got unusable links: %q", message.Content)
		}
		got := buttons(t, message.Components)
		if len(got) != map[bool]int{false: 0, true: 1}[pending] || (pending && got[0].Label != "Finish closing") {
			t.Fatalf("pending %v buttons = %+v", pending, got)
		}
		if len(message.Files) != 1 {
			t.Fatal("owner transcript missing")
		}
		if body, err := io.ReadAll(message.Files[0].Reader); err != nil || string(body) != transcript.Content {
			t.Fatalf("transcript = %q, %v", body, err)
		}
	}
	staff := detailMessage(ticket, nil, modules.Actor{CanModerate: true}, false, nil, 0)
	if !strings.Contains(staff.Content, ticket.TranscriptURL) {
		t.Fatal("staff queue link missing")
	}
	ticket.Status = StatusOpen
	open := detailMessage(ticket, nil, modules.Actor{CanManage: true}, false, nil, 0)
	if !strings.Contains(open.Content, "<#deleted-thread>") || len(buttons(t, open.Components)) != 2 {
		t.Fatalf("open controls = %+v", open)
	}
	ticket.Status = StatusCancelled
	cancelled := detailMessage(ticket, nil, modules.Actor{}, false, nil, 0)
	if len(cancelled.Components) != 0 || strings.Contains(cancelled.Content, "<#") {
		t.Fatalf("old cancelled ticket has controls: %+v", cancelled)
	}
}

func TestDetailHistoryPages(t *testing.T) {
	events := []Event{{Body: strings.Repeat("🙂*\n", 2000)}, {Body: "last historical entry"}}
	pages := historyPages(events)
	if len(pages) < 2 || !strings.Contains(pages[len(pages)-1], "last historical entry") {
		t.Fatal("history lost")
	}
	ticket := &Ticket{ID: strings.Repeat("T", 26), OwnerDiscordUserID: "12345678901234567890", Status: StatusResolved}
	for page := range pages {
		message := detailMessage(ticket, events, modules.Actor{}, true, nil, page)
		if n := len(utf16.Encode([]rune(message.Content))); n > 2000 {
			t.Fatalf("page %d has %d units", page, n)
		}
	}
	if id, page := detailPayload(ticket.ID + "~3"); id != ticket.ID || page != 3 {
		t.Fatalf("paged payload = %q, %d", id, page)
	}
	if id, page := detailPayload(ticket.ID); id != ticket.ID || page != 0 {
		t.Fatalf("plain payload = %q, %d", id, page)
	}
}

// TestViewUpdatesOnlyPrivateMessages refreshes a private view in place but
// answers a public post privately.
func TestViewUpdatesOnlyPrivateMessages(t *testing.T) {
	for _, scenario := range []struct {
		payload  string
		flags    discordgo.MessageFlags
		response discordgo.InteractionResponseType
	}{
		{"ticket", discordgo.MessageFlagsEphemeral, discordgo.InteractionResponseDeferredMessageUpdate},
		{"ticket", 0, discordgo.InteractionResponseDeferredChannelMessageWithSource},
		{"ticket~1", discordgo.MessageFlagsEphemeral, discordgo.InteractionResponseDeferredMessageUpdate},
		{"ticket~1", 0, discordgo.InteractionResponseDeferredChannelMessageWithSource},
	} {
		interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			GuildID: "guild", Type: discordgo.InteractionMessageComponent,
			Message: &discordgo.Message{Flags: scenario.flags},
			Data:    discordgo.MessageComponentInteractionData{CustomID: customID("view", scenario.payload)},
		}}
		if result := (&Module{}).viewComponent(context.Background(), interaction); result.Response.Type != scenario.response {
			t.Fatalf("%s flags %d: response %d", scenario.payload, scenario.flags, result.Response.Type)
		}
	}
}

func TestMemberDMRetryIsManagerOnly(t *testing.T) {
	ticket := &Ticket{ID: "ticket", Status: StatusResolved}
	owner := detailMessage(ticket, nil, modules.Actor{}, false, nil, 0)
	manager := detailMessage(ticket, nil, modules.Actor{CanManage: true}, false, nil, 0)
	if got := buttons(t, manager.Components); len(owner.Components) != 0 || len(got) != 1 || got[0].Label != "Retry member DM" {
		t.Fatalf("owner = %+v, manager = %+v", owner.Components, got)
	}
}

func TestExistingTicketFeedbackOffersNextStep(t *testing.T) {
	opening := existingTicketMessage(nil)
	if !strings.Contains(opening.Content, "still opening") || len(opening.Components) != 0 {
		t.Fatalf("opening = %+v", opening)
	}
	open := existingTicketMessage(&Ticket{ID: "ticket", Status: StatusOpen, ThreadDiscordChannelID: "thread"})
	if !strings.Contains(open.Content, "<#thread>") {
		t.Fatal("open ticket lacks its link")
	}
	closing := existingTicketMessage(&Ticket{ID: "ticket", Status: StatusResolved, ThreadDiscordChannelID: "thread"})
	got := buttons(t, closing.Components)
	if strings.Contains(closing.Content, "<#thread>") || len(got) != 1 {
		t.Fatalf("closing = %+v", closing)
	}
	if id := decode(t, got[0]); id.Action != "close" || id.Payload != "ticket" {
		t.Fatalf("finish closing = %+v", id)
	}
}

func TestCloseFailureDistinguishesConfirmedProgress(t *testing.T) {
	for _, scenario := range []struct {
		ticket  *Ticket
		want    string
		buttons int
	}{
		{nil, "permission", 0},
		{&Ticket{ID: "ticket", Status: StatusOpen}, "could not finish closing", 1},
		{&Ticket{ID: "ticket", Status: StatusResolved}, "cleanup did not finish", 1},
	} {
		message := closeFailureMessage(scenario.ticket, ErrPermissionDenied)
		if !strings.Contains(message.Content, scenario.want) || len(message.Components) != scenario.buttons {
			t.Fatalf("message = %+v", message)
		}
	}
}

// deniedStaff fails every live authority lookup.
type deniedStaff struct{}

func (deniedStaff) ResolveDiscordStaffContext(context.Context, quack.DiscordStaffContextInput) (*quack.GuildStaffContext, error) {
	return nil, quack.ErrAuthorizationDenied
}

// TestRepairNeedsLiveAuthority re-checks authority when Repair ticket is
// pressed rather than trusting who was shown the button.
func TestRepairNeedsLiveAuthority(t *testing.T) {
	m := &Module{staff: deniedStaff{}}
	interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		GuildID: "guild", Type: discordgo.InteractionMessageComponent,
		Member: &discordgo.Member{User: &discordgo.User{ID: "former-admin"}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID("repair", "ticket")},
	}}
	result := m.repairComponent(context.Background(), interaction)
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatal("repair did not defer for live authority")
	}
	responder := &progressResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if len(responder.messages) != 1 || !strings.Contains(responder.messages[0], "could not verify") {
		t.Fatalf("revoked authority reached repair: %q", responder.messages)
	}
}
