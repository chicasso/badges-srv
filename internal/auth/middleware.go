package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ohhcgan/badges-srv/pkg/dto"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

/**
 * ValidateSession parses and validates the JWT from the session cookie.
 */
func (h *AuthHandler) ValidateSession(r *http.Request) (*JWTPayload, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, errors.New("no session cookie")
	}

	token, err := jwt.ParseWithClaims(cookie.Value, &JWTPayload{}, func(jwtTkn *jwt.Token) (any, error) {
		if _, ok := jwtTkn.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", jwtTkn.Header["alg"])
		}
		return h.jwtSecret, nil
	})

	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid session: %w", err)
	}

	claims, ok := token.Claims.(*JWTPayload)
	if !ok {
		return nil, errors.New("malformed claims")
	}
	return claims, nil
}

/**
 * Main authentication middleware
 */
func (h *AuthHandler) RequireAuth(ctx *gin.Context) {
	payload, err := h.ValidateSession(ctx.Request)

	if err != nil {
		ctx.JSON(http.StatusUnauthorized, dto.Response[any]{
			Success: false,
			Data:    nil,
			Message: "unauthorized",
			Error: &dto.ErrorResponse{
				Message: "unauthorized",
				Code:    dto.UNAUTHORIZED,
			},
		})
		return
	}

	//
	// TODO: uncomment
	//
	// userID, err := uuid.Parse(payload.UserID)
	// if err != nil {
	// 	ctx.JSON(http.StatusUnauthorized, dto.Response[any]{
	// 		Success: false,
	// 		Data:    nil,
	// 		Message: "unauthorized",
	// 		Error: &dto.ErrorResponse{
	// 			"message": "unauthorized",
	// 			"code":    "UNAUTHORIZED",
	// 		},
	// 	})
	// 	return
	// }
	//

	ctx.Set(ContextKeyUserID, payload.UserID)
	ctx.Set(ContextKeyLogin, payload.GithubLogin)

	ctx.Next()
}

/**
 * Main admin authentication middleware
 */
func RequireAdmin(adminKey string) func(*gin.Context) {
	return func(ctx *gin.Context) {
		if adminKey == "" {
			ctx.JSON(http.StatusNotFound, dto.Response[any]{
				Success: false,
				Data:    nil,
				Message: "page not found",
				Error: &dto.ErrorResponse{
					Message: "page not found",
					Code:    dto.NOT_FOUND,
				},
			})
			return
		}

		if ctx.GetHeader("X-Admin-Key") != adminKey {
			ctx.JSON(http.StatusForbidden, dto.Response[any]{
				Success: false,
				Data:    nil,
				Message: "forbidden",
				Error: &dto.ErrorResponse{
					Message: "forbidden",
					Code:    dto.FORBIDDEN,
				},
			})
			return
		}

		ctx.Next()
	}
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ContextKeyUserID).(string)
	return v, ok
}

func LoginFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ContextKeyLogin).(string)
	return v, ok
}
