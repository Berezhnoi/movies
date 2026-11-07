package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
	"homework/internal/service"
)

const testServiceSecret = "service-test-secret-with-at-least-32-bytes"

func TestAuthServiceRegister(t *testing.T) {
	tests := []struct {
		name    string
		request model.RegisterRequest
		wantErr string
	}{
		{
			name:    "registers normalized viewer",
			request: model.RegisterRequest{Email: "  Reader@Example.com ", Password: "correct horse", Name: " Reader "},
		},
		{
			name:    "rejects short password",
			request: model.RegisterRequest{Email: "reader@example.com", Password: "short", Name: "Reader"},
			wantErr: "password",
		},
		{
			name:    "rejects long password",
			request: model.RegisterRequest{Email: "reader@example.com", Password: strings.Repeat("x", 73), Name: "Reader"},
			wantErr: "password",
		},
		{
			name:    "rejects invalid email",
			request: model.RegisterRequest{Email: "Reader <reader@example.com>", Password: "correct horse", Name: "Reader"},
			wantErr: "email",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authService := newTestAuthService(t)
			user, err := authService.Register(context.Background(), test.request)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantErr) {
					t.Fatalf("Register() error = %v, want %q error", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Register() error = %v", err)
			}
			if user.Email != "reader@example.com" || user.Name != "Reader" || user.Role != model.RoleViewer {
				t.Fatalf("Register() = %#v, want normalized viewer", user)
			}
		})
	}
}

func TestAuthServiceDuplicateEmail(t *testing.T) {
	authService := newTestAuthService(t)
	first := model.RegisterRequest{Email: "reader@example.com", Password: "correct horse", Name: "Reader"}
	if _, err := authService.Register(context.Background(), first); err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}
	second := model.RegisterRequest{Email: "READER@example.com", Password: "correct horse", Name: "Reader"}
	_, err := authService.Register(context.Background(), second)
	var conflictError service.ConflictError
	if !errors.As(err, &conflictError) || conflictError.Message != "email already registered" {
		t.Fatalf("Register(duplicate) error = %v, want email already registered conflict error", err)
	}
}

func TestAuthServiceLogin(t *testing.T) {
	tests := []struct {
		name     string
		request  model.LoginRequest
		create   bool
		wantAuth bool
	}{
		{
			name:     "login succeeds",
			request:  model.LoginRequest{Email: "reader@example.com", Password: "correct horse"},
			create:   true,
			wantAuth: true,
		},
		{
			name:     "wrong password",
			request:  model.LoginRequest{Email: "reader@example.com", Password: "wrong password"},
			create:   true,
			wantAuth: false,
		},
		{
			name:     "unknown email",
			request:  model.LoginRequest{Email: "missing@example.com", Password: "wrong password"},
			wantAuth: false,
		},
	}
	var authErrors []error
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authService := newTestAuthService(t)
			if test.create {
				if _, err := authService.Register(context.Background(), model.RegisterRequest{
					Email: "reader@example.com", Password: "correct horse", Name: "Reader",
				}); err != nil {
					t.Fatalf("Register() error = %v", err)
				}
			}
			response, err := authService.Login(context.Background(), test.request)
			if !test.wantAuth {
				if err != service.ErrUnauthorized {
					t.Fatalf("Login() error = %v, want the shared ErrUnauthorized value", err)
				}
				authErrors = append(authErrors, err)
				return
			}
			if err != nil {
				t.Fatalf("Login() error = %v", err)
			}
			if response.AccessToken == "" || response.TokenType != "Bearer" || response.ExpiresIn != int64((24*time.Hour).Seconds()) {
				t.Fatalf("Login() response = %#v, want bearer token expiring in 24h", response)
			}
		})
	}
	if len(authErrors) != 2 || authErrors[0] != authErrors[1] {
		t.Fatalf("login failure errors = %#v, want identical error values", authErrors)
	}
}

func TestAuthServiceUnknownEmailPerformsDummyHashComparison(t *testing.T) {
	authService := newTestAuthService(t)
	startedAt := time.Now()
	_, err := authService.Login(context.Background(), model.LoginRequest{
		Email:    "missing@example.com",
		Password: "wrong password",
	})
	elapsed := time.Since(startedAt)

	if err != service.ErrUnauthorized {
		t.Fatalf("Login() error = %v, want ErrUnauthorized", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("unknown-email login completed in %s, expected dummy bcrypt comparison", elapsed)
	}
}

func newTestAuthService(t *testing.T) *service.AuthService {
	t.Helper()
	tokenManager, err := auth.NewTokenManager(testServiceSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	return service.NewAuthService(repository.NewInMemoryUserRepository(), tokenManager)
}
