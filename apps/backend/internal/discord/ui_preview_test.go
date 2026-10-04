package discord

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// previewTestCatalog creates distinct fake application emoji IDs, without network calls.
func previewTestCatalog(offset int) []*discordgo.Emoji {
	var catalog []*discordgo.Emoji
	for i, key := range previewIconKeys {
		catalog = append(catalog, &discordgo.Emoji{Name: "quack_" + key, ID: fmt.Sprintf("%019d", offset+i)})
	}
	return catalog
}

// TestUIPreviewDesignsAreSendableAndInert checks the entire gallery, including
// every punishment DM, against Discord limits and accidental live interactions.
func TestUIPreviewDesignsAreSendableAndInert(t *testing.T) {
	user := &discordgo.User{ID: "489264179472236557", Username: "preview-member"}
	samples := uiPreviewSamples(user, time.Unix(1788585917, 0))
	if len(samples) < 46 {
		t.Fatal("preview gallery lost surface coverage")
	}
	icons, missing := previewIconCatalog(previewTestCatalog(100))
	if len(missing) != 0 {
		t.Fatal(missing)
	}
	for _, style := range []string{"compact", "conversation", "spotlight"} {
		for _, sample := range samples {
			t.Run(style+"/"+sample.name, func(t *testing.T) {
				message := previewDesign(sample, style, icons)
				if len(message.Embeds) != 0 || message.Content == "" {
					t.Fatal("preview must be text only")
				}
				if len([]rune(message.Content)) > 1850 {
					t.Fatal("no room for sample label")
				}
				if !strings.Contains(message.Content, icons[sample.icon]) || icons[sample.icon] == "" {
					t.Fatal("missing application icon")
				}
				if message.AllowedMentions == nil || len(message.AllowedMentions.Parse) > 0 || len(message.AllowedMentions.Users) > 0 || message.AllowedMentions.RepliedUser {
					t.Fatal("preview can ping members")
				}
				for _, component := range message.Components {
					row := component.(discordgo.ActionsRow)
					if len(row.Components) > 5 {
						t.Fatal("too many buttons")
					}
					for _, child := range row.Components {
						button := child.(discordgo.Button)
						if !button.Disabled || button.URL != "" {
							t.Fatal("live preview control")
						}
						id, err := DecodeCustomID(button.CustomID)
						if err != nil || id.Namespace != "preview" {
							t.Fatal("control can route to real handler")
						}
					}
				}
			})
		}
	}
	gallery := previewIconGallery(icons)
	if len([]rune(gallery.Content)) > 2000 {
		t.Fatal("icon gallery exceeds content budget")
	}
	for _, key := range previewIconKeys {
		if !strings.Contains(gallery.Content, icons[key]) {
			t.Fatalf("missing %s", key)
		}
	}
}

// TestUIPreviewUsesInvokingApplicationIcons prevents cross-bot ID assumptions
// and makes an incomplete upload visible before any gallery messages are sent.
func TestUIPreviewUsesInvokingApplicationIcons(t *testing.T) {
	a, _ := previewIconCatalog(previewTestCatalog(100))
	b, _ := previewIconCatalog(previewTestCatalog(900))
	if a["ban"] == b["ban"] {
		t.Fatal("catalogs unexpectedly share IDs")
	}
	catalog := previewTestCatalog(100)[1:]
	catalog = append(catalog, nil, &discordgo.Emoji{Name: "quack_warn"}, &discordgo.Emoji{Name: "unrelated", ID: "123"})
	_, missing := previewIconCatalog(catalog)
	if len(missing) != 1 || missing[0] != "quack_warn" {
		t.Fatalf("missing = %v", missing)
	}
}
