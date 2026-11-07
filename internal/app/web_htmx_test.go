package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"homework/internal/app"
	"homework/internal/model"
	"homework/internal/repository"
)

func TestHTMXCatalogSearchRendersOnlyEscapedMovieRows(t *testing.T) {
	movies := repository.NewInMemoryMovieRepositoryAdapter()
	title := `<script>alert(1)</script>`
	if _, err := movies.Create(context.Background(), model.Movie{
		Title: title, Director: "Director", Country: "Country", Genre: "Drama",
	}); err != nil {
		t.Fatalf("create movie: %v", err)
	}
	router := app.NewRouterWithDeps(app.Deps{Movies: movies})
	values := url.Values{"q": {title}, "genre": {"Drama"}}
	request := httptest.NewRequest(http.MethodGet, "/web/movies?"+values.Encode(), nil)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("HTMX GET /web/movies = %d: %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	wantPushURL := "/catalog?" + values.Encode()
	if got := response.Header().Get("HX-Push-Url"); got != wantPushURL {
		t.Errorf("HX-Push-Url = %q, want %q", got, wantPushURL)
	}
	body := response.Body.String()
	if !strings.Contains(body, "<tr") || strings.Contains(body, "<html") ||
		strings.Contains(body, "<nav") || strings.Contains(body, "<!DOCTYPE") {
		t.Fatalf("HTMX response is not only movie rows: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") || strings.Contains(body, title) {
		t.Fatalf("hostile movie title was not safely escaped: %s", body)
	}
}

func TestWebMoviesWithoutHTMXRedirectsAndRejectsInvalidPagination(t *testing.T) {
	router := app.NewRouterWithDeps(app.Deps{})
	values := url.Values{"q": {"film title"}, "genre": {"Drama"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/web/movies?"+values.Encode(), nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog?"+values.Encode() {
		t.Fatalf("GET /web/movies = %d, Location %q; want redirect to /catalog with query", response.Code, response.Header().Get("Location"))
	}

	request := httptest.NewRequest(http.MethodGet, "/web/movies?page=bad", nil)
	request.Header.Set("HX-Request", "true")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("invalid HTMX pagination = %d (%q), want HTML 400", response.Code, response.Header().Get("Content-Type"))
	}
	if strings.Contains(response.Body.String(), "bad") || strings.Contains(response.Body.String(), "<!DOCTYPE") {
		t.Fatalf("invalid pagination response should be a small escaped message: %s", response.Body)
	}
}

func TestHTMXMovieDeleteAuthorizationAndValidation(t *testing.T) {
	movies := repository.NewInMemoryMovieRepositoryAdapter()
	movie, err := movies.Create(context.Background(), model.Movie{
		Title: "Delete Me", Director: "Director", Country: "Country", Genre: "Drama",
	})
	if err != nil {
		t.Fatalf("create movie: %v", err)
	}
	_, users, tokens := webAuthRouter(t)
	router := app.NewRouterWithDeps(app.Deps{Users: users, Tokens: tokens, Movies: movies})
	csrfCookie, csrfToken := getCSRF(t, router, "/catalog")
	adminToken := mustRoleToken(t, users, tokens, model.RoleAdmin)
	editorToken := mustRoleToken(t, users, tokens, model.RoleEditor)
	viewerToken := mustRoleToken(t, users, tokens, model.RoleViewer)

	deleteRequest := func(id, accessToken, submittedCSRF string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodDelete, "/web/movies/"+id, nil)
		request.Header.Set("HX-Request", "true")
		request.Header.Set("Origin", "http://example.com")
		if submittedCSRF != "" {
			request.Header.Set("X-CSRF-Token", submittedCSRF)
		}
		request.AddCookie(csrfCookie)
		if accessToken != "" {
			request.AddCookie(&http.Cookie{Name: "access_token", Value: accessToken})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	deleted := deleteRequest(fmt.Sprint(movie.ID), adminToken, csrfToken)
	if deleted.Code != http.StatusOK || deleted.Body.Len() != 0 {
		t.Fatalf("admin DELETE = %d with body %q, want empty 200", deleted.Code, deleted.Body.String())
	}
	if trigger := deleted.Header().Get("HX-Trigger"); trigger != "movieDeleted" {
		t.Fatalf("admin DELETE HX-Trigger = %q, want movieDeleted", trigger)
	}
	apiList := httptest.NewRecorder()
	router.ServeHTTP(apiList, httptest.NewRequest(http.MethodGet, "/api/v1/movies", nil))
	var listed []model.Movie
	if err := json.Unmarshal(apiList.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode API movie list: %v (%s)", err, apiList.Body)
	}
	for _, listedMovie := range listed {
		if listedMovie.ID == movie.ID {
			t.Fatalf("deleted movie %d remains in API list", movie.ID)
		}
	}

	for _, test := range []struct {
		name         string
		id           string
		token        string
		csrf         string
		wantStatus   int
		wantRedirect string
	}{
		{name: "editor forbidden", id: fmt.Sprint(movie.ID), token: editorToken, csrf: csrfToken, wantStatus: http.StatusForbidden},
		{name: "viewer forbidden", id: fmt.Sprint(movie.ID), token: viewerToken, csrf: csrfToken, wantStatus: http.StatusForbidden},
		{name: "anonymous redirects", id: fmt.Sprint(movie.ID), csrf: csrfToken, wantStatus: http.StatusUnauthorized, wantRedirect: "/login"},
		{name: "missing csrf", id: fmt.Sprint(movie.ID), token: adminToken, wantStatus: http.StatusForbidden},
		{name: "unknown id", id: "4294967295", token: adminToken, csrf: csrfToken, wantStatus: http.StatusNotFound},
		{name: "invalid id", id: "abc", token: adminToken, csrf: csrfToken, wantStatus: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := deleteRequest(test.id, test.token, test.csrf)
			if response.Code != test.wantStatus {
				t.Fatalf("DELETE /web/movies/%s = %d, want %d: %s", test.id, response.Code, test.wantStatus, response.Body)
			}
			if got := response.Header().Get("HX-Redirect"); got != test.wantRedirect {
				t.Errorf("HX-Redirect = %q, want %q", got, test.wantRedirect)
			}
		})
	}
}
