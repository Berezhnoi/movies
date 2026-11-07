package chat

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"homework/internal/apperr"
	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
	"homework/internal/service"
)

// accessTokenCookie is the web UI session cookie, because browsers cannot set headers on a WebSocket handshake.
const accessTokenCookie = "access_token"

// UserFinder resolves the display name shown next to chat messages.
type UserFinder interface {
	FindByID(ctx context.Context, id uint64) (model.User, error)
}

type Handler struct {
	hub      *Hub
	tokens   *auth.TokenManager
	users    UserFinder
	movies   *service.MovieService
	upgrader websocket.Upgrader
}

func NewHandler(hub *Hub, tokens *auth.TokenManager, users UserFinder, movies *service.MovieService, allowedOrigins []string) *Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = true
	}
	return &Handler{
		hub:    hub,
		tokens: tokens,
		users:  users,
		movies: movies,
		upgrader: websocket.Upgrader{
			// A WebSocket is not covered by CORS, so this is the only cross-site protection for the cookie.
			CheckOrigin: func(r *http.Request) bool {
				return allowed[r.Header.Get("Origin")]
			},
		},
	}
}

func (handler *Handler) Serve(c *gin.Context) {
	claims, err := handler.tokens.Parse(requestToken(c))
	if err != nil {
		apperr.WriteError(c, apperr.HTTPError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "authentication required"})
		return
	}
	id, err := service.ParseMovieID(c.Param("id"))
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	if _, err := handler.movies.GetByIDWithError(c.Request.Context(), id); err != nil {
		apperr.WriteError(c, err)
		return
	}
	// Upgrade writes its own 403 when CheckOrigin fails.
	conn, err := handler.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	// History goes straight to conn: writePump has not started, so this is still the only writer.
	history, err := handler.hub.broker.History(c.Request.Context(), id)
	if err != nil {
		log.Printf("load chat history: %v", err)
	}
	for _, message := range history {
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
			_ = conn.Close()
			return
		}
	}
	client := &Client{
		hub:    handler.hub,
		conn:   conn,
		room:   id,
		author: handler.authorName(c.Request.Context(), claims.UserID),
		send:   make(chan []byte, sendBufferSize),
	}
	select {
	case handler.hub.register <- client:
	case <-handler.hub.done:
		_ = conn.Close()
		return
	}
	go client.writePump()
	go client.readPump()
}

// authorName falls back to the user id when the account has no name or cannot be loaded.
func (handler *Handler) authorName(ctx context.Context, userID uint64) string {
	user, err := handler.users.FindByID(ctx, userID)
	if err == nil {
		if name := strings.TrimSpace(user.Name); name != "" {
			return name
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		log.Printf("load chat author: %v", err)
	}
	return "user-" + strconv.FormatUint(userID, 10)
}

// requestToken accepts the API's Bearer header, falling back to the web UI cookie.
func requestToken(c *gin.Context) string {
	if scheme, token, found := strings.Cut(c.GetHeader("Authorization"), " "); found && strings.EqualFold(scheme, "Bearer") {
		return token
	}
	token, _ := c.Cookie(accessTokenCookie)
	return token
}
