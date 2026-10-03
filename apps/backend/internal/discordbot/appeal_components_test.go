package discordbot

import (
	"testing"

	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestRegisterAppealComponentsRequiresCompleteDependencies(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	if err := RegisterAppealComponents(registry, &quack.Services{}, quack.NewAppealService(nil)); err == nil {
		t.Fatal("incomplete appeal component dependencies were accepted")
	}
	if _, found, err := registry.LookupComponent(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "reverse", Version: "v1", Payload: "appeal,execution,unban_user"})); err != nil || found {
		t.Fatalf("failed registration mutated component registry: found=%v err=%v", found, err)
	}
}

func TestRegisterAppealComponentsExposesReversalHandler(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	services := quack.New(quack.Deps{})
	if err := RegisterAppealComponents(registry, services, quack.NewAppealService(nil)); err != nil {
		t.Fatalf("register appeal component: %v", err)
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "reverse", Version: "v1", Payload: "appeal,execution,unban_user"})
	if _, found, err := registry.LookupComponent(customID); err != nil || !found {
		t.Fatalf("appeal reversal handler was not exposed: found=%v err=%v", found, err)
	}
}
