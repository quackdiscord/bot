package moduleintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type systemCaseCreatorFake struct {
	guildID string
	input   quack.CaseInput
	result  *quack.CaseResponse
}

func (f *systemCaseCreatorFake) CreateSystemHoneypot(_ context.Context, guildID string, input quack.CaseInput) (*quack.CaseResponse, error) {
	f.guildID, f.input = guildID, input
	return f.result, nil
}

func TestModuleAuditAdapterWritesImmutableCoreEntry(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:module-audit?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	repository := store.New(db, nil)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	guild := quack.Guild{ULIDModel: quack.ULIDModel{ID: "01J60000000000000000000001"}, DiscordGuildID: "discord-guild", Name: "Guild", IsActive: true}
	if err := db.Create(&guild).Error; err != nil {
		t.Fatalf("create guild: %v", err)
	}
	auditor := moduleAuditor{repository: repository}
	if err := auditor.RecordModuleAudit(context.Background(), modules.AuditEvent{
		GuildID: guild.ID, ActorDiscordUserID: "actor", Action: "ticket.open",
		ResourceType: "ticket", ResourceID: "ticket-1", Result: "success", MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("record module audit: %v", err)
	}
	entries, err := repository.ListAuditLogEntries(context.Background(), guild.ID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("read module audit: entries=%+v err=%v", entries, err)
	}
	if entries[0].Source != quack.AuditSourceAPI || entries[0].Action != "ticket.open" {
		t.Fatalf("unexpected module audit: %+v", entries[0])
	}
	if err := auditor.RecordModuleAudit(quack.ContextWithAuditSource(context.Background(), quack.AuditSourceDiscord), modules.AuditEvent{
		GuildID: guild.ID, ActorDiscordUserID: "actor", Action: "ticket.close",
		ResourceType: "ticket", ResourceID: "ticket-1", Result: "success", MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("record Discord module audit: %v", err)
	}
	if err := auditor.RecordModuleAudit(context.Background(), modules.AuditEvent{
		GuildID: guild.ID, Action: "honeypot.trigger.accepted", ResourceType: "honeypot_trigger", Result: "success", MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("record honeypot module audit: %v", err)
	}
	entries, err = repository.ListAuditLogEntries(context.Background(), guild.ID)
	if err != nil || len(entries) != 3 || entries[1].Source != quack.AuditSourceDiscord || entries[2].Source != quack.AuditSourceHoneypot {
		t.Fatalf("module audit sources were not propagated: entries=%+v err=%v", entries, err)
	}
}

func TestPrivateChannelACLRequiresEveryoneDenialAndStaffVisibility(t *testing.T) {
	channel := &discordgo.Channel{GuildID: "guild", PermissionOverwrites: ticketPermissionOverwrites("guild", "owner", "bot", []string{"staff"})}
	if err := validateTicketACL(channel, "guild", "owner", "bot", []string{"staff"}); err != nil {
		t.Fatalf("valid private ACL rejected: %v", err)
	}
	channel.PermissionOverwrites = channel.PermissionOverwrites[1:]
	if err := validateTicketACL(channel, "guild", "owner", "bot", []string{"staff"}); err == nil {
		t.Fatal("public ticket ACL was accepted")
	}
}

func TestOptionalModuleHTTPRegistrarsMountCompleteSurface(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:module-routes?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatalf("migrate registry: %v", err)
	}
	if err := tickets.Migration().Apply(db); err != nil {
		t.Fatalf("migrate tickets: %v", err)
	}
	if err := honeypot.Migration().Apply(db); err != nil {
		t.Fatalf("migrate honeypots: %v", err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor(), generallogging.Descriptor(), honeypot.Descriptor())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	runtime := &Runtime{
		Tickets:  tickets.NewService(registry, tickets.NewStore(db), nil),
		Logging:  generallogging.NewService(registry, nil, nil, nil),
		Honeypot: honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil),
	}
	repository := store.New(db, nil)
	services := quack.New(quack.Deps{Store: repository})
	engine := gin.New()
	if err := runtime.RegisterHTTP(engine.Group("/guilds"), services, config.Default(), httpplatform.FromRepository(repository)); err != nil {
		t.Fatalf("register module routes: %v", err)
	}
	want := map[string]bool{
		"GET /guilds/:discordGuildID/modules/tickets/status":               false,
		"GET /guilds/:discordGuildID/modules/tickets/:ticketID/transcript": false,
		"PUT /guilds/:discordGuildID/modules/general-logging/settings":     false,
		"PUT /guilds/:discordGuildID/modules/honeypot/settings":            false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, present := range want {
		if !present {
			t.Fatalf("missing optional module route %s", route)
		}
	}
}

func TestHoneypotCaseAdapterPreservesNormalPathEnvelope(t *testing.T) {
	creator := &systemCaseCreatorFake{result: &quack.CaseResponse{ID: "case-1"}}
	applier := honeypotCaseApplier{cases: creator}
	request := honeypot.ApplyRequest{
		GuildID: "guild", TemplateID: "template", TargetDiscordUserID: "target",
		ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
		ContextURL: "https://discord.com/channels/1/2/3", IdempotencyKey: "honeypot:guild:message",
		Source: honeypot.SourceHoneypot, ActorType: honeypot.ActorTypeSystem,
	}
	result, err := applier.ApplyHoneypotCase(context.Background(), request)
	if err != nil || result.CaseID != "case-1" {
		t.Fatalf("apply normal case: result=%+v err=%v", result, err)
	}
	if creator.guildID != request.GuildID || creator.input.TemplateID != request.TemplateID || creator.input.TargetDiscordUserID != request.TargetDiscordUserID || creator.input.Source != quack.CaseSourceHoneypot || creator.input.ContextChannelDiscordID != request.ContextChannelDiscordID || creator.input.ContextMessageDiscordID != request.ContextMessageDiscordID || creator.input.ContextURL != request.ContextURL || creator.input.IdempotencyKey != request.IdempotencyKey {
		t.Fatalf("adapter changed the normal-path envelope: guild=%q input=%+v", creator.guildID, creator.input)
	}
	request.ActorDiscordUserID = "fabricated-staff"
	if _, err := applier.ApplyHoneypotCase(context.Background(), request); err == nil {
		t.Fatal("adapter accepted fabricated staff attribution")
	}
}

func TestHoneypotProjectionUsesCurrentMemberRolesAndPermissions(t *testing.T) {
	guild := &discordgo.Guild{
		ID: "guild", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: "guild", Permissions: discordgo.PermissionViewChannel},
			{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
		},
	}
	channel := &discordgo.Channel{ID: "channel", GuildID: "guild"}
	member := &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "author", Bot: false}, Roles: []string{"staff", "exempt"}}
	event := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "author", Bot: true}}}
	projection, err := projectHoneypotMessage("internal-guild", event, guild, channel, member, "quack")
	if err != nil {
		t.Fatal(err)
	}
	if projection.GuildID != "internal-guild" || projection.IsBot || !projection.AuthorCanModerate || len(projection.AuthorRoleDiscordIDs) != 2 || projection.AuthorRoleDiscordIDs[1] != "exempt" || projection.MessageURL != "https://discord.com/channels/guild/channel/message" {
		t.Fatalf("projection trusted event claims or lost live facts: %+v", projection)
	}
	member.User.Bot = true
	projection, _ = projectHoneypotMessage("internal-guild", event, guild, channel, member, "author")
	if !projection.IsBot || !projection.IsQuack {
		t.Fatalf("live bot identity was not authoritative: %+v", projection)
	}
	if _, err := projectHoneypotMessage("", event, guild, channel, member, "author"); err == nil {
		t.Fatal("projection accepted a missing internal guild identity")
	}
}

func TestGatewayIntentsFollowEnabledModulesWithoutMessageContentLeak(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:module-intents?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{db: db}
	intents, err := runtime.RequiredGatewayIntents(context.Background())
	if err != nil || intents != discordgo.IntentGuilds {
		t.Fatalf("disabled modules requested optional intents: intents=%d err=%v", intents, err)
	}
	now := time.Now().UTC()
	if err := db.Create(&modules.Configuration{ID: "honeypot", GuildID: "guild", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: `{}`, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	intents, err = runtime.RequiredGatewayIntents(context.Background())
	if err != nil || intents&discordgo.IntentGuildMessages == 0 || intents&discordgo.IntentMessageContent != 0 {
		t.Fatalf("honeypot intent boundary is wrong: intents=%d err=%v", intents, err)
	}
	if err := db.Create(&modules.Configuration{ID: "logging", GuildID: "guild", ModuleID: modules.GeneralLogging, Enabled: true, ConfigJSON: `{}`, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	intents, err = runtime.RequiredGatewayIntents(context.Background())
	if err != nil || intents&discordgo.IntentGuildMembers == 0 || intents&discordgo.IntentGuildModeration == 0 || intents&discordgo.IntentMessageContent == 0 {
		t.Fatalf("logging intent boundary is incomplete: intents=%d err=%v", intents, err)
	}
}

func TestTemplateDriftDisablesOnlyMatchingHoneypotConfiguration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:honeypot-template-drift?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repository := store.New(db, nil)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	guild, err := repository.UpsertGuild(context.Background(), quack.UpsertGuildParams{DiscordGuildID: "discord", Name: "Guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	template, err := repository.CreateCaseTemplate(context.Background(), quack.CreateCaseTemplateParams{
		Template: quack.CaseTemplate{GuildID: guild.ID, Slug: "trap", Name: "Trap", ReasonTemplate: "Trap", CreatedByDiscordUserID: "admin", UpdatedByDiscordUserID: "admin"},
		Levels:   []quack.ExpandedCaseTemplateLevel{{Level: quack.CaseTemplateLevel{Name: "Default", Position: 1, IsDefault: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	settingsJSON, _ := json.Marshal(honeypot.Settings{ChannelDiscordID: "channel", TemplateID: template.Template.ID})
	if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: guild.ID, ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: string(settingsJSON)}); err != nil {
		t.Fatal(err)
	}
	service := honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil)
	runtime := &Runtime{repository: repository, HoneypotDiscord: honeypot.NewDiscordAdapter(service)}
	if _, err := repository.ArchiveCaseTemplate(context.Background(), guild.ID, template.Template.ID, nil); err != nil {
		t.Fatal(err)
	}
	runtime.HandleTemplateChange(context.Background(), guild.ID, template.Template.ID)
	configuration, err := registry.Configuration(context.Background(), guild.ID, modules.Honeypots)
	if err != nil || configuration == nil || configuration.Enabled {
		t.Fatalf("template drift did not disable matching honeypot: config=%+v err=%v", configuration, err)
	}
	var settings honeypot.Settings
	if err := json.Unmarshal([]byte(configuration.ConfigJSON), &settings); err != nil || settings.TemplateID != template.Template.ID || settings.ChannelDiscordID != "channel" || settings.DisabledReason == "" {
		t.Fatalf("drift repair context was not retained: settings=%+v err=%v", settings, err)
	}
}

func TestGatewayDriftForwardsChannelAndGuildDeletionWithIsolation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:honeypot-gateway-drift?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repository := store.New(db, nil)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	guildOne, _ := repository.UpsertGuild(context.Background(), quack.UpsertGuildParams{DiscordGuildID: "discord-1", Name: "One", OwnerDiscordUserID: "owner"})
	guildTwo, _ := repository.UpsertGuild(context.Background(), quack.UpsertGuildParams{DiscordGuildID: "discord-2", Name: "Two", OwnerDiscordUserID: "owner"})
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	set := func(guildID, channelID string) {
		t.Helper()
		payload, _ := json.Marshal(honeypot.Settings{ChannelDiscordID: channelID, TemplateID: "template"})
		if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: guildID, ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: string(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	set(guildOne.ID, "channel-1")
	set(guildTwo.ID, "channel-2")
	service := honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil)
	runtime := &Runtime{db: db, registry: registry, resolver: guildResolver{db: db}, HoneypotDiscord: honeypot.NewDiscordAdapter(service)}
	runtime.onChannelDelete(nil, &discordgo.ChannelDelete{Channel: &discordgo.Channel{ID: "channel-1", GuildID: "discord-1"}})
	one, _ := registry.Configuration(context.Background(), guildOne.ID, modules.Honeypots)
	two, _ := registry.Configuration(context.Background(), guildTwo.ID, modules.Honeypots)
	if one.Enabled || !two.Enabled {
		t.Fatalf("channel drift crossed guilds: one=%+v two=%+v", one, two)
	}
	set(guildOne.ID, "channel-1")
	runtime.onGuildDelete(nil, &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: "discord-1"}})
	one, _ = registry.Configuration(context.Background(), guildOne.ID, modules.Honeypots)
	two, _ = registry.Configuration(context.Background(), guildTwo.ID, modules.Honeypots)
	if one.Enabled || !two.Enabled {
		t.Fatalf("guild drift crossed guilds: one=%+v two=%+v", one, two)
	}
}

func TestHTTPActorMappingIncludesManagersAndAdministrators(t *testing.T) {
	for _, permissions := range []map[quack.PermissionAction]bool{
		{quack.PermissionActionGuildSettingsWrite: true},
		{quack.PermissionActionGuildSettingsWrite: true, quack.PermissionActionTicketResolve: true},
	} {
		ctx, _ := gin.CreateTestContext(nil)
		ctx.Set(middleware.ContextGuildKey, &quack.GuildStaffContext{
			Guild:              &quack.Guild{ULIDModel: quack.ULIDModel{ID: "guild"}},
			ActorDiscordUserID: "actor", Permissions: permissions,
		})
		actor, err := resolveTicketActor(ctx)
		if err != nil || !actor.CanManage {
			t.Fatalf("Manage Guild actor lost module authority: actor=%+v err=%v", actor, err)
		}
		loggingActor, err := resolveLoggingActor(ctx)
		if err != nil || !loggingActor.CanManage {
			t.Fatalf("Manage Guild actor lost logging authority: actor=%+v err=%v", loggingActor, err)
		}
	}
}

func TestModuleIdempotencyScopeIncludesOperation(t *testing.T) {
	engine := gin.New()
	seen := make(chan string, 2)
	engine.POST("/guilds/:discordGuildID/modules/tickets/:ticketID/reopen", func(c *gin.Context) {
		c.Set(middleware.ContextGuildKey, &quack.GuildStaffContext{Guild: &quack.Guild{ULIDModel: quack.ULIDModel{ID: "guild"}}, ActorDiscordUserID: "actor"})
		seen <- moduleWriteSubject(c)
	})
	engine.PUT("/guilds/:discordGuildID/modules/tickets/settings", func(c *gin.Context) {
		c.Set(middleware.ContextGuildKey, &quack.GuildStaffContext{Guild: &quack.Guild{ULIDModel: quack.ULIDModel{ID: "guild"}}, ActorDiscordUserID: "actor"})
		seen <- moduleWriteSubject(c)
	})
	for method, path := range map[string]string{
		http.MethodPost: "/guilds/discord/modules/tickets/ticket/reopen",
		http.MethodPut:  "/guilds/discord/modules/tickets/settings",
	} {
		engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	}
	first, second := <-seen, <-seen
	if first == second {
		t.Fatalf("distinct module writes shared idempotency scope %q", first)
	}
}

func TestRuntimeWorkerShutdownIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:module-runtime?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	repository := store.New(db, nil)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	session, err := discordgo.New("Bot integration-test")
	if err != nil {
		t.Fatalf("new Discord session: %v", err)
	}
	runtime, err := New(context.Background(), repository, session, quack.New(quack.Deps{Store: repository}))
	if err != nil {
		t.Fatalf("new module runtime: %v", err)
	}
	runtime.Close()
	runtime.Close()
}

func TestModuleIdempotencyStoresNormalizedErrors(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	engine := gin.New()
	store := httpplatform.NewIdempotencyStore(client, "qi1:test:")
	engine.Use(store.Protect("module", time.Hour, func(*gin.Context) string { return "actor:guild:operation" }))
	engine.Use(middleware.ErrorEnvelope)
	engine.POST("/write", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token=must-not-persist"})
	})
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/write", nil)
		request.Header.Set("Idempotency-Key", "same-write")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "must-not-persist") {
			t.Fatalf("unsafe idempotent error response: status=%d body=%s", response.Code, response.Body.String())
		}
		if attempt == 1 && response.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatal("expected normalized error replay")
		}
	}
}

func TestBulkLoggingSubmissionIsSafeDuringShutdown(t *testing.T) {
	runtime := &Runtime{bulk: make(chan bulkDeleteEvent, 128)}
	var workers sync.WaitGroup
	for i := 0; i < 64; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runtime.submitBulkDelete(bulkDeleteEvent{guildID: "guild", messageIDs: []string{"message"}})
		}()
	}
	runtime.Close()
	workers.Wait()
	runtime.Close()
}
