package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/quackdiscord/bot/structs"
)

type WebhookPayload struct {
	Embeds    []structs.Embed `json:"embeds"`
	Username  string          `json:"username"`
	AvatarURL string          `json:"avatar_url"`
}

func SendWHEmbed(webhookURL string, embed structs.Embed) error {
	// Create the payload
	payload := WebhookPayload{
		Embeds:    []structs.Embed{embed},
		Username:  "Quack Logging",
		AvatarURL: "https://quackbot.xyz/images/webp/logo.webp",
	}

	// Marshal the payload to JSON
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	// Create a new HTTP request
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set the appropriate headers
	req.Header.Set("Content-Type", "application/json")

	// Send the HTTP request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Check the response status code
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected response status: %s", resp.Status)
	}

	return nil
}
