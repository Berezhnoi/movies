package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"homework/internal/app"
	"homework/internal/auth"
	"homework/internal/chat"
	"homework/internal/config"
	"homework/internal/repository"
	"homework/internal/webhook"
)

// @title Movie API
// @version 1.0
// @description REST API for creating, listing, replacing, and deleting movies.
// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and the JWT. Example: "Bearer <token>".
func main() {
	if err := run(); err != nil {
		log.Printf("movie API stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logLevel := gormlogger.Warn
	if strings.EqualFold(os.Getenv("LOG_SQL"), "true") {
		logLevel = gormlogger.Info
	}
	db, err := gorm.Open(postgres.Open(cfg.PostgresDSN()), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(logLevel),
		TranslateError: true,
	})
	if err != nil {
		return fmt.Errorf("open PostgreSQL connection: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get PostgreSQL connection pool: %w", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			log.Printf("close PostgreSQL connection: %v", err)
		}
	}()

	// A modest shared pool avoids excessive database connections while supporting normal API concurrency.
	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.DBConnMaxIdleTime)

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}

	tokens, err := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL)
	if err != nil {
		return fmt.Errorf("JWT_SECRET is invalid: %w", err)
	}

	webhookCfg, err := webhook.ConfigFromEnv()
	if err != nil {
		return fmt.Errorf("load webhook configuration: %w", err)
	}
	var users repository.UserRepository = repository.NewGormUserRepository(db)
	if webhookCfg.URL != "" {
		users = webhook.NewNotifyingUsers(users, webhook.NewClient(webhookCfg), webhookCfg.Timeout)
		log.Printf("user-created webhook enabled")
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chatHub := chat.NewHub()
	if cfg.RedisURL != "" {
		options, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("REDIS_URL is invalid: %w", err)
		}
		rdb := redis.NewClient(options)
		defer func() {
			if err := rdb.Close(); err != nil {
				log.Printf("close Redis connection: %v", err)
			}
		}()
		redisPingCtx, cancelRedisPing := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelRedisPing()
		if err := rdb.Ping(redisPingCtx).Err(); err != nil {
			return fmt.Errorf("connect to Redis: %w", err)
		}
		chatHub = chat.NewRedisHub(rdb, cfg.ChatHistorySize)
		log.Printf("chat uses Redis (history size %d)", cfg.ChatHistorySize)
	}
	go chatHub.Run(signalCtx)

	server := &http.Server{
		Addr: ":" + strconv.Itoa(cfg.AppPort),
		Handler: webhook.MarkAdminRegistration(app.NewRouterWithDeps(app.Deps{
			Movies:             repository.NewGormMovieRepository(db),
			Users:              users,
			Tokens:             tokens,
			EnableSwagger:      true,
			WebUIEnabled:       &cfg.WebUIEnabled,
			CookieSecure:       cfg.CookieSecure,
			Chat:               chatHub,
			ChatAllowedOrigins: cfg.WSAllowedOrigins,
		})),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveResult := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", server.Addr)
		serveResult <- server.ListenAndServe()
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-signalCtx.Done():
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("gracefully shut down HTTP server: %w", err)
		}
		err := <-serveResult
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}
