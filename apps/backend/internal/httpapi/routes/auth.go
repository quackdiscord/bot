package routes

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/quack"
)

const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	maxDiscordBodyBytes = 1 << 20
)

var (
	// discordTokenEndpoint and discordMeEndpoint remain replaceable only for bounded adapter contract tests.
	discordTokenEndpoint = "https://discord.com/api/v10/oauth2/token"
	discordMeEndpoint    = "https://discord.com/api/v10/users/@me"
	discordHTTPClient    = &http.Client{Timeout: 10 * time.Second}
)

// userSessionRevoker is the optional auth-store contract used for compromise and account-change revocation.
type userSessionRevoker interface {
	RevokeUserSessions(context.Context, string) error
}

// discordTokenResponse is the transport-neutral representation returned for discord token response.
type discordTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// discordUserResponse is the transport-neutral representation returned for discord user response.
type discordUserResponse struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
}

// setupAuthRoutes explicitly wires setup auth routes so runtime behavior does not depend on init-time registration.
func setupAuthRoutes(r *gin.Engine, services *Deps) {
	auth := r.Group("/auth")
	primitives := httpplatform.FromRepository(services.Store)
	oauthLimit := httpplatform.RateLimit{
		Maximum: services.Config.Limits.OAuth.Max,
		Window:  services.Config.Limits.OAuth.Window,
	}
	{
		auth.GET("/discord/login", primitives.RateLimits.Limit("oauth-login", oauthLimit, httpplatform.ClientIPSubject), func(c *gin.Context) { discordLogin(c, services) })
		auth.GET("/discord/callback", primitives.RateLimits.Limit("oauth-callback", oauthLimit, httpplatform.ClientIPSubject), func(c *gin.Context) { discordCallback(c, services) })

		protected := auth.Group("")
		protected.Use(middleware.RequireAuth(services.Store, services.Config.Auth))
		protected.GET("/me", authMe)
		protected.POST("/logout", func(c *gin.Context) { authLogout(c, services) })
		protected.POST("/logout-all", func(c *gin.Context) { authLogoutAll(c, services) })
	}
}

// discordLogin starts OAuth by redirecting or returning a JSON authorization URL.
// @Summary Start Discord sign-in
// @Tags Authentication
// @Produce json
// @Param mode query string false "Response mode" Enums(redirect,json)
// @Param redirect_to query string false "Post-login relative redirect"
// @Success 200 {object} map[string]interface{}
// @Success 302 {string} string
// @Failure 503 {object} apierror.Response
// @Router /auth/discord/login [get]
func discordLogin(c *gin.Context, services *Deps) {
	if err := validateDiscordOAuthConfig(services.Config); err != nil {
		apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "Discord sign-in is unavailable")
		return
	}

	mode := strings.ToLower(strings.TrimSpace(c.DefaultQuery("mode", "redirect")))
	if mode != "json" {
		mode = "redirect"
	}

	stateID := quack.NewID()

	redirectTo := sanitizeRedirectTarget(c.Query("redirect_to"), services.Config.Auth.PostLoginRedirect)
	stateTTL := services.Config.Auth.StateTTL
	statePayload := &quack.OAuthState{
		RedirectTo:   redirectTo,
		ResponseMode: mode,
		CreatedAt:    time.Now().UTC(),
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := services.Store.SaveOAuthState(ctx, stateID, statePayload, stateTTL); err != nil {
		slog.Error("oauth state dependency unavailable", "request_id", quack.RequestIDFromContext(c.Request.Context()))
		apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "Discord sign-in is temporarily unavailable")
		return
	}

	setOAuthStateCookie(c, services.Config, stateID, int(stateTTL.Seconds()))
	authURL := buildDiscordAuthURL(services.Config, stateID)
	if mode == "json" {
		c.JSON(http.StatusOK, gin.H{"auth_url": authURL, "state": stateID})
		return
	}

	c.Redirect(http.StatusFound, authURL)
}

// discordCallback completes Discord OAuth and creates a browser session.
// @Summary Complete Discord sign-in
// @Tags Authentication
// @Produce json
// @Param code query string false "Discord authorization code"
// @Param state query string true "Single-use OAuth state"
// @Param error query string false "Discord OAuth error"
// @Success 200 {object} map[string]interface{}
// @Success 302 {string} string
// @Failure 400 {object} apierror.Response
// @Failure 401 {object} apierror.Response
// @Failure 503 {object} apierror.Response
// @Router /auth/discord/callback [get]
func discordCallback(c *gin.Context, services *Deps) {
	if err := validateDiscordOAuthConfig(services.Config); err != nil {
		apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "Discord sign-in is unavailable")
		return
	}

	if oauthErr := c.Query("error"); oauthErr != "" {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "Discord authorization was not granted; sign in again")
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "OAuth code and state are required")
		return
	}

	browserState, cookieErr := c.Cookie(oauthStateCookieName(services.Config))
	if cookieErr != nil || subtle.ConstantTimeCompare([]byte(browserState), []byte(state)) != 1 {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "Discord sign-in must finish in the browser that started it")
		return
	}
	setOAuthStateCookie(c, services.Config, "", -1)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	statePayload, err := services.Store.ConsumeOAuthState(ctx, state)
	if err != nil {
		slog.Error("oauth state dependency unavailable", "request_id", quack.RequestIDFromContext(c.Request.Context()))
		apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "Discord sign-in is temporarily unavailable")
		return
	}
	if statePayload == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "Discord sign-in expired; sign in again")
		return
	}

	tokenData, err := exchangeDiscordCode(ctx, services.Config, code)
	if err != nil {
		slog.Warn("Discord OAuth grant rejected", "request_id", quack.RequestIDFromContext(c.Request.Context()))
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "Discord authorization is invalid or revoked; sign in again")
		return
	}

	user, err := fetchDiscordUser(ctx, tokenData.AccessToken)
	if err != nil {
		slog.Warn("Discord OAuth identity request rejected", "request_id", quack.RequestIDFromContext(c.Request.Context()))
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "Discord authorization is invalid or revoked; sign in again")
		return
	}

	sessionID := quack.NewID()
	csrfToken, err := middleware.NewCSRFToken()
	if err != nil {
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "could not create authentication session")
		return
	}

	now := time.Now().UTC()
	sessionTTL := services.Config.Auth.SessionTTL
	session := &quack.AuthSession{
		ID:               sessionID,
		DiscordUserID:    user.ID,
		Username:         user.Username,
		GlobalName:       user.GlobalName,
		Avatar:           user.Avatar,
		AccessToken:      tokenData.AccessToken,
		RefreshToken:     tokenData.RefreshToken,
		CSRFToken:        csrfToken,
		TokenType:        tokenData.TokenType,
		Scope:            tokenData.Scope,
		TokenExpiresAt:   now.Add(time.Duration(tokenData.ExpiresIn) * time.Second),
		SessionExpiresAt: now.Add(sessionTTL),
		CreatedAt:        now,
		LastSeenAt:       now,
	}

	if err := services.Store.SaveSession(ctx, session, sessionTTL); err != nil {
		slog.Error("auth session dependency unavailable", "request_id", quack.RequestIDFromContext(c.Request.Context()))
		apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "authentication service unavailable")
		return
	}

	setAuthCookies(c, services.Config, sessionID, csrfToken, int(sessionTTL.Seconds()))

	if statePayload.ResponseMode == "json" {
		c.JSON(http.StatusOK, gin.H{
			"csrf_token": csrfToken,
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

	c.Redirect(http.StatusFound, sanitizeRedirectTarget(statePayload.RedirectTo, services.Config.Auth.PostLoginRedirect))
}

// authMe returns the authenticated user and session summary.
// @Summary Get the current session
// @Tags Authentication
// @Produce json
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} apierror.Response
// @Router /auth/me [get]
func authMe(c *gin.Context) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"csrf_token": session.CSRFToken,
		"user": gin.H{
			"id":          session.DiscordUserID,
			"username":    session.Username,
			"global_name": session.GlobalName,
			"avatar":      session.Avatar,
			"avatar_url":  discordAvatarURL(session.DiscordUserID, session.Avatar),
		},
		"session": gin.H{
			"expires_at": session.SessionExpiresAt,
			"last_seen":  session.LastSeenAt,
		},
	})
}

// authLogout revokes the current session and clears its cookies.
// @Summary Log out the current session
// @Tags Authentication
// @Security CookieAuth
// @Success 204
// @Failure 503 {object} apierror.Response
// @Router /auth/logout [post]
func authLogout(c *gin.Context, services *Deps) {
	session := middleware.GetAuthSession(c)
	if session != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err := services.Store.DeleteSession(ctx, session.ID); err != nil {
			slog.Error("auth logout dependency unavailable", "request_id", quack.RequestIDFromContext(c.Request.Context()))
			apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "authentication service unavailable")
			return
		}
	}

	clearAuthCookies(c, services.Config)
	c.Status(http.StatusNoContent)
}

// authLogoutAll revokes all indexed sessions for compromise response or account changes.
// @Summary Log out every user session
// @Tags Authentication
// @Security CookieAuth
// @Success 204
// @Failure 401 {object} apierror.Response
// @Failure 503 {object} apierror.Response
// @Router /auth/logout-all [post]
func authLogoutAll(c *gin.Context, services *Deps) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
		return
	}
	revoker, ok := services.Store.(userSessionRevoker)
	if !ok {
		apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "session revocation unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := revoker.RevokeUserSessions(ctx, session.DiscordUserID); err != nil {
		slog.Error("auth compromise revocation dependency unavailable", "request_id", quack.RequestIDFromContext(c.Request.Context()))
		apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "session revocation unavailable")
		return
	}
	clearAuthCookies(c, services.Config)
	c.Status(http.StatusNoContent)
}
