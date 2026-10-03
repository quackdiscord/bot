package discordbot

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestDiscordActionClassificationRedactsAndProtectsIrreversibleOutcomes(t *testing.T) {
	tests := []struct {
		name                 string
		status               int
		irreversible         bool
		code                 string
		retryable, uncertain bool
	}{{"validation", 400, false, "validation_failed", false, false}, {"permission", 403, false, "permission_or_hierarchy_denied", false, false}, {"unknown", 404, false, "unknown_member_or_resource", false, false}, {"rate", 429, true, "rate_limited", true, false}, {"safe server", 500, false, "discord_server_error", true, false}, {"uncertain ban", 500, true, "discord_server_error", false, true}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &discordgo.RESTError{Response: &http.Response{StatusCode: test.status}}
			err := classifyDiscordOperation("ban", source, test.irreversible)
			var classified quack.DiscordError
			if !errors.As(err, &classified) {
				t.Fatalf("not classified: %v", err)
			}
			if classified.Retryable != test.retryable || classified.OutcomeUncertain != test.uncertain || classified.Message == source.Error() || !strings.Contains(classified.Code, test.code) {
				t.Fatalf("unexpected classification: %+v", classified)
			}
		})
	}
}
