package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"homework/internal/model"
	"homework/internal/service"
)

type AuthService interface {
	Register(ctx context.Context, request model.RegisterRequest) (model.UserResponse, error)
	Login(ctx context.Context, request model.LoginRequest) (model.TokenResponse, error)
}

type AuthHandler struct {
	service  AuthService
	renderer *Renderer
	secure   bool
}

func NewAuthHandler(authService AuthService, renderer *Renderer, secure bool) *AuthHandler {
	return &AuthHandler{service: authService, renderer: renderer, secure: secure}
}

func (handler *AuthHandler) LoginPage(c *gin.Context) {
	handler.rendererPage(c, http.StatusOK, "login", LoginPage{
		BasePage: BasePage{Title: "Log in"},
		Next:     safeNext(c.Query("next")),
	})
}

func (handler *AuthHandler) RegisterPage(c *gin.Context) {
	handler.rendererPage(c, http.StatusOK, "register", RegisterPage{
		BasePage: BasePage{Title: "Register"},
	})
}

func (handler *AuthHandler) Login(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		handler.loginFailure(c)
		return
	}
	response, err := handler.service.Login(c.Request.Context(), model.LoginRequest{
		Email: c.Request.PostForm.Get("email"), Password: c.Request.PostForm.Get("password"),
	})
	if errors.Is(err, service.ErrUnauthorized) {
		handler.loginFailure(c)
		return
	}
	if err != nil {
		log.Printf("web login failed: %v", err)
		handler.rendererPage(c, http.StatusInternalServerError, "error", ErrorPage{
			StatusText: "Internal server error", Message: "Please try again later.",
		})
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: accessTokenCookie, Value: response.AccessToken, Path: "/",
		MaxAge: int(response.ExpiresIn), HttpOnly: true, Secure: handler.secure,
		SameSite: http.SameSiteLaxMode,
	})
	nextCandidate := c.Request.PostForm.Get("next")
	if nextCandidate == "" {
		nextCandidate = c.Query("next")
	}
	c.Redirect(http.StatusSeeOther, safeNext(nextCandidate))
}

func (handler *AuthHandler) Register(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		handler.registerFailure(c, "", "", "Registration details are invalid.")
		return
	}
	email := c.Request.PostForm.Get("email")
	name := c.Request.PostForm.Get("name")
	_, err := handler.service.Register(c.Request.Context(), model.RegisterRequest{
		Email: email, Name: name, Password: c.Request.PostForm.Get("password"),
	})
	if err != nil {
		var validation service.ValidationError
		if errors.As(err, &validation) {
			handler.registerFailure(c, email, name, validation.Message)
			return
		}
		log.Printf("web registration failed: %v", err)
		handler.rendererPage(c, http.StatusInternalServerError, "error", ErrorPage{
			StatusText: "Internal server error", Message: "Please try again later.",
		})
		return
	}
	c.Redirect(http.StatusSeeOther, "/catalog?notice=registered")
}

func (handler *AuthHandler) Logout(c *gin.Context) {
	clearAccessCookie(c, handler.secure)
	c.Redirect(http.StatusSeeOther, "/catalog")
}

func (handler *AuthHandler) loginFailure(c *gin.Context) {
	handler.rendererPage(c, http.StatusUnauthorized, "login", LoginPage{
		BasePage: BasePage{Title: "Log in"},
		Next:     safeNext(c.Request.PostForm.Get("next")),
		Error:    "Invalid email or password",
	})
}

func (handler *AuthHandler) registerFailure(c *gin.Context, email, name, message string) {
	handler.rendererPage(c, http.StatusUnprocessableEntity, "register", RegisterPage{
		BasePage: BasePage{Title: "Register"},
		Email:    email,
		Name:     name,
		Error:    message,
	})
}

func (handler *AuthHandler) rendererPage(c *gin.Context, status int, name string, data any) {
	handler.renderer.Page(c, status, name, data)
}

func safeNext(candidate string) string {
	if candidate == "" || !strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "//") || strings.Contains(candidate, "\\") {
		return "/catalog"
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.ContainsAny(candidate, "\r\n") {
		return "/catalog"
	}
	return candidate
}
