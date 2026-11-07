package apperr

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"homework/internal/service"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (apiError HTTPError) Error() string { return apiError.Message }

func WriteError(c *gin.Context, err error) {
	var validationError service.ValidationError
	var notFoundError service.NotFoundError
	var conflictError service.ConflictError
	var invalidIDError service.InvalidIDError
	var invalidPaginationError service.InvalidPaginationError
	var apiError HTTPError
	switch {
	case errors.Is(err, service.ErrUnauthorized):
		apiError = HTTPError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "invalid credentials"}
	case errors.Is(err, service.ErrForbidden):
		apiError = HTTPError{Status: http.StatusForbidden, Code: "forbidden", Message: "Forbidden"}
	case errors.As(err, &validationError):
		apiError = HTTPError{Status: http.StatusUnprocessableEntity, Code: "validation_error", Message: validationError.Message}
	case errors.As(err, &notFoundError):
		apiError = HTTPError{Status: http.StatusNotFound, Code: "not_found", Message: notFoundError.Message}
	case errors.As(err, &conflictError):
		apiError = HTTPError{Status: http.StatusConflict, Code: "conflict", Message: conflictError.Message}
	case errors.As(err, &invalidIDError):
		apiError = HTTPError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "Invalid movie id"}
	case errors.As(err, &invalidPaginationError):
		apiError = HTTPError{Status: http.StatusBadRequest, Code: "invalid_pagination", Message: invalidPaginationError.Message}
	case errors.As(err, &apiError):
	default:
		apiError = HTTPError{Status: http.StatusInternalServerError, Code: "internal_server_error", Message: "Internal server error"}
	}
	if apiError.Status == http.StatusUnauthorized {
		c.Header("WWW-Authenticate", "Bearer")
	}
	c.AbortWithStatusJSON(apiError.Status, gin.H{"error": Error{Code: apiError.Code, Message: apiError.Message}})
}
