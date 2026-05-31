package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	goEnv "github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/ohhcgan/badges-srv/internal/auth"
	"github.com/ohhcgan/badges-srv/internal/config"
	"github.com/ohhcgan/badges-srv/internal/database"
	"github.com/ohhcgan/badges-srv/internal/logger"
	"github.com/ohhcgan/badges-srv/internal/mailer"
	"github.com/ohhcgan/badges-srv/internal/poster"
	"github.com/ohhcgan/badges-srv/internal/scheduler"
	"github.com/ohhcgan/badges-srv/internal/server"
	controllers "github.com/ohhcgan/badges-srv/internal/server/controllers"
	"github.com/ohhcgan/badges-srv/internal/stats"
	"github.com/ohhcgan/badges-srv/internal/user"
)

var cfg *config.Config = nil

func init() {
	err := goEnv.Load(".env")
	if err != nil {
		panic(fmt.Errorf("env: while reading env file - %w", err))
	}

	cfg, err = config.Load()
	if err != nil {
		panic(fmt.Errorf("config: loading config - %w", err))
	}
}

func main() {
	logger, err := logger.Logger(cfg.Env)
	if err != nil {
		panic(fmt.Errorf("logger: initialize logger: %v", err))
	}

	defer logger.Sync()

	if err := run(logger, cfg); err != nil {
		logger.Fatal("server exited with error:", zap.Error(err))
	}
}

func run(logger *zap.Logger, cfg *config.Config) error {
	ctx := context.Background()

	pool, err := database.NewPool(ctx, cfg.DatabaseURL, &database.DBConfig{
		MinConns:              2,
		MaxConns:              20,
		MaxConnIdleTime:       30 * time.Minute,
		MaxConnLifetime:       time.Hour,
		HealthCheckPeriod:     time.Minute,
		MaxConnLifetimeJitter: time.Minute,
	})
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	logger.Info("Database connected")

	/**
	 * User and Stats collection pool stores.
	 */
	userStore := user.NewStore(pool)
	statsStore := stats.NewStore(pool)

	/**
	 * TODO:
	 * Move generator to another micro-service which is independently scaled up or down.
	 */
	generator, err := poster.NewGenerator()
	if err != nil {
		return fmt.Errorf("initializing poster generator: %w", err)
	}

	/**
	 * TODO:
	 * Move mailer to another micro-service which is independently scaled up or down.
	 */
	mailerSvc := mailer.New(cfg.SMTPHost, cfg.SMTPUser, cfg.SMTPPass, cfg.EmailFrom, cfg.SMTPPort)
	/**
	 * TODO:
	 * Move statsCollector to another micro-service which is independently scaled up or down.
	 */
	collector := stats.NewCollector(userStore, statsStore, cfg.EncryptionKeyBytes(), logger)
	/**
	 * TODO:
	 * Move scheduler to another micro-service which is independently scaled up or down.
	 */
	scheduled := scheduler.New(collector, generator, mailerSvc, userStore, statsStore, logger)
	scheduled.Start()
	defer scheduled.Stop()

	/** Handlers */
	userCont := controllers.NewControllers(userStore, statsStore, logger, scheduled.RunMonthlyJob)
	/* AuthHandlers */
	authCont := auth.NewHandler(cfg, userStore, logger)

	srv := server.New(cfg, authCont, userCont, logger)

	/**
	 * Start listening in a goroutine so we can wait on OS signals.
	 */
	errorChan := make(chan error, 1)
	go func() {
		logger.Info("Server starting at", zap.String("addr", srv.Addr))

		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errorChan <- err
		}
		close(errorChan)
	}()

	/**
	 * ^C (SigInt) and SigTerm channels.
	 */
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-errorChan:
		return err
	case sig := <-quit:
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))
	}

	logger.Info("shutting down the server in", zap.Int64("seconds", 30*int64(time.Second)))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("server stopped gracefully")
	return nil
}
