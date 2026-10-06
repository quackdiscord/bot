package quack_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

type fakeEvidenceClient struct {
	message   quack.DiscordMessageSnapshot
	preserved quack.PreservedDiscordAttachment
}

func (f *fakeEvidenceClient) FetchMessageEvidence(context.Context, quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	copyValue := f.message
	return &copyValue, nil
}

func (f *fakeEvidenceClient) PreserveEvidenceAttachment(context.Context, string, string, quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	copyValue := f.preserved
	return &copyValue, nil
}

func (f *fakeEvidenceClient) EnsureEvidenceChannel(context.Context, string, string) (string, error) {
	return "999999999999999999", nil
}

type unavailableEvidenceClient struct{ err error }

func (f unavailableEvidenceClient) FetchMessageEvidence(context.Context, quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	return nil, f.err
}

func (unavailableEvidenceClient) PreserveEvidenceAttachment(context.Context, string, string, quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	return nil, nil
}

func (unavailableEvidenceClient) EnsureEvidenceChannel(context.Context, string, string) (string, error) {
	return "", nil
}

type evidenceClientFixture struct {
	message   quack.DiscordMessageSnapshot
	preserved quack.PreservedDiscordAttachment
}

func (f evidenceClientFixture) FetchMessageEvidence(context.Context, quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	message := f.message
	return &message, nil
}

func (f evidenceClientFixture) PreserveEvidenceAttachment(context.Context, string, string, quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	preserved := f.preserved
	return &preserved, nil
}

func (evidenceClientFixture) EnsureEvidenceChannel(context.Context, string, string) (string, error) {
	return "evidence-channel", nil
}

func TestParseDiscordMessageLinkRejectsLookalikesAndCrossGuildCapture(t *testing.T) {
	valid := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	ref, err := quack.ParseDiscordMessageLink(valid)
	if err != nil || ref.MessageID != "333333333333333333" {
		t.Fatalf("valid link: %+v err=%v", ref, err)
	}
	for _, invalid := range []string{"https://discord.example/channels/111111111111111111/222222222222222222/333333333333333333", "https://discord.com/channels/@me/2/3", "javascript:alert(1)"} {
		if _, err := quack.ParseDiscordMessageLink(invalid); !errors.Is(err, quack.ErrEvidenceValidation) {
			t.Fatalf("accepted invalid link %q: %v", invalid, err)
		}
	}
	service := quack.NewEvidenceService(nil, unavailableEvidenceClient{})
	if _, err := service.Capture(context.Background(), "999999999999999999", "actor", "target", "", []string{valid}, false); !errors.Is(err, quack.ErrEvidenceValidation) {
		t.Fatalf("cross-guild capture accepted: %v", err)
	}
}

func TestUnavailableEvidenceRequiresOtherVisibleContext(t *testing.T) {
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	for _, outcome := range []string{"deleted", "inaccessible"} {
		service := quack.NewEvidenceService(nil, unavailableEvidenceClient{err: &quack.EvidenceUnavailableError{Outcome: outcome, Message: "message " + outcome}})
		if _, err := service.Capture(context.Background(), "111111111111111111", "actor", "target", "", []string{link}, false); err == nil {
			t.Fatalf("%s message continued without visible fallback context", outcome)
		}
		captured, err := service.Capture(context.Background(), "111111111111111111", "actor", "target", "", []string{link}, true)
		if err != nil || len(captured.Snapshots) != 1 || captured.Snapshots[0].CaptureOutcome != outcome || captured.Snapshots[0].MessageCreatedAt.IsZero() {
			t.Fatalf("partial %s capture: %+v err=%v", outcome, captured, err)
		}
	}
}

func TestLiveEvidencePreservesSupportedAndRetainsUnsupportedOrOversizedMetadata(t *testing.T) {
	const (
		guildID  = "111111111111111111"
		channel  = "222222222222222222"
		message  = "333333333333333333"
		targetID = "444444444444444444"
	)
	link := "https://discord.com/channels/" + guildID + "/" + channel + "/" + message
	client := evidenceClientFixture{
		message: quack.DiscordMessageSnapshot{
			GuildID: guildID, ChannelID: channel, MessageID: message,
			AuthorDiscordUserID: targetID, URL: link, Content: "live evidence", CreatedAt: time.Now().UTC(),
			Attachments: []quack.DiscordAttachmentSnapshot{
				{ID: "supported", Filename: "proof.png", ContentType: "image/png", SizeBytes: 1024, URL: "https://cdn.example/proof"},
				{ID: "unsupported", Filename: "archive.zip", ContentType: "application/zip", SizeBytes: 1024, URL: "https://cdn.example/archive"},
				{ID: "oversized", Filename: "large.png", ContentType: "image/png", SizeBytes: 26 << 20, URL: "https://cdn.example/large"},
			},
		},
		preserved: quack.PreservedDiscordAttachment{URL: "https://cdn.example/preserved", MessageID: "copy-message", AttachmentID: "copy-attachment"},
	}
	captured, err := quack.NewEvidenceService(nil, client).Capture(context.Background(), guildID, "actor", targetID, "evidence-channel", []string{link}, false)
	if err != nil {
		t.Fatalf("capture live evidence: %v", err)
	}
	if len(captured.Snapshots) != 1 || captured.Snapshots[0].CaptureOutcome != "captured" || len(captured.Attachments) != 3 {
		t.Fatalf("unexpected live capture: %+v", captured)
	}
	if captured.Attachments[0].CopyOutcome != "preserved" || captured.Attachments[0].PreservedURL == "" {
		t.Fatalf("supported attachment was not preserved: %+v", captured.Attachments[0])
	}
	if captured.Attachments[1].CopyOutcome != "metadata_only" || !strings.Contains(captured.Attachments[1].Warning, "type") {
		t.Fatalf("unsupported attachment lost its warning/metadata: %+v", captured.Attachments[1])
	}
	if captured.Attachments[2].CopyOutcome != "metadata_only" || !strings.Contains(captured.Attachments[2].Warning, "size") {
		t.Fatalf("oversized attachment lost its warning/metadata: %+v", captured.Attachments[2])
	}
}

func FuzzParseDiscordMessageLink(f *testing.F) {
	f.Add("https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333")
	f.Add("not-a-url")
	f.Fuzz(func(t *testing.T, value string) {
		ref, err := quack.ParseDiscordMessageLink(value)
		if err == nil && (ref.GuildID == "" || ref.ChannelID == "" || ref.MessageID == "") {
			t.Fatalf("successful parse returned empty identity: %+v", ref)
		}
	})
}
