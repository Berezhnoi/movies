package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// CORS and concurrency. Run with `go test -race ./...` to catch data races.

func TestStage5_CORSPreflightReturns204(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest(http.MethodOptions, e.base(), nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	r, p := e.tryReq(req)

	if p != nil {
		t.Fatalf("OPTIONS %s: handler panicked: %v", e.base(), p)
	}
	if r.Code != http.StatusNoContent {
		t.Errorf("a preflight request must return 204, got %d", r.Code)
	}
	if r.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Errorf("the preflight response is missing Access-Control-Allow-Origin")
	}
	if !strings.Contains(strings.ToUpper(r.Header.Get("Access-Control-Allow-Methods")), "POST") {
		t.Errorf("Access-Control-Allow-Methods must list POST, got %q", r.Header.Get("Access-Control-Allow-Methods"))
	}
}

func TestStage5_CORSHeaderOnRegularResponses(t *testing.T) {
	e := newEnv(t)

	for _, path := range []string{"/health", e.base()} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Origin", "http://example.com")

		r, p := e.tryReq(req)

		if p != nil {
			t.Fatalf("GET %s: handler panicked: %v", path, p)
		}
		if r.Header.Get("Access-Control-Allow-Origin") == "" {
			t.Errorf("GET %s: the response is missing Access-Control-Allow-Origin", path)
		}
	}
}

func TestStage5_ConcurrentCreatesGetUniqueIDs(t *testing.T) {
	e := newEnv(t)
	const n = 50

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int]bool{}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			b, _ := json.Marshal(e.payload(fmt.Sprint("c", i), "x"))
			r, p := e.try(http.MethodPost, e.base(), string(b))
			if p != nil {
				t.Errorf("handler panicked: %v", p)
				return
			}
			if r.Code != http.StatusCreated {
				t.Errorf("POST: expected 201, got %d (body: %s)", r.Code, r.Body)
				return
			}
			var obj map[string]any
			if err := json.Unmarshal(r.Body, &obj); err != nil {
				t.Errorf("response is not a JSON object: %v", err)
				return
			}
			id, _ := obj["id"].(float64)

			mu.Lock()
			defer mu.Unlock()
			if seen[int(id)] {
				t.Errorf("duplicate id %d", int(id))
			}
			seen[int(id)] = true
		}(i)
	}
	wg.Wait()

	if len(seen) != n {
		t.Errorf("expected %d distinct ids, got %d", n, len(seen))
	}
}

func TestStage5_ConcurrentReadsAndWritesAreRaceFree(t *testing.T) {
	e := newEnv(t)
	id := idOf(t, e.create(e.valid()))
	body, err := json.Marshal(e.payload("w", "x"))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				r, p := e.try(http.MethodPut, e.item(id), string(body))
				if p != nil || r.Code != http.StatusOK {
					t.Errorf("PUT: expected 200, got %d (panic: %v)", r.Code, p)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				for _, path := range []string{e.item(id), e.base()} {
					r, p := e.try(http.MethodGet, path, "")
					if p != nil || r.Code != http.StatusOK {
						t.Errorf("GET %s: expected 200, got %d (panic: %v)", path, r.Code, p)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
