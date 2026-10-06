package discordtext

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

// TestCatalogMatchesSharedAssets keeps the generated emoji catalog in step
// with assets/icons/quack/manifest.json and never mixes one application's
// emoji IDs into another's.
func TestCatalogMatchesSharedAssets(t *testing.T) {
	data, err := os.ReadFile("../../../../assets/icons/quack/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Icons []struct {
			Key, Name string
			EmojiIDs  map[string]string `json:"emojiIds"`
		}
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, icon := range manifest.Icons {
		for appID, id := range icon.EmojiIDs {
			want := "<:" + icon.Name + ":" + id + ">"
			if got := Resolve(Icon(icon.Key), appID); got != want {
				t.Errorf("%s/%s: got %q want %q", appID, icon.Key, got, want)
			}
		}
	}
	for appID, icons := range applicationIcons {
		if len(icons) != len(manifest.Icons) {
			t.Errorf("%s catalog is incomplete", appID)
		}
	}
}

// TestUntrustedContextStaysQuoted checks that multi-line Markdown stays
// quoted and that an escaped placeholder is never resolved.
func TestUntrustedContextStaysQuoted(t *testing.T) {
	body := Conversation("case", "Case added.", Plain("**reason**\n{{quack:ban}}\n> extra"), "You can appeal.", "Case #12")
	got := Resolve(body, "819019613371236432")
	if strings.Contains(got, "<:quack_ban:") || !strings.Contains(got, "> \\*\\*reason\\*\\*\n> ") || !strings.HasSuffix(got, "\n-# Case #12") {
		t.Fatal(got)
	}
	if Resolve(Icon("case")+" Case added.", "unknown") != "Case added." {
		t.Fatal("unknown application lost readable fallback")
	}
}

// TestWithIconDecoratesOnce keeps a rendered conversation from getting a
// second icon.
func TestWithIconDecoratesOnce(t *testing.T) {
	if got := WithIcon("info", "Done."); got != "{{quack:info}} Done." {
		t.Fatal(got)
	}
	rendered := Conversation("case", "Case added.", "", "", "")
	if got := WithIcon("info", rendered); got != rendered {
		t.Fatal(got)
	}
}

// TestReversalSentencesDescribeResultingState stays accurate whether a
// reversal removed the punishment or found it already gone.
func TestReversalSentencesDescribeResultingState(t *testing.T) {
	for action, want := range map[quack.ActionType]string{
		quack.ActionRemoveTimeout: "Member is no longer timed out.",
		quack.ActionUnbanUser:     "Member is no longer banned.",
	} {
		if got := ActionSentence(action, quack.ActionExecutionSucceeded); got != want {
			t.Fatalf("%s: %s", action, got)
		}
	}
}
