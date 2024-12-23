package utils

import "github.com/bwmarrin/discordgo"

// CheckPerms checks if a user has a certian permission (redundant but just in case discord breaks)
func CheckPerms(u *discordgo.Member, perms int64) bool {
	// get the user's guild permissions
	guildPerms := u.Permissions
	// check if the user has the required permissions
	return guildPerms&perms == perms
}
