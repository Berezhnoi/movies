package chat

import (
	"context"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// broker carries room messages to every instance and keeps the room history.
type broker interface {
	Publish(ctx context.Context, room uint32, data []byte) error
	// Subscribe passes every published message to deliver until ctx is done.
	Subscribe(ctx context.Context, deliver func(room uint32, data []byte))
	// History returns the stored messages of a room, oldest first.
	History(ctx context.Context, room uint32) ([][]byte, error)
}

// localBroker is the single-instance chat: it hands messages straight to the hub and keeps no history.
type localBroker struct {
	hub *Hub
}

func (b localBroker) Publish(_ context.Context, room uint32, data []byte) error {
	b.hub.send(envelope{room: room, data: data})
	return nil
}

func (localBroker) Subscribe(context.Context, func(uint32, []byte)) {}

func (localBroker) History(context.Context, uint32) ([][]byte, error) { return nil, nil }

const (
	channelPrefix = "chat:movie:"
	historyPrefix = "chat:history:movie:"
)

// redisBroker shares messages between instances through Redis pub/sub and a capped list per room.
type redisBroker struct {
	rdb  *redis.Client
	size int64
}

func (b redisBroker) Publish(ctx context.Context, room uint32, data []byte) error {
	id := strconv.FormatUint(uint64(room), 10)
	_, err := b.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Publish(ctx, channelPrefix+id, data)
		pipe.LPush(ctx, historyPrefix+id, data)
		pipe.LTrim(ctx, historyPrefix+id, 0, b.size-1)
		return nil
	})
	return err
}

func (b redisBroker) Subscribe(ctx context.Context, deliver func(uint32, []byte)) {
	sub := b.rdb.PSubscribe(ctx, channelPrefix+"*")
	defer sub.Close()
	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messages:
			if !ok {
				return
			}
			room, err := strconv.ParseUint(strings.TrimPrefix(message.Channel, channelPrefix), 10, 32)
			if err == nil {
				deliver(uint32(room), []byte(message.Payload))
			}
		}
	}
}

func (b redisBroker) History(ctx context.Context, room uint32) ([][]byte, error) {
	items, err := b.rdb.LRange(ctx, historyPrefix+strconv.FormatUint(uint64(room), 10), 0, b.size-1).Result()
	if err != nil {
		return nil, err
	}
	history := make([][]byte, len(items))
	for i, item := range items {
		history[len(items)-1-i] = []byte(item) // LPUSH stores the newest first.
	}
	return history, nil
}
