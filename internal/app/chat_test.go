package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"homework/internal/app"
	"homework/internal/auth"
	"homework/internal/chat"
	"homework/internal/model"
	"homework/internal/repository"
)

const chatTestOrigin = "http://chat.example.test"

type chatTestEnv struct {
	server *httptest.Server
	token  string
	movies []uint32
}

func newChatTestEnv(t *testing.T) *chatTestEnv {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hub := chat.NewHub()
	go hub.Run(ctx)
	return newChatTestEnvWithHub(t, hub)
}

// newChatTestEnvWithHub serves hub, which the caller runs. Every env has the same movie IDs.
func newChatTestEnvWithHub(t *testing.T, hub *chat.Hub) *chatTestEnv {
	t.Helper()

	movies := repository.NewInMemoryMovieRepositoryAdapter()
	env := &chatTestEnv{}
	for _, title := range []string{"First", "Second"} {
		movie, err := movies.Create(context.Background(), model.Movie{
			Title: title, Director: "Director", Country: "Country", Genre: "Drama",
		})
		if err != nil {
			t.Fatalf("movies.Create() error = %v", err)
		}
		env.movies = append(env.movies, movie.ID)
	}
	tokens, err := auth.NewTokenManager(authTestSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	env.token, err = tokens.Issue(model.User{ID: 7, Role: model.RoleViewer})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	env.server = httptest.NewServer(app.NewRouterWithDeps(app.Deps{
		Movies: movies, Tokens: tokens, Chat: hub, ChatAllowedOrigins: []string{chatTestOrigin},
	}))
	t.Cleanup(env.server.Close)
	return env
}

func (e *chatTestEnv) dial(path, origin, token string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	header.Set("Origin", origin)
	if token != "" {
		// The browser path: the web UI session cookie.
		header.Set("Cookie", "access_token="+token)
	}
	return websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.server.URL, "http")+path, header)
}

// join connects and waits for its own echo, which proves the hub has registered the client.
func (e *chatTestEnv) join(t *testing.T, movieID uint32, name string) *websocket.Conn {
	t.Helper()

	conn, response, err := e.dial(fmt.Sprintf("/ws/movies/%d", movieID), chatTestOrigin, e.token)
	if err != nil {
		t.Fatalf("dial: %v (response: %v)", err, response)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.WriteJSON(map[string]string{"text": name + " joined"}); err != nil {
		t.Fatalf("write join message: %v", err)
	}
	readChatText(t, conn, name+" joined")
	return conn
}

type chatMessage struct {
	Room   uint32    `json:"room"`
	Author string    `json:"author"`
	Text   string    `json:"text"`
	Time   time.Time `json:"time"`
	Error  string    `json:"error"`
}

// readChatText skips earlier messages (such as other clients joining) until want arrives.
func readChatText(t *testing.T, conn *websocket.Conn, want string) chatMessage {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var message chatMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("waiting for %q: %v", want, err)
		}
		if message.Text == want {
			return message
		}
	}
}

func TestChatBroadcastsOnlyWithinRoom(t *testing.T) {
	env := newChatTestEnv(t)
	sender := env.join(t, env.movies[0], "sender")
	receiver := env.join(t, env.movies[0], "receiver")
	other := env.join(t, env.movies[1], "other")

	if err := sender.WriteJSON(map[string]string{"text": "  hello room  ", "author": "spoofed"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	for name, conn := range map[string]*websocket.Conn{"sender": sender, "receiver": receiver} {
		message := readChatText(t, conn, "hello room")
		if message.Room != env.movies[0] || message.Author != "user-7" || message.Time.IsZero() {
			t.Errorf("%s got %+v, want room %d and author from the JWT", name, message, env.movies[0])
		}
	}

	_ = other.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var leaked chatMessage
	if err := other.ReadJSON(&leaked); err == nil {
		t.Fatalf("other room received %+v", leaked)
	}
}

func TestChatRejectsInvalidText(t *testing.T) {
	env := newChatTestEnv(t)
	conn := env.join(t, env.movies[0], "client")

	for _, text := range []string{"   ", strings.Repeat("я", 501)} {
		if err := conn.WriteJSON(map[string]string{"text": text}); err != nil {
			t.Fatalf("write: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var message chatMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read: %v", err)
		}
		if message.Error == "" || message.Text != "" {
			t.Errorf("text of %d runes got %+v, want an error message", len([]rune(text)), message)
		}
	}
	if err := conn.WriteJSON(map[string]string{"text": strings.Repeat("я", 500)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	readChatText(t, conn, strings.Repeat("я", 500))
}

func TestChatHandshakeErrors(t *testing.T) {
	env := newChatTestEnv(t)
	tests := []struct {
		name   string
		path   string
		origin string
		token  string
		status int
		code   string
	}{
		{name: "missing token", path: "/ws/movies/1", origin: chatTestOrigin, status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "invalid token", path: "/ws/movies/1", origin: chatTestOrigin, token: "not-a-jwt", status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "invalid id", path: "/ws/movies/abc", origin: chatTestOrigin, token: env.token, status: http.StatusBadRequest, code: "invalid_id"},
		{name: "unknown movie", path: "/ws/movies/999", origin: chatTestOrigin, token: env.token, status: http.StatusNotFound, code: "not_found"},
		{name: "bad origin", path: "/ws/movies/1", origin: "http://evil.example.test", token: env.token, status: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, response, err := env.dial(tt.path, tt.origin, tt.token)
			if err == nil {
				_ = conn.Close()
				t.Fatal("handshake succeeded, want an error")
			}
			if !errors.Is(err, websocket.ErrBadHandshake) || response == nil {
				t.Fatalf("dial error = %v, want a bad handshake response", err)
			}
			if response.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", response.StatusCode, tt.status)
			}
			body, _ := io.ReadAll(response.Body)
			if tt.code != "" && !strings.Contains(string(body), `"code":"`+tt.code+`"`) {
				t.Errorf("body = %s, want error code %q", body, tt.code)
			}
		})
	}
}

func TestChatRouteIsNotRegisteredWithoutHub(t *testing.T) {
	for name, router := range map[string]http.Handler{
		"NewRouter":                 app.NewRouter(),
		"NewRouterWithDeps no chat": app.NewRouterWithDeps(app.Deps{}),
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ws/movies/1", nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s: GET /ws/movies/1 status = %d, want 404", name, response.Code)
		}
	}
}

func TestChatClientsDoNotLeakGoroutines(t *testing.T) {
	env := newChatTestEnv(t)
	baseline := runtime.NumGoroutine()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _, err := env.dial(fmt.Sprintf("/ws/movies/%d", env.movies[0]), chatTestOrigin, env.token)
			if err != nil {
				t.Errorf("dial: %v", err)
				return
			}
			_ = conn.Close()
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want <= baseline %d", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestChatAuthorUsesAccountName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hub := chat.NewHub()
	go hub.Run(ctx)

	movies := repository.NewInMemoryMovieRepositoryAdapter()
	movie, err := movies.Create(ctx, model.Movie{Title: "First", Director: "Director", Country: "Country", Genre: "Drama"})
	if err != nil {
		t.Fatalf("movies.Create() error = %v", err)
	}
	users := repository.NewInMemoryUserRepository()
	user := model.User{Email: "named@example.com", Name: "Named Viewer", PasswordHash: "hash", Role: model.RoleViewer}
	if err := users.Create(ctx, &user); err != nil {
		t.Fatalf("users.Create() error = %v", err)
	}
	tokens, err := auth.NewTokenManager(authTestSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	token, err := tokens.Issue(user)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	env := &chatTestEnv{token: token, movies: []uint32{movie.ID}}
	env.server = httptest.NewServer(app.NewRouterWithDeps(app.Deps{
		Movies: movies, Users: users, Tokens: tokens, Chat: hub, ChatAllowedOrigins: []string{chatTestOrigin},
	}))
	t.Cleanup(env.server.Close)

	conn := env.join(t, movie.ID, "named")
	if err := conn.WriteJSON(map[string]string{"text": "hi"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if message := readChatText(t, conn, "hi"); message.Author != "Named Viewer" {
		t.Fatalf("author = %q, want account name", message.Author)
	}
}
