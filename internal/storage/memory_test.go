package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"homework/internal/service"
)

func TestMemoryPutGetHeadDelete(t *testing.T) {
	ctx := context.Background()
	memory := NewMemory()
	data := []byte("0123456789")

	if err := memory.Put(ctx, "trailers/1.mp4", bytes.NewReader(data), int64(len(data)), "video/mp4"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	size, err := memory.Head(ctx, "trailers/1.mp4")
	if err != nil || size != 10 {
		t.Fatalf("Head() = %d, %v, want 10, nil", size, err)
	}

	whole, err := memory.Get(ctx, "trailers/1.mp4", nil)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := readAll(t, whole.Body); !bytes.Equal(got, data) || whole.ContentLength != 10 || whole.ContentRange != "" {
		t.Errorf("Get() = %q, length %d, range %q", got, whole.ContentLength, whole.ContentRange)
	}

	part, err := memory.Get(ctx, "trailers/1.mp4", &service.ByteRange{Start: 2, End: 4})
	if err != nil {
		t.Fatalf("ranged Get() error = %v", err)
	}
	if got := readAll(t, part.Body); string(got) != "234" || part.ContentLength != 3 || part.ContentRange != "bytes 2-4/10" {
		t.Errorf("ranged Get() = %q, length %d, range %q", got, part.ContentLength, part.ContentRange)
	}

	replacement := []byte("abc")
	if err := memory.Put(ctx, "trailers/1.mp4", bytes.NewReader(replacement), 3, "video/mp4"); err != nil {
		t.Fatalf("overwrite Put() error = %v", err)
	}
	if size, _ := memory.Head(ctx, "trailers/1.mp4"); size != 3 || memory.Len() != 1 {
		t.Errorf("after overwrite size = %d, objects = %d, want 3, 1", size, memory.Len())
	}

	if err := memory.Delete(ctx, "trailers/1.mp4"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := memory.Head(ctx, "trailers/1.mp4"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Head() after Delete error = %v, want ErrObjectNotFound", err)
	}
	if err := memory.Delete(ctx, "trailers/1.mp4"); err != nil {
		t.Errorf("Delete() of a missing key error = %v, want nil", err)
	}
}

func TestMemoryMissingKey(t *testing.T) {
	ctx := context.Background()
	memory := NewMemory()

	if _, err := memory.Head(ctx, "missing"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Head() error = %v, want ErrObjectNotFound", err)
	}
	if _, err := memory.Get(ctx, "missing", nil); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Get() error = %v, want ErrObjectNotFound", err)
	}
}

func TestMemoryCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	memory := NewMemory()

	if err := memory.Put(ctx, "k", bytes.NewReader([]byte("x")), 1, "video/mp4"); !errors.Is(err, context.Canceled) {
		t.Errorf("Put() error = %v, want context.Canceled", err)
	}
	if _, err := memory.Head(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("Head() error = %v, want context.Canceled", err)
	}
}

func readAll(t *testing.T, body io.ReadCloser) []byte {
	t.Helper()
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}
