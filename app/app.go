package app

import "github.com/quackdiscord/bot/storage"

// Services is the application service boundary shared by API routes and
// future Discord command adapters. Business use cases should be added here
// instead of being implemented directly in handlers.
type Services struct {
	Store     *storage.Store
	Guilds    *GuildService
	Templates *TemplateService
	Cases     *CaseService
	Actions   *ActionService
}

func New(store *storage.Store) *Services {
	return NewWithDiscordClient(store, NewDiscordAPIClient())
}

func NewWithDiscordClient(store *storage.Store, discord DiscordClient) *Services {
	services := &Services{Store: store}
	services.Guilds = NewGuildService(store, discord)
	services.Templates = NewTemplateService(store)
	services.Cases = NewCaseService(store)
	services.Actions = NewActionService(store, NewDiscordActionClient())
	return services
}
