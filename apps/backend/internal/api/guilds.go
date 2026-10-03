package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

// guild resolves the caller's live Discord permissions in the
// {discordGuildID} guild and requires action. An empty action only requires
// that the caller is present in the guild; such routes check finer
// capabilities themselves. It must run after requireAuth.
func (s *Server) guild(action quack.PermissionAction) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			session := sessionFrom(ctx)
			discordGuildID := r.PathValue("discordGuildID")
			staff, err := s.services.Guilds.ResolveStaffContext(ctx, session, discordGuildID)
			if err != nil {
				slog.WarnContext(ctx, "live guild authorization denied",
					"actor_discord_user_id", session.DiscordUserID, "discord_guild_id", discordGuildID)
				if errors.Is(err, quack.ErrBotNotInGuild) {
					writeError(w, r, http.StatusNotFound, codeNotFound, "guild not found")
				} else {
					writeError(w, r, http.StatusForbidden, codeAuthorization, "live guild authorization unavailable")
				}
				return
			}
			if err := s.services.Guilds.Authorize(ctx, staff, action, quack.AuditSourceAPI); err != nil {
				slog.WarnContext(ctx, "guild permission denied",
					"actor_discord_user_id", session.DiscordUserID, "discord_guild_id", discordGuildID,
					"permission_action", string(action))
				writeError(w, r, http.StatusForbidden, codeAuthorization, "access denied")
				return
			}
			next.ServeHTTP(w, r.WithContext(quack.ContextWithStaff(ctx, staff)))
		})
	}
}

// allow gates a route on a check that needs the guild context, answering
// with denied when it fails. Writes put it before idempotency, so a replay
// cannot outlive the caller's permission.
func allow(check func(*http.Request) bool, denied http.HandlerFunc) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !check(r) {
				denied(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// can reports whether the guild staff can perform action.
func can(action quack.PermissionAction) func(*http.Request) bool {
	return func(r *http.Request) bool {
		staff := quack.StaffFromContext(r.Context())
		return staff != nil && staff.Can(action)
	}
}

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
	staff := quack.StaffFromContext(r.Context())
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
	settings, err := s.services.Settings.Get(r.Context(), quack.StaffFromContext(r.Context()))
	if err != nil {
		settingsErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

// updateSettings applies a partial settings update. An undecodable payload
// is still audited, as a failed update.
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	var input quack.GuildSettingsInput
	if err := decodeJSON(r, &input); err != nil {
		settingsErrors.write(w, r, s.services.Settings.RejectUpdatePayload(r.Context(), staff, err))
		return
	}
	settings, err := s.services.Settings.Update(r.Context(), staff, input)
	if err != nil {
		settingsErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

// acknowledgeStarterPolicyNotice dismisses the one-time starter template
// notice.
func (s *Server) acknowledgeStarterPolicyNotice(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	settings, err := s.services.Settings.AcknowledgeStarterPolicyNotice(r.Context(), staff)
	if err != nil {
		settingsErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}
