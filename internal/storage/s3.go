package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"homework/internal/service"
)

// S3Config holds the settings for an S3-compatible emulator such as Floci.
type S3Config struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

// S3 is a TrailerStorage backed by one S3 bucket. The client is built from
// S3Config only (never config.LoadDefaultConfig), so it cannot pick up
// ~/.aws, IMDS or AWS_ENDPOINT_URL.
type S3 struct {
	client *s3.Client
	bucket string
}

var _ service.TrailerStorage = (*S3)(nil)

func NewS3(cfg S3Config) *S3 {
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		Credentials: aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	})
	return &S3{client: client, bucket: cfg.Bucket}
}

// EnsureBucket creates the bucket unless it already exists.
func (storage *S3) EnsureBucket(ctx context.Context) error {
	_, err := storage.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(storage.bucket)})
	if err != nil && !hasCode(err, "BucketAlreadyOwnedByYou", "BucketAlreadyExists") {
		return fmt.Errorf("create bucket %q: %w", storage.bucket, mapError(err))
	}
	return nil
}

// Put writes the object. Floci loses its bucket on restart, so a put into a
// missing bucket creates the bucket and retries once.
func (storage *S3) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	err := storage.put(ctx, key, body, size, contentType)
	if err == nil || !hasCode(err, "NoSuchBucket") {
		return wrap("put", key, err)
	}
	if err := storage.EnsureBucket(ctx); err != nil {
		return err
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind %q: %w", key, err)
	}
	return wrap("put", key, storage.put(ctx, key, body, size, contentType))
}

func (storage *S3) put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	_, err := storage.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(storage.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	return err
}

func (storage *S3) Head(ctx context.Context, key string) (int64, error) {
	out, err := storage.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(storage.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, wrap("head", key, err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

func (storage *S3) Get(ctx context.Context, key string, byteRange *service.ByteRange) (service.StoredObject, error) {
	input := &s3.GetObjectInput{Bucket: aws.String(storage.bucket), Key: aws.String(key)}
	if byteRange != nil {
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", byteRange.Start, byteRange.End))
	}
	out, err := storage.client.GetObject(ctx, input)
	if err != nil {
		return service.StoredObject{}, wrap("get", key, err)
	}
	return service.StoredObject{
		Body:          out.Body,
		ContentLength: aws.ToInt64(out.ContentLength),
		ContentRange:  aws.ToString(out.ContentRange),
	}, nil
}

// Delete removes the object; S3 reports success for a missing key and
// NoSuchBucket (mapped to ErrObjectNotFound) for a missing bucket.
func (storage *S3) Delete(ctx context.Context, key string) error {
	_, err := storage.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(storage.bucket),
		Key:    aws.String(key),
	})
	return wrap("delete", key, err)
}

func wrap(op, key string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %q: %w", op, key, mapError(err))
}

// mapError follows docs/floci-notes.md: Floci's NoSuchBucket does not match
// *types.NoSuchBucket, so codes are compared as strings. HEAD has no body, so
// its 404 arrives as "NotFound". A transport error (no API error) or a 5xx
// means the storage is unavailable.
func mapError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if hasCode(err, "NoSuchKey", "NoSuchBucket", "NotFound") || statusCode(err) == http.StatusNotFound {
		return fmt.Errorf("%w: %v", service.ErrObjectNotFound, err)
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || statusCode(err) >= http.StatusInternalServerError {
		return fmt.Errorf("%w: %v", service.ErrStorageUnavailable, err)
	}
	return err
}

func hasCode(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, code := range codes {
		if apiErr.ErrorCode() == code {
			return true
		}
	}
	return false
}

func statusCode(err error) int {
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode()
	}
	return 0
}
