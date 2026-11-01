// Package chat implements a per-movie WebSocket chat room, optionally shared between instances via Redis.
package chat

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

// envelope is one outgoing message. A nil to means "everyone in room".
type envelope struct {
	room uint32
	to   *Client
	data []byte
}

// Hub routes messages between clients. Only Run touches rooms, so it needs no mutex.
type Hub struct {
	rooms      map[uint32]map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan envelope
	done       chan struct{} // closed when Run returns, so no sender blocks on a stopped hub
	broker     broker
}

// NewHub is the single-instance chat without history.
func NewHub() *Hub {
	hub := &Hub{
		rooms:      make(map[uint32]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan envelope),
		done:       make(chan struct{}),
	}
	hub.broker = localBroker{hub: hub}
	return hub
}

// NewRedisHub shares messages with other instances and keeps the last historySize messages per room.
func NewRedisHub(rdb *redis.Client, historySize int) *Hub {
	hub := NewHub()
	hub.broker = redisBroker{rdb: rdb, size: int64(historySize)}
	return hub
}

// Run lives until ctx is done; start it once at startup.
func (hub *Hub) Run(ctx context.Context) {
	subscribed := make(chan struct{})
	go func() {
		defer close(subscribed)
		// The subscriber only feeds broadcast; Run stays the only owner of rooms.
		hub.broker.Subscribe(ctx, func(room uint32, data []byte) {
			hub.send(envelope{room: room, data: data})
		})
	}()
	defer func() {
		// Closing every send channel stops the writePumps, which close their connections and end the readPumps.
		for _, clients := range hub.rooms {
			for client := range clients {
				hub.remove(client)
			}
		}
		close(hub.done)
		<-subscribed
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case client := <-hub.register:
			if hub.rooms[client.room] == nil {
				hub.rooms[client.room] = make(map[*Client]bool)
			}
			hub.rooms[client.room][client] = true
		case client := <-hub.unregister:
			hub.remove(client)
		case message := <-hub.broadcast:
			if message.to != nil {
				// The client may already be dropped; its send channel would then be closed.
				if hub.rooms[message.to.room][message.to] {
					hub.deliver(message.to, message.data)
				}
				continue
			}
			for client := range hub.rooms[message.room] {
				hub.deliver(client, message.data)
			}
		}
	}
}

// publish sends a room message through the broker. If Redis fails, the message still reaches this instance.
func (hub *Hub) publish(room uint32, data []byte) {
	if err := hub.broker.Publish(context.Background(), room, data); err != nil {
		log.Printf("publish chat message: %v", err)
		hub.send(envelope{room: room, data: data})
	}
}

// send hands a message to Run, or drops it once Run has stopped.
func (hub *Hub) send(message envelope) {
	select {
	case hub.broadcast <- message:
	case <-hub.done:
	}
}

// deliver never blocks: a client whose buffer is full is dropped so it cannot stall the room.
func (hub *Hub) deliver(client *Client, data []byte) {
	select {
	case client.send <- data:
	default:
		hub.remove(client)
	}
}

// remove is idempotent, so close(client.send) runs exactly once.
func (hub *Hub) remove(client *Client) {
	clients := hub.rooms[client.room]
	if !clients[client] {
		return
	}
	delete(clients, client)
	close(client.send)
	if len(clients) == 0 {
		delete(hub.rooms, client.room)
	}
}
