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
	var trailerUnavailableError service.TrailerUnavailableError
	var payloadTooLargeError service.PayloadTooLargeError
	var unsupportedMediaTypeError service.UnsupportedMediaTypeError
	var rangeNotSatisfiableError service.RangeNotSatisfiableError
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
	case errors.As(err, &trailerUnavailableError):
		apiError = HTTPError{Status: http.StatusNotFound, Code: "trailer_unavailable", Message: trailerUnavailableError.Error()}
	case errors.As(err, &payloadTooLargeError):
		apiError = HTTPError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: payloadTooLargeError.Error()}
	case errors.As(err, &unsupportedMediaTypeError):
		apiError = HTTPError{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: unsupportedMediaTypeError.Error()}
	case errors.As(err, &rangeNotSatisfiableError):
		apiError = HTTPError{Status: http.StatusRequestedRangeNotSatisfiable, Code: "range_not_satisfiable", Message: rangeNotSatisfiableError.Error()}
	case errors.Is(err, service.ErrStorageUnavailable):
		apiError = HTTPError{Status: http.StatusServiceUnavailable, Code: "storage_unavailable", Message: "Trailer storage is unavailable, try again later"}
	case errors.As(err, &apiError):
	default:
		apiError = HTTPError{Status: http.StatusInternalServerError, Code: "internal_server_error", Message: "Internal server error"}
	}
	if apiError.Status == http.StatusUnauthorized {
		c.Header("WWW-Authenticate", "Bearer")
	}
	c.AbortWithStatusJSON(apiError.Status, gin.H{"error": Error{Code: apiError.Code, Message: apiError.Message}})
}
