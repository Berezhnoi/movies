package app_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"homework/internal/app"
	"homework/internal/auth"
	"homework/internal/chat"
	webhandler "homework/internal/handler/web"
	"homework/internal/model"
	"homework/internal/repository"
)

const (
	testWebEmail    = "viewer@example.test"
	testWebPassword = "correct horse battery staple"
	testWebName     = "Test Viewer"
)

func TestWebLoginCookieAndProtectedPage(t *testing.T) {
	router, users, tokens := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	response := submitForm(router, "/login", url.Values{
		"email": {testWebEmail}, "password": {testWebPassword}, "csrf_token": {csrfToken},
	}, csrfCookie)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog" {
		t.Fatalf("POST /login = %d, Location %q; want 303 /catalog", response.Code, response.Header().Get("Location"))
	}
	accessCookie := findCookie(t, response, "access_token")
	if !accessCookie.HttpOnly || accessCookie.SameSite != http.SameSiteLaxMode ||
		accessCookie.Path != "/" || accessCookie.MaxAge != int(tokens.TTL().Seconds()) {
		t.Errorf("access cookie attributes = %+v; want HttpOnly, SameSite=Lax, Path=/, MaxAge=JWT TTL", accessCookie)
	}
	if strings.Contains(response.Body.String(), accessCookie.Value) {
		t.Fatal("access token appeared in the login response body")
	}

	catalog := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/catalog", nil)
	request.AddCookie(accessCookie)
	router.ServeHTTP(catalog, request)
	if catalog.Code != http.StatusOK || !strings.Contains(catalog.Body.String(), testWebName) {
		t.Fatalf("GET /catalog with session cookie = %d; expected authenticated catalog: %s", catalog.Code, catalog.Body)
	}

	protected := gin.New()
	protected.Use(webhandler.OptionalAuth(tokens, users, false))
	protected.GET("/protected", webhandler.RequireLogin(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	protectedResponse := httptest.NewRecorder()
	protectedRequest := httptest.NewRequest(http.MethodGet, "/protected", nil)
	protectedRequest.AddCookie(accessCookie)
	protected.ServeHTTP(protectedResponse, protectedRequest)
	if protectedResponse.Code != http.StatusOK {
		t.Fatalf("protected page with login cookie = %d, want 200", protectedResponse.Code)
	}
}

func TestWebLoginUsesOneGenericFailure(t *testing.T) {
	router, _, _ := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	submit := func(email, password string) *httptest.ResponseRecorder {
		return submitForm(router, "/login", url.Values{
			"email": {email}, "password": {password}, "csrf_token": {csrfToken},
		}, csrfCookie)
	}
	wrongPassword := submit(testWebEmail, "incorrect password")
	unknownEmail := submit("missing@example.test", "incorrect password")
	if wrongPassword.Code != http.StatusUnauthorized || unknownEmail.Code != http.StatusUnauthorized {
		t.Fatalf("login statuses = (%d, %d), want both 401", wrongPassword.Code, unknownEmail.Code)
	}
	if wrongPassword.Body.String() != unknownEmail.Body.String() {
		t.Fatalf("login failure bodies differ:\nwrong password: %s\nunknown email: %s", wrongPassword.Body, unknownEmail.Body)
	}
	if !strings.Contains(wrongPassword.Body.String(), "Invalid email or password") ||
		strings.Contains(wrongPassword.Body.String(), "incorrect password") {
		t.Fatalf("login failure did not show only the generic message: %s", wrongPassword.Body)
	}
}

func TestWebLoginRejectsUnsafeNextPaths(t *testing.T) {
	router, _, _ := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	for _, next := range []string{"//evil.com", "https://evil.com", `/\evil.com`} {
		t.Run(next, func(t *testing.T) {
			response := submitForm(router, "/login", url.Values{
				"email": {testWebEmail}, "password": {testWebPassword},
				"csrf_token": {csrfToken}, "next": {next},
			}, csrfCookie)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog" {
				t.Errorf("unsafe next %q redirected with status %d to %q", next, response.Code, response.Header().Get("Location"))
			}
		})
	}
}

func TestWebLogoutClearsCookie(t *testing.T) {
	router, _, _ := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	login := submitForm(router, "/login", url.Values{
		"email": {testWebEmail}, "password": {testWebPassword}, "csrf_token": {csrfToken},
	}, csrfCookie)
	accessCookie := findCookie(t, login, "access_token")
	logout := submitForm(router, "/logout", url.Values{"csrf_token": {csrfToken}}, csrfCookie, accessCookie)
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/catalog" {
		t.Fatalf("POST /logout = %d, Location %q; want 303 /catalog", logout.Code, logout.Header().Get("Location"))
	}
	cleared := findCookie(t, logout, "access_token")
	if cleared.MaxAge != -1 || cleared.Value != "" || !cleared.HttpOnly ||
		cleared.SameSite != http.SameSiteLaxMode || cleared.Path != "/" {
		t.Errorf("logout cookie = %+v, want expired cookie with the session attributes", cleared)
	}
}

func TestWebCSRFRejectsMissingWrongTokenAndMismatchedOrigin(t *testing.T) {
	router, _, _ := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	tests := []struct {
		name          string
		formToken     string
		header        string
		origin        string
		missingOrigin bool
		wantStatus    int
	}{
		{name: "missing token", wantStatus: http.StatusForbidden},
		{name: "wrong token", header: "wrong-token", wantStatus: http.StatusForbidden},
		{name: "missing origin", header: csrfToken, missingOrigin: true, wantStatus: http.StatusForbidden},
		{name: "mismatched origin", header: csrfToken, origin: "https://attacker.example", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{
				"email": {testWebEmail}, "password": {testWebPassword}, "csrf_token": {test.formToken},
			}.Encode()))
			if !test.missingOrigin {
				request.Header.Set("Origin", "http://example.com")
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if test.header != "" {
				request.Header.Set("X-CSRF-Token", test.header)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			request.AddCookie(csrfCookie)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Errorf("POST /login status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestWebAuthDoesNotChangeAPIHeaderAuthentication(t *testing.T) {
	router, _, _ := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	login := submitForm(router, "/login", url.Values{
		"email": {testWebEmail}, "password": {testWebPassword}, "csrf_token": {csrfToken},
	}, csrfCookie)
	accessCookie := findCookie(t, login, "access_token")

	cookieOnly := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	cookieOnly.AddCookie(accessCookie)
	cookieResponse := httptest.NewRecorder()
	router.ServeHTTP(cookieResponse, cookieOnly)
	if cookieResponse.Code != http.StatusUnauthorized {
		t.Fatalf("API request with only access cookie = %d, want 401", cookieResponse.Code)
	}

	headerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	headerRequest.Header.Set("Authorization", "Bearer "+accessCookie.Value)
	headerResponse := httptest.NewRecorder()
	router.ServeHTTP(headerResponse, headerRequest)
	if headerResponse.Code != http.StatusOK {
		t.Fatalf("API request with Authorization header = %d, want 200: %s", headerResponse.Code, headerResponse.Body)
	}
}

func TestSecureCookieFlagAndInvalidCookieHandling(t *testing.T) {
	_, users, tokens := webAuthRouter(t)
	router := app.NewRouterWithDeps(app.Deps{Users: users, Tokens: tokens, CookieSecure: true})
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	if !csrfCookie.Secure {
		t.Fatal("CSRF cookie is missing Secure when COOKIE_SECURE is enabled")
	}
	login := submitForm(router, "/login", url.Values{
		"email": {testWebEmail}, "password": {testWebPassword}, "csrf_token": {csrfToken},
	}, csrfCookie)
	if !findCookie(t, login, "access_token").Secure {
		t.Fatal("access cookie is missing Secure when COOKIE_SECURE is enabled")
	}

	invalidRequest := httptest.NewRequest(http.MethodGet, "/catalog", nil)
	invalidRequest.AddCookie(&http.Cookie{Name: "access_token", Value: "invalid-token"})
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusOK {
		t.Fatalf("GET /catalog with invalid cookie = %d, want 200", invalidResponse.Code)
	}
	cleared := findCookie(t, invalidResponse, "access_token")
	if cleared.MaxAge != -1 || !cleared.Secure || !cleared.HttpOnly ||
		cleared.SameSite != http.SameSiteLaxMode || cleared.Path != "/" {
		t.Errorf("invalid session cookie was not safely cleared: %+v", cleared)
	}
}

func TestWebProtectedRouteResponses(t *testing.T) {
	_, users, tokens := webAuthRouter(t)
	renderer, err := webhandler.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(webhandler.OptionalAuth(tokens, users, false))
	router.GET("/login-required", webhandler.RequireLogin(), func(c *gin.Context) {
		c.String(http.StatusOK, "handler reached")
	})
	router.GET("/editor", webhandler.RequireRole(renderer, model.RoleEditor), func(c *gin.Context) {
		c.String(http.StatusOK, "handler reached")
	})

	anonymous := httptest.NewRecorder()
	router.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/login-required", nil))
	if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/login?next=%2Flogin-required" {
		t.Errorf("normal anonymous response = %d, Location %q", anonymous.Code, anonymous.Header().Get("Location"))
	}

	htmxAnonymousRequest := httptest.NewRequest(http.MethodGet, "/login-required", nil)
	htmxAnonymousRequest.Header.Set("HX-Request", "true")
	htmxAnonymous := httptest.NewRecorder()
	router.ServeHTTP(htmxAnonymous, htmxAnonymousRequest)
	if htmxAnonymous.Code != http.StatusUnauthorized || htmxAnonymous.Header().Get("HX-Redirect") != "/login" ||
		htmxAnonymous.Body.Len() != 0 {
		t.Errorf("HTMX anonymous response = %d, HX-Redirect %q, body %q",
			htmxAnonymous.Code, htmxAnonymous.Header().Get("HX-Redirect"), htmxAnonymous.Body.String())
	}

	user, err := users.FindByEmail(context.Background(), testWebEmail)
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := tokens.Issue(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		htmx     bool
		wantHTML bool
	}{
		{name: "normal request", wantHTML: true},
		{name: "htmx request", htmx: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/editor", nil)
			request.AddCookie(&http.Cookie{Name: "access_token", Value: viewerToken})
			if test.htmx {
				request.Header.Set("HX-Request", "true")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("wrong-role response = %d, want 403", response.Code)
			}
			if test.wantHTML && !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
				t.Errorf("normal role failure Content-Type = %q, want HTML", response.Header().Get("Content-Type"))
			}
			if test.htmx && response.Body.Len() != 0 {
				t.Errorf("HTMX wrong-role body = %q, want empty", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "handler reached") {
				t.Error("forbidden request reached the handler")
			}
		})
	}
}

func TestWebRegistrationRendersValidationAndPreservesNonPasswordFields(t *testing.T) {
	router, users, tokens := webAuthRouter(t)
	admin, err := users.FindByEmail(context.Background(), testWebEmail)
	if err != nil {
		t.Fatal(err)
	}
	admin, err = users.UpdateRole(context.Background(), admin.ID, model.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := tokens.Issue(admin)
	if err != nil {
		t.Fatal(err)
	}
	viewerRequest := httptest.NewRequest(http.MethodGet, "/register", nil)
	viewerRequest.AddCookie(&http.Cookie{Name: "access_token", Value: mustIssueToken(t, tokens, model.User{
		ID: admin.ID, Role: model.RoleViewer,
	})})
	viewerResponse := httptest.NewRecorder()
	router.ServeHTTP(viewerResponse, viewerRequest)
	if viewerResponse.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /register = %d, want 403", viewerResponse.Code)
	}
	adminRequest := httptest.NewRequest(http.MethodGet, "/register", nil)
	adminRequest.AddCookie(&http.Cookie{Name: "access_token", Value: adminToken})
	adminResponse := httptest.NewRecorder()
	router.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || !strings.Contains(adminResponse.Body.String(), "<h1>Create an account</h1>") {
		t.Fatalf("admin GET /register = %d, want registration form", adminResponse.Code)
	}
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	response := submitForm(router, "/register", url.Values{
		"name": {"Typed Name"}, "email": {"not-an-email"}, "password": {"short"},
		"csrf_token": {csrfToken},
	}, csrfCookie, &http.Cookie{Name: "access_token", Value: adminToken})
	body := response.Body.String()
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "role=\"alert\"") ||
		!strings.Contains(body, `value="Typed Name"`) || !strings.Contains(body, `value="not-an-email"`) ||
		strings.Contains(body, "short") {
		t.Fatalf("invalid registration response = %d, body %s", response.Code, body)
	}
}

func mustIssueToken(t *testing.T, tokens *auth.TokenManager, user model.User) string {
	t.Helper()
	token, err := tokens.Issue(user)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestWebRoleAwareCatalog(t *testing.T) {
	router, users, tokens := webAuthRouter(t)
	user, err := users.FindByEmail(context.Background(), testWebEmail)
	if err != nil {
		t.Fatal(err)
	}
	movieRepo := repository.NewInMemoryMovieRepositoryAdapter()
	if _, err := movieRepo.Create(context.Background(), model.Movie{Title: "Role Fixture", Director: "Director", Country: "Country", Genre: "Drama"}); err != nil {
		t.Fatal(err)
	}
	router = app.NewRouterWithDeps(app.Deps{Users: users, Tokens: tokens, Movies: movieRepo})

	for _, test := range []struct {
		role        string
		canEdit     bool
		canDelete   bool
		canRegister bool
	}{
		{role: model.RoleViewer},
		{role: model.RoleEditor, canEdit: true},
		{role: model.RoleAdmin, canEdit: true, canDelete: true, canRegister: true},
	} {
		t.Run(test.role, func(t *testing.T) {
			updated, err := users.UpdateRole(context.Background(), user.ID, test.role)
			if err != nil {
				t.Fatal(err)
			}
			token, err := tokens.Issue(updated)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/catalog", nil)
			request.AddCookie(&http.Cookie{Name: "access_token", Value: token})
			router.ServeHTTP(response, request)
			body := response.Body.String()
			if response.Code != http.StatusOK {
				t.Fatalf("GET /catalog status = %d", response.Code)
			}
			if got := strings.Contains(body, ">Add movie</a>"); got != test.canEdit {
				t.Errorf("Add movie visibility = %t, want %t", got, test.canEdit)
			}
			if got := strings.Contains(body, ">Edit</a>"); got != test.canEdit {
				t.Errorf("Edit visibility = %t, want %t", got, test.canEdit)
			}
			if got := strings.Contains(body, ">Delete</button>"); got != test.canDelete {
				t.Errorf("Delete visibility = %t, want %t", got, test.canDelete)
			}
			if got := strings.Contains(body, `href="/register">Register</a>`); got != test.canRegister {
				t.Errorf("Register visibility = %t, want %t", got, test.canRegister)
			}
		})
	}
}

func TestRequestLoggerDoesNotLogCredentialsCookiesOrFormBody(t *testing.T) {
	router, _, _ := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/login")
	var logs bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{
		"email": {"private-user@example.test"}, "password": {"password-log-marker"},
		"csrf_token": {csrfToken},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://example.com")
	request.AddCookie(csrfCookie)
	request.AddCookie(&http.Cookie{Name: "access_token", Value: "cookie-log-marker"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	for _, secret := range []string{"private-user@example.test", "password-log-marker", "cookie-log-marker"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("request logs reveal %q: %s", secret, logs.String())
		}
	}
}

func webAuthRouter(t *testing.T) (http.Handler, *repository.InMemoryUserRepository, *auth.TokenManager) {
	t.Helper()
	users := repository.NewInMemoryUserRepository()
	tokens, err := auth.NewTokenManager("web-auth-test-secret-with-more-than-32-bytes", 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	router := app.NewRouterWithDeps(app.Deps{Users: users, Tokens: tokens})
	registration := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(
		fmt.Sprintf(`{"email":%q,"password":%q,"name":%q}`, testWebEmail, testWebPassword, testWebName),
	))
	registration.Header.Set("Content-Type", "application/json")
	registrationResponse := httptest.NewRecorder()
	router.ServeHTTP(registrationResponse, registration)
	if registrationResponse.Code != http.StatusCreated {
		t.Fatalf("API fixture registration = %d: %s", registrationResponse.Code, registrationResponse.Body)
	}
	return router, users, tokens
}

func getCSRF(t *testing.T, router http.Handler, path string) (*http.Cookie, string) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, response.Code, response.Body)
	}
	cookie := findCookie(t, response, "csrf_token")
	const marker = `<meta name="csrf-token" content="`
	_, rest, found := strings.Cut(response.Body.String(), marker)
	if !found {
		t.Fatal("page is missing the CSRF meta token")
	}
	token, _, found := strings.Cut(rest, `"`)
	if !found || token == "" || token != cookie.Value {
		t.Fatalf("page CSRF token %q does not match cookie %q", token, cookie.Value)
	}
	return cookie, token
}

func submitForm(router http.Handler, path string, values url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://example.com")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func findCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response does not contain cookie %q", name)
	return nil
}

func TestWebCatalogLinksToChatOnlyWhenEnabledAndLoggedIn(t *testing.T) {
	_, users, tokens := webAuthRouter(t)
	user, err := users.FindByEmail(context.Background(), testWebEmail)
	if err != nil {
		t.Fatal(err)
	}
	movieRepo := repository.NewInMemoryMovieRepositoryAdapter()
	movie, err := movieRepo.Create(context.Background(), model.Movie{Title: "Chat Fixture", Director: "Director", Country: "Country", Genre: "Drama"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(user)
	if err != nil {
		t.Fatal(err)
	}
	chatLink := fmt.Sprintf(`href="/movies/%d/chat"`, movie.ID)

	for _, test := range []struct {
		name     string
		hub      *chat.Hub
		token    string
		wantLink bool
	}{
		{name: "logged in with chat", hub: chat.NewHub(), token: token, wantLink: true},
		{name: "anonymous with chat", hub: chat.NewHub()},
		{name: "logged in without chat", token: token},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := app.NewRouterWithDeps(app.Deps{Users: users, Tokens: tokens, Movies: movieRepo, Chat: test.hub})
			request := httptest.NewRequest(http.MethodGet, "/catalog", nil)
			if test.token != "" {
				request.AddCookie(&http.Cookie{Name: "access_token", Value: test.token})
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if got := strings.Contains(response.Body.String(), chatLink); got != test.wantLink {
				t.Errorf("chat link visibility = %t, want %t", got, test.wantLink)
			}
		})
	}
}
