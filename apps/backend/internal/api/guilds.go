package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

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
		guildListErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, guildListResponse{Guilds: guilds})
}

type guildListResponse struct {
	Guilds []quack.UserGuildListItem `json:"guilds"`
}

// guildMeResponse is the guild and the caller's live staff standing in it.
// Permissions maps each permission action to whether the caller has it.
type guildMeResponse struct {
	Guild       guildSummary    `json:"guild"`
	Permissions map[string]bool `json:"permissions" nullable:"false"`
	Staff       staffSummary    `json:"staff"`
}

type guildSummary struct {
	DiscordGuildID     string `json:"discord_guild_id"`
	IconURL            string `json:"icon_url"`
	ID                 string `json:"id"`
	Name               string `json:"name"`
	OwnerDiscordUserID string `json:"owner_discord_user_id"`
}

// staffSummary is the caller as guild staff. Permission bit sets are
// decimal strings, since they overflow JavaScript numbers.
type staffSummary struct {
	DiscordUserID       string     `json:"discord_user_id"`
	DisplayName         string     `json:"display_name"`
	ID                  string     `json:"id"`
	IsAdmin             bool       `json:"is_admin"`
	IsModerator         bool       `json:"is_moderator"`
	LastActiveAt        *time.Time `json:"last_active_at"`
	LastSeenPermissions string     `json:"last_seen_permissions"`
	PermissionBits      string     `json:"permission_bits"`
}

// settingsEnvelope wraps the guild's settings.
type settingsEnvelope struct {
	Settings *quack.GuildSettingsResponse `json:"settings" nullable:"false"`
}

// guildMe describes the guild and the caller's live staff permissions in it.
func (s *Server) guildMe(w http.ResponseWriter, r *http.Request) {
	staff := quack.StaffFromContext(r.Context())
	writeJSON(w, http.StatusOK, guildMeResponse{
		Guild: guildSummary{
			ID:                 staff.Guild.ID,
			DiscordGuildID:     staff.Guild.DiscordGuildID,
			Name:               staff.Guild.Name,
			IconURL:            staff.Guild.IconURL,
			OwnerDiscordUserID: staff.Guild.OwnerDiscordUserID,
		},
		Staff: staffSummary{
			ID:                  staff.Staff.ID,
			DiscordUserID:       staff.Staff.DiscordUserID,
			DisplayName:         staff.Staff.LastKnownDisplayName,
			PermissionBits:      quack.PermissionBitsString(staff.PermissionBits),
			IsAdmin:             staff.IsAdmin,
			IsModerator:         staff.IsModerator,
			LastActiveAt:        staff.Staff.LastActiveAt,
			LastSeenPermissions: quack.PermissionBitsString(staff.Staff.LastSeenPermissionBits),
		},
		Permissions: quack.PermissionMapStrings(staff.Permissions),
	})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.services.Settings.Get(r.Context(), quack.StaffFromContext(r.Context()))
	if err != nil {
		settingsErrors.write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsEnvelope{Settings: settings})
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
	writeJSON(w, http.StatusOK, settingsEnvelope{Settings: settings})
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
	writeJSON(w, http.StatusOK, settingsEnvelope{Settings: settings})
}
