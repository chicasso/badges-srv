package server

import (
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/ohhcgan/badges-srv/internal/auth"
	"github.com/ohhcgan/badges-srv/internal/config"
	"github.com/ohhcgan/badges-srv/internal/middlewares"
	controllers "github.com/ohhcgan/badges-srv/internal/server/controllers"

	"github.com/gin-gonic/gin"
)

/**
 * TODO:
 * Move from chi to gin.
 */
func New(
	cfg *config.Config,
	authHandler *auth.AuthHandler,
	cont *controllers.Controllers,
	logger *zap.Logger,
) *http.Server {
	gin.SetMode(gin.ReleaseMode)
	routerMux := gin.New()

	routerMux.Use(RequestLogger(logger))
	routerMux.Use(middlewares.CorsMiddleware([]string{cfg.FrontendURL}))

	routerMux.GET("/health", cont.HealthCheck)

	routerMux.GET("/auth/login", authHandler.HandleLogin)
	routerMux.GET("/auth/logout", authHandler.HandleLogout)
	routerMux.GET("/auth/callback", authHandler.HandleCallback)

	apiGroup := routerMux.Group("/api")
	apiGroup.Use(authHandler.RequireAuth)
	apiGroup.GET("/me", cont.GetMe)
	apiGroup.GET("/stats", cont.GetStats)
	apiGroup.GET("/stats/history", cont.GetStatsHistory)

	adminGroup := routerMux.Group("/api/admin")
	adminGroup.Use(auth.RequireAdmin(cfg.AdminKey))
	/** TODO:
	 * Make it a cron job or a cron job triggers this api
	 */
	adminGroup.POST("/trigger-monthly", cont.TriggerMonthly)

	routerMux.NoRoute(cont.NoRoute)

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: routerMux,
		/**
		 * TODO: move ReadTimeout, WriteTimeout and IdleTimeout to cfg.
		 */
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

func RequestLogger(logger *zap.Logger) func(*gin.Context) {
	return func(ctx *gin.Context) {
		start := time.Now()
		ctx.Next()

		logger.Info("http request",
			zap.String("method", ctx.Request.Method),
			zap.String("path", ctx.Request.URL.Path),
			zap.Int("status", ctx.Writer.Status()),
			zap.Duration("duration", time.Since(start)),
			zap.String("remote", ctx.Request.RemoteAddr),
		)
	}
}
