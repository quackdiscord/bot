package storage

import (
	"time"

	"github.com/quackdiscord/bot/structs"
)

// ----
// Core getters and setters
// for getting data in mod tables
// ----

func GetActiveModsByGuildID(guildID string) ([]string, error) {
	// just look at the mod_profiles table
	return nil, nil
}

func GetAllModsByGuildID(guildID string) ([]string, error) {
	// first will look at mod_profiles table, then backfill with cases table
	return nil, nil
}

// Mod profile storage
func CreateModProfile(m *structs.ModProfile) error {
	return nil
}

func GetModProfile(userID, guildID string) (*structs.ModProfile, error) {
	return nil, nil
}

// Mod flags storage
func CreateModFlag(f *structs.ModFlag) error {
	return nil
}

func GetModFlagByID(id string) (*structs.ModFlag, error) {
	return nil, nil
}

func GetModFlagsByUserID(userID, guildID string) ([]*structs.ModFlag, error) {
	return nil, nil
}

// admin notes storage
func CreateAdminNote(n *structs.Note) error {
	return nil
}

func GetModNoteByID(id string) (*structs.Note, error) {
	return nil, nil
}

func GetModNotesByUserID(userID, guildID string) ([]*structs.Note, error) {
	return nil, nil
}

// ----
// Alternate getters
// for getting stats in other tables
// ----

func GetModCaseCountByType(userID, guildID string) ([]int, error) {
	return nil, nil
}

func GetModCaseCountByTypeInTimeWindow(userID, guildID string, window time.Duration) ([]int, error) {
	return nil, nil
}

func GetModTicketCount(userID, guildID string) (int, error) {
	return 0, nil
}

func GetModAppealCount(userID, guildID string) (int, error) {
	return 0, nil
}

func GetModNoteCount(userID, guildID string) (int, error) {
	return 0, nil
}

func GetModFrequentUsersPunished(userID, guildID string) ([]string, error) {
	return nil, nil
}

func GetModUniqueUsersPunished(userID, guildID string) (int, error) {
	return 0, nil
}
