package discord

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// roundTripper serves Discord REST fixtures without a network listener.
type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// textResponse answers request with status and body.
func textResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

// jsonResponse answers request with 200 OK and body encoded as JSON.
func jsonResponse(request *http.Request, body any) *http.Response {
	encoded, _ := json.Marshal(body)
	return textResponse(request, http.StatusOK, string(encoded))
}

// testBot returns a bot whose REST calls are answered by serve.
func testBot(t *testing.T, serve func(*http.Request) (*http.Response, error)) *Bot {
	t.Helper()
	bot, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	bot.Session.State.User = &discordgo.User{ID: "bot"}
	bot.Session.Client = &http.Client{Transport: roundTripper(serve)}
	return bot
}

func TestStatusTracksGateway(t *testing.T) {
	bot, err := New("token")
	if err != nil {
		t.Fatal(err)
	}
	bot.Session.State.User = &discordgo.User{ID: "bot-1", Username: "quack"}
	steps := []struct {
		event any
		want  bool
	}{
		{&discordgo.Ready{}, true},
		{&discordgo.Disconnect{}, false},
		{&discordgo.Resumed{}, true},
	}
	for _, step := range steps {
		bot.trackGateway(bot.Session, step.event)
		if connected, _, _ := bot.Status(); connected != step.want {
			t.Fatalf("after %T: connected=%v, want %v", step.event, connected, step.want)
		}
	}
}

func TestClassifyRedactsAndProtectsIrreversibleOutcomes(t *testing.T) {
	tests := []struct {
		name                 string
		status               int
		irreversible         bool
		code                 string
		retryable, uncertain bool
	}{
		{"validation", 400, false, "validation_failed", false, false},
		{"permission", 403, false, "permission_or_hierarchy_denied", false, false},
		{"unknown", 404, false, "unknown_member_or_resource", false, false},
		{"rate", 429, true, "rate_limited", true, false},
		{"safe server", 500, false, "discord_server_error", true, false},
		{"uncertain ban", 500, true, "discord_server_error", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &discordgo.RESTError{Response: &http.Response{StatusCode: test.status}}
			var classified quack.DiscordError
			if !errors.As(classify("ban", source, test.irreversible), &classified) {
				t.Fatal("not classified")
			}
			if classified.Retryable != test.retryable || classified.OutcomeUncertain != test.uncertain ||
				classified.Message == source.Error() || classified.Code != "ban_"+test.code {
				t.Fatalf("unexpected classification: %+v", classified)
			}
		})
	}
}
