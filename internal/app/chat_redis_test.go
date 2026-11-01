package app_test

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"homework/internal/chat"
)

// newTestRedis connects to TEST_REDIS_URL and clears the history of the test rooms.
func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("invalid TEST_REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(options)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		t.Fatalf("ping Redis: %v", err)
	}
	if err := rdb.Del(context.Background(), "chat:history:movie:1", "chat:history:movie:2").Err(); err != nil {
		t.Fatalf("clear chat history: %v", err)
	}
	return rdb
}

// newRedisChatEnv is one instance: its own hub and server on the shared Redis.
func newRedisChatEnv(t *testing.T, rdb *redis.Client, historySize int) (*chatTestEnv, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hub := chat.NewRedisHub(rdb, historySize)
	go hub.Run(ctx)
	return newChatTestEnvWithHub(t, hub), cancel
}

func TestRedisChatSharesMessagesBetweenInstances(t *testing.T) {
	rdb := newTestRedis(t)
	t.Cleanup(func() { _ = rdb.Close() })
	first, _ := newRedisChatEnv(t, rdb, 50)
	second, _ := newRedisChatEnv(t, rdb, 50)

	sender := first.join(t, first.movies[0], "sender")
	receiver := second.join(t, second.movies[0], "receiver")
	other := second.join(t, second.movies[1], "other")

	if err := sender.WriteJSON(map[string]string{"text": "hello across"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	message := readChatText(t, receiver, "hello across")
	if message.Room != first.movies[0] || message.Author != "user-7" {
		t.Errorf("receiver got %+v", message)
	}

	// The sender gets its message once, through the subscription.
	if err := sender.WriteJSON(map[string]string{"text": "marker"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = sender.SetReadDeadline(time.Now().Add(2 * time.Second))
	seen := 0
	for {
		var got chatMessage
		if err := sender.ReadJSON(&got); err != nil {
			t.Fatalf("read: %v", err)
		}
		if got.Text == "marker" {
			break
		}
		if got.Text == "hello across" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("sender got its message %d times, want 1", seen)
	}

	_ = other.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var leaked chatMessage
	if err := other.ReadJSON(&leaked); err == nil {
		t.Fatalf("other room received %+v", leaked)
	}
}

func TestRedisChatSendsHistoryInOrder(t *testing.T) {
	rdb := newTestRedis(t)
	t.Cleanup(func() { _ = rdb.Close() })
	env, _ := newRedisChatEnv(t, rdb, 3)

	sender := env.join(t, env.movies[0], "sender")
	for i := 1; i <= 5; i++ {
		text := fmt.Sprintf("message %d", i)
		if err := sender.WriteJSON(map[string]string{"text": text}); err != nil {
			t.Fatalf("write: %v", err)
		}
		readChatText(t, sender, text)
	}

	conn, response, err := env.dial(fmt.Sprintf("/ws/movies/%d", env.movies[0]), chatTestOrigin, env.token)
	if err != nil {
		t.Fatalf("dial: %v (response: %v)", err, response)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for _, want := range []string{"message 3", "message 4", "message 5"} {
		var message chatMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read history: %v", err)
		}
		if message.Text != want || message.Room != env.movies[0] {
			t.Fatalf("history got %+v, want %q", message, want)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var extra chatMessage
	if err := conn.ReadJSON(&extra); err == nil {
		t.Fatalf("got %+v after the history, want only 3 messages", extra)
	}
}

func TestRedisChatStopsWithoutLeakingGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()
	rdb := newTestRedis(t)

	first, stopFirst := newRedisChatEnv(t, rdb, 50)
	second, stopSecond := newRedisChatEnv(t, rdb, 50)
	sender := first.join(t, first.movies[0], "sender")
	second.join(t, second.movies[0], "receiver")
	if err := sender.WriteJSON(map[string]string{"text": "bye"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	readChatText(t, sender, "bye")

	// Stop the instances while clients are still connected; the hubs must close them.
	stopFirst()
	stopSecond()
	first.server.Close()
	second.server.Close()
	_ = rdb.Close()

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines = %d, want <= baseline %d\n%s", runtime.NumGoroutine(), baseline, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}
