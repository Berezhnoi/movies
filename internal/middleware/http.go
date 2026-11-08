package middleware

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

const maxRequestBodySize = 1 << 20

func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				writeRequestLog(c, http.StatusInternalServerError, time.Since(startedAt))
				panic(recovered)
			}
			writeRequestLog(c, c.Writer.Status(), time.Since(startedAt))
		}()
		c.Next()
	}
}

// RequestBodyLimit caps request bodies at 1 MiB, except on the exempt routes,
// given as "METHOD /full/:path" (c.FullPath), whose handlers apply their own limit.
func RequestBodyLimit(exemptRoutes ...string) gin.HandlerFunc {
	exempt := make(map[string]bool, len(exemptRoutes))
	for _, route := range exemptRoutes {
		exempt[route] = true
	}
	return func(c *gin.Context) {
		if !exempt[c.Request.Method+" "+c.FullPath()] {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)
		}
		c.Next()
	}
}

func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"code":    "internal_server_error",
						"message": "internal server error",
					},
				})
			}
		}()
		c.Next()
	}
}

func CORS() gin.HandlerFunc {
	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "*"
	}
	return func(c *gin.Context) {
		startedAt := time.Now()
		c.Header("Access-Control-Allow-Origin", allowedOrigin)
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
			c.AbortWithStatus(http.StatusNoContent)
			// The preflight short-circuit does not reach the inner request logger.
			writeRequestLog(c, http.StatusNoContent, time.Since(startedAt))
			return
		}
		c.Next()
	}
}

func writeRequestLog(c *gin.Context, status int, duration time.Duration) {
	log.New(log.Writer(), "", 0).Printf("%s %s %d %s", c.Request.Method, c.Request.URL.Path, status, duration)
}
