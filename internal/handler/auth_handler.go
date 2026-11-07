package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"homework/internal/apperr"
	"homework/internal/model"
	"homework/internal/service"
)

type AuthService interface {
	Register(ctx context.Context, request model.RegisterRequest) (model.UserResponse, error)
	Login(ctx context.Context, request model.LoginRequest) (model.TokenResponse, error)
}

type AuthHandler struct {
	service AuthService
}

func NewAuthHandler(authService AuthService) *AuthHandler {
	return &AuthHandler{service: authService}
}

// Register godoc
// @Summary Register a user
// @Description Creates a viewer account; the role cannot be set by the client.
// @Tags auth
// @Accept json
// @Produce json
// @Param credentials body model.RegisterRequest true "Registration payload"
// @Success 201 {object} model.UserResponse
// @Failure 409 {object} model.ErrorResponse
// @Failure 422 {object} model.ErrorResponse
// @Failure 500 {object} model.ErrorResponse
// @Router /auth/register [post]
func (handler *AuthHandler) Register(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		apperr.WriteError(c, service.ValidationError{Message: "Request body could not be read"})
		return
	}
	request, err := decodeAuthRequest[model.RegisterRequest](body)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	user, err := handler.service.Register(c.Request.Context(), request)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, user)
}

// Login godoc
// @Summary Log in
// @Description Returns a Bearer access token for a valid email and password.
// @Tags auth
// @Accept json
// @Produce json
// @Param credentials body model.LoginRequest true "Login payload"
// @Success 200 {object} model.TokenResponse
// @Failure 401 {object} model.ErrorResponse
// @Failure 422 {object} model.ErrorResponse
// @Failure 500 {object} model.ErrorResponse
// @Router /auth/login [post]
func (handler *AuthHandler) Login(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		apperr.WriteError(c, service.ValidationError{Message: "Request body could not be read"})
		return
	}
	request, err := decodeAuthRequest[model.LoginRequest](body)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	response, err := handler.service.Login(c.Request.Context(), request)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func decodeAuthRequest[T any](body []byte) (T, error) {
	var request T
	decoder := json.NewDecoder(bytes.NewReader(body))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return request, service.ValidationError{Message: "Request body must be a JSON object"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return request, service.ValidationError{Message: "Request body must contain one JSON object"}
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return request, service.ValidationError{Message: "Request body contains invalid field types"}
	}
	return request, nil
}
