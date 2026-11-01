package chat

import (
	"strconv"
	"testing"
	"time"
)

// A real socket would need megabytes of unread data before the kernel buffer fills,
// so this drives the hub directly with a client whose send buffer nobody drains.
func TestHubDropsSlowClientWithoutBlockingOthers(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	slow := &Client{room: 1, send: make(chan []byte, sendBufferSize)}
	fast := &Client{room: 1, send: make(chan []byte, sendBufferSize)}
	hub.register <- slow
	hub.register <- fast

	for i := 0; i < sendBufferSize*3; i++ {
		hub.broadcast <- envelope{room: 1, data: []byte(strconv.Itoa(i))}
		select {
		case got := <-fast.send:
			if string(got) != strconv.Itoa(i) {
				t.Fatalf("fast client got %q, want %d", got, i)
			}
		case <-time.After(time.Second):
			t.Fatalf("fast client did not receive message %d", i)
		}
	}

	received := 0
	timeout := time.After(time.Second)
	for {
		select {
		case _, ok := <-slow.send:
			if !ok {
				if received != sendBufferSize {
					t.Errorf("slow client buffered %d messages, want %d", received, sendBufferSize)
				}
				hub.unregister <- slow // A late unregister must not close send twice.
				return
			}
			received++
		case <-timeout:
			t.Fatal("slow client was not dropped")
		}
	}
}
