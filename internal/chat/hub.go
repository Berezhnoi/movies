// Package chat implements a per-movie WebSocket chat room.
package chat

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
}

func NewHub() *Hub {
	return &Hub{
		rooms:      make(map[uint32]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan envelope),
	}
}

// Run lives for the whole process; start it once at startup.
func (hub *Hub) Run() {
	for {
		select {
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
