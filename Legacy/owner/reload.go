package owner

import (
	"os"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
)

func init() {
	Commands["reload"] = &Command{
		Name:    "reload",
		Handler: reload,
	}
}

func reload(s *discordgo.Session, m *discordgo.MessageCreate) {
	services.DisconnectDB()
	services.DisconnectRedis()
	services.DisconnectDiscord()

	err := restartprocess()
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to restart process")
		return
	}
}

func restartprocess() error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}

	return syscall.Exec(binary, os.Args, os.Environ())
}
