package quack

import (
	"strings"

	"github.com/quackdiscord/bot/internal/config"
)

// Services is the application service boundary shared by API routes and
// future Discord command adapters. Business use cases should be added here
// instead of being implemented directly in handlers.
type Services struct {
	Config    config.Config
	Store     Repository
	Guilds    *GuildService
	Settings  *GuildSettingsService
	Templates *TemplateService
	Cases     *CaseService
	Audits    *AuditService
	Actions   *ActionService
	Evidence  *EvidenceService
	Ops       *OpsService
}

// New constructs new with required dependencies explicit so callers control lifecycle and substitution.
func New(store Repository) *Services {
	return NewWithConfigDependencies(config.Default(), store, nil, nil, nil)
}

// NewWithDiscordClient constructs with discord client with required dependencies explicit so callers control lifecycle and substitution.
func NewWithDiscordClient(store Repository, discord DiscordClient) *Services {
	return NewWithConfigDependencies(config.Default(), store, discord, nil, nil)
}

// NewWithConfigDependencies constructs with config dependencies with required dependencies explicit so callers control lifecycle and substitution.
func NewWithConfigDependencies(cfg config.Config, store Repository, discord DiscordClient, actions DiscordActionClient, scheduler CaseWorkScheduler) *Services {
	services := &Services{Config: cfg, Store: store}
	services.Guilds = NewGuildService(store, discord)
	services.Settings = NewGuildSettingsService(store)
	if channels, ok := actions.(StaffChannelValidator); ok {
		services.Settings.WithStaffChannelValidator(channels)
	}
	services.Templates = NewTemplateService(store)
	services.Cases = NewCaseService(store, scheduler)
	if evidenceClient, ok := actions.(DiscordEvidenceClient); ok {
		services.Evidence = NewEvidenceService(evidenceClient, store)
		services.Cases.WithEvidenceCapture(services.Evidence)
	} else {
		services.Evidence = NewEvidenceService(nil, store)
	}
	if discord != nil {
		services.Cases.authorizer = services.Guilds
	}
	services.Audits = NewAuditService(store)
	services.Actions = NewActionService(store, actions).WithRecoveryControls(services.Guilds, scheduler).WithDashboardBaseURL(dashboardBaseURL(cfg))
	services.Ops = NewOpsService(store, scheduler)
	return services
}

// dashboardBaseURL selects the first secure configured dashboard origin.
func dashboardBaseURL(cfg config.Config) string {
	for _, origin := range cfg.API.CORSOrigins {
		if value := strings.TrimSpace(origin); strings.HasPrefix(value, "https://") {
			return strings.TrimRight(value, "/")
		}
	}
	return ""
}
