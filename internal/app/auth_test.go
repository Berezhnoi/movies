package app_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"homework/internal/auth"
	"homework/internal/model"
)

const (
	authTestEmail    = "reader@example.com"
	authTestPassword = "correct-horse-battery"
)

func TestAuth_RegisterDuplicateEmail(t *testing.T) {
	tests := []struct {
		name          string
		duplicateMail string
	}{
		{name: "same case", duplicateMail: authTestEmail},
		{name: "different letter case", duplicateMail: "READER@EXAMPLE.COM"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newAuthTestEnv(t)
			first := env.do(http.MethodPost, "/api/v1/auth/register",
				`{"email":"reader@example.com","password":"correct-horse-battery","name":"Reader"}`, "")
			if first.Code != http.StatusCreated {
				t.Fatalf("first registration status = %d, want 201: %s", first.Code, first.Body)
			}

			body, err := json.Marshal(map[string]string{
				"email":    test.duplicateMail,
				"password": "another-correct-password",
				"name":     "Duplicate",
			})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			response := env.do(http.MethodPost, "/api/v1/auth/register", string(body), "")
			assertAuthStatusAndCode(t, response, http.StatusConflict, "conflict")
		})
	}
}

func TestAuth_RegisterCreatesViewerWithoutCredentials(t *testing.T) {
	env := newAuthTestEnv(t)
	response := env.do(http.MethodPost, "/api/v1/auth/register",
		`{"email":"new-reader@example.com","password":"correct-horse-battery","name":"New Reader","role":"admin"}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, want 201: %s", response.Code, response.Body)
	}

	var result map[string]any
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatalf("decode registration response: %v (body: %s)", err, response.Body)
	}
	if result["role"] != model.RoleViewer {
		t.Errorf("registered role = %v, want viewer", result["role"])
	}
	for _, key := range []string{"password_hash", "password"} {
		if _, exists := result[key]; exists {
			t.Errorf("registration response contains %q: %s", key, response.Body)
		}
	}
}

func TestAuth_LoginReturnsTokenWithExpectedClaims(t *testing.T) {
	env := newAuthTestEnv(t)
	user := env.createUser(t, authTestEmail, authTestPassword, model.RoleEditor)
	beforeLogin := time.Now()
	body, err := json.Marshal(map[string]string{"email": authTestEmail, "password": authTestPassword})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	response := env.do(http.MethodPost, "/api/v1/auth/login", string(body), "")
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200: %s", response.Code, response.Body)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatalf("decode login response: %v (body: %s)", err, response.Body)
	}
	if result.AccessToken == "" {
		t.Fatal("login response has no access_token")
	}
	if result.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", result.TokenType)
	}
	if result.ExpiresIn != 86400 {
		t.Errorf("expires_in = %d, want 86400", result.ExpiresIn)
	}
	claims, err := env.tokens.Parse(result.AccessToken)
	if err != nil {
		t.Fatalf("TokenManager.Parse() error = %v", err)
	}
	if claims.UserID != user.ID {
		t.Errorf("JWT user_id = %d, want %d", claims.UserID, user.ID)
	}
	if claims.Role != model.RoleEditor {
		t.Errorf("JWT role = %q, want editor", claims.Role)
	}
	if claims.ExpiresAt == nil {
		t.Fatal("JWT has no exp claim")
	}
	expiresIn := claims.ExpiresAt.Time.Sub(beforeLogin)
	if expiresIn < 24*time.Hour-time.Minute || expiresIn > 24*time.Hour+time.Minute {
		t.Errorf("JWT exp is %s after login, want about 24 hours", expiresIn)
	}
}

func TestAuth_LoginFailuresHaveIdenticalBodies(t *testing.T) {
	env := newAuthTestEnv(t)
	env.createUser(t, authTestEmail, authTestPassword, model.RoleViewer)

	wrongPassword := env.do(http.MethodPost, "/api/v1/auth/login",
		`{"email":"reader@example.com","password":"incorrect-password"}`, "")
	unknownEmail := env.do(http.MethodPost, "/api/v1/auth/login",
		`{"email":"missing@example.com","password":"incorrect-password"}`, "")
	if wrongPassword.Code != http.StatusUnauthorized || unknownEmail.Code != http.StatusUnauthorized {
		t.Fatalf("login statuses = (%d, %d), want both 401", wrongPassword.Code, unknownEmail.Code)
	}
	if string(wrongPassword.Body) != string(unknownEmail.Body) {
		t.Fatalf("login failure bodies differ: %q vs %q", wrongPassword.Body, unknownEmail.Body)
	}
	assertAuthStatusAndCode(t, wrongPassword, http.StatusUnauthorized, "unauthorized")
	if !strings.Contains(string(wrongPassword.Body), `"message":"invalid credentials"`) {
		t.Errorf("login failure message is not generic: %s", wrongPassword.Body)
	}
}

func TestAuth_InvalidRegisterInputReturns422(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "bad JSON", body: `{"email":`},
		{name: "missing fields", body: `{"email":"reader@example.com"}`},
		{name: "short password", body: `{"email":"reader@example.com","password":"short","name":"Reader"}`},
		{name: "password over 72 bytes", body: `{"email":"reader@example.com","password":"` + strings.Repeat("x", 73) + `","name":"Reader"}`},
		{name: "invalid email", body: `{"email":"not-an-email","password":"correct-horse-battery","name":"Reader"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := newAuthTestEnv(t).do(http.MethodPost, "/api/v1/auth/register", test.body, "")
			assertAuthStatusAndCode(t, response, http.StatusUnprocessableEntity, "validation_error")
		})
	}
}

func TestAuth_ProtectedMeValidatesBearerToken(t *testing.T) {
	tests := []struct {
		name          string
		authorization func(*testing.T, *authTestEnv) string
		wantStatus    int
	}{
		{name: "missing authorization", wantStatus: http.StatusUnauthorized},
		{name: "basic scheme", authorization: func(*testing.T, *authTestEnv) string { return "Basic abc123" }, wantStatus: http.StatusUnauthorized},
		{name: "garbage token", authorization: func(*testing.T, *authTestEnv) string { return "Bearer not-a-jwt" }, wantStatus: http.StatusUnauthorized},
		{name: "expired token", authorization: func(t *testing.T, env *authTestEnv) string {
			return "Bearer " + env.signTokenWithExpiration(t, time.Now().Add(-time.Minute))
		}, wantStatus: http.StatusUnauthorized},
		{name: "wrong signing secret", authorization: func(t *testing.T, _ *authTestEnv) string {
			return "Bearer " + signAuthTestToken(t, jwt.SigningMethodHS256, []byte("another-valid-test-secret-longer-than-32-bytes"))
		}, wantStatus: http.StatusUnauthorized},
		{name: "none algorithm", authorization: func(t *testing.T, _ *authTestEnv) string {
			return "Bearer " + signAuthTestToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType)
		}, wantStatus: http.StatusUnauthorized},
		{name: "tampered payload", authorization: func(t *testing.T, env *authTestEnv) string {
			valid := env.login(t, authTestEmail, authTestPassword)
			parts := strings.Split(valid, ".")
			var payload map[string]any
			decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatalf("decode JWT payload: %v", err)
			}
			if err := json.Unmarshal(decoded, &payload); err != nil {
				t.Fatalf("unmarshal JWT payload: %v", err)
			}
			payload["role"] = model.RoleAdmin
			tampered, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal tampered JWT payload: %v", err)
			}
			parts[1] = base64.RawURLEncoding.EncodeToString(tampered)
			return "Bearer " + strings.Join(parts, ".")
		}, wantStatus: http.StatusUnauthorized},
		{name: "valid token", authorization: func(t *testing.T, env *authTestEnv) string {
			return "Bearer " + env.login(t, authTestEmail, authTestPassword)
		}, wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newAuthTestEnv(t)
			user := env.createUser(t, authTestEmail, authTestPassword, model.RoleViewer)
			authorization := ""
			if test.authorization != nil {
				authorization = test.authorization(t, env)
			}
			response := env.doWithAuthorization(http.MethodGet, "/api/v1/me", "", authorization)
			if response.Code != test.wantStatus {
				t.Fatalf("GET /me status = %d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
			if test.wantStatus == http.StatusUnauthorized {
				assertAuthStatusAndCode(t, response, http.StatusUnauthorized, "unauthorized")
				return
			}
			var result map[string]any
			if err := json.Unmarshal(response.Body, &result); err != nil {
				t.Fatalf("decode /me response: %v (body: %s)", err, response.Body)
			}
			if _, exists := result["password_hash"]; exists {
				t.Errorf("/me response contains password_hash: %s", response.Body)
			}
			if result["id"] != float64(user.ID) {
				t.Errorf("/me id = %v, want %d", result["id"], user.ID)
			}
		})
	}
}

func signAuthTestToken(t *testing.T, method jwt.SigningMethod, secret any) string {
	t.Helper()

	token := jwt.NewWithClaims(method, auth.Claims{
		UserID: 1,
		Role:   model.RoleViewer,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

func assertAuthStatusAndCode(t *testing.T, response authTestResponse, status int, code string) {
	t.Helper()

	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body)
	}
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatalf("decode error response: %v (body: %s)", err, response.Body)
	}
	if result.Error.Code != code {
		t.Errorf("error code = %q, want %q: %s", result.Error.Code, code, response.Body)
	}
	if status == http.StatusUnauthorized && response.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", response.Header.Get("WWW-Authenticate"))
	}
}
