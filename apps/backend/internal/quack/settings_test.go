package quack_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestGuildSettingsServiceAuthorizationAuditAndNotice(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	bootstrap, err := repositories.BootstrapGuild(ctx, quack.BootstrapGuildParams{Starter: quack.StarterTemplate(),
		DiscordGuildID: "settings-guild", Name: "Settings Guild", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	manager := templateGuildContext(t, repositories, "settings-guild", "manager-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repositories, "settings-guild", "moderator-1", uint64(discordgo.PermissionModerateMembers))
	registry := modules.NewRegistry(repositories.DB())
	service := quack.NewGuildSettingsService(repositories, allowStaffChannel{}, registry)

	auditChannel := "100000000000000001"
	intro, footer := "Welcome to this guild", "Review case details in Quack"
	tickets, logging, honeypot := true, true, false
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{TicketsEnabled: &tickets}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("switched on a module that was never set up: %v", err)
	}
	for _, id := range []modules.ID{modules.Tickets, modules.GeneralLogging} {
		if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: id}); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := service.Update(ctx, manager, quack.GuildSettingsInput{
		AuditMirrorChannelDiscordID: &auditChannel,
		NotificationIntroduction:    &intro, NotificationFooter: &footer,
		TicketsEnabled: &tickets, GeneralLoggingEnabled: &logging, HoneypotEnabled: &honeypot,
	})
	if err != nil {
		t.Fatalf("update guild settings: %v", err)
	}
	if updated.AuditMirrorChannelDiscordID != auditChannel || updated.ManagedEvidenceChannelDiscordID != "" || !updated.TicketsEnabled || !updated.GeneralLoggingEnabled || updated.HoneypotEnabled {
		t.Fatalf("unexpected settings response: %+v", updated)
	}
	if states, err := registry.ModuleStates(ctx, bootstrap.Guild.ID); err != nil || states != (quack.ModuleStates{Tickets: true, GeneralLogging: true}) {
		t.Fatalf("module switches = %+v, %v", states, err)
	}
	noModules := quack.NewGuildSettingsService(repositories, allowStaffChannel{}, nil)
	if _, err := noModules.Update(ctx, manager, quack.GuildSettingsInput{HoneypotEnabled: &tickets}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("switched a module on without modules: %v", err)
	}

	evidenceChannel, noEvidence := "100000000000000002", ""
	if updated, err := service.Update(ctx, manager, quack.GuildSettingsInput{ManagedEvidenceChannelDiscordID: &evidenceChannel}); err != nil || updated.ManagedEvidenceChannelDiscordID != evidenceChannel {
		t.Fatalf("set evidence channel = %+v, %v", updated, err)
	}
	if updated, err := service.Update(ctx, manager, quack.GuildSettingsInput{ManagedEvidenceChannelDiscordID: &noEvidence}); err != nil || updated.ManagedEvidenceChannelDiscordID != "" {
		t.Fatalf("clear evidence channel = %+v, %v", updated, err)
	}
	unvalidated := quack.NewGuildSettingsService(repositories, nil, registry)
	if _, err := unvalidated.Update(ctx, manager, quack.GuildSettingsInput{ManagedEvidenceChannelDiscordID: &evidenceChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("unvalidated evidence destination accepted: %v", err)
	}
	if _, err := unvalidated.Update(ctx, manager, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &auditChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("unvalidated audit destination accepted: %v", err)
	}
	deniedValue := "forbidden"
	if _, err := service.Update(ctx, moderator, quack.GuildSettingsInput{NotificationFooter: &deniedValue}); !errors.Is(err, quack.ErrGuildSettingsPermissionDenied) {
		t.Fatalf("expected denied moderator write, got %v", err)
	}
	tooLong := strings.Repeat("x", 2001)
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{NotificationIntroduction: &tooLong}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("expected validation failure, got %v", err)
	}
	invalidChannel := "not-a-channel"
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &invalidChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("expected non-snowflake channel rejection, got %v", err)
	}

	audits, err := repositories.ListAuditLogEntries(ctx, bootstrap.Guild.ID)
	if err != nil {
		t.Fatalf("list settings audits: %v", err)
	}
	results := map[quack.AuditResult]bool{}
	for _, audit := range audits {
		if audit.Action == "guild_settings.update" {
			results[audit.Result] = true
		}
	}
	for _, result := range []quack.AuditResult{quack.AuditResultSuccess, quack.AuditResultFailure} {
		if !results[result] {
			t.Fatalf("missing %s settings audit in %+v", result, audits)
		}
	}
	if results[quack.AuditResultDenied] {
		t.Fatalf("a refused settings change was audited: %+v", audits)
	}

	acknowledged, err := service.AcknowledgeStarterPolicyNotice(ctx, manager)
	if err != nil {
		t.Fatalf("acknowledge starter notice: %v", err)
	}
	if acknowledged.StarterPolicyReviewRequired || acknowledged.StarterPolicyNoticeAcknowledgedAt == nil {
		t.Fatalf("starter notice did not become one-time acknowledged state: %+v", acknowledged)
	}
	starter, err := repositories.GetCaseTemplateExpanded(ctx, bootstrap.Guild.ID, bootstrap.StarterTemplate.Template.ID)
	if err != nil || starter == nil || starter.Template.ArchivedAt != nil {
		t.Fatalf("acknowledgement changed starter policy availability: starter=%+v err=%v", starter, err)
	}
	read, err := service.Get(ctx, manager)
	if err != nil || read.StarterPolicyReviewRequired {
		t.Fatalf("read did not expose acknowledged setup state: read=%+v err=%v", read, err)
	}
}

// allowStaffChannel isolates settings persistence tests from the live Discord adapter.
type allowStaffChannel struct{}

func (allowStaffChannel) ValidateStaffChannel(context.Context, string, string) error { return nil }

// staffChannelsAndRoles accepts every staff channel, and records the staff
// roles each was judged by. Its guild has the roles in roles, "200" and
// "201" unless set.
type staffChannelsAndRoles struct {
	allowStaffChannel
	roles  *[]string
	judged *[]quack.StaffRoles
}

func (c staffChannelsAndRoles) GuildRoles(context.Context, string) ([]quack.DiscordRole, error) {
	ids := []string{"201", "200"}
	if c.roles != nil {
		ids = *c.roles
	}
	out := make([]quack.DiscordRole, len(ids))
	for i, id := range ids {
		out[i] = quack.DiscordRole{ID: id, Name: "Role " + id}
	}
	return out, nil
}

func (c staffChannelsAndRoles) ValidateStaffChannelForRoles(_ context.Context, _, _ string, roles quack.StaffRoles) error {
	if c.judged != nil {
		*c.judged = append(*c.judged, roles)
	}
	return nil
}

func TestGuildSettingsStaffRoles(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	bootstrap, err := repositories.BootstrapGuild(ctx, quack.BootstrapGuildParams{Starter: quack.StarterTemplate(),
		DiscordGuildID: "300", Name: "Roles Guild", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	owner := templateGuildContext(t, repositories, "300", "owner-1", 0)
	manager := templateGuildContext(t, repositories, "300", "manager-1", uint64(discordgo.PermissionManageGuild))
	if !owner.Can(quack.PermissionActionStaffRolesWrite) || manager.Can(quack.PermissionActionStaffRolesWrite) {
		t.Fatalf("staff_roles.write: owner %v, manager %v", owner.Permissions, manager.Permissions)
	}
	guildRoles := []string{"201", "200", "202"}
	var judged []quack.StaffRoles
	service := quack.NewGuildSettingsService(repositories, staffChannelsAndRoles{roles: &guildRoles, judged: &judged}, nil)

	got, err := service.Get(ctx, manager)
	if err != nil || got.ModeratorRoleIDs == nil || len(got.ModeratorRoleIDs) != 0 || got.RulesManagerRoleIDs == nil {
		t.Fatalf("unset staff roles = %+v, %v; want empty lists", got, err)
	}
	moderators, rules := []string{" 201 ", "201"}, []string{"200"}
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{ModeratorRoleIDs: &moderators}); !errors.Is(err, quack.ErrGuildSettingsPermissionDenied) {
		t.Fatalf("manager changed moderator roles: %v", err)
	}
	if got, err = service.Update(ctx, manager, quack.GuildSettingsInput{RulesManagerRoleIDs: &rules}); err != nil || !slices.Equal(got.RulesManagerRoleIDs, []string{"200"}) {
		t.Fatalf("manager set rules manager roles = %+v, %v", got, err)
	}
	channel := "100000000000000009"
	got, err = service.Update(ctx, owner, quack.GuildSettingsInput{ModeratorRoleIDs: &moderators, AuditMirrorChannelDiscordID: &channel})
	if err != nil || !slices.Equal(got.ModeratorRoleIDs, []string{"201"}) || !slices.Equal(got.RulesManagerRoleIDs, []string{"200"}) {
		t.Fatalf("owner set moderator roles = %+v, %v", got, err)
	}
	if len(judged) != 1 || !slices.Equal(judged[0].ModeratorRoleIDs, []string{"201"}) {
		t.Fatalf("audit channel judged by %+v, want the moderator roles being saved", judged)
	}
	same := []string{"201"}
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{ModeratorRoleIDs: &same}); err != nil {
		t.Fatalf("manager resending the same moderator roles: %v", err)
	}

	// A stored role deleted in Discord no longer blocks saving: it is
	// dropped, and dropping it is no change a manager needs permission for.
	more := []string{"201", "202"}
	if _, err := service.Update(ctx, owner, quack.GuildSettingsInput{ModeratorRoleIDs: &more}); err != nil {
		t.Fatal(err)
	}
	guildRoles = []string{"201", "200"}
	if got, err = service.Update(ctx, manager, quack.GuildSettingsInput{ModeratorRoleIDs: &more}); err != nil || !slices.Equal(got.ModeratorRoleIDs, []string{"201"}) {
		t.Fatalf("resaving with a deleted role = %+v, %v; want it dropped", got, err)
	}
	newAndGone := []string{"201", "203"}
	if _, err := service.Update(ctx, owner, quack.GuildSettingsInput{ModeratorRoleIDs: &newAndGone}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("new unknown role accepted: %v", err)
	}

	tooMany := make([]string, 26)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(1000 + i)
	}
	for name, roleIDs := range map[string][]string{
		"not a snowflake": {"mods"},
		"leading zero":    {"0201"},
		"@everyone":       {"300"},
		"unknown role":    {"204"},
		"too many":        tooMany,
	} {
		if _, err := service.Update(ctx, owner, quack.GuildSettingsInput{RulesManagerRoleIDs: &roleIDs}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
			t.Errorf("%s: accepted %v (err %v)", name, roleIDs, err)
		}
	}
	unchecked := quack.NewGuildSettingsService(repositories, allowStaffChannel{}, nil)
	other := []string{"200"}
	if _, err := unchecked.Update(ctx, owner, quack.GuildSettingsInput{ModeratorRoleIDs: &other}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("staff roles set without role validation: %v", err)
	}
	none := []string{}
	if got, err := unchecked.Update(ctx, owner, quack.GuildSettingsInput{ModeratorRoleIDs: &none}); err != nil || len(got.ModeratorRoleIDs) != 0 {
		t.Fatalf("clearing moderator roles = %+v, %v", got, err)
	}

	audits, err := repositories.ListAuditLogEntries(ctx, bootstrap.Guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	results := map[quack.AuditResult]int{}
	for _, audit := range audits {
		if audit.Action == "guild_settings.update" {
			results[audit.Result]++
		}
	}
	// The manager's refused moderator change is not audited.
	want := map[quack.AuditResult]int{quack.AuditResultSuccess: 6, quack.AuditResultFailure: 7}
	if !maps.Equal(results, want) {
		t.Fatalf("settings audits = %v, want %v", results, want)
	}
}

// interleavingStore runs between once, right after the next settings read,
// to commit a concurrent change between an update's read and its write.
type interleavingStore struct {
	quack.SettingsStore
	between func()
}

func (s *interleavingStore) GetGuildSettings(ctx context.Context, guildID string) (*quack.GuildSettings, error) {
	settings, err := s.SettingsStore.GetGuildSettings(ctx, guildID)
	if between := s.between; between != nil {
		s.between = nil
		between()
	}
	return settings, err
}

// TestGuildSettingsUpdateKeepsConcurrentStaffRoleChanges replays a Manage
// Server user's update racing an owner who changes the moderator roles
// after the update read the settings. The update must never put back the
// roles it read.
func TestGuildSettingsUpdateKeepsConcurrentStaffRoleChanges(t *testing.T) {
	testConcurrentStaffRoleChanges(t, newMigratedStore(t))
}

// testConcurrentStaffRoleChanges runs the race of
// TestGuildSettingsUpdateKeepsConcurrentStaffRoleChanges on repositories.
func testConcurrentStaffRoleChanges(t *testing.T, repositories *store.Store) {
	ctx := context.Background()
	bootstrap, err := repositories.BootstrapGuild(ctx, quack.BootstrapGuildParams{Starter: quack.StarterTemplate(),
		DiscordGuildID: "400", Name: "Race Guild", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	owner := templateGuildContext(t, repositories, "400", "owner-1", 0)
	manager := templateGuildContext(t, repositories, "400", "manager-1", uint64(discordgo.PermissionManageGuild))
	channels := staffChannelsAndRoles{}
	admin := quack.NewGuildSettingsService(repositories, channels, nil)
	racing := &interleavingStore{SettingsStore: repositories}
	managers := quack.NewGuildSettingsService(racing, channels, nil)
	setModerators := func(roleIDs ...string) func() {
		return func() {
			if _, err := admin.Update(ctx, owner, quack.GuildSettingsInput{ModeratorRoleIDs: &roleIDs}); err != nil {
				t.Fatalf("owner sets moderator roles %v: %v", roleIDs, err)
			}
		}
	}
	stored := func() *quack.GuildSettings {
		t.Helper()
		settings, err := repositories.GetGuildSettings(ctx, bootstrap.Guild.ID)
		if err != nil {
			t.Fatal(err)
		}
		return settings
	}
	setModerators("200")()

	// The owner revokes 200 after the manager's footer update read it.
	racing.between = setModerators("201")
	footer := "Footer"
	got, err := managers.Update(ctx, manager, quack.GuildSettingsInput{NotificationFooter: &footer})
	if err != nil || got.NotificationFooter != footer || !slices.Equal(got.ModeratorRoleIDs, []string{"201"}) {
		t.Fatalf("racing footer update = %+v, %v; want the footer saved and the owner's roles kept", got, err)
	}
	if settings := stored(); !slices.Equal(settings.ModeratorRoleIDs, []string{"201"}) || settings.NotificationFooter != footer {
		t.Fatalf("stored after footer update = %+v", settings)
	}

	// Resending the roles it read, now stale, is refused as a whole.
	racing.between = setModerators("200")
	read, newFooter := []string{"201"}, "Other footer"
	if _, err := managers.Update(ctx, manager, quack.GuildSettingsInput{ModeratorRoleIDs: &read, NotificationFooter: &newFooter}); !errors.Is(err, quack.ErrGuildSettingsConflict) {
		t.Fatalf("resending stale moderator roles = %v, want ErrGuildSettingsConflict", err)
	}
	if settings := stored(); !slices.Equal(settings.ModeratorRoleIDs, []string{"200"}) || settings.NotificationFooter != footer {
		t.Fatalf("stored after stale resend = %+v; want nothing written", settings)
	}

	// A staff channel judged by roles that changed meanwhile is refused too.
	racing.between = setModerators("201")
	channel := "100000000000000010"
	if _, err := managers.Update(ctx, manager, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &channel}); !errors.Is(err, quack.ErrGuildSettingsConflict) {
		t.Fatalf("audit channel judged by stale roles = %v, want ErrGuildSettingsConflict", err)
	}
	if settings := stored(); settings.AuditMirrorChannelDiscordID != "" {
		t.Fatalf("stored after stale channel = %+v; want nothing written", settings)
	}
}
