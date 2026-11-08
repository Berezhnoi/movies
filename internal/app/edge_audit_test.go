package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"homework/internal/middleware"
)

func TestEdgeAuditPostAndPutBodies(t *testing.T) {
	valid := `{"title":"Film","director":"Director","country":"Country","genre":"Drama"}`
	unicodeTitle := `{"title":"映画 🎬","director":"Director","country":"Country","genre":"Drama"}`
	longTitle := strings.Repeat("L", 10000)
	longTitleJSON, err := json.Marshal(map[string]any{
		"title": longTitle, "director": "Director", "country": "Country", "genre": "Drama",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		body      string
		wantOK    bool
		wantTitle string
		wantNulls bool
	}{
		{name: "empty body", body: ""},
		{name: "array", body: `[]`},
		{name: "bare string", body: `"str"`},
		{name: "null object", body: `null`},
		{name: "broken object", body: `{`},
		{name: "empty object", body: `{}`},
		{name: "whitespace title", body: `{"title":"   ","director":"Director","country":"Country","genre":"Drama"}`},
		{name: "null title", body: `{"title":null,"director":"Director","country":"Country","genre":"Drama"}`},
		{name: "numeric title", body: `{"title":123,"director":"Director","country":"Country","genre":"Drama"}`},
		{name: "missing genre", body: `{"title":"Film","director":"Director","country":"Country"}`},
		{name: "fractional release year", body: `{"title":"Film","director":"Director","country":"Country","genre":"Drama","release_year":1.5}`},
		{name: "string release year", body: `{"title":"Film","director":"Director","country":"Country","genre":"Drama","release_year":"2015"}`},
		{name: "null release year", body: `{"title":"Film","director":"Director","country":"Country","genre":"Drama","release_year":null}`, wantOK: true, wantTitle: "Film", wantNulls: true},
		{name: "numeric synopsis", body: `{"title":"Film","director":"Director","country":"Country","genre":"Drama","synopsis":5}`},
		{name: "null synopsis", body: `{"title":"Film","director":"Director","country":"Country","genre":"Drama","synopsis":null}`, wantOK: true, wantTitle: "Film", wantNulls: true},
		{name: "overflow exponent release year", body: `{"title":"Film","director":"Director","country":"Country","genre":"Drama","release_year":1e400}`},
		{name: "overflow decimal release year", body: `{"title":"Film","director":"Director","country":"Country","genre":"Drama","release_year":99999999999999999999}`},
		{name: "unicode title", body: unicodeTitle, wantOK: true, wantTitle: "映画 🎬"},
		{name: "very long title", body: string(longTitleJSON), wantOK: true, wantTitle: longTitle},
		{name: "valid control body", body: valid, wantOK: true, wantTitle: "Film"},
	}

	for _, test := range tests {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				e := newEnv(t)
				path := e.base()
				wantStatus := http.StatusCreated
				if method == http.MethodPut {
					id := idOf(t, e.create(e.valid()))
					path = e.item(id)
					wantStatus = http.StatusOK
				}
				response := e.do(method, path, test.body)
				if test.wantOK {
					e.expect(response, wantStatus)
					movie := response.obj(t)
					if movie["title"] != test.wantTitle {
						t.Errorf("title = %v, want %q", movie["title"], test.wantTitle)
					}
					if test.wantNulls && (movie["release_year"] != nil || movie["synopsis"] != nil) {
						t.Errorf("null optionals were not preserved: release_year=%v synopsis=%v", movie["release_year"], movie["synopsis"])
					}
					return
				}
				e.wantError(response, http.StatusUnprocessableEntity, "validation_error")
			})
		}
	}
}

func TestEdgeAuditIDsAndPutCheckOrder(t *testing.T) {
	e := newEnv(t)
	created := e.create(e.valid())
	id := idOf(t, created)
	tests := []struct {
		name   string
		target string
		status int
		code   string
	}{
		{name: "zero is valid but absent", target: "0", status: http.StatusNotFound, code: "not_found"},
		{name: "negative", target: "-1", status: http.StatusBadRequest, code: "invalid_id"},
		{name: "alphabetic", target: "abc", status: http.StatusBadRequest, code: "invalid_id"},
		{name: "fractional", target: "1.5", status: http.StatusBadRequest, code: "invalid_id"},
		{name: "uint32 maximum is valid but absent", target: "4294967295", status: http.StatusNotFound, code: "not_found"},
		{name: "uint32 overflow", target: "4294967296", status: http.StatusBadRequest, code: "invalid_id"},
		{name: "huge overflow", target: "99999999999999999999", status: http.StatusBadRequest, code: "invalid_id"},
		{name: "encoded space", target: "%20", status: http.StatusBadRequest, code: "invalid_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := e.do(http.MethodGet, e.base()+"/"+test.target, "")
			e.wantError(response, test.status, test.code)
		})
	}

	plusResponse := e.do(http.MethodGet, e.base()+"/+"+fmt.Sprint(id), "")
	e.wantError(plusResponse, http.StatusBadRequest, "invalid_id")

	e.wantError(e.do(http.MethodPut, e.item("abc"), e.validJSON()), http.StatusBadRequest, "invalid_id")
	e.wantError(e.do(http.MethodPut, e.item(999), `[]`), http.StatusUnprocessableEntity, "validation_error")
	e.wantError(e.do(http.MethodPut, e.item(999), e.validJSON()), http.StatusNotFound, "not_found")
}

func TestEdgeAuditPutPersistenceAndDeleteSemantics(t *testing.T) {
	e := newEnv(t)
	originalBody := with(with(e.valid(), "release_year", 2015), "synopsis", "original")
	created := e.create(originalBody)
	id := idOf(t, created)
	oldUpdatedAt := created["updated_at"]

	invalidUpdate := e.doJSON(http.MethodPut, e.item(id), with(e.valid(), "title", "   "))
	e.wantError(invalidUpdate, http.StatusUnprocessableEntity, "validation_error")
	afterRejectedUpdate := e.do(http.MethodGet, e.item(id), "").obj(t)
	if !reflect.DeepEqual(created, afterRejectedUpdate) {
		t.Errorf("rejected PUT changed stored movie: before=%v after=%v", created, afterRejectedUpdate)
	}

	updated := e.doJSON(http.MethodPut, e.item(id), e.payload("replaced", "Drama"))
	e.expect(updated, http.StatusOK)
	updatedMovie := updated.obj(t)
	if idOf(t, updatedMovie) != id {
		t.Errorf("PUT response omitted or changed the id")
	}
	if updatedMovie["created_at"] != created["created_at"] {
		t.Errorf("created_at changed: was %v, now %v", created["created_at"], updatedMovie["created_at"])
	}
	if updatedMovie["updated_at"] == oldUpdatedAt {
		t.Errorf("updated_at did not change: %v", oldUpdatedAt)
	}
	if updatedMovie["release_year"] != nil || updatedMovie["synopsis"] != nil {
		t.Errorf("omitted optionals should be null: %v", updatedMovie)
	}

	secondID := idOf(t, e.create(e.valid()))
	deleted := e.do(http.MethodDelete, e.item(id), "")
	e.expect(deleted, http.StatusNoContent)
	if len(deleted.Body) != 0 {
		t.Errorf("204 body was not empty: %q", deleted.Body)
	}
	if deleted.Header.Get("Content-Length") != "0" {
		t.Errorf("204 Content-Length = %q, want 0", deleted.Header.Get("Content-Length"))
	}
	e.wantError(e.do(http.MethodDelete, e.item(id), ""), http.StatusNotFound, "not_found")
	newID := idOf(t, e.create(e.valid()))
	if newID <= secondID {
		t.Errorf("created id was reused after delete: got %d after %d", newID, secondID)
	}
}

func TestEdgeAuditListOrderingDefaultsLimitsAndOverflow(t *testing.T) {
	e := newEnv(t)
	if response := e.do(http.MethodGet, e.base(), ""); response.text() != "[]" {
		t.Errorf("empty list body = %q, want exactly []", response.Body)
	}
	for index := 1; index <= 12; index++ {
		e.create(e.payload(fmt.Sprintf("default-%d", index), "Drama"))
	}
	if got := e.listIDs(""); !equalInts(got, seq(1, 10)) {
		t.Errorf("default page/limit ids = %v, want 1..10", got)
	}
	if got := e.listIDs("?page=9223372036854775807"); len(got) != 0 {
		t.Errorf("huge page returned ids %v, want empty", got)
	}
	if got := e.listIDs("?page=999999999999999999999999999999999999999"); len(got) != 0 {
		t.Errorf("overflow page returned ids %v, want empty", got)
	}
	if got := e.listIDs("?limit=9223372036854775807"); !equalInts(got, seq(1, 12)) {
		t.Errorf("huge limit should clamp and return available ids, got %v", got)
	}
	if got := e.listIDs("?limit=999999999999999999999999999999999999999"); !equalInts(got, seq(1, 12)) {
		t.Errorf("overflow limit should clamp and return available ids, got %v", got)
	}

	for index := 13; index <= 105; index++ {
		e.create(e.payload(fmt.Sprintf("many-%d", index), "Drama"))
	}
	if got := e.listIDs("?limit=1000"); len(got) != 100 || got[0] != 1 || got[len(got)-1] != 100 {
		t.Errorf("limit=1000 returned %d items, ids %v..%v; want 100 items, ids 1..100", len(got), got[0], got[len(got)-1])
	}

	_ = e.do(http.MethodDelete, e.item(2), "")
	_ = e.doJSON(http.MethodPut, e.item(5), e.payload("moved", "Drama"))
	got := append(e.listIDs("?page=1&limit=100"), e.listIDs("?page=2&limit=100")...)
	if !equalInts(got, append(seq(1, 1), seq(3, 105)...)) {
		t.Errorf("list after delete/PUT not sorted or contains wrong ids: %v", got)
	}
}

func TestEdgeAuditPaginationValidationAndGenreFilters(t *testing.T) {
	e := newEnv(t)
	for _, query := range []string{
		"?page=", "?page=0", "?page=-1", "?page=abc", "?page=1.5",
		"?limit=", "?limit=0", "?limit=-1", "?limit=abc", "?limit=1.5",
	} {
		t.Run(query, func(t *testing.T) {
			e.wantError(e.do(http.MethodGet, e.base()+query, ""), http.StatusBadRequest, "invalid_pagination")
		})
	}

	e.create(e.payload("upper-a", "Drama"))
	e.create(e.payload("lower", "drama"))
	e.create(e.payload("upper-b", "Drama"))
	e.create(e.payload("spaces", "Science Fiction"))
	e.create(e.payload("unicode", "空想 科学"))
	if got := e.listIDs("?genre=Drama&page=2&limit=1"); !equalInts(got, []int{3}) {
		t.Errorf("filtered page ids = %v, want [3]", got)
	}
	if got := e.listIDs("?genre=drama"); !equalInts(got, []int{2}) {
		t.Errorf("case-sensitive filter ids = %v, want [2]", got)
	}
	if got := e.listIDs("?genre="); !equalInts(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("empty genre should disable filter, got %v", got)
	}
	for _, test := range []struct {
		genre string
		want  []int
	}{{genre: "Science Fiction", want: []int{4}}, {genre: "空想 科学", want: []int{5}}} {
		query := "?" + url.Values{"genre": []string{test.genre}}.Encode()
		if got := e.listIDs(query); !equalInts(got, test.want) {
			t.Errorf("encoded genre %q returned ids %v, want %v", test.genre, got, test.want)
		}
	}
}

func TestEdgeAuditStatusHeadersAndNoInternalLeak(t *testing.T) {
	t.Setenv("ALLOWED_ORIGIN", "https://audit.example")
	e := newEnv(t)
	created := e.create(e.valid())
	id := idOf(t, created)
	responses := []resp{
		e.do(http.MethodGet, "/health", ""),
		e.do(http.MethodPost, e.base(), e.validJSON()),
		e.do(http.MethodDelete, e.item(id), ""),
		e.do(http.MethodGet, e.item("abc"), ""),
		e.do(http.MethodGet, "/missing-route", ""),
		e.do(http.MethodPost, e.base(), `{}`),
	}
	for _, response := range responses {
		if got := response.Header.Get("Access-Control-Allow-Origin"); got != "https://audit.example" {
			t.Errorf("status %d allow-origin = %q", response.Code, got)
		}
		if response.Code != http.StatusNoContent && !isJSONContent(response) {
			t.Errorf("status %d Content-Type = %q, want application/json", response.Code, response.Header.Get("Content-Type"))
		}
		assertNoInternalLeak(t, response)
	}
	unknown := e.do(http.MethodGet, "/missing-route", "")
	e.wantError(unknown, http.StatusNotFound, "not_found")
	patch := e.do(http.MethodPatch, e.base(), "")
	if patch.Code < 400 || patch.Code >= 500 || !isJSONContent(patch) {
		t.Errorf("PATCH status/content-type = %d/%q, want JSON 4xx", patch.Code, patch.Header.Get("Content-Type"))
	}
	assertNoInternalLeak(t, patch)

	gin.SetMode(gin.ReleaseMode)
	panicRouter := gin.New()
	panicRouter.Use(middleware.CORS(), middleware.Recovery(), middleware.RequestLogger())
	panicRouter.GET("/panic", func(*gin.Context) { panic("private panic details") })
	panicRecorder := httptest.NewRecorder()
	panicRouter.ServeHTTP(panicRecorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	panicResponse := resp{Code: panicRecorder.Code, Header: panicRecorder.Header(), Body: panicRecorder.Body.Bytes()}
	if panicResponse.Code != http.StatusInternalServerError {
		t.Errorf("panic status = %d, want 500", panicResponse.Code)
	}
	if panicResponse.Header.Get("Access-Control-Allow-Origin") != "https://audit.example" {
		t.Errorf("panic response missing configured CORS header: %q", panicResponse.Header.Get("Access-Control-Allow-Origin"))
	}
	if !isJSONContent(panicResponse) {
		t.Errorf("panic Content-Type = %q, want application/json", panicResponse.Header.Get("Content-Type"))
	}
	assertNoInternalLeak(t, panicResponse)
}

func TestEdgeAuditOptionsAndRouterIsolation(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/unknown", e.base() + "/1"} {
		response, panicValue := e.try(http.MethodOptions, path, "")
		if panicValue != nil || response.Code != http.StatusNoContent || len(response.Body) != 0 {
			t.Errorf("OPTIONS %s: status=%d body=%q panic=%v", path, response.Code, response.Body, panicValue)
		}
		if !strings.Contains(response.Header.Get("Access-Control-Allow-Methods"), "POST") {
			t.Errorf("OPTIONS %s methods = %q", path, response.Header.Get("Access-Control-Allow-Methods"))
		}
	}

	first := newEnv(t)
	second := newEnv(t)
	first.create(first.valid())
	second.wantError(second.do(http.MethodGet, second.item(1), ""), http.StatusNotFound, "not_found")
}

func (e *env) validJSON() string {
	body, err := json.Marshal(e.valid())
	if err != nil {
		e.t.Fatal(err)
	}
	return string(body)
}

func assertNoInternalLeak(t *testing.T, response resp) {
	t.Helper()
	body := strings.ToLower(string(response.Body))
	for _, forbidden := range []string{"goroutine", ".go:", "panic"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("status %d response leaked %q: %s", response.Code, forbidden, response.Body)
		}
	}
}
