package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/api/middleware"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/v10/oauth2/token"
	discordMeURL        = "https://discord.com/api/v10/users/@me"
)

type discordTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

type discordUserResponse struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
}

func setupAuthRoutes(r *gin.Engine, services *app.Services) {
	auth := r.Group("/auth")
	{
		auth.GET("/discord/login", func(c *gin.Context) { discordLogin(c, services) })
		auth.GET("/discord/callback", func(c *gin.Context) { discordCallback(c, services) })

		protected := auth.Group("")
		protected.Use(middleware.RequireAuth(services.Store))
		protected.GET("/me", authMe)
		protected.POST("/logout", func(c *gin.Context) { authLogout(c, services) })
	}
}

// starts oauth flow
// default is redirect but mode=json returns url payload
func discordLogin(c *gin.Context, services *app.Services) {
	if err := validateDiscordOAuthConfig(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(c.DefaultQuery("mode", "redirect")))
	if mode != "json" {
		mode = "redirect"
	}

	stateID, err := lib.NewULID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate oauth state"})
		return
	}

	redirectTo := sanitizeRedirectTarget(c.Query("redirect_to"), lib.Config.Auth.PostLoginRedirect)
	stateTTL := time.Duration(lib.Config.Auth.StateTTLMinutes) * time.Minute
	statePayload := &structs.OAuthState{
		RedirectTo:   redirectTo,
		ResponseMode: mode,
		CreatedAt:    time.Now().UTC(),
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := services.Store.SaveOAuthState(ctx, stateID, statePayload, stateTTL); err != nil {
		log.Error().Err(err).Msg("failed to save oauth state")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist oauth state"})
		return
	}

	authURL := buildDiscordAuthURL(stateID)
	if mode == "json" {
		c.JSON(http.StatusOK, gin.H{"auth_url": authURL, "state": stateID})
		return
	}

	c.Redirect(http.StatusFound, authURL)
}

// handles oauth callback from discord
func discordCallback(c *gin.Context, services *app.Services) {
	if err := validateDiscordOAuthConfig(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	if oauthErr := c.Query("error"); oauthErr != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "oauth denied", "details": oauthErr})
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing oauth code or state"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	statePayload, err := services.Store.ConsumeOAuthState(ctx, state)
	if err != nil {
		log.Error().Err(err).Msg("failed to consume oauth state")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to validate oauth state"})
		return
	}
	if statePayload == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "oauth state is invalid or expired"})
		return
	}

	tokenData, err := exchangeDiscordCode(ctx, code)
	if err != nil {
		log.Error().Err(err).Msg("failed to exchange oauth code")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "failed to exchange oauth code"})
		return
	}

	user, err := fetchDiscordUser(ctx, tokenData.AccessToken)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch discord user")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "failed to fetch discord user"})
		return
	}

	sessionID, err := lib.NewULID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create auth session"})
		return
	}

	now := time.Now().UTC()
	sessionTTL := time.Duration(lib.Config.Auth.SessionTTLHours) * time.Hour
	session := &structs.AuthSession{
		ID:               sessionID,
		DiscordUserID:    user.ID,
		Username:         user.Username,
		GlobalName:       user.GlobalName,
		Avatar:           user.Avatar,
		AccessToken:      tokenData.AccessToken,
		RefreshToken:     tokenData.RefreshToken,
		TokenType:        tokenData.TokenType,
		Scope:            tokenData.Scope,
		TokenExpiresAt:   now.Add(time.Duration(tokenData.ExpiresIn) * time.Second),
		SessionExpiresAt: now.Add(sessionTTL),
		CreatedAt:        now,
		LastSeenAt:       now,
	}

	if err := services.Store.SaveSession(ctx, session, sessionTTL); err != nil {
		log.Error().Err(err).Msg("failed to save auth session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save auth session"})
		return
	}

	setAuthCookie(c, sessionID, int(sessionTTL.Seconds()))

	if statePayload.ResponseMode == "json" {
		c.JSON(http.StatusOK, gin.H{
			"session_id": session.ID,
			"user": gin.H{
				"id":          session.DiscordUserID,
				"username":    session.Username,
				"global_name": session.GlobalName,
				"avatar":      session.Avatar,
				"avatar_url":  discordAvatarURL(session.DiscordUserID, session.Avatar),
			},
			"expires_at": session.SessionExpiresAt,
		})
		return
	}

	c.Redirect(http.StatusFound, sanitizeRedirectTarget(statePayload.RedirectTo, lib.Config.Auth.PostLoginRedirect))
}

// simple whoami endpoint
func authMe(c *gin.Context) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing auth session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":          session.DiscordUserID,
			"username":    session.Username,
			"global_name": session.GlobalName,
			"avatar":      session.Avatar,
			"avatar_url":  discordAvatarURL(session.DiscordUserID, session.Avatar),
		},
		"session": gin.H{
			"id":         session.ID,
			"expires_at": session.SessionExpiresAt,
			"last_seen":  session.LastSeenAt,
		},
	})
}

// handles logout request
func authLogout(c *gin.Context, services *app.Services) {
	session := middleware.GetAuthSession(c)
	if session != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err := services.Store.DeleteSession(ctx, session.ID); err != nil {
			log.Error().Err(err).Msg("failed to delete auth session")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete auth session"})
			return
		}
	}

	clearAuthCookie(c)
	c.Status(http.StatusNoContent)
}

// builds discord oauth authorization url
func buildDiscordAuthURL(state string) string {
	v := url.Values{}
	v.Set("client_id", lib.Config.Discord.AppID)
	v.Set("redirect_uri", lib.Config.Discord.OAuthRedirectURI)
	v.Set("response_type", "code")
	v.Set("scope", lib.Config.Discord.OAuthScopes)
	v.Set("state", state)

	return discordAuthorizeURL + "?" + v.Encode()
}

// exchanges discord authorization code for access token
func exchangeDiscordCode(ctx context.Context, code string) (*discordTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", lib.Config.Discord.AppID)
	body.Set("client_secret", lib.Config.Discord.ClientSecret)
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("redirect_uri", lib.Config.Discord.OAuthRedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	var token discordTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}

	if resp.StatusCode >= 400 || token.AccessToken == "" {
		if token.Error != "" {
			return nil, fmt.Errorf("discord token error: %s", token.Error)
		}
		return nil, fmt.Errorf("discord token exchange failed with status %d", resp.StatusCode)
	}

	return &token, nil
}

// fetches discord user information
func fetchDiscordUser(ctx context.Context, accessToken string) (*discordUserResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordMeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("user request failed: %w", err)
	}
	defer resp.Body.Close()

	var user discordUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode user response: %w", err)
	}

	if resp.StatusCode >= 400 || user.ID == "" {
		return nil, fmt.Errorf("discord user fetch failed with status %d", resp.StatusCode)
	}

	return &user, nil
}

// sets auth cookie
func setAuthCookie(c *gin.Context, sessionID string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		lib.Config.Auth.SessionCookieName,
		sessionID,
		maxAge,
		"/",
		"",
		lib.Config.Auth.CookieSecure,
		true,
	)
}

// clears auth cookie
func clearAuthCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		lib.Config.Auth.SessionCookieName,
		"",
		-1,
		"/",
		"",
		lib.Config.Auth.CookieSecure,
		true,
	)
}

// builds discord avatar url
func discordAvatarURL(userID, avatarHash string) string {
	if userID == "" || avatarHash == "" {
		return ""
	}

	ext := "png"
	if strings.HasPrefix(avatarHash, "a_") {
		ext = "gif"
	}

	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s", userID, avatarHash, ext)
}

// sanitizes redirect target
func sanitizeRedirectTarget(target, fallback string) string {
	target = strings.TrimSpace(target)
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		fallback = "/"
	}

	if target == "" {
		return fallback
	}

	if strings.HasPrefix(target, "/") {
		return target
	}

	targetURL, err := url.Parse(target)
	if err != nil || targetURL.Scheme == "" || targetURL.Host == "" {
		return fallback
	}

	fallbackURL, err := url.Parse(fallback)
	if err != nil {
		return "/"
	}

	if fallbackURL.Host == "" {
		return fallback
	}

	if strings.EqualFold(targetURL.Host, fallbackURL.Host) {
		return target
	}

	return fallback
}

// validates discord oauth configuration
func validateDiscordOAuthConfig() error {
	if strings.TrimSpace(lib.Config.Discord.AppID) == "" {
		return fmt.Errorf("discord oauth is not configured missing DISCORD_APP_ID")
	}
	if strings.TrimSpace(lib.Config.Discord.ClientSecret) == "" {
		return fmt.Errorf("discord oauth is not configured missing DISCORD_CLIENT_SECRET")
	}
	if strings.TrimSpace(lib.Config.Discord.OAuthRedirectURI) == "" {
		return fmt.Errorf("discord oauth is not configured missing DISCORD_OAUTH_REDIRECT_URI")
	}
	return nil
}
