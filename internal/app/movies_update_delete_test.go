package app_test

import (
	"net/http"
	"testing"
)

// Movie update, delete, ids and timestamps.

func TestStage3_UpdateReplacesFieldsAndKeepsID(t *testing.T) {
	e := newEnv(t)
	created := e.create(e.valid())
	id := idOf(t, created)

	upd := with(with(e.payload("new", "cat-new"), "release_year", 1999), "synopsis", "updated")
	r := e.doJSON(http.MethodPut, e.item(id), upd)

	e.expect(r, http.StatusOK)
	got := r.obj(t)
	if gotID := idOf(t, got); gotID != id {
		t.Errorf("PUT must return the stored resource with the same id %d, got %d", id, gotID)
	}
	for _, f := range movieRequiredFields {
		if got[f] != upd[f] {
			t.Errorf("field %q: expected %v, got %v", f, upd[f], got[f])
		}
	}
	if got["release_year"] != float64(1999) {
		t.Errorf("field %q: expected 1999, got %v", "release_year", got["release_year"])
	}
	if got["synopsis"] != "updated" {
		t.Errorf("field %q: expected %q, got %v", "synopsis", "updated", got["synopsis"])
	}
	if got["created_at"] != created["created_at"] {
		t.Errorf("created_at must not change on update: was %v, now %v", created["created_at"], got["created_at"])
	}
	if parseTime(t, got, "updated_at").Before(parseTime(t, created, "updated_at")) {
		t.Errorf("updated_at must not go backwards")
	}

	// the change must be persisted
	again := e.do(http.MethodGet, e.item(id), "").obj(t)
	if again["title"] != upd["title"] {
		t.Errorf("GET after PUT returned old data: %v", again)
	}
}

func TestStage3_UpdateWithoutOptionalFieldsClearsThem(t *testing.T) {
	e := newEnv(t)
	id := idOf(t, e.create(with(with(e.valid(), "release_year", 2015), "synopsis", "text")))

	r := e.doJSON(http.MethodPut, e.item(id), e.payload("other", "cat-other"))

	e.expect(r, http.StatusOK)
	got := r.obj(t)
	for _, f := range []string{"release_year", "synopsis"} {
		if val, present := got[f]; !present || val != nil {
			t.Errorf("PUT replaces the whole resource, so omitted optional field %q must become null, got %v", f, val)
		}
	}
}

func TestStage3_DeleteReturns204AndRemovesResource(t *testing.T) {
	e := newEnv(t)
	id := idOf(t, e.create(e.valid()))

	r := e.do(http.MethodDelete, e.item(id), "")

	e.expect(r, http.StatusNoContent)
	if len(r.Body) != 0 {
		t.Errorf("a 204 response must have an empty body, got %q", r.Body)
	}
	e.wantError(e.do(http.MethodGet, e.item(id), ""), http.StatusNotFound, "not_found")
	e.wantError(e.do(http.MethodDelete, e.item(id), ""), http.StatusNotFound, "not_found")
}

func TestStage3_IDsStartAtOneAndAreNotReused(t *testing.T) {
	e := newEnv(t)

	first := idOf(t, e.create(e.payload("a", "x")))
	second := idOf(t, e.create(e.payload("b", "x")))
	third := idOf(t, e.create(e.payload("c", "x")))

	if first != 1 || second != 2 || third != 3 {
		t.Fatalf("ids must be sequential starting at 1, got %d, %d, %d", first, second, third)
	}

	e.expect(e.do(http.MethodDelete, e.item(third), ""), http.StatusNoContent)

	fourth := idOf(t, e.create(e.payload("d", "x")))
	if fourth != 4 {
		t.Errorf("the id of a deleted resource must not be reused: expected 4, got %d", fourth)
	}
}

func TestStage3_TimestampsAreSetOnCreate(t *testing.T) {
	e := newEnv(t)

	obj := e.create(e.valid())

	for _, key := range []string{"created_at", "updated_at"} {
		if ts := parseTime(t, obj, key); ts.Year() < 2000 {
			t.Errorf("%s looks like an unset timestamp: %v", key, ts)
		}
	}
}
