package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"homework/internal/app"
	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
)

const testSecret = "webhook-test-secret-with-more-than-32-bytes"

type received struct {
	header http.Header
	body   []byte
}

// webhookServer records every delivery and reports it on the returned channel.
func webhookServer(t *testing.T, status int) (*httptest.Server, chan received) {
	t.Helper()
	deliveries := make(chan received, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		deliveries <- received{header: r.Header.Clone(), body: body}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server, deliveries
}

// adminRouter mirrors the cmd/api wiring with in-memory repositories and returns
// the handler, the decorated repository and an admin access cookie.
func adminRouter(t *testing.T, webhookURL string) (http.Handler, *NotifyingUsers, *http.Cookie) {
	t.Helper()
	inner := repository.NewInMemoryUserRepository()
	tokens, err := auth.NewTokenManager("webhook-router-test-secret-more-than-32b", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("admin-password-123")
	if err != nil {
		t.Fatal(err)
	}
	admin := model.User{Email: "admin@example.com", Name: "Admin", PasswordHash: hash, Role: model.RoleAdmin}
	if err := inner.Create(context.Background(), &admin); err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(admin)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{URL: webhookURL, Secret: testSecret, Timeout: time.Second}
	users := NewNotifyingUsers(inner, NewClient(cfg), cfg.Timeout)
	router := MarkAdminRegistration(app.NewRouterWithDeps(app.Deps{Users: users, Tokens: tokens}))
	return router, users, &http.Cookie{Name: "access_token", Value: token}
}

func csrf(t *testing.T, router http.Handler, cookies ...*http.Cookie) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/register", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /register = %d", response.Code)
	}
	var csrfCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "csrf_token" {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil {
		t.Fatal("missing csrf_token cookie")
	}
	return csrfCookie, csrfCookie.Value
}

func post(router http.Handler, path, contentType, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Origin", "http://example.com")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestAdminWebRegistrationSendsSignedEvent(t *testing.T) {
	server, deliveries := webhookServer(t, http.StatusOK)
	router, _, adminCookie := adminRouter(t, server.URL)
	csrfCookie, csrfToken := csrf(t, router, adminCookie)

	form := url.Values{
		"email": {"  New.User@Example.com "}, "name": {"New User"},
		"password": {"new-user-password"}, "csrf_token": {csrfToken},
	}
	response := post(router, "/register", "application/x-www-form-urlencoded", form.Encode(), csrfCookie, adminCookie)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog?notice=registered" {
		t.Fatalf("POST /register = %d %q; want unchanged 303 redirect", response.Code, response.Header().Get("Location"))
	}

	select {
	case delivery := <-deliveries:
		timestamp := delivery.header.Get(TimestampHeader)
		want := "sha256=" + Sign([]byte(testSecret), timestamp, delivery.body)
		if delivery.header.Get(SignatureHeader) != want {
			t.Errorf("signature = %q; want %q", delivery.header.Get(SignatureHeader), want)
		}
		var event UserCreatedEvent
		if err := json.Unmarshal(delivery.body, &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != EventUserCreated || event.User.Email != "new.user@example.com" ||
			event.User.Name != "New User" || event.User.Role != model.RoleViewer || event.User.ID == 0 {
			t.Errorf("event = %+v", event)
		}
		for _, forbidden := range []string{"password", "new-user-password", "hash"} {
			if strings.Contains(string(delivery.body), forbidden) {
				t.Errorf("event body contains %q", forbidden)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not called")
	}
}

func TestOtherRequestsDoNotSendEvents(t *testing.T) {
	server, deliveries := webhookServer(t, http.StatusOK)
	router, _, adminCookie := adminRouter(t, server.URL)

	// Public API registration keeps its contract and is not an admin action.
	api := post(router, "/api/v1/auth/register", "application/json",
		`{"email":"public@example.com","password":"public-password","name":"Public"}`)
	if api.Code != http.StatusCreated {
		t.Fatalf("API register = %d", api.Code)
	}
	// Anonymous web registration is rejected before the user is created.
	csrfCookie, csrfToken := csrf(t, router, adminCookie)
	form := url.Values{"email": {"anon@example.com"}, "name": {"Anon"}, "password": {"anon-password"}, "csrf_token": {csrfToken}}
	anonymous := post(router, "/register", "application/x-www-form-urlencoded", form.Encode(), csrfCookie)
	if anonymous.Header().Get("Location") == "/catalog?notice=registered" {
		t.Fatalf("anonymous POST /register succeeded (%d)", anonymous.Code)
	}

	select {
	case <-deliveries:
		t.Fatal("webhook called for a request that is not an admin user creation")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestDeliveryFailureDoesNotChangeResponse(t *testing.T) {
	server, _ := webhookServer(t, http.StatusInternalServerError)
	router, users, adminCookie := adminRouter(t, server.URL)
	var wg sync.WaitGroup
	wg.Add(1)
	users.done = wg.Done

	csrfCookie, csrfToken := csrf(t, router, adminCookie)
	form := url.Values{"email": {"fail@example.com"}, "name": {"Fail"}, "password": {"fail-password"}, "csrf_token": {csrfToken}}
	if response := post(router, "/register", "application/x-www-form-urlencoded", form.Encode(), csrfCookie, adminCookie); response.Code != http.StatusSeeOther {
		t.Fatalf("POST /register = %d; want 303 even when the webhook fails", response.Code)
	}
	wg.Wait()
	if _, err := users.FindByEmail(context.Background(), "fail@example.com"); err != nil {
		t.Fatalf("user was not kept after webhook failure: %v", err)
	}
}

type fakeSender struct{ calls int }

func (sender *fakeSender) Send(context.Context, UserCreatedEvent) error { sender.calls++; return nil }

type failingUsers struct{ repository.UserRepository }

func (failingUsers) Create(context.Context, *model.User) error { return errors.New("insert failed") }

func TestFailedCreateSendsNothing(t *testing.T) {
	sender := &fakeSender{}
	users := &NotifyingUsers{UserRepository: failingUsers{}, sender: sender, timeout: time.Second, now: time.Now}
	ctx := context.WithValue(context.Background(), triggerKey{}, true)
	if err := users.Create(ctx, &model.User{}); err == nil {
		t.Fatal("Create error was swallowed")
	}
	if sender.calls != 0 {
		t.Fatalf("sender called %d times after a failed Create", sender.calls)
	}
}

func TestConfigFromEnv(t *testing.T) {
	tests := []struct {
		name, url, secret, timeout string
		wantErr                    bool
	}{
		{name: "disabled", url: ""},
		{name: "valid", url: "http://webhook.local/hook", secret: testSecret, timeout: "2s"},
		{name: "relative url", url: "/hook", secret: testSecret, wantErr: true},
		{name: "bad scheme", url: "ftp://host/hook", secret: testSecret, wantErr: true},
		{name: "short secret", url: "http://host/hook", secret: "short", wantErr: true},
		{name: "bad timeout", url: "http://host/hook", secret: testSecret, timeout: "-1s", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WEBHOOK_URL", test.url)
			t.Setenv("WEBHOOK_SECRET", test.secret)
			t.Setenv("WEBHOOK_TIMEOUT", test.timeout)
			cfg, err := ConfigFromEnv()
			if (err != nil) != test.wantErr {
				t.Fatalf("err = %v; wantErr %v", err, test.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), test.secret) && test.secret != "" {
				t.Fatal("error message leaks the secret")
			}
			if test.name == "valid" && cfg.Timeout != 2*time.Second {
				t.Fatalf("timeout = %v", cfg.Timeout)
			}
		})
	}
}
