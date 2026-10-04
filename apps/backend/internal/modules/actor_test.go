package modules_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestActorForMapsLivePermissions(t *testing.T) {
	staff := &quack.GuildStaffContext{
		Guild:              &quack.Guild{ULIDModel: quack.ULIDModel{ID: "guild"}},
		ActorDiscordUserID: "actor",
		Permissions:        map[quack.PermissionAction]bool{quack.PermissionActionGuildSettingsWrite: true},
	}
	want := modules.Actor{GuildID: "guild", DiscordUserID: "actor", CanManage: true}
	if got := modules.ActorFor(staff); got != want {
		t.Fatalf("manager actor = %+v, want %+v", got, want)
	}
	staff.Permissions[quack.PermissionActionTicketResolve] = true
	if got := modules.ActorFor(staff); !got.CanModerate {
		t.Fatalf("moderator actor = %+v, want CanModerate", got)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := modules.RequestActor(request); err == nil {
		t.Fatal("RequestActor resolved an actor without a guild context")
	}
	request = request.WithContext(quack.ContextWithStaff(request.Context(), staff))
	if got, err := modules.RequestActor(request); err != nil || got.GuildID != "guild" {
		t.Fatalf("RequestActor = %+v, %v; want guild", got, err)
	}
}
