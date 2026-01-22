package owner

import (
	"io"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// command: !!!curl <url>
func Curl(s *discordgo.Session, m *discordgo.MessageCreate) {
	url := strings.Split(m.Content, " ")[1]
	if url == "" || !strings.HasPrefix(url, "https://") {
		s.ChannelMessageSend(m.ChannelID, "Please provide a valid URL")
		return
	}

	resp, err := http.Get(url)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to fetch URL: "+err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Failed to read response body: "+err.Error())
		return
	}

	// send a code block with the body trimmed if over 1997, add ...
	bodyStr := string(body)
	if len(bodyStr) > 1997 {
		bodyStr = bodyStr[:1997] + "..."
	}
	s.ChannelMessageSend(m.ChannelID, "```"+bodyStr+"```")
}
