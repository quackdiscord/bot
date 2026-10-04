package quack_test

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// failingEnforcer fails every action permanently.
type failingEnforcer struct{ fakeEnforcementClient }

func (failingEnforcer) TimeoutMember(context.Context, string, string, int, string) (map[string]any, error) {
	return nil, quack.DiscordError{Code: "timeout_permission_or_hierarchy_denied", Message: "Missing permissions"}
}

func TestAuditMirrorDescribesTheCaseBehindAnEntry(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	if err := store.DB().Create(&quack.GuildSettings{ULIDModel: quack.ULIDModel{ID: quack.NewID()}, GuildID: admin.Guild.ID, AuditMirrorChannelDiscordID: "123456789012345678"}).Error; err != nil {
		t.Fatal(err)
	}
	client := &failingEnforcer{}
	created, _ := timeoutCase(t, store, "mirrored", "target-1", client, client)

	sender := &fakeAuditMirrorSender{}
	if err := quack.NewAuditMirror(store, sender).PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var create, failed *quack.AuditMirrorMessage
	for i, message := range sender.messages {
		switch message.Action {
		case string(quack.AuditActionCaseCreate):
			create = &sender.messages[i]
		case string(quack.AuditActionActionFailed):
			failed = &sender.messages[i]
		}
	}
	if create == nil || create.CaseID != created.ID || create.CaseNumber != created.CaseNumber || create.TargetDiscordUserID != "target-1" ||
		create.RuleName != "Spam" || create.SelectedLevelName != "Default" || create.SelectedOutcome != "Timeout (1h)" {
		t.Fatalf("case.create message = %+v", create)
	}
	if failed == nil || failed.ActionType != quack.ActionTimeoutUser || failed.RetryExecutionID == "" || failed.SelectedOutcome != "" {
		t.Fatalf("case_action.failed message = %+v", failed)
	}
}
