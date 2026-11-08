package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
)

func TestAuthAndRoleFailuresAbortBeforeHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager, err := auth.NewTokenManager("middleware-test-secret-with-at-least-32-bytes", time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	users := repository.NewInMemoryUserRepository()
	viewer := model.User{Email: "viewer@example.com", Name: "Viewer", PasswordHash: "hash", Role: model.RoleViewer}
	if err := users.Create(context.Background(), &viewer); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	validToken, err := manager.Issue(viewer)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	expiredToken := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		UserID: 1,
		Role:   model.RoleViewer,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	})
	expired, err := expiredToken.SignedString([]byte("middleware-test-secret-with-at-least-32-bytes"))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}

	tests := []struct {
		name       string
		token      string
		addHeader  bool
		wantStatus int
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized},
		{name: "bad token", token: "not-a-token", addHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "expired token", token: expired, addHeader: true, wantStatus: http.StatusUnauthorized},
		{name: "wrong role", token: validToken, addHeader: true, wantStatus: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			router := gin.New()
			router.Use(AuthMiddleware(manager, users), RequireRole(model.RoleAdmin))
			router.GET("/protected", func(c *gin.Context) {
				calls++
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if test.addHeader {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if calls != 0 {
				t.Errorf("sentinel handler called %d times, want 0", calls)
			}
			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", response.Code, test.wantStatus, response.Body)
			}
		})
	}
}

func TestAuthMiddlewareUsesCurrentRoleFromRepository(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager, err := auth.NewTokenManager("middleware-test-secret-with-at-least-32-bytes", time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	users := repository.NewInMemoryUserRepository()
	admin := model.User{Email: "admin@example.com", Name: "Admin", PasswordHash: "hash", Role: model.RoleAdmin}
	if err := users.Create(context.Background(), &admin); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	adminToken, err := manager.Issue(admin)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	ghostToken, err := manager.Issue(model.User{ID: 999, Role: model.RoleAdmin})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	router := gin.New()
	router.Use(AuthMiddleware(manager, users), RequireRole(model.RoleAdmin))
	router.GET("/protected", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	status := func(token string) int {
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response.Code
	}

	if got := status(adminToken); got != http.StatusNoContent {
		t.Fatalf("admin status = %d, want 204", got)
	}
	if _, err := users.UpdateRole(context.Background(), admin.ID, model.RoleViewer); err != nil {
		t.Fatalf("UpdateRole() error = %v", err)
	}
	if got := status(adminToken); got != http.StatusForbidden {
		t.Errorf("demoted admin status = %d, want 403", got)
	}
	if got := status(ghostToken); got != http.StatusUnauthorized {
		t.Errorf("unknown user status = %d, want 401", got)
	}
}
