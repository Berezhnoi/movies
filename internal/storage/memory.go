package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"homework/internal/service"
)

// Memory is an in-memory TrailerStorage for tests and local runs without Floci.
type Memory struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

var _ service.TrailerStorage = (*Memory)(nil)

func NewMemory() *Memory {
	return &Memory{objects: make(map[string][]byte)}
}

func (memory *Memory) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(body, size))
	if err != nil {
		return fmt.Errorf("read object body: %w", err)
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	memory.objects[key] = data
	return nil
}

func (memory *Memory) Head(ctx context.Context, key string) (int64, error) {
	data, err := memory.object(ctx, key)
	return int64(len(data)), err
}

func (memory *Memory) Get(ctx context.Context, key string, byteRange *service.ByteRange) (service.StoredObject, error) {
	data, err := memory.object(ctx, key)
	if err != nil {
		return service.StoredObject{}, err
	}
	if byteRange == nil {
		return service.StoredObject{Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
	}
	part := data[byteRange.Start : byteRange.End+1]
	return service.StoredObject{
		Body:          io.NopCloser(bytes.NewReader(part)),
		ContentLength: int64(len(part)),
		ContentRange:  fmt.Sprintf("bytes %d-%d/%d", byteRange.Start, byteRange.End, len(data)),
	}, nil
}

// Delete removes the object; a missing key is not an error, as in S3.
func (memory *Memory) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	delete(memory.objects, key)
	return nil
}

// Len returns the number of stored objects.
func (memory *Memory) Len() int {
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	return len(memory.objects)
}

func (memory *Memory) object(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	data, ok := memory.objects[key]
	if !ok {
		return nil, fmt.Errorf("object %q: %w", key, service.ErrObjectNotFound)
	}
	return data, nil
}
