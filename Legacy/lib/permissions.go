package lib

import "github.com/bwmarrin/discordgo"

var Permissions = struct {
	// Core permissions (commonly used)
	BanMembers      int64
	KickMembers     int64
	ModerateMembers int64
	Administrator   int64

	// Alternate permissions (sparsely used across codebase)
	SendMessages          int64
	SendMessagesInThreads int64
}{
	BanMembers:      discordgo.PermissionBanMembers,
	KickMembers:     discordgo.PermissionKickMembers,
	ModerateMembers: discordgo.PermissionModerateMembers,
	Administrator:   discordgo.PermissionAdministrator,

	SendMessages:          discordgo.PermissionSendMessages,
	SendMessagesInThreads: discordgo.PermissionSendMessagesInThreads,
}
