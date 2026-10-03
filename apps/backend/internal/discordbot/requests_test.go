package discordbot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// requestTransport isolates REST behavior from both network access and Discord.
type requestTransport func(*http.Request) (*http.Response, error)

func (f requestTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestEnforcementCarriesContextAndDoesNotRetryBehindWorker(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "trace")
	calls := 0
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Context().Value(key{}) != "trace" {
			t.Error("request lost its context")
		}
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"upstream unavailable"}`)), Request: request}, nil
	})}
	_, err = (&Bot{Session: session}).BanMember(ctx, "guild", "member", 0, "case")
	var classified quack.DiscordError
	if calls != 1 || !errors.As(err, &classified) || !classified.OutcomeUncertain || classified.Retryable {
		t.Fatalf("expected one uncertain attempt: calls=%d error=%+v", calls, err)
	}
}

func TestEvidenceDownloadRejectsUnexpectedSizeBeforeUpload(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("too many bytes")), Request: request}, nil
	})}
	// No Discord session is supplied: an upload attempt would panic.
	bot := &Bot{HTTPClient: client}
	_, err := bot.PreserveEvidenceAttachment(context.Background(), "guild", "channel", quack.DiscordAttachmentSnapshot{URL: "https://cdn.discordapp.com/attachments/1/2/file.png", SizeBytes: 3})
	if err == nil || calls != 1 {
		t.Fatalf("size mismatch was accepted: calls=%d err=%v", calls, err)
	}
	for _, raw := range []string{"http://cdn.discordapp.com/attachments/1/2/x", "https://localhost/attachments/1/2/x", "https://cdn.discordapp.com.evil.test/attachments/x", "https://user:secret@cdn.discordapp.com/attachments/x"} {
		_, err := bot.PreserveEvidenceAttachment(context.Background(), "guild", "channel", quack.DiscordAttachmentSnapshot{URL: raw, SizeBytes: 3})
		if err == nil || calls != 1 {
			t.Fatalf("unsafe download: %s", raw)
		}
	}
}
