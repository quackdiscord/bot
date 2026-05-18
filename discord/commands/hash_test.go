package commands

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCommandHashIgnoresDiscordGeneratedFields(t *testing.T) {
	left := CommandDefinition()
	right := CommandDefinition()
	right.ID = "remote-command"
	right.ApplicationID = "app"
	right.GuildID = "guild"
	right.Version = "version"

	leftHash, err := CommandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := CommandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected generated fields to be ignored, got %s and %s", leftHash, rightHash)
	}
}

func TestCommandHashChangesWhenDefinitionChanges(t *testing.T) {
	left := CommandDefinition()
	right := CommandDefinition()
	right.Options[0].Options[0].Description = "Changed template description"

	leftHash, err := CommandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := CommandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash == rightHash {
		t.Fatalf("expected command hash to change")
	}
}

func TestCommandHashTreatsUnsetTypeAsChatCommand(t *testing.T) {
	left := &discordgo.ApplicationCommand{Name: "case", Description: "Case"}
	right := &discordgo.ApplicationCommand{Name: "case", Description: "Case", Type: discordgo.ChatApplicationCommand}

	leftHash, err := CommandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := CommandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected unset command type to match chat command")
	}
}

func TestCommandHashIgnoresRemoteDefaultNSFWFalse(t *testing.T) {
	left := CommandDefinition()
	right := CommandDefinition()
	nsfw := false
	right.NSFW = &nsfw

	leftHash, err := CommandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := CommandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected nsfw=false to be ignored")
	}
}

func TestCommandHashChangesForExplicitNSFWTrue(t *testing.T) {
	left := CommandDefinition()
	right := CommandDefinition()
	nsfw := true
	right.NSFW = &nsfw

	leftHash, err := CommandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := CommandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash == rightHash {
		t.Fatalf("expected nsfw=true to change hash")
	}
}

func TestCommandHashIgnoresRemoteDefaultIntegrationTypes(t *testing.T) {
	left := CommandDefinition()
	right := CommandDefinition()
	integrationTypes := []discordgo.ApplicationIntegrationType{
		discordgo.ApplicationIntegrationGuildInstall,
		discordgo.ApplicationIntegrationUserInstall,
	}
	right.IntegrationTypes = &integrationTypes

	leftHash, err := CommandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := CommandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected default integration types to be ignored")
	}
}

func TestCommandHashChangesForExplicitNonDefaultIntegrationTypes(t *testing.T) {
	left := CommandDefinition()
	right := CommandDefinition()
	integrationTypes := []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}
	right.IntegrationTypes = &integrationTypes

	leftHash, err := CommandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := CommandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash == rightHash {
		t.Fatalf("expected explicit non-default integration types to change hash")
	}
}
