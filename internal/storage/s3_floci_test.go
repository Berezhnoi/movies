package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"homework/internal/service"
)

// newFlociStorage returns an S3 storage on a fresh bucket, or skips the test
// unless TEST_S3_ENDPOINT points at Floci (for example http://localhost:4566).
func newFlociStorage(t *testing.T) *S3 {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT is not set")
	}
	storage := NewS3(S3Config{
		Endpoint:        endpoint,
		Bucket:          fmt.Sprintf("trailers-test-%d", time.Now().UnixNano()),
		Region:          "us-east-1",
		AccessKeyID:     "test",
		SecretAccessKey: "test",
	})
	if err := storage.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket() error = %v", err)
	}
	t.Cleanup(func() { deleteBucket(t, storage) })
	return storage
}

func TestS3PutHeadGetRange(t *testing.T) {
	storage := newFlociStorage(t)
	ctx := context.Background()
	data := bytes.Repeat([]byte("0123456789"), 100)

	if err := storage.Put(ctx, "trailers/1.mp4", bytes.NewReader(data), int64(len(data)), "video/mp4"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if size, err := storage.Head(ctx, "trailers/1.mp4"); err != nil || size != 1000 {
		t.Fatalf("Head() = %d, %v, want 1000, nil", size, err)
	}

	whole, err := storage.Get(ctx, "trailers/1.mp4", nil)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got := readAll(t, whole.Body); !bytes.Equal(got, data) || whole.ContentLength != 1000 {
		t.Errorf("Get() = %d bytes, length %d, want the 1000 stored bytes", len(got), whole.ContentLength)
	}

	part, err := storage.Get(ctx, "trailers/1.mp4", &service.ByteRange{Start: 0, End: 99})
	if err != nil {
		t.Fatalf("ranged Get() error = %v", err)
	}
	if got := readAll(t, part.Body); !bytes.Equal(got, data[:100]) || part.ContentLength != 100 || part.ContentRange != "bytes 0-99/1000" {
		t.Errorf("ranged Get() = %d bytes, length %d, range %q", len(got), part.ContentLength, part.ContentRange)
	}

	if err := storage.Delete(ctx, "trailers/1.mp4"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := storage.Head(ctx, "trailers/1.mp4"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Head() after Delete error = %v, want ErrObjectNotFound", err)
	}
}

func TestS3MissingKey(t *testing.T) {
	storage := newFlociStorage(t)
	ctx := context.Background()

	if _, err := storage.Head(ctx, "trailers/404.mp4"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Head() error = %v, want ErrObjectNotFound", err)
	}
	if _, err := storage.Get(ctx, "trailers/404.mp4", nil); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Get() error = %v, want ErrObjectNotFound", err)
	}
	if err := storage.Delete(ctx, "trailers/404.mp4"); err != nil {
		t.Errorf("Delete() error = %v, want nil for a missing key", err)
	}
}

func TestS3MissingBucketThenPutRecreatesIt(t *testing.T) {
	storage := newFlociStorage(t)
	ctx := context.Background()
	data := []byte("trailer bytes")
	if err := storage.Put(ctx, "trailers/1.mp4", bytes.NewReader(data), int64(len(data)), "video/mp4"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	// Simulate a Floci restart, which drops the bucket and its objects.
	deleteBucket(t, storage)

	if _, err := storage.Head(ctx, "trailers/1.mp4"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Head() error = %v, want ErrObjectNotFound", err)
	}
	if _, err := storage.Get(ctx, "trailers/1.mp4", nil); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Get() error = %v, want ErrObjectNotFound", err)
	}
	if err := storage.Delete(ctx, "trailers/1.mp4"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("Delete() error = %v, want ErrObjectNotFound", err)
	}

	if err := storage.Put(ctx, "trailers/1.mp4", bytes.NewReader(data), int64(len(data)), "video/mp4"); err != nil {
		t.Fatalf("Put() into a missing bucket error = %v, want the bucket recreated", err)
	}
	got, err := storage.Get(ctx, "trailers/1.mp4", nil)
	if err != nil {
		t.Fatalf("Get() after recreate error = %v", err)
	}
	if body := readAll(t, got.Body); !bytes.Equal(body, data) {
		t.Errorf("Get() after recreate = %q, want %q", body, data)
	}
}

func TestS3UnreachableIsUnavailable(t *testing.T) {
	if os.Getenv("TEST_S3_ENDPOINT") == "" {
		t.Skip("TEST_S3_ENDPOINT is not set")
	}
	// Port 1 on loopback refuses connections, like a stopped Floci.
	storage := NewS3(S3Config{Endpoint: "http://127.0.0.1:1", Bucket: "trailers", Region: "us-east-1", AccessKeyID: "test", SecretAccessKey: "test"})
	if _, err := storage.Head(context.Background(), "trailers/1.mp4"); !errors.Is(err, service.ErrStorageUnavailable) {
		t.Errorf("Head() error = %v, want ErrStorageUnavailable", err)
	}
}

// deleteBucket empties and removes the storage's bucket; a missing bucket is fine.
func deleteBucket(t *testing.T, storage *S3) {
	t.Helper()
	ctx := context.Background()
	list, err := storage.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(storage.bucket)})
	if err != nil {
		if hasCode(err, "NoSuchBucket") {
			return
		}
		t.Fatalf("list bucket: %v", err)
	}
	for _, object := range list.Contents {
		if _, err := storage.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(storage.bucket), Key: object.Key}); err != nil {
			t.Fatalf("delete %q: %v", aws.ToString(object.Key), err)
		}
	}
	if _, err := storage.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(storage.bucket)}); err != nil && !hasCode(err, "NoSuchBucket") {
		t.Fatalf("delete bucket: %v", err)
	}
}
