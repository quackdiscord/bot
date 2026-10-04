package quack_test

import (
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestDashboardLinksAcceptOnlyPlainBases(t *testing.T) {
	for _, base := range []string{
		"", "dash.example", "/relative", "ftp://dash.example", "https://", "https://user:pw@dash.example",
		"https://dash.example?x=1", "https://dash.example/?", "https://dash.example#frag", "mailto:a@dash.example",
	} {
		links := quack.NewDashboardLinks(base)
		if links.Base() != "" || links.Staff("guild") != "" || links.MemberAppeal("guild", "case") != "" {
			t.Errorf("%q produced links", base)
		}
	}
	for base, want := range map[string]string{
		"https://dash.example":        "https://dash.example",
		" https://dash.example/app/ ": "https://dash.example/app",
		"http://localhost:3000/":      "http://localhost:3000",
	} {
		if got := quack.NewDashboardLinks(base).Base(); got != want {
			t.Errorf("%q: base %q, want %q", base, got, want)
		}
	}
}

func TestDashboardLinksBuildTheDashboardURLMap(t *testing.T) {
	links := quack.NewDashboardLinks("https://dash.example/app/")
	for _, test := range []struct{ got, want string }{
		{links.Staff("123"), "https://dash.example/app/guilds/123"},
		{links.Staff("123", "cases"), "https://dash.example/app/guilds/123/cases"},
		{links.Staff("123", "cases", "01J0CASE"), "https://dash.example/app/guilds/123/cases/01J0CASE"},
		{links.Staff("123", "members", "456"), "https://dash.example/app/guilds/123/members/456"},
		{links.Staff("123", "appeals", "01J0APPL"), "https://dash.example/app/guilds/123/appeals/01J0APPL"},
		{links.Staff("123", "rules", "rule_1"), "https://dash.example/app/guilds/123/rules/rule_1"},
		{links.Staff("123", "modules", "tickets"), "https://dash.example/app/guilds/123/modules/tickets"},
		{links.MemberAppeal("01J0GUILD", "01J0CASE"), "https://dash.example/app/guilds/01J0GUILD/cases/01J0CASE/appeal"},
	} {
		if test.got != test.want {
			t.Errorf("got %q, want %q", test.got, test.want)
		}
	}
}

// TestDashboardLinksRefuseUnsafeSegments keeps IDs from custom IDs or
// stored rows from adding URL syntax, walking up the path, or overflowing
// a Discord button.
func TestDashboardLinksRefuseUnsafeSegments(t *testing.T) {
	links := quack.NewDashboardLinks("https://dash.example")
	for _, segment := range []string{"", "..", "a/b", "a b", "a?b", "a#b", "a%2Fb", "é", strings.Repeat("a", 512)} {
		if got := links.Staff("guild", "cases", segment); got != "" {
			t.Errorf("segment %q produced %q", segment, got)
		}
		if got := links.MemberAppeal(segment, "case"); got != "" {
			t.Errorf("guild %q produced %q", segment, got)
		}
	}
	if got := links.Staff(""); got != "" {
		t.Errorf("empty guild produced %q", got)
	}
}
