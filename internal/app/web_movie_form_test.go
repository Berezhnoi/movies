package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"homework/internal/model"
)

func TestWebMovieFormCreateAuthorizationAndSuccess(t *testing.T) {
	router, users, tokens := webAuthRouter(t)
	csrfCookie, csrfToken := getCSRF(t, router, "/catalog")

	for _, role := range []string{model.RoleEditor, model.RoleAdmin} {
		t.Run(role+" can create", func(t *testing.T) {
			accessToken := mustRoleToken(t, users, tokens, role)
			page := movieFormRequest(router, http.MethodGet, "/movies/new", nil, csrfCookie, accessToken)
			if page.Code != http.StatusOK {
				t.Fatalf("GET /movies/new = %d: %s", page.Code, page.Body)
			}
			for _, expected := range []string{
				"<h1>Add movie</h1>", `action="/movies/new"`,
				`href="/movies/new">Add movie</a>`,
				`name="csrf_token"`, `for="title"`, `for="director"`, `for="country"`,
				`for="genre"`, `for="release_year"`, `for="synopsis"`,
				`id="release_year" name="release_year" type="number"`,
				`id="synopsis" name="synopsis" rows="5"`,
				`<datalist id="movie-genres">`,
			} {
				if !strings.Contains(page.Body.String(), expected) {
					t.Errorf("new-movie form missing %q", expected)
				}
			}
			title := fmt.Sprintf("Created by %s", role)
			storedTitle := title
			if role == model.RoleEditor {
				title = "  " + title + "  "
			}
			values := validMovieForm(title, csrfToken)
			response := submitForm(router, "/movies/new", values, csrfCookie, &http.Cookie{Name: "access_token", Value: accessToken})
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog?notice=created" {
				t.Fatalf("POST /movies/new = %d, Location %q: %s", response.Code, response.Header().Get("Location"), response.Body)
			}
			if strings.Contains(response.Header().Get("Location"), "Movie created") {
				t.Fatal("success message was included as free text in redirect URL")
			}
			listed := apiMovies(t, router)
			found := false
			for _, movie := range listed {
				if movie.Title == storedTitle {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("created movie is absent from API list: %+v", listed)
			}
		})
	}

	viewerToken := mustRoleToken(t, users, tokens, model.RoleViewer)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run("viewer "+method+" forbidden", func(t *testing.T) {
			if method == http.MethodGet {
				response := movieFormRequest(router, method, "/movies/new", nil, csrfCookie, viewerToken)
				if response.Code != http.StatusForbidden || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("%s /movies/new = %d, want HTML 403: %s", method, response.Code, response.Body)
				}
				return
			}
			response := submitForm(router, "/movies/new", validMovieForm("Denied", csrfToken), csrfCookie,
				&http.Cookie{Name: "access_token", Value: viewerToken})
			if response.Code != http.StatusForbidden || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("%s /movies/new = %d, want HTML 403: %s", method, response.Code, response.Body)
			}
		})
	}

	for _, path := range []string{"/movies/new", "/movies/1/edit"} {
		t.Run("anonymous "+path, func(t *testing.T) {
			response := movieFormRequest(router, http.MethodGet, path, nil, csrfCookie, "")
			if response.Code != http.StatusSeeOther || !strings.HasPrefix(response.Header().Get("Location"), "/login?next=") {
				t.Fatalf("GET %s = %d, Location %q; want login redirect", path, response.Code, response.Header().Get("Location"))
			}
		})
	}
}

func TestWebMovieFormValidationPreservesAndEscapesInput(t *testing.T) {
	router, users, tokens := webAuthRouter(t)
	editorToken := mustRoleToken(t, users, tokens, model.RoleEditor)
	csrfCookie, csrfToken := getCSRF(t, router, "/catalog")

	tests := []struct {
		name          string
		field         string
		value         string
		wantMessage   string
		wantPreserved string
	}{
		{name: "empty title", field: "title", value: "", wantMessage: "title must be a non-empty string"},
		{name: "whitespace genre", field: "genre", value: "   ", wantMessage: "genre must be a non-empty string", wantPreserved: `value="   "`},
		{name: "non-numeric year", field: "release_year", value: "abc", wantMessage: "release_year must be an integer", wantPreserved: `value="abc"`},
		{name: "fractional year", field: "release_year", value: "1.5", wantMessage: "release_year must be an integer", wantPreserved: `value="1.5"`},
		{name: "hostile title", field: "title", value: `<script>alert("x")</script>`, wantMessage: "genre must be a non-empty string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := validMovieForm("Valid title", csrfToken)
			values.Set(test.field, test.value)
			if test.name == "hostile title" {
				values.Set("genre", " ")
			}
			response := submitForm(router, "/movies/new", values, csrfCookie,
				&http.Cookie{Name: "access_token", Value: editorToken})
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST /movies/new = %d, want 422: %s", response.Code, response.Body)
			}
			body := response.Body.String()
			if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, test.wantMessage) {
				t.Errorf("422 response missing error summary/message %q: %s", test.wantMessage, body)
			}
			if test.wantPreserved != "" && !strings.Contains(body, test.wantPreserved) {
				t.Errorf("422 response did not preserve %q: %s", test.value, body)
			}
			if test.name == "hostile title" {
				if strings.Contains(body, test.value) || !strings.Contains(body, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;") {
					t.Errorf("hostile title was not safely escaped and preserved: %s", body)
				}
			}
		})
	}
}

func TestWebMovieFormEditPreservesIdentityAndCreatedAt(t *testing.T) {
	router, users, tokens := webAuthRouter(t)
	editorToken := mustRoleToken(t, users, tokens, model.RoleEditor)
	csrfCookie, csrfToken := getCSRF(t, router, "/catalog")
	created := submitForm(router, "/movies/new", validMovieForm("Before edit", csrfToken), csrfCookie,
		&http.Cookie{Name: "access_token", Value: editorToken})
	if created.Code != http.StatusSeeOther {
		t.Fatalf("create movie = %d: %s", created.Code, created.Body)
	}
	movies := apiMovies(t, router)
	if len(movies) != 1 {
		t.Fatalf("API list has %d movies, want 1", len(movies))
	}
	original := movies[0]

	editPath := fmt.Sprintf("/movies/%d/edit", original.ID)
	editPage := movieFormRequest(router, http.MethodGet, editPath, nil, csrfCookie, editorToken)
	if editPage.Code != http.StatusOK || !strings.Contains(editPage.Body.String(), "<h1>Edit movie</h1>") ||
		!strings.Contains(editPage.Body.String(), `value="Before edit"`) ||
		!strings.Contains(editPage.Body.String(), `<option value="Drama">`) {
		t.Fatalf("GET %s = %d: %s", editPath, editPage.Code, editPage.Body)
	}
	catalog := movieFormRequest(router, http.MethodGet, "/catalog", nil, csrfCookie, editorToken)
	if !strings.Contains(catalog.Body.String(), fmt.Sprintf(`href="%s">Edit</a>`, editPath)) {
		t.Errorf("catalog edit link does not target %s: %s", editPath, catalog.Body)
	}

	updatedValues := validMovieForm("After edit", csrfToken)
	updatedValues.Set("release_year", "")
	updatedValues.Set("synopsis", "")
	response := submitForm(router, editPath, updatedValues, csrfCookie,
		&http.Cookie{Name: "access_token", Value: editorToken})
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog?notice=updated" {
		t.Fatalf("POST %s = %d, Location %q: %s", editPath, response.Code, response.Header().Get("Location"), response.Body)
	}
	updatedMovies := apiMovies(t, router)
	if len(updatedMovies) != 1 {
		t.Fatalf("API list has %d movies after edit, want 1", len(updatedMovies))
	}
	updated := updatedMovies[0]
	if updated.ID != original.ID || updated.Title != "After edit" {
		t.Errorf("updated movie = %+v, original = %+v", updated, original)
	}
	if !updated.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("created_at changed from %s to %s", original.CreatedAt, updated.CreatedAt)
	}
	if updated.ReleaseYear != nil || updated.Synopsis != nil {
		t.Errorf("empty optional fields should become null on replacement: release_year=%v synopsis=%v", updated.ReleaseYear, updated.Synopsis)
	}
}

func TestWebMovieFormInvalidMissingIDsAndCSRF(t *testing.T) {
	router, users, tokens := webAuthRouter(t)
	editorToken := mustRoleToken(t, users, tokens, model.RoleEditor)
	csrfCookie, csrfToken := getCSRF(t, router, "/catalog")

	invalidID := movieFormRequest(router, http.MethodGet, "/movies/not-a-number/edit", nil, csrfCookie, editorToken)
	if invalidID.Code != http.StatusBadRequest || !strings.Contains(invalidID.Header().Get("Content-Type"), "text/html") {
		t.Errorf("invalid edit ID = %d, want HTML 400: %s", invalidID.Code, invalidID.Body)
	}
	missingID := movieFormRequest(router, http.MethodGet, "/movies/4294967295/edit", nil, csrfCookie, editorToken)
	if missingID.Code != http.StatusNotFound || !strings.Contains(missingID.Header().Get("Content-Type"), "text/html") {
		t.Errorf("unknown edit ID = %d, want HTML 404: %s", missingID.Code, missingID.Body)
	}

	values := validMovieForm("Missing CSRF", csrfToken)
	values.Del("csrf_token")
	response := submitForm(router, "/movies/new", values, csrfCookie,
		&http.Cookie{Name: "access_token", Value: editorToken})
	if response.Code != http.StatusForbidden || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("POST /movies/new without CSRF = %d, want HTML 403: %s", response.Code, response.Body)
	}
}

func TestWebMovieFormNoticesAreWhitelisted(t *testing.T) {
	router, _, _ := webAuthRouter(t)
	for _, test := range []struct {
		query string
		want  string
	}{
		{query: "created", want: "Movie created successfully."},
		{query: "updated", want: "Movie updated successfully."},
		{query: `<script>alert(1)</script>`, want: ""},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/catalog?notice="+url.QueryEscape(test.query), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET catalog with notice %q = %d", test.query, response.Code)
		}
		if test.want != "" && !strings.Contains(response.Body.String(), test.want) {
			t.Errorf("catalog missing notice %q", test.want)
		}
		if test.want == "" && strings.Contains(response.Body.String(), "<script>alert(1)</script>") {
			t.Errorf("untrusted notice rendered in catalog: %s", response.Body)
		}
	}
}

func validMovieForm(title, csrfToken string) url.Values {
	return url.Values{
		"title": {title}, "director": {"Form Director"}, "country": {"Form Country"},
		"genre": {"Drama"}, "release_year": {"2020"}, "synopsis": {"A form synopsis"},
		"csrf_token": {csrfToken},
	}
}

func movieFormRequest(router http.Handler, method, path string, values url.Values, csrfCookie *http.Cookie, accessToken string) *httptest.ResponseRecorder {
	var request *http.Request
	if values == nil {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if csrfCookie != nil {
		request.AddCookie(csrfCookie)
	}
	if accessToken != "" {
		request.AddCookie(&http.Cookie{Name: "access_token", Value: accessToken})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func apiMovies(t *testing.T, router http.Handler) []model.Movie {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/movies", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/movies = %d: %s", response.Code, response.Body)
	}
	var movies []model.Movie
	if err := json.Unmarshal(response.Body.Bytes(), &movies); err != nil {
		t.Fatalf("decode movie API list: %v: %s", err, response.Body)
	}
	return movies
}
