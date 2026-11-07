package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"homework/internal/auth"
	apimiddleware "homework/internal/middleware"
	"homework/internal/repository"
)

const (
	accessTokenCookie = "access_token"
	csrfTokenCookie   = "csrf_token"
	csrfContextKey    = "csrf_token"
)

func OptionalAuth(tokens *auth.TokenManager, users repository.UserRepository, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie(accessTokenCookie)
		if err != nil {
			for _, presented := range c.Request.Cookies() {
				if presented.Name == accessTokenCookie {
					clearAccessCookie(c, secure)
					break
				}
			}
			c.Next()
			return
		}
		if cookie == "" {
			clearAccessCookie(c, secure)
			c.Next()
			return
		}
		claims, err := tokens.Parse(cookie)
		if err != nil {
			clearAccessCookie(c, secure)
			c.Next()
			return
		}
		user, err := users.FindByID(c.Request.Context(), claims.UserID)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				log.Printf("load web session user: %v", err)
			}
			clearAccessCookie(c, secure)
			c.Next()
			return
		}
		apimiddleware.SetPrincipal(c, claims.UserID, user.Role)
		apimiddleware.SetDisplayName(c, user.Name)
		c.Next()
	}
}

func CSRF(secure bool, renderer *Renderer) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(csrfTokenCookie)
		if err != nil || token == "" {
			token, err = newCSRFToken()
			if err != nil {
				log.Printf("create CSRF token: %v", err)
				renderWebError(c, http.StatusInternalServerError, "Internal server error", "Please try again later.", renderer)
				c.Abort()
				return
			}
			http.SetCookie(c.Writer, &http.Cookie{
				Name: csrfTokenCookie, Value: token, Path: "/", HttpOnly: true,
				Secure: secure, SameSite: http.SameSiteLaxMode,
			})
		}
		c.Set(csrfContextKey, token)

		if changesState(c.Request.Method) && !validCSRFRequest(c, token) {
			if isHTMX(c) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			renderWebError(c, http.StatusForbidden, "Forbidden", "Your request could not be verified.", renderer)
			c.Abort()
			return
		}
		c.Next()
	}
}

func RequireLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := apimiddleware.UserIDFrom(c); ok {
			c.Next()
			return
		}
		if isHTMX(c) {
			c.Header("HX-Redirect", "/login")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		next := c.Request.URL.RequestURI()
		c.Redirect(http.StatusSeeOther, "/login?next="+url.QueryEscape(next))
		c.Abort()
	}
}

func RequireRole(renderer *Renderer, roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, ok := apimiddleware.RoleFrom(c)
		if !ok || role == "" {
			if isHTMX(c) {
				c.Header("HX-Redirect", "/login")
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			next := c.Request.URL.RequestURI()
			c.Redirect(http.StatusSeeOther, "/login?next="+url.QueryEscape(next))
			c.Abort()
			return
		}
		if !apimiddleware.RoleAllowed(role, roles...) {
			if isHTMX(c) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			renderWebError(c, http.StatusForbidden, "Forbidden", "You are not allowed to do that.", renderer)
			c.Abort()
			return
		}
		c.Next()
	}
}

func CSRFTokenFrom(c *gin.Context) string {
	value, _ := c.Get(csrfContextKey)
	token, _ := value.(string)
	return token
}

func clearAccessCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: accessTokenCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func newCSRFToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func changesState(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func validCSRFRequest(c *gin.Context, cookieToken string) bool {
	origin := c.GetHeader("Origin")
	parsed, err := url.Parse(origin)
	if origin == "" || err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, c.Request.Host) {
		return false
	}
	submitted := c.GetHeader("X-CSRF-Token")
	if submitted == "" {
		if err := c.Request.ParseForm(); err != nil {
			return false
		}
		submitted = c.Request.PostForm.Get("csrf_token")
	}
	return subtle.ConstantTimeCompare([]byte(submitted), []byte(cookieToken)) == 1
}

func isHTMX(c *gin.Context) bool {
	return strings.EqualFold(c.GetHeader("HX-Request"), "true")
}

func renderWebError(c *gin.Context, status int, title, message string, renderer *Renderer) {
	if renderer == nil {
		c.AbortWithStatus(status)
		return
	}
	renderer.Page(c, status, "error", ErrorPage{
		BasePage:   BasePage{Title: title, CSRFToken: CSRFTokenFrom(c)},
		StatusText: title,
		Message:    message,
	})
}
