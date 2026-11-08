package middleware

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"homework/internal/apperr"
	"homework/internal/auth"
	"homework/internal/repository"
)

const (
	userIDContextKey = "user_id"
	roleContextKey   = "role"
)

// AuthMiddleware loads the user on every request so role changes and
// deletions take effect immediately instead of when the token expires.
func AuthMiddleware(tokens *auth.TokenManager, users repository.UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			abortUnauthorized(c, "authentication required")
			return
		}
		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || token != strings.TrimSpace(token) {
			abortUnauthorized(c, "invalid or expired token")
			return
		}
		claims, err := tokens.Parse(token)
		if err != nil {
			abortUnauthorized(c, "invalid or expired token")
			return
		}
		user, err := users.FindByID(c.Request.Context(), claims.UserID)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				log.Printf("load API user: %v", err)
				apperr.WriteError(c, err)
				return
			}
			abortUnauthorized(c, "invalid or expired token")
			return
		}
		SetPrincipal(c, claims.UserID, user.Role)
		c.Next()
	}
}

func UserIDFrom(c *gin.Context) (uint, bool) {
	value, ok := c.Get(userIDContextKey)
	if !ok {
		return 0, false
	}
	id, ok := value.(uint64)
	return uint(id), ok
}

func SetPrincipal(c *gin.Context, userID uint64, role string) {
	c.Set(userIDContextKey, userID)
	c.Set(roleContextKey, role)
}

func RoleFrom(c *gin.Context) (string, bool) {
	value, ok := c.Get(roleContextKey)
	if !ok {
		return "", false
	}
	role, ok := value.(string)
	return role, ok
}

func SetDisplayName(c *gin.Context, name string) {
	c.Set("user_name", name)
}

func DisplayNameFrom(c *gin.Context) (string, bool) {
	value, ok := c.Get("user_name")
	if !ok {
		return "", false
	}
	name, ok := value.(string)
	return name, ok
}

func abortUnauthorized(c *gin.Context, message string) {
	apperr.WriteError(c, apperr.HTTPError{
		Status:  http.StatusUnauthorized,
		Code:    "unauthorized",
		Message: message,
	})
}
