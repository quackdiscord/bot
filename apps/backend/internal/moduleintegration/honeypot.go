package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
)

// systemHoneypotCaseCreator is QP-A's single system-attributed moderation entrypoint.
type systemHoneypotCaseCreator interface {
	CreateSystemHoneypot(context.Context, string, quack.CaseInput) (*quack.CaseResponse, error)
}

// honeypotCaseApplier adapts QP-F exclusively to QP-A's normal system case
// boundary; it has no repository access and cannot bypass case orchestration.
type honeypotCaseApplier struct{ cases systemHoneypotCaseCreator }

// ApplyHoneypotCase validates the fixed automation envelope before preserving
// every request field in the core case input.
func (a honeypotCaseApplier) ApplyHoneypotCase(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	if a.cases == nil {
		return honeypot.ApplyResult{}, errors.New("honeypot case application is not configured")
	}
	if request.Source != honeypot.SourceHoneypot || request.ActorType != honeypot.ActorTypeSystem || strings.TrimSpace(request.ActorDiscordUserID) != "" {
		return honeypot.ApplyResult{}, errors.New("honeypot case attribution is invalid")
	}
	if strings.TrimSpace(request.GuildID) == "" || strings.TrimSpace(request.TemplateID) == "" || strings.TrimSpace(request.TargetDiscordUserID) == "" || strings.TrimSpace(request.ContextChannelDiscordID) == "" || strings.TrimSpace(request.ContextMessageDiscordID) == "" || strings.TrimSpace(request.ContextURL) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return honeypot.ApplyResult{}, errors.New("honeypot case request is incomplete")
	}
	created, err := a.cases.CreateSystemHoneypot(ctx, request.GuildID, quack.CaseInput{
		TemplateID: request.TemplateID, TargetDiscordUserID: request.TargetDiscordUserID,
		Source:                  quack.CaseSourceHoneypot,
		ContextChannelDiscordID: request.ContextChannelDiscordID,
		ContextMessageDiscordID: request.ContextMessageDiscordID,
		ContextURL:              request.ContextURL, IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return honeypot.ApplyResult{}, err
	}
	return honeypot.ApplyResult{CaseID: created.ID}, nil
}

// honeypotTemplateValidator projects live core policy without duplicating it
// into optional-module persistence.
type honeypotTemplateValidator struct{ repository templateReader }

// templateReader loads a template with its levels.
type templateReader interface {
	GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*quack.ExpandedCaseTemplate, error)
}

// ValidateHoneypotTemplate requires an active, unattended-compatible v5 policy.
func (v honeypotTemplateValidator) ValidateHoneypotTemplate(ctx context.Context, guildID, templateID string) error {
	if v.repository == nil {
		return errors.New("honeypot template repository is not configured")
	}
	template, err := v.repository.GetCaseTemplateExpanded(ctx, strings.TrimSpace(guildID), strings.TrimSpace(templateID))
	if err != nil {
		return err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return honeypot.ErrTemplateUnavailable
	}
	for _, field := range template.ContextFields {
		if field.Required {
			return fmt.Errorf("%w: required context field %s cannot be supplied unattended", honeypot.ErrTemplateUnavailable, field.Key)
		}
	}
	defaults := 0
	for _, level := range template.Levels {
		if level.Level.IsDefault {
			defaults++
		}
		if len(level.Actions) > 1 {
			return fmt.Errorf("%w: template level has multiple actions", honeypot.ErrTemplateUnavailable)
		}
		for _, action := range level.Actions {
			switch action.ActionType {
			case quack.ActionSendDM, quack.ActionTimeoutUser, quack.ActionKickUser, quack.ActionBanUser:
			default:
				return fmt.Errorf("%w: unsupported unattended action %s", honeypot.ErrTemplateUnavailable, action.ActionType)
			}
		}
	}
	if defaults != 1 || len(template.Levels) == 0 {
		return fmt.Errorf("%w: template must have exactly one default level", honeypot.ErrTemplateUnavailable)
	}
	return nil
}

// honeypotChannelValidator checks the current Discord channel and bot access.
type honeypotChannelValidator struct {
	session  *discordgo.Session
	resolver guildResolver
}

// ValidateHoneypotChannel requires an exact live guild/channel match and bot
// visibility; it never accepts request-supplied permission claims.
func (v honeypotChannelValidator) ValidateHoneypotChannel(ctx context.Context, guildID, channelID string) error {
	if v.session == nil {
		return errors.New("honeypot Discord session is not configured")
	}
	discordGuildID, err := v.resolver.discordID(ctx, strings.TrimSpace(guildID))
	if err != nil {
		return err
	}
	channel, err := v.session.Channel(strings.TrimSpace(channelID), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || channel == nil || channel.GuildID != discordGuildID {
		return honeypot.ErrChannelUnavailable
	}
	guild, member, err := currentBotMember(v.session, discordGuildID)
	if err != nil {
		return err
	}
	permissions := channelPermissions(guild, channel, member)
	if permissions&discordgo.PermissionViewChannel == 0 {
		return honeypot.ErrChannelUnavailable
	}
	return nil
}

// currentBotMember loads current bot membership rather than trusting gateway
// message fields or stale optional-module configuration.
func currentBotMember(session *discordgo.Session, discordGuildID string) (*discordgo.Guild, *discordgo.Member, error) {
	if session == nil {
		return nil, nil, errors.New("Discord session is not configured")
	}
	botID := currentBotID(session)
	if botID == "" {
		user, err := session.User("@me")
		if err != nil || user == nil {
			return nil, nil, errors.New("current Discord bot identity is unavailable")
		}
		botID = user.ID
	}
	guild, err := session.Guild(discordGuildID)
	if err != nil || guild == nil {
		return nil, nil, errors.New("current Discord guild is unavailable")
	}
	member, err := session.GuildMember(discordGuildID, botID)
	if err != nil || member == nil || member.User == nil || member.User.ID != botID {
		return nil, nil, errors.New("current Discord bot membership is unavailable")
	}
	return guild, member, nil
}

// currentBotID returns the gateway-authenticated identity when already known.
func currentBotID(session *discordgo.Session) string {
	if session != nil && session.State != nil && session.State.User != nil {
		return session.State.User.ID
	}
	return ""
}

// projectHoneypotMessage combines a gateway identity with freshly loaded
// member, guild, channel, and permission state.
func projectHoneypotMessage(internalGuildID string, event *discordgo.MessageCreate, guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member, botID string) (honeypot.Message, error) {
	if strings.TrimSpace(internalGuildID) == "" || event == nil || event.Message == nil || event.GuildID == "" || channel == nil || channel.GuildID != event.GuildID || member == nil || member.User == nil || member.User.ID == "" {
		return honeypot.Message{}, errors.New("honeypot message projection is incomplete")
	}
	if event.Author == nil || event.Author.ID != member.User.ID {
		return honeypot.Message{}, errors.New("honeypot message author does not match current member")
	}
	permissions := channelPermissions(guild, channel, member)
	administrator := permissions&discordgo.PermissionAdministrator != 0
	staffPermissions := int64(discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers | discordgo.PermissionBanMembers | discordgo.PermissionManageServer)
	return honeypot.Message{
		GuildID: strings.TrimSpace(internalGuildID), ChannelDiscordID: channel.ID,
		MessageDiscordID: event.ID, AuthorDiscordUserID: member.User.ID,
		MessageURL:           fmt.Sprintf("https://discord.com/channels/%s/%s/%s", event.GuildID, channel.ID, event.ID),
		AuthorRoleDiscordIDs: append([]string(nil), member.Roles...),
		IsBot:                member.User.Bot, IsQuack: member.User.ID == botID,
		IsWebhook:         event.WebhookID != "",
		AuthorCanModerate: administrator || permissions&staffPermissions != 0,
	}, nil
}

// channelPermissions applies Discord's role and channel-overwrite precedence
// to current REST projections.
func channelPermissions(guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member) int64 {
	if guild == nil || channel == nil || member == nil || member.User == nil {
		return 0
	}
	permissions := int64(0)
	roles := make(map[string]struct{}, len(member.Roles))
	for _, roleID := range member.Roles {
		roles[roleID] = struct{}{}
	}
	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		if role.ID == guild.ID {
			permissions |= role.Permissions
		}
		if _, ok := roles[role.ID]; ok {
			permissions |= role.Permissions
		}
	}
	if member.User.ID == guild.OwnerID || permissions&discordgo.PermissionAdministrator != 0 {
		return discordgo.PermissionAll
	}
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == guild.ID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	roleDeny, roleAllow := int64(0), int64(0)
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Type != discordgo.PermissionOverwriteTypeRole {
			continue
		}
		if _, ok := roles[overwrite.ID]; ok {
			roleDeny |= overwrite.Deny
			roleAllow |= overwrite.Allow
		}
	}
	permissions = permissions&^roleDeny | roleAllow
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == member.User.ID && overwrite.Type == discordgo.PermissionOverwriteTypeMember {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	return permissions
}

// RequiredGatewayIntents derives optional Discord subscriptions from durable
// enabled module envelopes at startup.
func (r *Runtime) RequiredGatewayIntents(ctx context.Context) (discordgo.Intent, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("optional module runtime is not configured")
	}
	intents := discordgo.IntentGuilds
	loggingEnabled, err := r.anyModuleEnabled(ctx, modules.GeneralLogging)
	if err != nil {
		return 0, err
	}
	honeypotEnabled, err := r.anyModuleEnabled(ctx, modules.Honeypots)
	if err != nil {
		return 0, err
	}
	if loggingEnabled {
		intents |= discordgo.IntentGuildMembers | discordgo.IntentGuildModeration | discordgo.IntentGuildMessages | discordgo.IntentMessageContent
	}
	if honeypotEnabled {
		required := honeypot.RequiredIntents(true)
		if required.Guilds {
			intents |= discordgo.IntentGuilds
		}
		if required.GuildMessages {
			intents |= discordgo.IntentGuildMessages
		}
		if required.MessageContent {
			intents |= discordgo.IntentMessageContent
		}
	}
	return intents, nil
}

// anyModuleEnabled checks startup intent requirements without crossing guilds.
func (r *Runtime) anyModuleEnabled(ctx context.Context, moduleID modules.ID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&modules.Configuration{}).Where("module_id = ? AND enabled = ?", moduleID, true).Count(&count).Error
	return count > 0, err
}

// HandleTemplateChange forwards archive and unattended-compatibility drift to
// the isolated adapter while retaining the selected reference for repair.
func (r *Runtime) HandleTemplateChange(ctx context.Context, guildID, templateID string) {
	if r == nil || r.HoneypotDiscord == nil {
		return
	}
	if err := (honeypotTemplateValidator{repository: r.repository}).ValidateHoneypotTemplate(ctx, guildID, templateID); err != nil {
		_ = r.HoneypotDiscord.HandleTemplateUnavailable(ctx, guildID, templateID)
	}
}
