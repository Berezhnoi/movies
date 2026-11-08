package repository

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// trailerTruncateSQL clears trailers and the movies they reference.
const trailerTruncateSQL = "TRUNCATE TABLE movie_trailers, movies, movie_directors, genres, directors, users RESTART IDENTITY CASCADE"

func openIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("invalid TEST_DATABASE_URL")
	}
	databaseName := strings.TrimPrefix(parsedURL.Path, "/")
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf("refusing integration tests: TEST_DATABASE_URL database %q must end in _test", databaseName)
	}
	if configuredName := os.Getenv("TEST_DB_NAME"); configuredName != "" && databaseName != configuredName {
		t.Fatalf("refusing integration tests: TEST_DATABASE_URL database %q differs from TEST_DB_NAME %q", databaseName, configuredName)
	}
	if databaseName == os.Getenv("DB_NAME") {
		t.Fatal("refusing integration tests: TEST_DATABASE_URL must not use DB_NAME")
	}

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatal("open TEST_DATABASE_URL")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("get test database connection")
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return db
}
