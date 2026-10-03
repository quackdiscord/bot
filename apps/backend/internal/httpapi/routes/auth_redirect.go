package routes

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/quackdiscord/bot/internal/config"
)

// builds discord avatar url
func discordAvatarURL(userID, avatarHash string) string {
	if userID == "" || avatarHash == "" {
		return ""
	}

	ext := "png"
	if strings.HasPrefix(avatarHash, "a_") {
		ext = "gif"
	}

	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s", userID, avatarHash, ext)
}

// sanitizeRedirectTarget permits local paths and URLs on the configured dashboard origin.
func sanitizeRedirectTarget(target, fallback string) string {
	target, fallback = strings.TrimSpace(target), strings.TrimSpace(fallback)
	fallbackURL, ok := safeRedirectURL(fallback)
	if !ok {
		fallback, fallbackURL = "/", &url.URL{Path: "/"}
	}
	targetURL, ok := safeRedirectURL(target)
	if !ok {
		return fallback
	}
	if targetURL.Host == "" {
		return target
	}
	if strings.EqualFold(targetURL.Scheme, fallbackURL.Scheme) && strings.EqualFold(targetURL.Host, fallbackURL.Host) {
		return target
	}
	return fallback
}

// safeRedirectURL rejects browser-normalized network paths and non-HTTP destinations.
func safeRedirectURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" || parsed.User != nil || strings.ContainsAny(raw, "\\\r\n\t") || strings.Contains(parsed.Path, "\\") || strings.HasPrefix(parsed.Path, "//") {
		return nil, false
	}
	if parsed.Scheme == "" {
		return parsed, strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && parsed.Host == ""
	}
	return parsed, (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.Opaque == ""
}

// validates discord oauth configuration
func validateDiscordOAuthConfig(cfg config.Config) error {
	if strings.TrimSpace(cfg.Discord.AppID) == "" {
		return fmt.Errorf("discord oauth is not configured: missing discord.app_id")
	}
	if strings.TrimSpace(cfg.Discord.ClientSecret) == "" {
		return fmt.Errorf("discord oauth is not configured: missing discord.client_secret")
	}
	if strings.TrimSpace(cfg.Discord.OAuthRedirectURI) == "" {
		return fmt.Errorf("discord oauth is not configured: missing discord.oauth_redirect_uri")
	}
	return nil
}
