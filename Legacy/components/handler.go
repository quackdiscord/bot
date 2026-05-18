package components

import (
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/rs/zerolog/log"
)

var Components = make(map[string]func(*discordgo.Session, *discordgo.InteractionCreate) *discordgo.InteractionResponse)

func OnInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	log.Debug().Msgf("[components.OnInteraction] invoked")
	// Defensive: InteractionMessageComponent may occur in DMs where Message is nil or other fields missing
	var data discordgo.MessageComponentInteractionData
	defer func() {
		if r := recover(); r != nil {
			log.Debug().Msgf("[components.OnInteraction] panic while reading component data: %v", r)
		}
	}()
	data = i.MessageComponentData()
	log.Debug().Msgf("[components.OnInteraction] customID: %s", data.CustomID)
	handler, ok := Components[data.CustomID]
	log.Debug().Msgf("[components.OnInteraction] direct lookup ok: %v", ok)
	if !ok {
		// support dynamic custom IDs with suffix like "key:payload"
		if idx := strings.IndexByte(data.CustomID, ':'); idx > 0 {
			key := data.CustomID[:idx]
			log.Debug().Msgf("[components.OnInteraction] trying dynamic key: %s", key)
			if h, exists := Components[key]; exists {
				handler = h
				ok = true
			}
		}
		log.Debug().Msgf("[components.OnInteraction] after dynamic lookup ok: %v", ok)
		if !ok {
			log.Debug().Msgf("[components.OnInteraction] no handler found for: %s", data.CustomID)
			if err := s.InteractionRespond(i.Interaction, ContentResponse("Something went wrong handling that interaction.", true)); err != nil {
				log.Debug().Msgf("[components.OnInteraction] error responding to unknown component: %s", err.Error())
			}
			return
		}
	}

	log.Debug().Msgf("[components.OnInteraction] invoking handler for: %s", data.CustomID)
	start := time.Now()
	resp := handler(s, i)
	elapsed := time.Since(start)
	log.Debug().Msgf("[components.OnInteraction] handler returned resp!=nil: %v elapsed: %s", resp != nil, elapsed.String())
	if resp != nil && elapsed <= 3*time.Second {
		if err := s.InteractionRespond(i.Interaction, resp); err != nil {
			log.Debug().Msgf("[components.OnInteraction] InteractionRespond error: %s", err.Error())
		} else {
			log.Debug().Msgf("[components.OnInteraction] InteractionRespond success")
		}
	} else if resp != nil {
		log.Debug().Msgf("[components.OnInteraction] response generated but exceeded 3s window; not responding")
	}
}

// HandleModalSubmit routes modal submissions to the appropriate handler(s)
func HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	log.Debug().Msgf("[components.HandleModalSubmit] invoked")
	data := i.ModalSubmitData()
	log.Debug().Msgf("[components.HandleModalSubmit] customID: %s", data.CustomID)
	// Appeals modal
	if strings.HasPrefix(data.CustomID, "appeal-modal:") {
		log.Debug().Msgf("[components.HandleModalSubmit] routing to handleAppealSubmit")
		handleAppealSubmit(s, i)
		return
	}
	log.Debug().Msgf("[components.HandleModalSubmit] no handler matched for: %s", data.CustomID)
}
