package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "movies")
	t.Setenv("DB_PASSWORD", "test-password")
	t.Setenv("DB_NAME", "movies")
	t.Setenv("DB_PORT", "")
	t.Setenv("APP_PORT", "")
	t.Setenv("DB_SSLMODE", "")
	t.Setenv("ALLOWED_ORIGIN", "")
	t.Setenv("DB_MAX_OPEN_CONNS", "")
	t.Setenv("DB_MAX_IDLE_CONNS", "")
	t.Setenv("DB_CONN_MAX_LIFETIME", "")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "")
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-bytes")
	t.Setenv("JWT_TTL", "")
	t.Setenv("ADMIN_EMAIL", "")
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("WEB_UI_ENABLED", "true")
	t.Setenv("COOKIE_SECURE", "false")
	if err := os.Unsetenv("COOKIE_SECURE"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DBPort != 5432 || cfg.AppPort != 8080 {
		t.Errorf("ports = (%d, %d), want (5432, 8080)", cfg.DBPort, cfg.AppPort)
	}
	if cfg.DBMaxOpenConns != 25 || cfg.DBMaxIdleConns != 25 {
		t.Errorf("pool sizes = (%d, %d), want (25, 25)", cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != 30*time.Minute || cfg.DBConnMaxIdleTime != 5*time.Minute {
		t.Errorf("pool durations = (%s, %s), want (30m, 5m)", cfg.DBConnMaxLifetime, cfg.DBConnMaxIdleTime)
	}
	if cfg.DBSSLMode != "disable" || cfg.AllowedOrigin != "*" {
		t.Errorf("defaults = (%q, %q), want (%q, %q)", cfg.DBSSLMode, cfg.AllowedOrigin, "disable", "*")
	}
	if cfg.JWTTTL != 24*time.Hour || cfg.AdminEmail != "" || cfg.AdminPassword != "" {
		t.Errorf("auth defaults = (%s, %q, %q), want (24h, empty, empty)", cfg.JWTTTL, cfg.AdminEmail, cfg.AdminPassword)
	}
	if !cfg.WebUIEnabled {
		t.Error("WebUIEnabled = false, want default true")
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure = true, want default false for local HTTP development")
	}
	if got, want := cfg.PostgresDSN(), "postgres://movies:test-password@localhost:5432/movies?sslmode=disable"; got != want {
		t.Errorf("PostgresDSN() = %q, want %q", got, want)
	}

}

func TestLoadParsesCookieSecureStrictly(t *testing.T) {
	setDatabaseEnvironment(t)
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-bytes")
	t.Setenv("COOKIE_SECURE", "true")
	cfg, err := Load()
	if err != nil || !cfg.CookieSecure {
		t.Fatalf("Load() CookieSecure = %t, error = %v; want true, nil", cfg.CookieSecure, err)
	}
	t.Setenv("COOKIE_SECURE", "yes")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("Load() error = %v, want strict COOKIE_SECURE parse error", err)
	}
}

func TestLoadParsesWebUIEnabledStrictly(t *testing.T) {
	setDatabaseEnvironment(t)
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-bytes")
	t.Setenv("WEB_UI_ENABLED", "false")
	cfg, err := Load()
	if err != nil || cfg.WebUIEnabled {
		t.Fatalf("Load() WebUIEnabled = %t, error = %v; want false, nil", cfg.WebUIEnabled, err)
	}
	t.Setenv("WEB_UI_ENABLED", "yes")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WEB_UI_ENABLED") {
		t.Fatalf("Load() error = %v, want strict WEB_UI_ENABLED parse error", err)
	}
}

func TestBoolValueDefaultsOnlyWhenUnset(t *testing.T) {
	t.Setenv("WEB_UI_ENABLED", "true")
	if err := os.Unsetenv("WEB_UI_ENABLED"); err != nil {
		t.Fatal(err)
	}
	if got, err := boolValue("WEB_UI_ENABLED", true); err != nil || !got {
		t.Fatalf("boolValue() = %t, %v; want true, nil when unset", got, err)
	}
	t.Setenv("WEB_UI_ENABLED", "")
	if _, err := boolValue("WEB_UI_ENABLED", true); err == nil {
		t.Fatal("boolValue() accepted an explicitly empty value")
	}
}

func TestLoadMissingRequiredVariable(t *testing.T) {
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "movies")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_NAME", "movies")
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-bytes")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DB_PASSWORD") {
		t.Fatalf("Load() error = %v, want missing DB_PASSWORD error", err)
	}
}

func TestLoadRequiresJWTSecret(t *testing.T) {
	setDatabaseEnvironment(t)
	t.Setenv("JWT_SECRET", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("Load() error = %v, want missing JWT_SECRET error", err)
	}
}

func TestLoadRejectsShortJWTSecretWithoutEchoingIt(t *testing.T) {
	setDatabaseEnvironment(t)
	const secret = "short-test-secret"
	t.Setenv("JWT_SECRET", secret)

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("Load() error = %v, want short JWT_SECRET error", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Load() error reveals JWT_SECRET: %v", err)
	}
}

func setDatabaseEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_USER", "movies")
	t.Setenv("DB_PASSWORD", "test-password")
	t.Setenv("DB_NAME", "movies")
}

func TestLoadTrailersGuardsS3Endpoint(t *testing.T) {
	tests := []struct {
		name     string
		enabled  string
		endpoint string
		bucket   string
		wantErr  string
	}{
		{name: "disabled without S3", enabled: "false"},
		{name: "enabled without endpoint", enabled: "true", bucket: "trailers", wantErr: "S3_ENDPOINT_URL"},
		{name: "enabled without bucket", enabled: "true", endpoint: "http://floci:4566", wantErr: "S3_BUCKET"},
		{name: "real AWS host", enabled: "true", endpoint: "https://s3.amazonaws.com", bucket: "trailers", wantErr: "amazonaws.com"},
		{name: "real AWS regional host", enabled: "true", endpoint: "https://bucket.s3.eu-west-1.amazonaws.com", bucket: "trailers", wantErr: "amazonaws.com"},
		{name: "real AWS China host", enabled: "true", endpoint: "https://s3.cn-north-1.amazonaws.com.cn", bucket: "trailers", wantErr: "amazonaws.com"},
		{name: "real AWS dual-stack host", enabled: "true", endpoint: "https://s3.dualstack.us-east-1.api.aws", bucket: "trailers", wantErr: "api.aws"},
		{name: "upper-case AWS host", enabled: "true", endpoint: "https://S3.AMAZONAWS.COM.", bucket: "trailers", wantErr: "amazonaws.com"},
		{name: "not http", enabled: "true", endpoint: "ftp://floci:4566", bucket: "trailers", wantErr: "S3_ENDPOINT_URL"},
		{name: "no host", enabled: "true", endpoint: "http://", bucket: "trailers", wantErr: "S3_ENDPOINT_URL"},
		{name: "Floci", enabled: "true", endpoint: "http://floci:4566", bucket: "trailers"},
		{name: "look-alike host", enabled: "true", endpoint: "http://notamazonaws.com:4566", bucket: "trailers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setDatabaseEnvironment(t)
			t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-bytes")
			t.Setenv("TRAILERS_ENABLED", tt.enabled)
			t.Setenv("S3_ENDPOINT_URL", tt.endpoint)
			t.Setenv("S3_BUCKET", tt.bucket)

			cfg, err := Load()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.TrailersEnabled != (tt.enabled == "true") {
				t.Errorf("TrailersEnabled = %t, want %s", cfg.TrailersEnabled, tt.enabled)
			}
		})
	}
}

func TestLoadTrailerS3Defaults(t *testing.T) {
	setDatabaseEnvironment(t)
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-bytes")
	t.Setenv("TRAILERS_ENABLED", "true")
	t.Setenv("S3_ENDPOINT_URL", "http://localhost:4566")
	t.Setenv("S3_BUCKET", "trailers")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AWSRegion != "us-east-1" || cfg.AWSAccessKeyID != "test" || cfg.AWSSecretAccessKey != "test" {
		t.Errorf("S3 defaults = (%q, %q, %q), want (us-east-1, test, test)", cfg.AWSRegion, cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey)
	}
}
