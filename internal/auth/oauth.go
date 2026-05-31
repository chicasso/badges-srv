package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"

	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	githubOAuth "golang.org/x/oauth2/github"

	"github.com/ohhcgan/badges-srv/internal/config"
	"github.com/ohhcgan/badges-srv/internal/crypto"
	githubClient "github.com/ohhcgan/badges-srv/internal/github"
	"github.com/ohhcgan/badges-srv/internal/user"
	"github.com/ohhcgan/badges-srv/pkg/dto"
)

const (
	stateCookieName   = "oauth_state"
	sessionCookieName = "session"
)

const (
	ContextKeyUserID = "user_id"
	ContextKeyLogin  = "login"
)

const (
	stateLength   = 16
	stateMaxAge   = 600              /* 10 minutes in seconds */
	sessionMaxAge = 7 * 24 * 60 * 60 /* 7 days in seconds */
)

type JWTPayload struct {
	UserID      string `json:"uid"`
	GithubLogin string `json:"login"`

	jwt.RegisteredClaims
}

/**
 * AuthHandler handles the GitHub OAuth login/callback flow.
 */
type AuthHandler struct {
	oauthConfig *oauth2.Config
	userStore   *user.Store
	cryptoKey   []byte
	jwtSecret   []byte
	frontendURL string
	logger      *zap.Logger
}

func NewHandler(conf *config.Config, userStore *user.Store, logger *zap.Logger) *AuthHandler {
	oauthCfg := &oauth2.Config{
		ClientID:     conf.GithubClientID,
		ClientSecret: conf.GithubClientSecret,
		RedirectURL:  conf.GithubRedirectURL,
		Scopes:       []string{"read:user", "user:email"},
		Endpoint:     githubOAuth.Endpoint,
	}

	return &AuthHandler{
		oauthConfig: oauthCfg,
		userStore:   userStore,
		cryptoKey:   conf.EncryptionKeyBytes(),
		jwtSecret:   []byte(conf.JWTSecret),
		frontendURL: conf.FrontendURL,
		logger:      logger,
	}
}

/**
 * HandleLogin redirects the browser to GitHub's OAuth consent page.
 */
func (h *AuthHandler) HandleLogin(ctx *gin.Context) {
	state, err := randomString(stateLength)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, dto.Response[any]{
			Message: "Something unexpected happened",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "Something unexpected happened",
				Code:    dto.INTERNAL_SERVER_ERROR,
			},
		})
		return
	}

	ctx.SetCookieData(&http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		MaxAge:   stateMaxAge,
		Path:     "/",
		Secure:   ctx.Request.TLS != nil,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,

		// Domain:  "",                               /* TODO: add domain */
		// Quoted:  false,                            /* TODO: upgrade to go1.23 to use Quoted */
		// Expires: time.Now().Add(10 * time.Minute), /* TODO: Add Expires */
	})

	githubAuthServUrl := h.oauthConfig.AuthCodeURL(state)
	ctx.Redirect(http.StatusTemporaryRedirect, githubAuthServUrl)
}

/**
 * HandleCallback handles the GitHub redirect after the user grants (or denies) access.
 */
func (h *AuthHandler) HandleCallback(ctx *gin.Context) {
	stateCookie, err := ctx.Cookie(stateCookieName)
	if err != nil || strings.TrimSpace(stateCookie) == "" {
		ctx.JSON(http.StatusBadRequest, dto.Response[any]{
			Message: "invalid oauth state",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "invalid oauth state",
				Code:    dto.BAD_REQUEST,
			},
		})
		return
	}

	code := ctx.Request.URL.Query().Get("code")
	state := ctx.Request.URL.Query().Get("state")
	if stateCookie != state {
		ctx.JSON(http.StatusBadRequest, dto.Response[any]{
			Message: "invalid oauth state",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "invalid oauth state",
				Code:    dto.BAD_REQUEST,
			},
		})
		return
	}

	ctx.SetCookieData(&http.Cookie{
		Name:   stateCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	if code == "" {
		ctx.JSON(http.StatusBadRequest, dto.Response[any]{
			Message: "missing oauth code",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "missing oauth code",
				Code:    dto.BAD_REQUEST,
			},
		})
		return
	}

	token, err := h.oauthConfig.Exchange(ctx.Request.Context(), code)
	if err != nil {
		h.logger.Error("oauth token exchange failed", zap.Error(err))
		ctx.JSON(http.StatusBadGateway, dto.Response[any]{
			Message: "token exchange failed",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "token exchange failed",
				Code:    dto.BAD_GATEWAY,
			},
		})
		return
	}

	githubID, login, name, email, avatarURL, err := githubClient.UserInfo(ctx.Request.Context(), token.AccessToken)

	if err != nil {
		h.logger.Error("failed to fetch github user info", zap.Error(err))
		ctx.JSON(http.StatusBadGateway, dto.Response[any]{
			Message: "could not fetch github profile",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "could not fetch github profile",
				Code:    dto.BAD_GATEWAY,
			},
		})
		return
	}

	tokenJSON, err := json.Marshal(token)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, dto.Response[any]{
			Message: "something unexpected happened",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "something unexpected happened",
				Code:    dto.INTERNAL_SERVER_ERROR,
			},
		})
		return
	}

	encToken, err := crypto.Encrypt(h.cryptoKey, tokenJSON)
	if err != nil {
		h.logger.Error("token encryption failed", zap.Error(err))
		ctx.JSON(http.StatusInternalServerError, dto.Response[any]{
			Message: "something unexpected happened",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "something unexpected happened",
				Code:    dto.INTERNAL_SERVER_ERROR,
			},
		})
		return
	}

	u := &user.User{
		GithubID:       githubID,
		GithubLogin:    login,
		Name:           name,
		Email:          email,
		AvatarURL:      avatarURL,
		EncryptedToken: encToken,
	}

	if err := h.userStore.Upsert(ctx.Request.Context(), u); err != nil {
		h.logger.Error("user upsert failed", zap.Error(err), zap.String("login", login))
		ctx.JSON(http.StatusServiceUnavailable, dto.Response[any]{
			Message: "service temporary unavailable",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "service temporary unavailable",
				Code:    dto.SERVICE_TEMP_UNAVAILABLE,
			},
		})
		return
	}

	sessionToken, err := h.generateJWT(u)
	if err != nil {
		h.logger.Error("jwt generation failed", zap.Error(err))
		ctx.JSON(http.StatusInternalServerError, dto.Response[any]{
			Message: "something unexpected happened",
			Success: false,
			Data:    nil,
			Error: &dto.ErrorResponse{
				Message: "something unexpected happened",
				Code:    dto.INTERNAL_SERVER_ERROR,
			},
		})
		return
	}

	ctx.SetCookieData(&http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken,
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   ctx.Request.TLS != nil,
		Path:     "/",
	})

	h.logger.Info("user logged in", zap.String("login", login), zap.String("user_id", u.ID))
	ctx.Redirect(http.StatusTemporaryRedirect, h.frontendURL)
}

func (h *AuthHandler) HandleLogout(ctx *gin.Context) {
	ctx.SetCookieData(&http.Cookie{
		Name:   sessionCookieName,
		Value:  "",
		MaxAge: -1,
		Path:   "/",
	})

	ctx.Redirect(http.StatusTemporaryRedirect, h.frontendURL)
}

func (h *AuthHandler) generateJWT(u *user.User) (string, error) {
	now := time.Now()

	payload := JWTPayload{
		UserID:      u.ID,
		GithubLogin: u.GithubLogin,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
		},
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, payload).SignedString(h.jwtSecret)
}

func randomString(length int16) (string, error) {
	b := make([]byte, length)

	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
