package api

import (
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

// listGuilds lists the caller's Discord guilds where they can use Quack.
func (s *Server) listGuilds(w http.ResponseWriter, r *http.Request) {
	guilds, err := s.services.Guilds.ListUserManageableGuilds(r.Context(), sessionFrom(r.Context()))
	if err != nil {
		writeError(w, r, http.StatusBadGateway, codeDependency, "failed to list discord guilds")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guilds": guilds})
}

// guildMe describes the guild and the caller's live staff permissions in it.
func (s *Server) guildMe(w http.ResponseWriter, r *http.Request) {
	staff := GuildStaff(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"guild": map[string]any{
			"id":                    staff.Guild.ID,
			"discord_guild_id":      staff.Guild.DiscordGuildID,
			"name":                  staff.Guild.Name,
			"icon_url":              staff.Guild.IconURL,
			"owner_discord_user_id": staff.Guild.OwnerDiscordUserID,
		},
		"staff": map[string]any{
			"id":                    staff.Staff.ID,
			"discord_user_id":       staff.Staff.DiscordUserID,
			"display_name":          staff.Staff.LastKnownDisplayName,
			"permission_bits":       quack.PermissionBitsString(staff.PermissionBits),
			"is_admin":              staff.IsAdmin,
			"is_moderator":          staff.IsModerator,
			"last_active_at":        staff.Staff.LastActiveAt,
			"last_seen_permissions": quack.PermissionBitsString(staff.Staff.LastSeenPermissionBits),
		},
		"permissions": quack.PermissionMapStrings(staff.Permissions),
	})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.services.Settings.Get(r.Context(), GuildStaff(r.Context()))
	if err != nil {
		writeSettingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

// updateSettings applies a partial settings update. An undecodable payload
// is still audited, as a failed update.
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	staff := GuildStaff(r.Context())
	var input quack.GuildSettingsInput
	if err := decodeJSON(r, &input); err != nil {
		writeSettingsError(w, r, s.services.Settings.RejectUpdatePayload(r.Context(), staff, err))
		return
	}
	settings, err := s.services.Settings.Update(r.Context(), staff, input)
	if err != nil {
		writeSettingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

// acknowledgeStarterPolicyNotice dismisses the one-time starter template
// notice.
func (s *Server) acknowledgeStarterPolicyNotice(w http.ResponseWriter, r *http.Request) {
	settings, err := s.services.Settings.AcknowledgeStarterPolicyNotice(r.Context(), GuildStaff(r.Context()))
	if err != nil {
		writeSettingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}
