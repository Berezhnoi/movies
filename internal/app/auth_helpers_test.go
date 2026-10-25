package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"homework/internal/app"
	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
)

const authTestSecret = "auth-rbac-integration-test-secret-at-least-32-bytes"

type authTestEnv struct {
	router http.Handler
	users  *repository.InMemoryUserRepository
	tokens *auth.TokenManager
}

type authTestResponse struct {
	Code   int
	Header http.Header
	Body   []byte
}

func newAuthTestEnv(t *testing.T) *authTestEnv {
	t.Helper()

	users := repository.NewInMemoryUserRepository()
	tokens, err := auth.NewTokenManager(authTestSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	return &authTestEnv{
		router: app.NewRouterWithDeps(app.Deps{
			Movies: repository.NewInMemoryMovieRepositoryAdapter(),
			Users:  users,
			Tokens: tokens,
		}),
		users:  users,
		tokens: tokens,
	}
}

func (e *authTestEnv) createUser(t *testing.T, email, password, role string) model.User {
	t.Helper()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	user := model.User{
		Email:        email,
		Name:         "Integration User",
		PasswordHash: string(passwordHash),
		Role:         role,
	}
	if err := e.users.Create(context.Background(), &user); err != nil {
		t.Fatalf("users.Create() error = %v", err)
	}
	return user
}

func (e *authTestEnv) login(t *testing.T, email, password string) string {
	t.Helper()

	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	response := e.do(http.MethodPost, "/api/v1/auth/login", string(body), "")
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200: %s", response.Code, response.Body)
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatalf("decode login response: %v (body: %s)", err, response.Body)
	}
	if result.AccessToken == "" {
		t.Fatalf("login response has no access_token: %s", response.Body)
	}
	return result.AccessToken
}

func (e *authTestEnv) signTokenWithExpiration(t *testing.T, expiration time.Time) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		UserID: 1,
		Role:   model.RoleViewer,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiration),
		},
	})
	signed, err := token.SignedString([]byte(authTestSecret))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return signed
}

func (e *authTestEnv) do(method, path, body, token string) authTestResponse {
	authHeader := ""
	if token != "" {
		authHeader = "Bearer " + token
	}
	return e.doWithAuthorization(method, path, body, authHeader)
}

func (e *authTestEnv) doWithAuthorization(method, path, body, authorization string) authTestResponse {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	e.router.ServeHTTP(response, request)
	return authTestResponse{Code: response.Code, Header: response.Header().Clone(), Body: response.Body.Bytes()}
}
