package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost            string
	DBPort            int
	DBUser            string
	DBPassword        string
	DBName            string
	DBSSLMode         string
	AppPort           int
	AllowedOrigin     string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	DBConnMaxIdleTime time.Duration
	JWTSecret         string
	JWTTTL            time.Duration
	AdminEmail        string
	AdminPassword     string
	WebUIEnabled      bool
	CookieSecure      bool
	WSAllowedOrigins  []string
	RedisURL          string
	ChatHistorySize   int
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	var cfg Config
	var err error

	if cfg.DBHost, err = required("DB_HOST"); err != nil {
		return Config{}, err
	}
	if cfg.DBUser, err = required("DB_USER"); err != nil {
		return Config{}, err
	}
	if cfg.DBPassword, err = required("DB_PASSWORD"); err != nil {
		return Config{}, err
	}
	if cfg.DBName, err = required("DB_NAME"); err != nil {
		return Config{}, err
	}

	if cfg.DBPort, err = intValue("DB_PORT", 5432); err != nil {
		return Config{}, err
	}
	if cfg.AppPort, err = intValue("APP_PORT", 8080); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxOpenConns, err = intValue("DB_MAX_OPEN_CONNS", 25); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxIdleConns, err = intValue("DB_MAX_IDLE_CONNS", 25); err != nil {
		return Config{}, err
	}
	if cfg.DBConnMaxLifetime, err = durationValue("DB_CONN_MAX_LIFETIME", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.DBConnMaxIdleTime, err = durationValue("DB_CONN_MAX_IDLE_TIME", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.JWTSecret, err = required("JWT_SECRET"); err != nil {
		return Config{}, err
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}
	if cfg.JWTTTL, err = durationValue("JWT_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.JWTTTL <= 0 {
		return Config{}, fmt.Errorf("JWT_TTL must be greater than zero")
	}

	cfg.DBSSLMode = stringValue("DB_SSLMODE", "disable")
	cfg.AllowedOrigin = stringValue("ALLOWED_ORIGIN", "*")
	cfg.AdminEmail = stringValue("ADMIN_EMAIL", "")
	cfg.AdminPassword = stringValue("ADMIN_PASSWORD", "")
	if cfg.WebUIEnabled, err = boolValue("WEB_UI_ENABLED", true); err != nil {
		return Config{}, err
	}
	if cfg.CookieSecure, err = boolValue("COOKIE_SECURE", false); err != nil {
		return Config{}, err
	}
	for _, origin := range strings.Split(stringValue("WS_ALLOWED_ORIGINS", "http://localhost:8080"), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			cfg.WSAllowedOrigins = append(cfg.WSAllowedOrigins, origin)
		}
	}
	cfg.RedisURL = stringValue("REDIS_URL", "")
	if cfg.ChatHistorySize, err = intValue("CHAT_HISTORY_SIZE", 50); err != nil {
		return Config{}, err
	}
	if cfg.ChatHistorySize <= 0 {
		return Config{}, fmt.Errorf("CHAT_HISTORY_SIZE must be greater than zero")
	}
	return cfg, nil
}

func (c Config) PostgresDSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   net.JoinHostPort(c.DBHost, strconv.Itoa(c.DBPort)),
		Path:   c.DBName,
	}
	query := url.Values{}
	query.Set("sslmode", c.DBSSLMode)
	u.RawQuery = query.Encode()
	return u.String()
}

func required(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is missing", name)
	}
	return value, nil
}

func stringValue(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func intValue(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func boolValue(name string, fallback bool) (bool, error) {
	value, exists := os.LookupEnv(name)
	if !exists {
		return fallback, nil
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("parse %s: must be true or false", name)
	}
}

func durationValue(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}
