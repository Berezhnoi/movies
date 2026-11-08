package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"homework/internal/app"
	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
	"homework/internal/service"
	"homework/internal/storage"
)

const webCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// countingStorage counts every call, to prove which pages never touch storage.
type countingStorage struct {
	service.TrailerStorage
	calls atomic.Int64
}

func (s *countingStorage) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	s.calls.Add(1)
	return s.TrailerStorage.Put(ctx, key, body, size, contentType)
}

func (s *countingStorage) Head(ctx context.Context, key string) (int64, error) {
	s.calls.Add(1)
	return s.TrailerStorage.Head(ctx, key)
}

func (s *countingStorage) Get(ctx context.Context, key string, byteRange *service.ByteRange) (service.StoredObject, error) {
	s.calls.Add(1)
	return s.TrailerStorage.Get(ctx, key, byteRange)
}

func (s *countingStorage) Delete(ctx context.Context, key string) error {
	s.calls.Add(1)
	return s.TrailerStorage.Delete(ctx, key)
}

type webTrailerEnv struct {
	router     http.Handler
	users      *repository.InMemoryUserRepository
	tokens     *auth.TokenManager
	storage    *storage.Memory
	counter    *countingStorage
	trailers   *repository.InMemoryTrailerRepository
	csrfCookie *http.Cookie
	csrfToken  string
	editor     string
}

func newWebTrailerEnv(t *testing.T) *webTrailerEnv {
	t.Helper()
	users := repository.NewInMemoryUserRepository()
	tokens, err := auth.NewTokenManager(authTestSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	objects := storage.NewMemory()
	env := &webTrailerEnv{
		users: users, tokens: tokens, storage: objects,
		counter:  &countingStorage{TrailerStorage: objects},
		trailers: repository.NewInMemoryTrailerRepository(),
	}
	env.router = app.NewRouterWithDeps(app.Deps{
		Users: users, Tokens: tokens, TrailerStorage: env.counter, Trailers: env.trailers,
	})
	env.csrfCookie, env.csrfToken = getCSRF(t, env.router, "/catalog")
	env.editor = mustRoleToken(t, users, tokens, model.RoleEditor)
	return env
}

// web sends a cookie-authenticated web request; accessToken may be empty.
func (env *webTrailerEnv) web(request *http.Request, accessToken string) *httptest.ResponseRecorder {
	if accessToken != "" {
		request.AddCookie(&http.Cookie{Name: "access_token", Value: accessToken})
	}
	request.AddCookie(env.csrfCookie)
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	return response
}

func (env *webTrailerEnv) get(path, accessToken string) *httptest.ResponseRecorder {
	return env.web(httptest.NewRequest(http.MethodGet, path, nil), accessToken)
}

// htmx sends a state-changing HTMX request with the given Origin and CSRF header.
func (env *webTrailerEnv) htmx(method, path string, body io.Reader, contentType, origin, csrf, accessToken string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("HX-Request", "true")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	return env.web(request, accessToken)
}

func (env *webTrailerEnv) upload(t *testing.T, id string, file []byte, origin, csrf, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, "file", file)
	return env.htmx(http.MethodPost, "/movies/"+id+"/trailer", bytes.NewReader(body), contentType, origin, csrf, accessToken)
}

func (env *webTrailerEnv) createMovie(t *testing.T, title string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/movies", strings.NewReader(
		`{"title":"`+title+`","director":"D","country":"C","genre":"Drama"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+env.editor)
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create movie = %d: %s", response.Code, response.Body)
	}
	var movie model.Movie
	if err := json.Unmarshal(response.Body.Bytes(), &movie); err != nil {
		t.Fatalf("decode movie: %v", err)
	}
	return movieIDText(movie.ID)
}

func (env *webTrailerEnv) movieWithTrailer(t *testing.T, title string) string {
	t.Helper()
	id := env.createMovie(t, title)
	if response := env.upload(t, id, mp4Bytes(1000), "http://example.com", env.csrfToken, env.editor); response.Code != http.StatusOK {
		t.Fatalf("web upload = %d: %s", response.Code, response.Body)
	}
	return id
}

func TestWebTrailer_EditFormShowsSectionToStaff(t *testing.T) {
	env := newWebTrailerEnv(t)
	id := env.createMovie(t, "Section")
	admin := mustRoleToken(t, env.users, env.tokens, model.RoleAdmin)

	for name, token := range map[string]string{"editor": env.editor, "admin": admin} {
		response := env.get("/movies/"+id+"/edit", token)
		if response.Code != http.StatusOK {
			t.Fatalf("%s GET edit = %d: %s", name, response.Code, response.Body)
		}
		body := response.Body.String()
		for _, want := range []string{`id="trailer-section"`, `hx-post="/movies/` + id + `/trailer"`, `hx-encoding="multipart/form-data"`, "Upload trailer"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s edit form missing %s", name, want)
			}
		}
		if strings.Contains(body, "Delete trailer") {
			t.Errorf("%s edit form offers delete without a trailer", name)
		}
	}
	if body := env.get("/movies/new", env.editor).Body.String(); strings.Contains(body, "trailer-section") {
		t.Error("new movie form shows a trailer section")
	}
}

func TestWebTrailer_ViewerAndAnonymousSeeNoControls(t *testing.T) {
	env := newWebTrailerEnv(t)
	id := env.movieWithTrailer(t, "Hidden")
	viewer := mustRoleToken(t, env.users, env.tokens, model.RoleViewer)

	if response := env.get("/movies/"+id+"/edit", viewer); response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "trailer-section") {
		t.Errorf("viewer GET edit = %d, want 403 without the trailer section", response.Code)
	}
	if response := env.get("/movies/"+id+"/edit", ""); response.Code != http.StatusSeeOther {
		t.Errorf("anonymous GET edit = %d, want redirect to login", response.Code)
	}
	for name, token := range map[string]string{"viewer": viewer, "anonymous": ""} {
		for _, path := range []string{"/catalog", "/movies/" + id + "/trailer"} {
			body := env.get(path, token).Body.String()
			if strings.Contains(body, "/trailer\" hx-") || strings.Contains(body, "hx-post") || strings.Contains(body, "Delete trailer") {
				t.Errorf("%s sees trailer controls on %s", name, path)
			}
		}
	}

	upload := env.upload(t, id, mp4Bytes(100), "http://example.com", env.csrfToken, viewer)
	if upload.Code != http.StatusForbidden {
		t.Errorf("viewer upload = %d, want 403", upload.Code)
	}
	anonymous := env.upload(t, id, mp4Bytes(100), "http://example.com", env.csrfToken, "")
	if anonymous.Code != http.StatusUnauthorized || anonymous.Header().Get("HX-Redirect") != "/login" {
		t.Errorf("anonymous upload = %d (HX-Redirect %q), want 401 to /login", anonymous.Code, anonymous.Header().Get("HX-Redirect"))
	}
	deleted := env.htmx(http.MethodDelete, "/web/movies/"+id+"/trailer", nil, "", "http://example.com", env.csrfToken, viewer)
	if deleted.Code != http.StatusForbidden || env.trailers.Len() != 1 {
		t.Errorf("viewer delete = %d with %d rows, want 403 and the row kept", deleted.Code, env.trailers.Len())
	}
}

func TestWebTrailer_UploadRequiresCSRFAndOrigin(t *testing.T) {
	env := newWebTrailerEnv(t)
	id := env.createMovie(t, "Upload")

	for _, test := range []struct {
		name, origin, csrf string
	}{
		{name: "missing token", origin: "http://example.com"},
		{name: "wrong token", origin: "http://example.com", csrf: "wrong"},
		{name: "missing origin", csrf: env.csrfToken},
		{name: "foreign origin", origin: "http://evil.example", csrf: env.csrfToken},
	} {
		if response := env.upload(t, id, mp4Bytes(100), test.origin, test.csrf, env.editor); response.Code != http.StatusForbidden {
			t.Errorf("%s: upload = %d, want 403", test.name, response.Code)
		}
	}
	if env.trailers.Len() != 0 || env.storage.Len() != 0 {
		t.Fatalf("rejected uploads stored %d rows and %d objects", env.trailers.Len(), env.storage.Len())
	}

	// 2 MiB is over the global 1 MiB cap, so this also proves the exemption.
	response := env.upload(t, id, mp4Bytes(2<<20), "http://example.com", env.csrfToken, env.editor)
	if response.Code != http.StatusOK {
		t.Fatalf("upload = %d, want 200: %s", response.Code, response.Body)
	}
	body := response.Body.String()
	if !strings.HasPrefix(strings.TrimSpace(body), `<section id="trailer-section"`) || strings.Contains(body, "<html") {
		t.Errorf("upload response is not the section fragment: %s", body)
	}
	for _, want := range []string{"Trailer uploaded.", "2.0 MiB", "Replace trailer", "Delete trailer"} {
		if !strings.Contains(body, want) {
			t.Errorf("upload fragment missing %q: %s", want, body)
		}
	}
	if env.trailers.Len() != 1 || env.storage.Len() != 1 {
		t.Errorf("after upload: %d rows, %d objects; want 1 and 1", env.trailers.Len(), env.storage.Len())
	}
}

func TestWebTrailer_UploadErrorsRenderSection(t *testing.T) {
	env := newWebTrailerEnv(t)
	id := env.createMovie(t, "Errors")

	notMP4 := env.upload(t, id, []byte("plain text, not a video"), "http://example.com", env.csrfToken, env.editor)
	if notMP4.Code != http.StatusUnsupportedMediaType || !strings.Contains(notMP4.Body.String(), "Trailer file must be an MP4 video") {
		t.Errorf("non-MP4 upload = %d: %s", notMP4.Code, notMP4.Body)
	}
	empty := env.upload(t, id, nil, "http://example.com", env.csrfToken, env.editor)
	if empty.Code != http.StatusUnprocessableEntity || !strings.Contains(empty.Body.String(), `id="trailer-section"`) {
		t.Errorf("empty upload = %d: %s", empty.Code, empty.Body)
	}
	for path, want := range map[string]int{"/movies/abc/trailer": http.StatusBadRequest, "/movies/4294967295/trailer": http.StatusNotFound} {
		body, contentType := multipartBody(t, "file", mp4Bytes(100))
		response := env.htmx(http.MethodPost, path, bytes.NewReader(body), contentType, "http://example.com", env.csrfToken, env.editor)
		if response.Code != want {
			t.Errorf("POST %s = %d, want %d", path, response.Code, want)
		}
	}
	if env.trailers.Len() != 0 {
		t.Errorf("failed uploads stored %d rows", env.trailers.Len())
	}
}

func TestWebTrailer_DeleteReturnsRefreshedSection(t *testing.T) {
	env := newWebTrailerEnv(t)
	id := env.movieWithTrailer(t, "Delete")

	if response := env.htmx(http.MethodDelete, "/web/movies/"+id+"/trailer", nil, "", "http://example.com", "", env.editor); response.Code != http.StatusForbidden {
		t.Errorf("delete without CSRF = %d, want 403", response.Code)
	}
	response := env.htmx(http.MethodDelete, "/web/movies/"+id+"/trailer", nil, "", "http://example.com", env.csrfToken, env.editor)
	if response.Code != http.StatusOK {
		t.Fatalf("delete = %d, want 200: %s", response.Code, response.Body)
	}
	body := response.Body.String()
	for _, want := range []string{`id="trailer-section"`, "Trailer deleted.", "Upload trailer", `hx-post="/movies/` + id + `/trailer"`} {
		if !strings.Contains(body, want) {
			t.Errorf("delete fragment missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Delete trailer") {
		t.Errorf("delete fragment still offers delete: %s", body)
	}
	if env.trailers.Len() != 0 || env.storage.Len() != 0 {
		t.Errorf("after delete: %d rows, %d objects; want 0 and 0", env.trailers.Len(), env.storage.Len())
	}

	// A stale row whose object is lost is still removed.
	stale := env.movieWithTrailer(t, "Stale")
	env.storage.DropObject(service.TrailerObjectKey(2))
	if edit := env.get("/movies/"+stale+"/edit", env.editor).Body.String(); !strings.Contains(edit, "missing from storage") {
		t.Errorf("edit form does not report the lost object")
	}
	if response := env.htmx(http.MethodDelete, "/web/movies/"+stale+"/trailer", nil, "", "http://example.com", env.csrfToken, env.editor); response.Code != http.StatusOK || env.trailers.Len() != 0 {
		t.Errorf("stale delete = %d with %d rows, want 200 and 0", response.Code, env.trailers.Len())
	}
}

func TestWebTrailer_CatalogLinkUsesRowsOnly(t *testing.T) {
	env := newWebTrailerEnv(t)
	with := env.movieWithTrailer(t, "With Trailer")
	without := env.createMovie(t, "Without Trailer")

	env.storage.SetUnavailable(true)
	before := env.counter.calls.Load()
	for _, path := range []string{"/catalog", "/web/movies"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("HX-Request", "true")
		response := env.web(request, "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s with storage down = %d: %s", path, response.Code, response.Body)
		}
		body := response.Body.String()
		if !strings.Contains(body, `href="/movies/`+with+`/trailer"`) {
			t.Errorf("GET %s has no trailer link for movie %s", path, with)
		}
		if strings.Contains(body, `href="/movies/`+without+`/trailer"`) {
			t.Errorf("GET %s links a trailer for movie %s that has none", path, without)
		}
	}
	if calls := env.counter.calls.Load() - before; calls != 0 {
		t.Errorf("catalog made %d storage calls, want 0", calls)
	}
}

func TestWebTrailer_WatchPage(t *testing.T) {
	env := newWebTrailerEnv(t)
	id := env.movieWithTrailer(t, "Watch <Me>")
	none := env.createMovie(t, "No Trailer")

	normal := env.get("/movies/"+id+"/trailer", "")
	if normal.Code != http.StatusOK {
		t.Fatalf("watch page = %d: %s", normal.Code, normal.Body)
	}
	body := normal.Body.String()
	if !strings.Contains(body, `<video class="trailer-video" controls preload="metadata" src="/api/v1/movies/`+id+`/trailer/video">`) {
		t.Errorf("watch page has no player for the stream URL: %s", body)
	}
	if !strings.Contains(body, "Watch &lt;Me&gt;") || strings.Contains(body, "Watch <Me>") {
		t.Error("watch page does not escape the movie title")
	}
	if got := normal.Header().Get("Content-Security-Policy"); got != webCSP {
		t.Errorf("watch page CSP = %q, want %q", got, webCSP)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "style=") || strings.Contains(body, "<style") {
		t.Error("watch page has an inline script or style")
	}
	if video := env.get("/api/v1/movies/"+id+"/trailer/video", ""); video.Code != http.StatusOK || video.Body.Len() != 1000 {
		t.Errorf("stream for the player = %d with %d bytes", video.Code, video.Body.Len())
	}

	env.storage.DropObject(service.TrailerObjectKey(1))
	unavailable := env.get("/movies/"+id+"/trailer", "")
	if unavailable.Code != http.StatusNotFound || !strings.Contains(unavailable.Body.String(), "Trailer unavailable") || strings.Contains(unavailable.Body.String(), "<video") {
		t.Errorf("lost-object watch page = %d: %s", unavailable.Code, unavailable.Body)
	}

	missing := env.get("/movies/"+none+"/trailer", "")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "This movie has no trailer.") || strings.Contains(missing.Body.String(), "<video") {
		t.Errorf("no-trailer watch page = %d: %s", missing.Code, missing.Body)
	}
	for path, want := range map[string]int{"/movies/abc/trailer": http.StatusBadRequest, "/movies/4294967295/trailer": http.StatusNotFound} {
		if response := env.get(path, ""); response.Code != want || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
			t.Errorf("GET %s = %d, want HTML %d", path, response.Code, want)
		}
	}

	env.storage.SetUnavailable(true)
	if down := env.get("/movies/"+id+"/trailer", ""); down.Code != http.StatusServiceUnavailable {
		t.Errorf("watch page with storage down = %d, want 503", down.Code)
	}
}

func TestWebTrailer_CSPUnchanged(t *testing.T) {
	env := newWebTrailerEnv(t)
	id := env.createMovie(t, "CSP")
	for _, path := range []string{"/catalog", "/movies/" + id + "/edit", "/static/css/style.css"} {
		if got := env.get(path, env.editor).Header().Get("Content-Security-Policy"); got != webCSP {
			t.Errorf("GET %s CSP = %q, want %q", path, got, webCSP)
		}
	}
}

func TestWebTrailer_NoUIWhenDisabled(t *testing.T) {
	users := repository.NewInMemoryUserRepository()
	tokens, err := auth.NewTokenManager(authTestSecret, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	movies := repository.NewInMemoryMovieRepositoryAdapter()
	if _, err := movies.Create(context.Background(), model.Movie{Title: "Plain", Director: "D", Country: "C", Genre: "Drama"}); err != nil {
		t.Fatal(err)
	}
	trailers := repository.NewInMemoryTrailerRepository()
	if _, err := trailers.Upsert(context.Background(), model.Trailer{MovieID: 1, ObjectKey: service.TrailerObjectKey(1)}); err != nil {
		t.Fatal(err)
	}

	// TRAILERS_ENABLED=false: no storage, even with a leftover row.
	router := app.NewRouterWithDeps(app.Deps{Users: users, Tokens: tokens, Movies: movies, Trailers: trailers})
	editor := mustRoleToken(t, users, tokens, model.RoleEditor)
	for _, path := range []string{"/catalog", "/movies/1/edit"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "access_token", Value: editor})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK || strings.Contains(strings.ToLower(response.Body.String()), "trailer") {
			t.Errorf("trailers off: GET %s = %d or mentions a trailer", path, response.Code)
		}
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/movies/1/trailer", nil),
		httptest.NewRequest(http.MethodPost, "/movies/1/trailer", nil),
		httptest.NewRequest(http.MethodDelete, "/web/movies/1/trailer", nil),
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Errorf("trailers off: %s %s = %d, want 404 or 405", request.Method, request.URL.Path, response.Code)
		}
	}

	// WEB_UI_ENABLED=false: the trailer API works, the web routes do not exist.
	disabled := false
	apiOnly := app.NewRouterWithDeps(app.Deps{
		Users: users, Tokens: tokens, Movies: movies, Trailers: trailers,
		TrailerStorage: storage.NewMemory(), WebUIEnabled: &disabled,
	})
	for path, want := range map[string]int{"/movies/1/trailer": http.StatusNotFound, "/catalog": http.StatusNotFound, "/api/v1/movies/1/trailer": http.StatusNotFound} {
		response := httptest.NewRecorder()
		apiOnly.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != want || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
			t.Errorf("API only: GET %s = %d (%s), want JSON %d", path, response.Code, response.Header().Get("Content-Type"), want)
		}
	}
	response := httptest.NewRecorder()
	apiOnly.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/movies/1/trailer", nil))
	if !strings.Contains(response.Body.String(), "trailer_unavailable") {
		t.Errorf("API only: trailer metadata = %s, want the API's trailer_unavailable", response.Body)
	}
}
