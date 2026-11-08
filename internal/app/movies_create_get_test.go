package app_test

import (
	"net/http"
	"testing"
)

// Health, 404, create and get a single movie.

func TestStage1_Health(t *testing.T) {
	e := newEnv(t)

	r := e.do(http.MethodGet, "/health", "")

	e.expect(r, http.StatusOK)
	if got, _ := r.obj(t)["status"].(string); got != "ok" {
		t.Errorf(`GET /health must return {"status":"ok"}, got %s`, r.Body)
	}
}

func TestStage1_UnknownRouteReturns404(t *testing.T) {
	e := newEnv(t)

	r := e.do(http.MethodGet, "/no-such-route", "")

	if r.Code != http.StatusNotFound {
		t.Errorf("expected 404 for an unknown route, got %d", r.Code)
	}
}

func TestStage1_CreateReturns201AndStoredObject(t *testing.T) {
	e := newEnv(t)
	body := e.valid()

	r := e.doJSON(http.MethodPost, e.base(), body)

	e.expect(r, http.StatusCreated)
	if !isJSONContent(r) {
		t.Errorf("Content-Type must start with application/json, got %q", r.Header.Get("Content-Type"))
	}
	obj := r.obj(t)
	if id := idOf(t, obj); id != 1 {
		t.Errorf("the first created resource must have id 1, got %d", id)
	}
	for _, f := range movieRequiredFields {
		if obj[f] != body[f] {
			t.Errorf("field %q: expected %v, got %v", f, body[f], obj[f])
		}
	}
	parseTime(t, obj, "created_at")
	parseTime(t, obj, "updated_at")
}

func TestStage1_OptionalFieldsAreAcceptedAndEchoed(t *testing.T) {
	e := newEnv(t)
	body := with(with(e.valid(), "release_year", 2015), "synopsis", "some text")

	obj := e.create(body)

	if obj["release_year"] != float64(2015) {
		t.Errorf("field %q: expected 2015, got %v", "release_year", obj["release_year"])
	}
	if obj["synopsis"] != "some text" {
		t.Errorf("field %q: expected %q, got %v", "synopsis", "some text", obj["synopsis"])
	}
}

func TestStage1_OptionalFieldsAreNullWhenOmitted(t *testing.T) {
	e := newEnv(t)

	obj := e.create(e.valid())

	for _, f := range []string{"release_year", "synopsis"} {
		val, present := obj[f]
		if !present {
			t.Errorf("field %q must be present in the response (as null) when it was not provided", f)
		} else if val != nil {
			t.Errorf("field %q must be null when it was not provided, got %v", f, val)
		}
	}
}

func TestStage1_GetByIDReturnsCreatedResource(t *testing.T) {
	e := newEnv(t)
	body := e.valid()
	id := idOf(t, e.create(body))

	r := e.do(http.MethodGet, e.item(id), "")

	e.expect(r, http.StatusOK)
	obj := r.obj(t)
	if got := idOf(t, obj); got != id {
		t.Errorf("expected id %d, got %d", id, got)
	}
	for _, f := range movieRequiredFields {
		if obj[f] != body[f] {
			t.Errorf("field %q: expected %v, got %v", f, body[f], obj[f])
		}
	}
}

func TestStage1_RoutersAreIsolated(t *testing.T) {
	a := newEnv(t)
	b := newEnv(t)
	a.create(a.valid())

	r := b.do(http.MethodGet, b.item(1), "")

	if r.Code != http.StatusNotFound {
		t.Errorf("every NewRouter() call must have its own empty storage, but a resource created through another router was visible (status %d)", r.Code)
	}
}
