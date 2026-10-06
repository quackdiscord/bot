package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// Directory supplies Discord display data, such as names, avatars, and
// channel lists, so the dashboard can show people and channels instead of
// raw snowflakes. It is display data only; moderation never depends on it.
// Implementations may cache, so results can be a few minutes stale.
type Directory interface {
	// SearchMembers returns up to limit current members of the guild whose
	// username or nickname starts with query.
	SearchMembers(ctx context.Context, discordGuildID, query string, limit int) ([]DirectoryUser, error)
	// LookupUsers describes each user in userIDs, in that order, as a guild
	// member when they are one and as a plain Discord user otherwise. Users
	// Discord does not know are left out.
	LookupUsers(ctx context.Context, discordGuildID string, userIDs []string) ([]DirectoryUser, error)
	// Channels lists the guild's text, announcement, forum, voice, stage,
	// and category channels in Discord's sidebar order.
	Channels(ctx context.Context, discordGuildID string) ([]DirectoryChannel, error)
}

// DirectoryUser is how the dashboard shows a Discord user.
type DirectoryUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	// Nick is the guild nickname, or "" when there is none or the user is
	// not a member.
	Nick string `json:"nick"`
	// DisplayName is Nick, else GlobalName, else Username.
	DisplayName string `json:"display_name"`
	// AvatarURL is the guild avatar, else the user avatar, else Discord's
	// default avatar for the user. It is never empty.
	AvatarURL string `json:"avatar_url"`
	Bot       bool   `json:"bot"`
	// InGuild reports whether the user is currently a member of the guild.
	InGuild bool `json:"in_guild"`
}

// ChannelType is the kind of a guild channel the directory lists.
type ChannelType string

// The channel types the directory lists. Threads and other kinds are left
// out.
const (
	ChannelTypeText         ChannelType = "text"
	ChannelTypeAnnouncement ChannelType = "announcement"
	ChannelTypeForum        ChannelType = "forum"
	ChannelTypeVoice        ChannelType = "voice"
	ChannelTypeStage        ChannelType = "stage"
	ChannelTypeCategory     ChannelType = "category"
)

// DirectoryChannel is one guild channel, for channel pickers.
type DirectoryChannel struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	Type ChannelType `json:"type"`
	// ParentID is the channel's category, or "" for none.
	ParentID string `json:"parent_id"`
	// Position is Discord's raw sort position. The list is already in
	// sidebar order, so clients need not sort by it.
	Position int `json:"position"`
}

// Directory request bounds.
const (
	memberSearchDefaultLimit = 10
	memberSearchMaxLimit     = 25
	userLookupMaxIDs         = 100
)

// memberSearchQuery is the query GET .../directory/members reads.
type memberSearchQuery struct {
	// Query matches the start of a username or nickname.
	Query string `query:"query" required:"true" minLength:"1"`
	// Limit is capped at 25.
	Limit string `query:"limit" type:"integer" minimum:"1" maximum:"100" default:"10"`
}

// userLookupQuery is the query GET .../directory/users reads.
type userLookupQuery struct {
	// IDs is a comma-separated list of up to 100 user IDs. Duplicates and
	// non-numeric entries are ignored.
	IDs string `query:"ids" required:"true"`
}

type memberSearchResponse struct {
	Members []DirectoryUser `json:"members" nullable:"false"`
}

type userLookupResponse struct {
	Users []DirectoryUser `json:"users" nullable:"false"`
}

type channelListResponse struct {
	Channels []DirectoryChannel `json:"channels" nullable:"false"`
}

// directoryFailures are the statuses a directory handler answers with
// itself: bad input, and Discord or the directory being unavailable.
var directoryFailures = []int{http.StatusBadRequest, http.StatusBadGateway, http.StatusServiceUnavailable}

// searchMembers finds current guild members by name, for picking a case
// target.
func (s *Server) searchMembers(w http.ResponseWriter, r *http.Request) {
	if !s.directoryAvailable(w, r) {
		return
	}
	var q memberSearchQuery
	modules.DecodeQuery(r, &q)
	query := strings.TrimSpace(q.Query)
	if query == "" {
		writeError(w, r, http.StatusBadRequest, codeValidation, "query is required")
		return
	}
	limit := memberSearchDefaultLimit
	if q.Limit != "" {
		// The endpoint policy has already rejected malformed limits.
		limit, _ = strconv.Atoi(q.Limit)
	}
	limit = min(max(limit, 1), memberSearchMaxLimit)
	members, err := s.directory.SearchMembers(r.Context(), r.PathValue("discordGuildID"), query, limit)
	if err != nil {
		writeDirectoryError(w, r, err, "failed to search discord members")
		return
	}
	writeJSON(w, http.StatusOK, memberSearchResponse{Members: nonNil(members)})
}

// lookupUsers describes the users a page refers to by ID.
func (s *Server) lookupUsers(w http.ResponseWriter, r *http.Request) {
	if !s.directoryAvailable(w, r) {
		return
	}
	var q userLookupQuery
	modules.DecodeQuery(r, &q)
	ids, ok := parseUserIDs(q.IDs)
	if !ok {
		writeError(w, r, http.StatusBadRequest, codeValidation,
			"ids must list at most "+strconv.Itoa(userLookupMaxIDs)+" user IDs")
		return
	}
	var users []DirectoryUser
	if len(ids) > 0 {
		var err error
		users, err = s.directory.LookupUsers(r.Context(), r.PathValue("discordGuildID"), ids)
		if err != nil {
			writeDirectoryError(w, r, err, "failed to look up discord users")
			return
		}
	}
	writeJSON(w, http.StatusOK, userLookupResponse{Users: nonNil(users)})
}

// listChannels lists the guild's channels for channel pickers.
func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	if !s.directoryAvailable(w, r) {
		return
	}
	channels, err := s.directory.Channels(r.Context(), r.PathValue("discordGuildID"))
	if err != nil {
		writeDirectoryError(w, r, err, "failed to list discord channels")
		return
	}
	writeJSON(w, http.StatusOK, channelListResponse{Channels: nonNil(channels)})
}

// directoryAvailable answers 503 when the server was built without a
// Directory.
func (s *Server) directoryAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.directory == nil {
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "discord directory unavailable")
		return false
	}
	return true
}

// parseUserIDs splits a comma-separated ID list, keeping the first
// occurrence of each numeric ID. It reports false when more than
// userLookupMaxIDs remain.
func parseUserIDs(raw string) ([]string, bool) {
	var ids []string
	for part := range strings.SplitSeq(raw, ",") {
		id := strings.TrimSpace(part)
		if _, err := strconv.ParseUint(id, 10, 64); err != nil || slices.Contains(ids, id) {
			continue
		}
		ids = append(ids, id)
		if len(ids) > userLookupMaxIDs {
			return nil, false
		}
	}
	return ids, true
}

// writeDirectoryError answers a failed Discord lookup like GET /guilds
// does, with a 502, except that a Discord rate limit is a 503: the request
// may succeed shortly.
func writeDirectoryError(w http.ResponseWriter, r *http.Request, err error, message string) {
	var discordErr quack.DiscordError
	if errors.As(err, &discordErr) && discordErr.HasFailure(quack.DiscordFailureRateLimited) {
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "discord rate limit reached")
		return
	}
	writeError(w, r, http.StatusBadGateway, codeDependency, message)
}

// nonNil returns items, or an empty slice for nil, so lists encode as [].
func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
