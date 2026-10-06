package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// fakeDirectory records what the handlers ask for and answers from fixed
// data.
type fakeDirectory struct {
	err         error
	searchQuery string
	searchLimit int
	lookedUp    []string
}

func (f *fakeDirectory) SearchMembers(_ context.Context, _, query string, limit int) ([]DirectoryUser, error) {
	f.searchQuery, f.searchLimit = query, limit
	if f.err != nil {
		return nil, f.err
	}
	return []DirectoryUser{{
		ID: "100", Username: "duck", GlobalName: "Duck", Nick: "Quacker", DisplayName: "Quacker",
		AvatarURL: "https://cdn.discordapp.com/embed/avatars/0.png", InGuild: true,
	}}, nil
}

func (f *fakeDirectory) LookupUsers(_ context.Context, _ string, ids []string) ([]DirectoryUser, error) {
	f.lookedUp = ids
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}

func (f *fakeDirectory) Channels(context.Context, string) ([]DirectoryChannel, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []DirectoryChannel{
		{ID: "1", Name: "general", Type: ChannelTypeText},
		{ID: "2", Name: "Staff", Type: ChannelTypeCategory, Position: 1},
		{ID: "3", Name: "mod-log", Type: ChannelTypeText, ParentID: "2"},
	}, nil
}

func (f *fakeDirectory) Roles(context.Context, string) ([]DirectoryRole, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []DirectoryRole{
		{ID: "20", Name: "Moderators", Color: 0x5865f2, Position: 3},
		{ID: "21", Name: "Quack", Position: 2, Managed: true},
	}, nil
}

// directoryServer serves guild-1 to a caller with permissions, backed by
// directory (none if nil).
func directoryServer(t *testing.T, permissions uint64, directory Directory) (*Server, string) {
	t.Helper()
	store := migratedStore(t)
	server := newTestServer(t, config.Default(), Deps{
		Services:  quack.New(quack.Deps{Store: store, Guilds: staffGuilds(permissions), Modules: modules.NewRegistry(store.DB())}),
		Store:     store,
		Redis:     store.Redis(),
		Directory: directory,
	})
	return server, saveSession(t, store, testSession("user-1"))
}

func TestDirectoryRoutesRequireCapabilities(t *testing.T) {
	moderator, moderatorSession := directoryServer(t, uint64(discordgo.PermissionModerateMembers), &fakeDirectory{})
	expectStatus(t, send(t, moderator, http.MethodGet, "/guilds/guild-1/directory/members?query=du", "", moderatorSession), http.StatusOK)
	expectStatus(t, send(t, moderator, http.MethodGet, "/guilds/guild-1/directory/users?ids=1", "", moderatorSession), http.StatusOK)
	assertEnvelope(t, send(t, moderator, http.MethodGet, "/guilds/guild-1/directory/channels", "", moderatorSession),
		http.StatusForbidden, codeAuthorization)
	assertEnvelope(t, send(t, moderator, http.MethodGet, "/guilds/guild-1/directory/roles", "", moderatorSession),
		http.StatusForbidden, codeAuthorization)

	manager, managerSession := directoryServer(t, uint64(discordgo.PermissionManageGuild), &fakeDirectory{})
	expectStatus(t, send(t, manager, http.MethodGet, "/guilds/guild-1/directory/channels", "", managerSession), http.StatusOK)
	expectStatus(t, send(t, manager, http.MethodGet, "/guilds/guild-1/directory/roles", "", managerSession), http.StatusOK)
	assertEnvelope(t, send(t, manager, http.MethodGet, "/guilds/guild-1/directory/members?query=du", "", managerSession),
		http.StatusForbidden, codeAuthorization)
	assertEnvelope(t, send(t, manager, http.MethodGet, "/guilds/guild-1/directory/users?ids=1", "", managerSession),
		http.StatusForbidden, codeAuthorization)
}

func TestSearchMembers(t *testing.T) {
	directory := &fakeDirectory{}
	server, sessionID := directoryServer(t, uint64(discordgo.PermissionAdministrator), directory)

	for _, query := range []string{"", "?query=", "?query=%20%20"} {
		assertEnvelope(t, send(t, server, http.MethodGet, "/guilds/guild-1/directory/members"+query, "", sessionID),
			http.StatusBadRequest, codeValidation)
	}

	response := send(t, server, http.MethodGet, "/guilds/guild-1/directory/members?query=%20du%20", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	if directory.searchQuery != "du" || directory.searchLimit != memberSearchDefaultLimit {
		t.Errorf("searched %q with limit %d", directory.searchQuery, directory.searchLimit)
	}
	var body map[string][]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	members := body["members"]
	if len(members) != 1 {
		t.Fatalf("members = %v", members)
	}
	var keys []string
	for key := range members[0] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	want := []string{"avatar_url", "bot", "display_name", "global_name", "id", "in_guild", "nick", "username"}
	if !slices.Equal(keys, want) {
		t.Errorf("member fields = %v, want %v", keys, want)
	}
	if members[0]["display_name"] != "Quacker" || members[0]["in_guild"] != true {
		t.Errorf("member = %v", members[0])
	}

	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/directory/members?query=du&limit=60", "", sessionID), http.StatusOK)
	if directory.searchLimit != memberSearchMaxLimit {
		t.Errorf("limit 60 searched with %d, want %d", directory.searchLimit, memberSearchMaxLimit)
	}
}

func TestLookupUsers(t *testing.T) {
	directory := &fakeDirectory{}
	server, sessionID := directoryServer(t, uint64(discordgo.PermissionModerateMembers), directory)

	response := send(t, server, http.MethodGet, "/guilds/guild-1/directory/users?ids=3,1,abc,3,,%202", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	if !slices.Equal(directory.lookedUp, []string{"3", "1", "2"}) {
		t.Errorf("looked up %v", directory.lookedUp)
	}
	if body := response.Body.String(); body != `{"users":[]}` {
		t.Errorf("body = %s, want an empty list", body)
	}

	ids := make([]string, userLookupMaxIDs+1)
	for i := range ids {
		ids[i] = strconv.Itoa(1000 + i)
	}
	assertEnvelope(t, send(t, server, http.MethodGet, "/guilds/guild-1/directory/users?ids="+strings.Join(ids, ","), "", sessionID),
		http.StatusBadRequest, codeValidation)
	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/directory/users?ids="+strings.Join(ids[:userLookupMaxIDs], ","), "", sessionID),
		http.StatusOK)
}

func TestListChannels(t *testing.T) {
	server, sessionID := directoryServer(t, uint64(discordgo.PermissionManageGuild), &fakeDirectory{})
	response := send(t, server, http.MethodGet, "/guilds/guild-1/directory/channels", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	want := `{"channels":[` +
		`{"id":"1","name":"general","type":"text","parent_id":"","position":0},` +
		`{"id":"2","name":"Staff","type":"category","parent_id":"","position":1},` +
		`{"id":"3","name":"mod-log","type":"text","parent_id":"2","position":0}]}`
	if body := response.Body.String(); body != want {
		t.Errorf("body = %s\nwant   %s", body, want)
	}
}

func TestListRoles(t *testing.T) {
	server, sessionID := directoryServer(t, uint64(discordgo.PermissionManageGuild), &fakeDirectory{})
	response := send(t, server, http.MethodGet, "/guilds/guild-1/directory/roles", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	want := `{"roles":[` +
		`{"id":"20","name":"Moderators","color":5793266,"position":3,"managed":false},` +
		`{"id":"21","name":"Quack","color":0,"position":2,"managed":true}]}`
	if body := response.Body.String(); body != want {
		t.Errorf("body = %s\nwant   %s", body, want)
	}
}

func TestDirectoryUnavailable(t *testing.T) {
	server, sessionID := directoryServer(t, uint64(discordgo.PermissionAdministrator), nil)
	for _, path := range []string{"members?query=du", "users?ids=1", "channels", "roles"} {
		assertEnvelope(t, send(t, server, http.MethodGet, "/guilds/guild-1/directory/"+path, "", sessionID),
			http.StatusServiceUnavailable, codeDependency)
	}

	failing, failingSession := directoryServer(t, uint64(discordgo.PermissionAdministrator),
		&fakeDirectory{err: quack.DiscordError{Code: "directory_channels_discord_server_error"}})
	assertEnvelope(t, send(t, failing, http.MethodGet, "/guilds/guild-1/directory/channels", "", failingSession),
		http.StatusBadGateway, codeDependency)
	limited, limitedSession := directoryServer(t, uint64(discordgo.PermissionAdministrator),
		&fakeDirectory{err: quack.DiscordError{Code: "directory_member_" + quack.DiscordFailureRateLimited}})
	assertEnvelope(t, send(t, limited, http.MethodGet, "/guilds/guild-1/directory/users?ids=1", "", limitedSession),
		http.StatusServiceUnavailable, codeDependency)
}
