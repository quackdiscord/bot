package components

import (
	"github.com/bwmarrin/discordgo"
)

//Modal ...
type Modal struct {
	*discordgo.Message
}


//NewModal returns a new modal object
func NewModal() *Modal {
	return &Modal{&discordgo.Message{}}
}
