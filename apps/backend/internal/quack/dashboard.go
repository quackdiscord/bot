package quack

import (
	"net/url"
	"strings"
)

// dashboardURLMaxLength is the longest URL a Discord link button accepts.
const dashboardURLMaxLength = 512

// DashboardLinks builds the dashboard URLs Quack links to from Discord. The
// paths are the dashboard's public URL map (docs/dashboard.md), so they must
// stay stable. Links carry only IDs and fixed page names, never evidence,
// context, or anything else a member or moderator wrote, and the dashboard
// checks access itself.
//
// The zero value has no dashboard: every method returns "", so callers can
// leave the link out.
type DashboardLinks struct {
	// base is the validated dashboard URL without a trailing slash.
	base string
}

// NewDashboardLinks returns links under base, an absolute http or https URL
// with a host and an optional path prefix. Which scheme is acceptable for
// the environment is decided by the caller (see config.Config.DashboardURL).
// A base with user info, a query, or a fragment, or that cannot be parsed,
// yields the zero DashboardLinks.
func NewDashboardLinks(base string) DashboardLinks {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return DashboardLinks{}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return DashboardLinks{base: parsed.String()}
}

// Base returns the dashboard URL links are built under, or "" when there is
// no dashboard.
func (l DashboardLinks) Base() string { return l.base }

// Staff returns a staff page of the guild with Discord ID discordGuildID:
// Staff(id) is the overview, Staff(id, "cases", caseID) a case, and
// Staff(id, "modules", "tickets") a module's settings. Every segment must
// be an opaque ID or page name (letters, digits, "-", and "_"), so a value
// taken from a custom ID cannot add URL syntax or walk up the path. It
// returns "" when there is no dashboard, a segment is unsafe, or the URL
// would be too long for a Discord button.
func (l DashboardLinks) Staff(discordGuildID string, page ...string) string {
	return l.build(append([]string{"guilds", discordGuildID}, page...))
}

// MemberAppeal returns the member's page for appealing a case, which shows
// the case, the appeal and its conversation, and the reply box when staff
// asked for information. guildID and caseID are Quack's internal IDs, not
// Discord's. It returns "" under the same conditions as Staff.
func (l DashboardLinks) MemberAppeal(guildID, caseID string) string {
	return l.build([]string{"guilds", guildID, "cases", caseID, "appeal"})
}

// build joins segments under the base.
func (l DashboardLinks) build(segments []string) string {
	if l.base == "" {
		return ""
	}
	for _, segment := range segments {
		if !dashboardSegment(segment) {
			return ""
		}
	}
	link := l.base + "/" + strings.Join(segments, "/")
	if len(link) > dashboardURLMaxLength {
		return ""
	}
	return link
}

// dashboardSegment reports whether value is safe as one URL path segment
// without escaping.
func dashboardSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}
