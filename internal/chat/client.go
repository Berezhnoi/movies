package chat

import (
	"encoding/json"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = pongWait * 9 / 10
	maxMessageSize = 4096 // 500 characters can take up to 2000 bytes of UTF-8 plus JSON overhead.
	maxTextLength  = 500
	sendBufferSize = 16
)

type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	room   uint32
	author string
	send   chan []byte
}

type incomingMessage struct {
	Text string `json:"text"`
}

type outgoingMessage struct {
	Room   uint32    `json:"room"`
	Author string    `json:"author"`
	Text   string    `json:"text"`
	Time   time.Time `json:"time"`
}

type errorMessage struct {
	Error string `json:"error"`
}

// readPump is the only reader. Its exit unregisters the client, which closes send and stops writePump.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				log.Printf("read chat message: %v", err)
			}
			return
		}
		var input incomingMessage
		text := ""
		if json.Unmarshal(data, &input) == nil {
			text = strings.TrimSpace(input.Text)
		}
		if text == "" || utf8.RuneCountInString(text) > maxTextLength {
			c.hub.broadcast <- envelope{to: c, data: mustJSON(errorMessage{Error: "text must be 1-500 characters"})}
			continue
		}
		c.hub.broadcast <- envelope{room: c.room, data: mustJSON(outgoingMessage{
			Room: c.room, Author: c.author, Text: text, Time: time.Now().UTC(),
		})}
	}
}

// writePump is the only writer to conn, as gorilla/websocket allows one concurrent writer.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
