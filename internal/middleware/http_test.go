package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestBodyLimitExemptsOnlyMethodAndPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestBodyLimit("PUT /upload/:id"))
	readBody := func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	}
	router.PUT("/upload/:id", readBody)
	router.POST("/upload/:id", readBody)
	router.PATCH("/upload/:id", readBody)
	router.PUT("/other/:id", readBody)

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodPut, path: "/upload/1", want: http.StatusOK},
		{method: http.MethodPost, path: "/upload/1", want: http.StatusRequestEntityTooLarge},
		{method: http.MethodPatch, path: "/upload/1", want: http.StatusRequestEntityTooLarge},
		{method: http.MethodPut, path: "/other/1", want: http.StatusRequestEntityTooLarge},
	}
	body := strings.Repeat("x", maxRequestBodySize+1)
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(body)))
			if response.Code != test.want {
				t.Errorf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}
