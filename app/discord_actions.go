package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/bwmarrin/discordgo"
	runtimeDiscord "github.com/quackdiscord/bot/discord"
)

type DiscordActionClient interface {
	SendDM(ctx context.Context, discordUserID, message string) (map[string]any, error)
	SendModLog(ctx context.Context, discordChannelID, message string) (map[string]any, error)
}

type DiscordActionError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e DiscordActionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type DiscordRuntimeActionClient struct{}

func NewDiscordActionClient() *DiscordRuntimeActionClient {
	return &DiscordRuntimeActionClient{}
}

func (c *DiscordRuntimeActionClient) SendDM(ctx context.Context, discordUserID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session := runtimeDiscord.Session
	if session == nil {
		return nil, DiscordActionError{Code: "discord_session_unavailable", Message: "discord session is unavailable", Retryable: true}
	}

	channel, err := session.UserChannelCreate(discordUserID)
	if err != nil {
		return nil, classifyDiscordError("send_dm_channel", err)
	}
	sent, err := session.ChannelMessageSend(channel.ID, message)
	if err != nil {
		return nil, classifyDiscordError("send_dm_message", err)
	}

	response := map[string]any{"channel_id": channel.ID}
	if sent != nil {
		response["message_id"] = sent.ID
	}
	return response, nil
}

func (c *DiscordRuntimeActionClient) SendModLog(ctx context.Context, discordChannelID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session := runtimeDiscord.Session
	if session == nil {
		return nil, DiscordActionError{Code: "discord_session_unavailable", Message: "discord session is unavailable", Retryable: true}
	}

	sent, err := session.ChannelMessageSend(discordChannelID, message)
	if err != nil {
		return nil, classifyDiscordError("send_mod_log", err)
	}

	response := map[string]any{"channel_id": discordChannelID}
	if sent != nil {
		response["message_id"] = sent.ID
	}
	return response, nil
}

func classifyDiscordError(code string, err error) error {
	if err == nil {
		return nil
	}

	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		status := restErr.Response.StatusCode
		retryable := status == http.StatusTooManyRequests || status >= 500
		return DiscordActionError{
			Code:      fmt.Sprintf("%s_%d", code, status),
			Message:   err.Error(),
			Retryable: retryable,
		}
	}

	return DiscordActionError{
		Code:      code,
		Message:   err.Error(),
		Retryable: true,
	}
}

func actionErrorFromDiscord(err error) ActionResult {
	if err == nil {
		return ActionResult{}
	}
	var actionErr DiscordActionError
	if errors.As(err, &actionErr) {
		if actionErr.Retryable {
			return retryableActionError(actionErr.Code, actionErr.Error())
		}
		return permanentActionError(actionErr.Code, actionErr.Error())
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return retryableActionError("context_cancelled", err.Error())
	}
	return retryableActionError("discord_error", err.Error())
}
