package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"homework/internal/app"
	"homework/internal/auth"
	"homework/internal/handler"
	"homework/internal/model"
	"homework/internal/repository"
	"homework/internal/service"
	"homework/internal/storage"
)

type trailerTestEnv struct {
	*authTestEnv
	storage  *storage.Memory
	trailers *repository.InMemoryTrailerRepository
}

func newTrailerTestEnv(t *testing.T) *trailerTestEnv {
	t.Helper()
	return newTrailerTestEnvWithRepository(t, repository.NewInMemoryTrailerRepository())
}

func newTrailerTestEnvWithRepository(t *testing.T, trailers repository.TrailerRepository) *trailerTestEnv {
	t.Helper()

	users := repository.NewInMemoryUserRepository()
	tokens, err := auth.NewTokenManager(authTestSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	objects := storage.NewMemory()
	inMemory, _ := trailers.(*repository.InMemoryTrailerRepository)
	return &trailerTestEnv{
		authTestEnv: &authTestEnv{
			router: app.NewRouterWithDeps(app.Deps{
				Movies:         repository.NewInMemoryMovieRepositoryAdapter(),
				Users:          users,
				Tokens:         tokens,
				TrailerStorage: objects,
				Trailers:       trailers,
			}),
			users:  users,
			tokens: tokens,
		},
		storage:  objects,
		trailers: inMemory,
	}
}

func (e *trailerTestEnv) tokenFor(t *testing.T, role string) string {
	t.Helper()
	user := e.createUser(t, role+"@example.com", authTestPassword, role)
	return e.login(t, user.Email, authTestPassword)
}

func (e *trailerTestEnv) createMovie(t *testing.T, token string) uint32 {
	t.Helper()
	response := e.do(http.MethodPost, "/api/v1/movies", rbacValidMovieBody, token)
	if response.Code != http.StatusCreated {
		t.Fatalf("create movie status = %d, want 201: %s", response.Code, response.Body)
	}
	var movie struct {
		ID uint32 `json:"id"`
	}
	if err := json.Unmarshal(response.Body, &movie); err != nil {
		t.Fatalf("decode movie: %v", err)
	}
	return movie.ID
}

func (e *trailerTestEnv) serve(request *http.Request, token string) authTestResponse {
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	e.router.ServeHTTP(response, request)
	return authTestResponse{Code: response.Code, Header: response.Header().Clone(), Body: response.Body.Bytes()}
}

func (e *trailerTestEnv) upload(t *testing.T, movieID, token string, file []byte) authTestResponse {
	t.Helper()
	body, contentType := multipartBody(t, "file", file)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/movies/"+movieID+"/trailer", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	return e.serve(request, token)
}

func (e *trailerTestEnv) get(path, rangeHeader string) authTestResponse {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	return e.serve(request, "")
}

func multipartBody(t *testing.T, field string, file []byte) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {fmt.Sprintf(`form-data; name=%q; filename="trailer.mp4"`, field)},
		"Content-Type":        {"video/mp4"},
	})
	if err != nil {
		t.Fatalf("CreatePart() error = %v", err)
	}
	if _, err := part.Write(file); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

// mp4Bytes returns size bytes that start with an MP4 "ftyp" box.
func mp4Bytes(size int) []byte {
	data := make([]byte, size)
	copy(data, []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	for i := 12; i < size; i++ {
		data[i] = byte(i % 251)
	}
	return data
}

func movieIDText(id uint32) string { return strconv.FormatUint(uint64(id), 10) }

func TestTrailer_UploadThenStream(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := movieIDText(env.createMovie(t, editor))
	file := mp4Bytes(1000)

	response := env.upload(t, id, editor, file)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201: %s", response.Code, response.Body)
	}
	var uploaded map[string]any
	if err := json.Unmarshal(response.Body, &uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploaded["movie_id"] != float64(1) || uploaded["content_type"] != "video/mp4" || uploaded["size_bytes"] != float64(1000) || uploaded["uploaded_at"] == nil {
		t.Errorf("upload response = %s", response.Body)
	}
	if _, leaked := uploaded["object_key"]; leaked {
		t.Errorf("upload response exposes the object key: %s", response.Body)
	}

	metadata := env.get("/api/v1/movies/"+id+"/trailer", "")
	if metadata.Code != http.StatusOK || !bytes.Equal(metadata.Body, response.Body) {
		t.Errorf("metadata = %d %s, want 200 %s", metadata.Code, metadata.Body, response.Body)
	}

	video := env.get("/api/v1/movies/"+id+"/trailer/video", "")
	if video.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200: %s", video.Code, video.Body)
	}
	if !bytes.Equal(video.Body, file) {
		t.Errorf("streamed %d bytes, want the uploaded %d bytes", len(video.Body), len(file))
	}
	for header, want := range map[string]string{
		"Content-Type":           "video/mp4",
		"X-Content-Type-Options": "nosniff",
		"Accept-Ranges":          "bytes",
		"Content-Length":         "1000",
	} {
		if got := video.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if env.storage.Len() != 1 {
		t.Errorf("stored objects = %d, want 1", env.storage.Len())
	}
}

func TestTrailer_StreamRanges(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := movieIDText(env.createMovie(t, editor))
	file := mp4Bytes(1000)
	if response := env.upload(t, id, editor, file); response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", response.Code, response.Body)
	}

	tests := []struct {
		rangeHeader  string
		status       int
		contentRange string
		body         []byte
	}{
		{rangeHeader: "bytes=0-99", status: 206, contentRange: "bytes 0-99/1000", body: file[:100]},
		{rangeHeader: "bytes=900-", status: 206, contentRange: "bytes 900-999/1000", body: file[900:]},
		{rangeHeader: "bytes=-50", status: 206, contentRange: "bytes 950-999/1000", body: file[950:]},
		{rangeHeader: "bytes=990-5000", status: 206, contentRange: "bytes 990-999/1000", body: file[990:]},
		{rangeHeader: "bytes=1000-1100", status: 416, contentRange: "bytes */1000"},
		{rangeHeader: "bytes=-0", status: 416, contentRange: "bytes */1000"},
		{rangeHeader: "bytes=0-1,5-9", status: 200, body: file},
		{rangeHeader: "items=0-9", status: 200, body: file},
		{rangeHeader: "bytes=9-0", status: 200, body: file},
	}
	for _, test := range tests {
		t.Run(test.rangeHeader, func(t *testing.T) {
			response := env.get("/api/v1/movies/"+id+"/trailer/video", test.rangeHeader)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			if got := response.Header.Get("Content-Range"); got != test.contentRange {
				t.Errorf("Content-Range = %q, want %q", got, test.contentRange)
			}
			if test.status == http.StatusRequestedRangeNotSatisfiable {
				assertRBACError(t, response, "range_not_satisfiable")
				return
			}
			if !bytes.Equal(response.Body, test.body) {
				t.Errorf("body has %d bytes, want %d", len(response.Body), len(test.body))
			}
			if got := response.Header.Get("Content-Length"); got != strconv.Itoa(len(test.body)) {
				t.Errorf("Content-Length = %q, want %d", got, len(test.body))
			}
		})
	}
}

func TestTrailer_UploadValidation(t *testing.T) {
	jsonRequest := func(id string) *http.Request {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/movies/"+id+"/trailer", strings.NewReader(`{"file":"x"}`))
		request.Header.Set("Content-Type", "application/json")
		return request
	}
	multipartRequest := func(t *testing.T, id, field string, file []byte) *http.Request {
		body, contentType := multipartBody(t, field, file)
		request := httptest.NewRequest(http.MethodPut, "/api/v1/movies/"+id+"/trailer", bytes.NewReader(body))
		request.Header.Set("Content-Type", contentType)
		return request
	}
	tests := []struct {
		name    string
		request func(t *testing.T, id string) *http.Request
		status  int
		code    string
	}{
		{
			name: "declared size over limit",
			request: func(t *testing.T, id string) *http.Request {
				request := multipartRequest(t, id, "file", mp4Bytes(100))
				request.ContentLength = handler.MaxTrailerRequestSize + 1
				return request
			},
			status: http.StatusRequestEntityTooLarge, code: "payload_too_large",
		},
		{
			name:    "streamed size over limit with unknown length",
			request: oversizedChunkedUpload,
			status:  http.StatusRequestEntityTooLarge, code: "payload_too_large",
		},
		{
			name: "not an MP4 despite video/mp4 part header",
			request: func(t *testing.T, id string) *http.Request {
				return multipartRequest(t, id, "file", []byte("this is plain text, not a video file"))
			},
			status: http.StatusUnsupportedMediaType, code: "unsupported_media_type",
		},
		{
			name: "shorter than an MP4 header",
			request: func(t *testing.T, id string) *http.Request {
				return multipartRequest(t, id, "file", []byte("abc"))
			},
			status: http.StatusUnsupportedMediaType, code: "unsupported_media_type",
		},
		{
			name: "empty file",
			request: func(t *testing.T, id string) *http.Request {
				return multipartRequest(t, id, "file", nil)
			},
			status: http.StatusUnprocessableEntity, code: "validation_error",
		},
		{
			name: "missing file field",
			request: func(t *testing.T, id string) *http.Request {
				return multipartRequest(t, id, "video", mp4Bytes(100))
			},
			status: http.StatusUnprocessableEntity, code: "validation_error",
		},
		{
			name:    "not multipart",
			request: func(_ *testing.T, id string) *http.Request { return jsonRequest(id) },
			status:  http.StatusUnprocessableEntity, code: "validation_error",
		},
		{
			name: "unknown movie",
			request: func(t *testing.T, _ string) *http.Request {
				return multipartRequest(t, "999", "file", mp4Bytes(100))
			},
			status: http.StatusNotFound, code: "not_found",
		},
		{
			name: "malformed movie id",
			request: func(t *testing.T, _ string) *http.Request {
				return multipartRequest(t, "abc", "file", mp4Bytes(100))
			},
			status: http.StatusBadRequest, code: "invalid_id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTrailerTestEnv(t)
			editor := env.tokenFor(t, model.RoleEditor)
			id := movieIDText(env.createMovie(t, editor))

			response := env.serve(test.request(t, id), editor)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			assertRBACError(t, response, test.code)
			if env.storage.Len() != 0 {
				t.Errorf("stored objects = %d, want 0", env.storage.Len())
			}
			if metadata := env.get("/api/v1/movies/"+id+"/trailer", ""); metadata.Code != http.StatusNotFound {
				t.Errorf("metadata after rejected upload = %d, want 404: %s", metadata.Code, metadata.Body)
			}
		})
	}
}

// oversizedChunkedUpload sends one byte more than the file limit without a
// Content-Length, so only the read limit can reject it.
func oversizedChunkedUpload(t *testing.T, id string) *http.Request {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", "trailer.mp4")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(mp4Bytes(12)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	head := bytes.Clone(buffer.Bytes())
	buffer.Reset()
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	filler := io.LimitReader(zeroReader{}, service.MaxTrailerSize+1-12)
	body := io.MultiReader(bytes.NewReader(head), filler, bytes.NewReader(buffer.Bytes()))
	request := httptest.NewRequest(http.MethodPut, "/api/v1/movies/"+id+"/trailer", body)
	request.ContentLength = -1
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestTrailer_UploadCheckOrder(t *testing.T) {
	tests := []struct {
		name   string
		role   string
		movie  string
		file   []byte
		status int
		code   string
	}{
		{name: "anonymous malformed id", movie: "abc", file: mp4Bytes(100), status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "viewer malformed id", role: model.RoleViewer, movie: "abc", file: mp4Bytes(100), status: http.StatusForbidden, code: "forbidden"},
		{name: "editor malformed id", role: model.RoleEditor, movie: "abc", file: mp4Bytes(100), status: http.StatusBadRequest, code: "invalid_id"},
		{name: "editor unknown movie with non-MP4", role: model.RoleEditor, movie: "999", file: []byte("not a video"), status: http.StatusNotFound, code: "not_found"},
		{name: "editor unknown movie with empty file", role: model.RoleEditor, movie: "999", status: http.StatusNotFound, code: "not_found"},
		{name: "viewer existing movie", role: model.RoleViewer, movie: "1", file: mp4Bytes(100), status: http.StatusForbidden, code: "forbidden"},
		{name: "admin existing movie", role: model.RoleAdmin, movie: "1", file: mp4Bytes(100), status: http.StatusCreated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := newTrailerTestEnv(t)
			env.createMovie(t, env.tokenFor(t, model.RoleEditor))
			token := ""
			if test.role != "" && test.role != model.RoleEditor {
				token = env.tokenFor(t, test.role)
			} else if test.role == model.RoleEditor {
				token = env.login(t, model.RoleEditor+"@example.com", authTestPassword)
			}

			response := env.upload(t, test.movie, token, test.file)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			if test.code != "" {
				assertRBACError(t, response, test.code)
			}
		})
	}
}

func TestTrailer_ReadsWithoutTrailer(t *testing.T) {
	env := newTrailerTestEnv(t)
	id := movieIDText(env.createMovie(t, env.tokenFor(t, model.RoleEditor)))

	for _, path := range []string{"/api/v1/movies/" + id + "/trailer", "/api/v1/movies/" + id + "/trailer/video", "/api/v1/movies/999/trailer"} {
		response := env.get(path, "")
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404: %s", path, response.Code, response.Body)
			continue
		}
		assertRBACError(t, response, "not_found")
	}
	response := env.get("/api/v1/movies/abc/trailer/video", "")
	if response.Code != http.StatusBadRequest {
		t.Errorf("GET malformed id status = %d, want 400: %s", response.Code, response.Body)
	}
}

func TestTrailer_RoutesAbsentWithoutStorage(t *testing.T) {
	routers := map[string]http.Handler{
		"NewRouter":                    app.NewRouter(),
		"NewRouterWithDeps no storage": newAuthTestEnv(t).router,
	}
	for name, router := range routers {
		for _, request := range []*http.Request{
			httptest.NewRequest(http.MethodGet, "/api/v1/movies/1/trailer", nil),
			httptest.NewRequest(http.MethodGet, "/api/v1/movies/1/trailer/video", nil),
			httptest.NewRequest(http.MethodPut, "/api/v1/movies/1/trailer", strings.NewReader("x")),
			httptest.NewRequest(http.MethodDelete, "/api/v1/movies/1/trailer", nil),
		} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s: %s %s status = %d, want 404 or 405", name, request.Method, request.URL.Path, response.Code)
			}
		}
	}
}

func TestTrailer_GlobalBodyLimitStillApplies(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	body := `{"title":"` + strings.Repeat("x", 1<<20+1) + `","director":"D","country":"C","genre":"Drama"}`

	response := env.do(http.MethodPost, "/api/v1/movies", body, editor)
	if response.Code != http.StatusUnprocessableEntity && response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST /movies with a body over 1 MiB status = %d, want 422 or 413", response.Code)
	}
}

func TestTrailer_UploadOverGlobalBodyLimit(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := movieIDText(env.createMovie(t, editor))

	response := env.upload(t, id, editor, mp4Bytes(2<<20))
	if response.Code != http.StatusCreated {
		t.Fatalf("upload of a 2 MiB trailer status = %d, want 201: %s", response.Code, response.Body)
	}
}

func TestTrailer_ReplaceReturnsNewBytes(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := movieIDText(env.createMovie(t, editor))

	if response := env.upload(t, id, editor, mp4Bytes(1000)); response.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want 201: %s", response.Code, response.Body)
	}
	replacement := mp4Bytes(600)
	replacement[599] = 0xAB
	response := env.upload(t, id, editor, replacement)
	if response.Code != http.StatusOK {
		t.Fatalf("replace status = %d, want 200: %s", response.Code, response.Body)
	}
	stream := env.get("/api/v1/movies/"+id+"/trailer/video", "")
	if stream.Code != http.StatusOK || !bytes.Equal(stream.Body, replacement) {
		t.Errorf("stream after replace status = %d, %d bytes, want 200 with the new %d bytes", stream.Code, len(stream.Body), len(replacement))
	}
	if env.storage.Len() != 1 || env.trailers.Len() != 1 {
		t.Errorf("after replace objects = %d, rows = %d, want 1, 1", env.storage.Len(), env.trailers.Len())
	}
}

func (e *trailerTestEnv) uploadMovieWithTrailer(t *testing.T, editor string) string {
	t.Helper()
	id := movieIDText(e.createMovie(t, editor))
	if response := e.upload(t, id, editor, mp4Bytes(1000)); response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201: %s", response.Code, response.Body)
	}
	return id
}

func (e *trailerTestEnv) assertReads(t *testing.T, id string, status int, code string) {
	t.Helper()
	for _, path := range []string{"/api/v1/movies/" + id + "/trailer", "/api/v1/movies/" + id + "/trailer/video"} {
		response := e.get(path, "")
		if response.Code != status {
			t.Errorf("GET %s status = %d, want %d: %s", path, response.Code, status, response.Body)
			continue
		}
		assertRBACError(t, response, code)
	}
}

// captureLog sends the standard logger to a buffer for the rest of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

func TestTrailer_LostObject(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := env.uploadMovieWithTrailer(t, editor)
	logs := captureLog(t)

	env.storage.DropObject("trailers/" + id + ".mp4")
	env.assertReads(t, id, http.StatusNotFound, "trailer_unavailable")
	if env.trailers.Len() != 1 {
		t.Errorf("rows after lost object = %d, want 1", env.trailers.Len())
	}
	if !strings.Contains(logs.String(), "trailer object missing: movie_id="+id) {
		t.Errorf("log = %q, want a lost-object warning with movie_id=%s", logs, id)
	}

	if response := env.upload(t, id, editor, mp4Bytes(500)); response.Code != http.StatusOK {
		t.Fatalf("re-upload status = %d, want 200: %s", response.Code, response.Body)
	}
	if response := env.get("/api/v1/movies/"+id+"/trailer/video", ""); response.Code != http.StatusOK || len(response.Body) != 500 {
		t.Errorf("stream after re-upload status = %d, %d bytes, want 200 with 500 bytes", response.Code, len(response.Body))
	}
}

func TestTrailer_LostBucket(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := env.uploadMovieWithTrailer(t, editor)

	env.storage.DropBucket()
	env.assertReads(t, id, http.StatusNotFound, "trailer_unavailable")
	if env.trailers.Len() != 1 {
		t.Errorf("rows after lost bucket = %d, want 1", env.trailers.Len())
	}

	other := movieIDText(env.createMovie(t, editor))
	if response := env.upload(t, other, editor, mp4Bytes(300)); response.Code != http.StatusCreated {
		t.Fatalf("upload after lost bucket status = %d, want 201: %s", response.Code, response.Body)
	}
	if env.storage.CreateBucketCalls() != 1 {
		t.Errorf("CreateBucket calls = %d, want 1", env.storage.CreateBucketCalls())
	}
	if response := env.get("/api/v1/movies/"+other+"/trailer/video", ""); response.Code != http.StatusOK {
		t.Errorf("stream after bucket recovery status = %d, want 200", response.Code)
	}
}

func TestTrailer_Delete(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := env.uploadMovieWithTrailer(t, editor)
	logs := captureLog(t)

	response := env.do(http.MethodDelete, "/api/v1/movies/"+id+"/trailer", "", editor)
	if response.Code != http.StatusNoContent || len(response.Body) != 0 {
		t.Fatalf("delete status = %d, want 204 with no body: %s", response.Code, response.Body)
	}
	if env.storage.Len() != 0 || env.trailers.Len() != 0 {
		t.Errorf("after delete objects = %d, rows = %d, want 0, 0", env.storage.Len(), env.trailers.Len())
	}
	env.assertReads(t, id, http.StatusNotFound, "not_found")
	if !strings.Contains(logs.String(), "trailer deleted: movie_id="+id) {
		t.Errorf("log = %q, want a delete entry with movie_id=%s", logs, id)
	}

	response = env.do(http.MethodDelete, "/api/v1/movies/"+id+"/trailer", "", editor)
	if response.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404: %s", response.Code, response.Body)
	}
	assertRBACError(t, response, "not_found")
}

func TestTrailer_DeleteAuthAndID(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	viewer := env.tokenFor(t, model.RoleViewer)
	id := env.uploadMovieWithTrailer(t, editor)

	tests := []struct {
		name, path, token, code string
		status                  int
	}{
		{name: "no token", path: id, status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "viewer", path: id, token: viewer, status: http.StatusForbidden, code: "forbidden"},
		{name: "malformed id", path: "abc", token: editor, status: http.StatusBadRequest, code: "invalid_id"},
	}
	for _, test := range tests {
		response := env.do(http.MethodDelete, "/api/v1/movies/"+test.path+"/trailer", "", test.token)
		if response.Code != test.status {
			t.Errorf("%s: status = %d, want %d: %s", test.name, response.Code, test.status, response.Body)
			continue
		}
		assertRBACError(t, response, test.code)
	}
	if env.trailers.Len() != 1 {
		t.Errorf("rows = %d, want 1", env.trailers.Len())
	}
}

func TestTrailer_DeleteStaleRecord(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	for name, lose := range map[string]func(id string){
		"lost object": func(id string) { env.storage.DropObject("trailers/" + id + ".mp4") },
		"lost bucket": func(string) { env.storage.DropBucket() },
	} {
		id := env.uploadMovieWithTrailer(t, editor)
		lose(id)
		if response := env.do(http.MethodDelete, "/api/v1/movies/"+id+"/trailer", "", editor); response.Code != http.StatusNoContent {
			t.Fatalf("%s: delete status = %d, want 204: %s", name, response.Code, response.Body)
		}
		env.assertReads(t, id, http.StatusNotFound, "not_found")
		if response := env.upload(t, id, editor, mp4Bytes(200)); response.Code != http.StatusCreated {
			t.Errorf("%s: upload after cleanup status = %d, want 201: %s", name, response.Code, response.Body)
		}
	}
}

func TestTrailer_StorageUnavailable(t *testing.T) {
	env := newTrailerTestEnv(t)
	editor := env.tokenFor(t, model.RoleEditor)
	id := env.uploadMovieWithTrailer(t, editor)
	other := movieIDText(env.createMovie(t, editor))
	logs := captureLog(t)

	env.storage.SetUnavailable(true)
	responses := map[string]authTestResponse{
		"upload":   env.upload(t, other, editor, mp4Bytes(100)),
		"replace":  env.upload(t, id, editor, mp4Bytes(100)),
		"metadata": env.get("/api/v1/movies/"+id+"/trailer", ""),
		"stream":   env.get("/api/v1/movies/"+id+"/trailer/video", ""),
		"delete":   env.do(http.MethodDelete, "/api/v1/movies/"+id+"/trailer", "", editor),
	}
	for name, response := range responses {
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503: %s", name, response.Code, response.Body)
			continue
		}
		assertRBACError(t, response, "storage_unavailable")
	}
	if env.trailers.Len() != 1 {
		t.Errorf("rows after failed delete = %d, want 1", env.trailers.Len())
	}
	if response := env.get("/api/v1/movies", ""); response.Code != http.StatusOK {
		t.Errorf("GET /api/v1/movies status = %d, want 200", response.Code)
	}
	if !strings.Contains(logs.String(), "trailer storage error: movie_id="+id) {
		t.Errorf("log = %q, want a storage error with movie_id=%s", logs, id)
	}

	env.storage.SetUnavailable(false)
	if response := env.get("/api/v1/movies/"+id+"/trailer", ""); response.Code != http.StatusOK {
		t.Errorf("metadata after recovery status = %d, want 200: %s", response.Code, response.Body)
	}
}

func TestTrailer_MovieDeleteRemovesTrailer(t *testing.T) {
	env := newTrailerTestEnv(t)
	admin := env.tokenFor(t, model.RoleAdmin)
	id := env.uploadMovieWithTrailer(t, admin)

	if response := env.do(http.MethodDelete, "/api/v1/movies/"+id, "", admin); response.Code != http.StatusNoContent {
		t.Fatalf("movie delete status = %d, want 204: %s", response.Code, response.Body)
	}
	if env.storage.Len() != 0 || env.trailers.Len() != 0 {
		t.Errorf("after movie delete objects = %d, rows = %d, want 0, 0", env.storage.Len(), env.trailers.Len())
	}
}

func TestTrailer_MovieDeleteIgnoresStorageFailure(t *testing.T) {
	env := newTrailerTestEnv(t)
	admin := env.tokenFor(t, model.RoleAdmin)
	id := env.uploadMovieWithTrailer(t, admin)
	logs := captureLog(t)

	env.storage.SetUnavailable(true)
	if response := env.do(http.MethodDelete, "/api/v1/movies/"+id, "", admin); response.Code != http.StatusNoContent {
		t.Fatalf("movie delete status = %d, want 204: %s", response.Code, response.Body)
	}
	if env.trailers.Len() != 0 {
		t.Errorf("rows after movie delete = %d, want 0", env.trailers.Len())
	}
	if !strings.Contains(logs.String(), "trailer object not removed: movie_id="+id) {
		t.Errorf("log = %q, want an object cleanup failure with movie_id=%s", logs, id)
	}
}

type failingUpsertRepository struct {
	*repository.InMemoryTrailerRepository
}

func (failingUpsertRepository) Upsert(context.Context, model.Trailer) (bool, error) {
	return false, errors.New("database is down")
}

func TestTrailer_UploadRepositoryFailure(t *testing.T) {
	env := newTrailerTestEnvWithRepository(t, failingUpsertRepository{repository.NewInMemoryTrailerRepository()})
	editor := env.tokenFor(t, model.RoleEditor)
	id := movieIDText(env.createMovie(t, editor))

	response := env.upload(t, id, editor, mp4Bytes(100))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("upload status = %d, want 500: %s", response.Code, response.Body)
	}
	assertRBACError(t, response, "internal_server_error")
}
