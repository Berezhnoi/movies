package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
	"homework/internal/service"
)

const handlerTestSecret = "handler-test-secret-with-at-least-32-bytes"

func TestAuthHandlerRegisterCreatesUserAndRejectsDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authService := newHandlerAuthService(t)
	handler := NewAuthHandler(authService)
	body := `{"email":"reader@example.com","password":"correct horse","name":"Reader","role":"admin"}`
	response := performAuthRequest(handler.Register, body)
	if response.Code != http.StatusCreated {
		t.Fatalf("Register() status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "password") {
		t.Fatalf("Register() response exposes credentials: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"role":"viewer"`) {
		t.Fatalf("Register() did not assign viewer role: %s", response.Body.String())
	}
	duplicate := performAuthRequest(handler.Register, body)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate Register() status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}
	if !strings.Contains(duplicate.Body.String(), `"code":"conflict"`) {
		t.Fatalf("duplicate Register() body lacks conflict: %s", duplicate.Body.String())
	}
}

func TestAuthHandlerLoginFailuresHaveIdenticalResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authService := newHandlerAuthService(t)
	if _, err := authService.Register(context.Background(), model.RegisterRequest{
		Email: "reader@example.com", Password: "correct horse", Name: "Reader",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	handler := NewAuthHandler(authService)
	wrongPassword := performAuthRequest(handler.Login, `{"email":"reader@example.com","password":"wrong password"}`)
	unknownEmail := performAuthRequest(handler.Login, `{"email":"missing@example.com","password":"wrong password"}`)
	if wrongPassword.Code != http.StatusUnauthorized || unknownEmail.Code != http.StatusUnauthorized {
		t.Fatalf("login statuses = (%d, %d), want both 401", wrongPassword.Code, unknownEmail.Code)
	}
	if wrongPassword.Body.String() != unknownEmail.Body.String() {
		t.Fatalf("login error bodies differ: %q vs %q", wrongPassword.Body.String(), unknownEmail.Body.String())
	}
	if !reflect.DeepEqual(wrongPassword.Header(), unknownEmail.Header()) {
		t.Fatalf("login error headers differ: %#v vs %#v", wrongPassword.Header(), unknownEmail.Header())
	}
	if wrongPassword.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", wrongPassword.Header().Get("WWW-Authenticate"))
	}
	if got, want := wrongPassword.Body.String(), "{\"error\":{\"code\":\"unauthorized\",\"message\":\"invalid credentials\"}}"; got != want {
		t.Fatalf("login error body = %q, want %q", got, want)
	}
}

func TestAuthHandlerRejectsInvalidJSONAndFieldTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(newHandlerAuthService(t))
	for _, body := range []string{`[]`, `{"email":7,"password":"correct horse","name":"Reader"}`, `{"email":"reader@example.com"`} {
		response := performAuthRequest(handler.Register, body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Errorf("Register(%q) status = %d, want 422", body, response.Code)
		}
	}
	for _, body := range []string{`[]`, `{"email":7,"password":"wrong password"}`, `{"email":"reader@example.com"`} {
		response := performAuthRequest(handler.Login, body)
		if response.Code != http.StatusUnprocessableEntity {
			t.Errorf("Login(%q) status = %d, want 422", body, response.Code)
		}
		if strings.Contains(response.Body.String(), "internal") {
			t.Errorf("Login(%q) leaked an internal error: %s", body, response.Body)
		}
	}
}

func newHandlerAuthService(t *testing.T) *service.AuthService {
	t.Helper()
	manager, err := auth.NewTokenManager(handlerTestSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	return service.NewAuthService(repository.NewInMemoryUserRepository(), manager)
}

func performAuthRequest(handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	handler(context)
	return recorder
}
