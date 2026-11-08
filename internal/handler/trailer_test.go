package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"homework/internal/repository"
	"homework/internal/service"
	"homework/internal/storage"
)

func TestTrailerUploadWithoutUserIsUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	trailers := NewTrailerHandler(service.NewTrailerService(
		repository.NewInMemoryMovieRepositoryAdapter(), repository.NewInMemoryTrailerRepository(), storage.NewMemory()))
	router := gin.New()
	router.PUT("/movies/:id/trailer", trailers.upload)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/movies/1/trailer", strings.NewReader("x")))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body.String(), `"code":"unauthorized"`) {
		t.Errorf("body = %s, want unauthorized error", response.Body)
	}
}
