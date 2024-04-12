package cmds

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

func init() {
	services.Commands[statsCmd.Name] = &services.Command{
		ApplicationCommand: statsCmd,
		Handler:            handleStats,
	}
}

var statsCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "stats",
	Description: "Get some stats about the bot",
}

func handleStats(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	cpuStat, _ := cpu.Times(true)
	totalDelta := float64(cpuStat[0].Total())
	idleDelta := float64(cpuStat[0].Idle)

	memStat, _ := mem.VirtualMemory()
	usedMemory := float64(memStat.Used)
	totalMemory := float64(memStat.Total)

	Servers := fmt.Sprint(len(s.State.Guilds))
	CPUPercent := fmt.Sprintf("%.1f%%", (totalDelta-idleDelta)/totalDelta*100)
	MemoryUsage := fmt.Sprintf("%.1f%%", usedMemory/totalMemory*100)
	HeartbeatLatency := fmt.Sprint(s.HeartbeatLatency().Milliseconds())
	CmdsRun := fmt.Sprint(services.Redis.HGet(services.Redis.Context(), "seeds:cmds", "total").Val())

	CodeDesc := "```asciidoc\n" +
		"Servers   ::   " + Servers + "      \n" +
		"CPU       ::   " + CPUPercent + "      \n" +
		"RAM       ::   " + MemoryUsage + "      \n" +
		"Ping      ::   " + HeartbeatLatency + "ms    \n" +
		"Cmds Run  ::   " + CmdsRun + "      \n" +
		"```"

	embed := components.NewEmbed().
		SetTitle("Quack's Stats").
		SetDescription("Some statistics about Quack\n"+CodeDesc).
		SetColor("Main").
		AddField("Links", "[🌐 Website](https://quackbot.xyz) | [<:invite:823987169978613851> Invite](https://quackbot.xyz/invite) | [<:discord:823989269626355793> Support](https://quackbot.xyz/discord)").
		MessageEmbed

	return EmbedResponse(embed, false)
}
