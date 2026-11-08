package app_test

import (
	"fmt"
	"net/http"
	"testing"
)

// Movie list, sorting, pagination and filter.

func TestStage4_EmptyListIsJSONArray(t *testing.T) {
	e := newEnv(t)

	r := e.do(http.MethodGet, e.base(), "")

	e.expect(r, http.StatusOK)
	if r.text() != "[]" {
		t.Errorf("an empty collection must be returned as [] (not null), got %s", r.Body)
	}
}

func TestStage4_ListIsSortedByID(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 25; i++ {
		e.create(e.payload(fmt.Sprint("n", i), "x"))
	}

	// ask several times: an unsorted map iteration gives a different order every time
	for attempt := 0; attempt < 3; attempt++ {
		got := e.listIDs("?limit=100")
		if !equalInts(got, seq(1, 25)) {
			t.Fatalf("the list must be sorted by id ascending, got %v", got)
		}
	}
}

func TestStage4_DefaultLimitIs10(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 12; i++ {
		e.create(e.payload(fmt.Sprint("n", i), "x"))
	}

	got := e.listIDs("")

	if !equalInts(got, seq(1, 10)) {
		t.Errorf("without parameters the first 10 resources are expected, got %v", got)
	}
}

func TestStage4_Pagination(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 5; i++ {
		e.create(e.payload(fmt.Sprint("n", i), "x"))
	}

	cases := []struct {
		query string
		want  []int
	}{
		{"?page=1&limit=2", []int{1, 2}},
		{"?page=2&limit=2", []int{3, 4}},
		{"?page=3&limit=2", []int{5}},
		{"?page=4&limit=2", []int{}},
		{"?limit=3", []int{1, 2, 3}},
		{"?page=2&limit=3", []int{4, 5}},
	}
	for _, tc := range cases {
		got := e.listIDs(tc.query)
		if !equalInts(got, tc.want) {
			t.Errorf("GET %s%s: expected ids %v, got %v", e.base(), tc.query, tc.want, got)
		}
	}
}

func TestStage4_PagesCoverAllResourcesExactlyOnce(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 30; i++ {
		e.create(e.payload(fmt.Sprint("n", i), "x"))
	}

	var all []int
	for page := 1; page <= 3; page++ {
		all = append(all, e.listIDs(fmt.Sprintf("?page=%d&limit=10", page))...)
	}

	if !equalInts(all, seq(1, 30)) {
		t.Errorf("three pages of 10 must contain ids 1..30 exactly once and in order, got %v", all)
	}
}

func TestStage4_FilterByFilterField(t *testing.T) {
	e := newEnv(t)
	filter := "genre"
	for i := 1; i <= 3; i++ {
		e.create(e.payload(fmt.Sprint("a", i), "a"))
	}
	for i := 1; i <= 2; i++ {
		e.create(e.payload(fmt.Sprint("b", i), "b"))
	}

	cases := []struct {
		query string
		want  []int
	}{
		{"?" + filter + "=b", []int{4, 5}},
		{"?" + filter + "=nope", []int{}},
		{"?" + filter + "=", []int{1, 2, 3, 4, 5}},
		{"?" + filter + "=a&limit=2&page=2", []int{3}},
	}
	for _, tc := range cases {
		got := e.listIDs(tc.query)
		if !equalInts(got, tc.want) {
			t.Errorf("GET %s%s: expected ids %v, got %v", e.base(), tc.query, tc.want, got)
		}
	}
}

func TestStage4_InvalidPaginationReturns400(t *testing.T) {
	queries := []string{"?page=abc", "?page=0", "?page=-1", "?limit=abc", "?limit=0", "?limit=-5"}

	for _, q := range queries {
		q := q
		t.Run(q, func(t *testing.T) {
			e := newEnv(t)

			r := e.do(http.MethodGet, e.base()+q, "")

			e.wantError(r, http.StatusBadRequest, "invalid_pagination")
		})
	}
}
