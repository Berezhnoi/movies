package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"homework/internal/app"
)

// env bundles a fresh router with the student's variant.
type env struct {
	t *testing.T
	h http.Handler
	v Variant
}

func newEnv(t *testing.T) *env {
	t.Helper()

	v := moviesVariant
	h := app.NewRouter()
	if h == nil {
		t.Fatal("app.NewRouter() returned nil")
	}
	return &env{t: t, h: h, v: v}
}

type resp struct {
	Code   int
	Header http.Header
	Body   []byte
}

// try serves a request and reports a panic as a value instead of crashing the whole test binary.
// It never calls t.Fatal, so it is safe to use from goroutines.
func (e *env) try(method, path, body string) (resp, any) {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	return e.tryReq(req)
}

func (e *env) tryReq(req *http.Request) (r resp, panicked any) {
	defer func() {
		if rec := recover(); rec != nil {
			panicked = rec
		}
	}()

	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return resp{Code: w.Code, Header: w.Header(), Body: w.Body.Bytes()}, nil
}

// do sends a request with a raw body and fails the test if the handler panics.
func (e *env) do(method, path, body string) resp {
	e.t.Helper()

	r, p := e.try(method, path, body)
	if p != nil {
		e.t.Fatalf("%s %s: handler panicked: %v", method, path, p)
	}
	return r
}

// doJSON sends v encoded as JSON.
func (e *env) doJSON(method, path string, v any) resp {
	e.t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.do(method, path, string(b))
}

func (e *env) base() string {
	return "/api/v1/" + e.v.Resource
}

func (e *env) item(id any) string {
	return fmt.Sprintf("%s/%v", e.base(), id)
}

// payload returns a valid request body. Every required field gets a value derived from tag;
// the filter field (the fourth required field) is set to filter.
func (e *env) payload(tag, filter string) map[string]any {
	m := map[string]any{}
	for _, f := range e.v.Required {
		m[f] = f + "-" + tag
	}
	m[e.v.Filter()] = filter
	return m
}

func (e *env) valid() map[string]any {
	return e.payload("x", "cat-x")
}

// create posts body, requires 201 and returns the decoded response object.
func (e *env) create(body map[string]any) map[string]any {
	e.t.Helper()

	r := e.doJSON(http.MethodPost, e.base(), body)
	if r.Code != http.StatusCreated {
		e.t.Fatalf("POST %s: expected 201, got %d (body: %s)", e.base(), r.Code, r.Body)
	}
	return r.obj(e.t)
}

func (e *env) expect(r resp, status int) {
	e.t.Helper()

	if r.Code != status {
		e.t.Fatalf("expected status %d, got %d (body: %s)", status, r.Code, r.Body)
	}
}

// listIDs requests the collection with the given query string and returns the ids in response order.
func (e *env) listIDs(query string) []int {
	e.t.Helper()

	r := e.do(http.MethodGet, e.base()+query, "")
	if r.Code != http.StatusOK {
		e.t.Fatalf("GET %s%s: expected 200, got %d (body: %s)", e.base(), query, r.Code, r.Body)
	}
	return idsOf(e.t, r.list(e.t))
}

// wantError checks the status code and the error body {"error":{"code":...,"message":...}}.
func (e *env) wantError(r resp, status int, code string) {
	e.t.Helper()

	if r.Code != status {
		e.t.Errorf("expected status %d, got %d (body: %s)", status, r.Code, r.Body)
		return
	}

	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		e.t.Errorf("error response must be JSON: %v (body: %s)", err, r.Body)
		return
	}
	inner, ok := m["error"].(map[string]any)
	if !ok {
		e.t.Errorf(`error response must look like {"error":{"code":"...","message":"..."}}, got: %s`, r.Body)
		return
	}
	if got, _ := inner["code"].(string); got != code {
		e.t.Errorf("expected error.code %q, got %q", code, got)
	}
	if msg, _ := inner["message"].(string); msg == "" {
		e.t.Errorf("error.message must be a non-empty string (body: %s)", r.Body)
	}
}

func (r resp) obj(t *testing.T) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("response body is not a JSON object: %v (body: %s)", err, r.Body)
	}
	return m
}

func (r resp) list(t *testing.T) []map[string]any {
	t.Helper()

	var l []map[string]any
	if err := json.Unmarshal(r.Body, &l); err != nil {
		t.Fatalf("response body is not a JSON array: %v (body: %s)", err, r.Body)
	}
	return l
}

func (r resp) text() string {
	return strings.TrimSpace(string(r.Body))
}

func isJSONContent(r resp) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

func idOf(t *testing.T, obj map[string]any) int {
	t.Helper()

	f, ok := obj["id"].(float64)
	if !ok || f < 1 || f != float64(int(f)) {
		t.Fatalf(`response must contain a positive integer "id", got %v`, obj["id"])
	}
	return int(f)
}

func idsOf(t *testing.T, items []map[string]any) []int {
	t.Helper()

	out := make([]int, 0, len(items))
	for _, it := range items {
		out = append(out, idOf(t, it))
	}
	return out
}

func parseTime(t *testing.T, obj map[string]any, key string) time.Time {
	t.Helper()

	s, ok := obj[key].(string)
	if !ok || s == "" {
		t.Fatalf("%q must be a non-empty string, got %v", key, obj[key])
	}
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%q must be an RFC 3339 timestamp, got %q", key, s)
	}
	return ts
}

func with(m map[string]any, key string, val any) map[string]any {
	c := make(map[string]any, len(m)+1)
	for k, v := range m {
		c[k] = v
	}
	c[key] = val
	return c
}

func without(m map[string]any, key string) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		if k != key {
			c[k] = v
		}
	}
	return c
}

func seq(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
