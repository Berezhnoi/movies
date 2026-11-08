package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"homework/internal/model"
	"homework/internal/repository"
)

// MaxTrailerSize is the largest accepted trailer file.
const MaxTrailerSize int64 = 50 << 20

var (
	// ErrObjectNotFound is returned by a TrailerStorage when the object or its bucket is missing.
	ErrObjectNotFound = errors.New("storage object not found")
	// ErrStorageUnavailable is returned by a TrailerStorage when the backend cannot be reached.
	ErrStorageUnavailable = errors.New("trailer storage unavailable")
)

// TrailerStorage keeps trailer videos; implementations wrap their errors with
// ErrObjectNotFound or ErrStorageUnavailable.
type TrailerStorage interface {
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error
	// Head returns the stored object's size.
	Head(ctx context.Context, key string) (int64, error)
	// Get opens the object, or only byteRange of it when byteRange is not nil.
	Get(ctx context.Context, key string, byteRange *ByteRange) (StoredObject, error)
}

// ByteRange is an inclusive, already validated byte range.
type ByteRange struct {
	Start int64
	End   int64
}

type StoredObject struct {
	Body          io.ReadCloser
	ContentLength int64
	// ContentRange is set for ranged reads, for example "bytes 0-99/1000".
	ContentRange string
}

type TrailerUnavailableError struct{}

func (TrailerUnavailableError) Error() string {
	return "Trailer file is unavailable; an editor needs to upload it again"
}

type PayloadTooLargeError struct{}

func (PayloadTooLargeError) Error() string { return "Trailer file must be at most 50 MiB" }

type UnsupportedMediaTypeError struct{}

func (UnsupportedMediaTypeError) Error() string { return "Trailer file must be an MP4 video" }

type RangeNotSatisfiableError struct {
	Size int64
}

func (RangeNotSatisfiableError) Error() string { return "Requested range is not satisfiable" }

type TrailerService struct {
	movies   repository.MovieRepository
	trailers repository.TrailerRepository
	storage  TrailerStorage
	now      func() time.Time
}

func NewTrailerService(movies repository.MovieRepository, trailers repository.TrailerRepository, storage TrailerStorage) *TrailerService {
	return &TrailerService{movies: movies, trailers: trailers, storage: storage, now: time.Now}
}

// TrailerObjectKey is fixed per movie, so a replace is a single overwrite.
func TrailerObjectKey(movieID uint32) string {
	return fmt.Sprintf("trailers/%d.mp4", movieID)
}

// RequireMovie returns a NotFoundError when the movie does not exist.
func (service *TrailerService) RequireMovie(ctx context.Context, movieID uint32) error {
	_, err := repositoryGetByID(ctx, service.movies, movieID)
	return repositoryError(err)
}

// Upload validates the file, stores it and records its metadata. It reports
// whether the movie had no trailer before.
func (service *TrailerService) Upload(ctx context.Context, movieID uint32, file io.Reader, uploadedBy uint64) (model.Trailer, bool, error) {
	spool, size, err := spoolTrailer(file)
	if spool != nil {
		defer removeSpool(spool)
	}
	if err != nil {
		return model.Trailer{}, false, err
	}

	key := TrailerObjectKey(movieID)
	if err := service.storage.Put(ctx, key, spool, size, model.TrailerContentType); err != nil {
		return model.Trailer{}, false, fmt.Errorf("store trailer: %w", err)
	}
	trailer := model.Trailer{
		MovieID:     movieID,
		ObjectKey:   key,
		ContentType: model.TrailerContentType,
		SizeBytes:   size,
		UploadedAt:  service.now().UTC().Truncate(time.Microsecond),
	}
	if uploadedBy != 0 {
		trailer.UploadedBy = &uploadedBy
	}
	created, err := service.trailers.Upsert(ctx, trailer)
	if err != nil {
		log.Printf("trailer stored but metadata not saved: movie_id=%d: %v", movieID, err)
		return model.Trailer{}, false, fmt.Errorf("trailer repository: %w", err)
	}
	log.Printf("trailer uploaded: movie_id=%d size=%d", movieID, size)
	return trailer, created, nil
}

// Metadata returns the trailer after checking that its object still exists.
func (service *TrailerService) Metadata(ctx context.Context, movieID uint32) (model.Trailer, error) {
	trailer, err := service.get(ctx, movieID)
	if err != nil {
		return model.Trailer{}, err
	}
	if _, err := service.storage.Head(ctx, trailer.ObjectKey); err != nil {
		return model.Trailer{}, storageError(movieID, err)
	}
	return trailer, nil
}

// Open opens the trailer for streaming. rangeHeader is the raw Range header;
// a missing, malformed or multi-range value returns the whole file.
func (service *TrailerService) Open(ctx context.Context, movieID uint32, rangeHeader string) (StoredObject, error) {
	trailer, err := service.get(ctx, movieID)
	if err != nil {
		return StoredObject{}, err
	}
	size, err := service.storage.Head(ctx, trailer.ObjectKey)
	if err != nil {
		return StoredObject{}, storageError(movieID, err)
	}
	byteRange, err := ParseByteRange(rangeHeader, size)
	if err != nil {
		return StoredObject{}, err
	}
	object, err := service.storage.Get(ctx, trailer.ObjectKey, byteRange)
	if err != nil {
		return StoredObject{}, storageError(movieID, err)
	}
	return object, nil
}

func (service *TrailerService) get(ctx context.Context, movieID uint32) (model.Trailer, error) {
	trailer, err := service.trailers.Get(ctx, movieID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Trailer{}, NotFoundError{Message: "Trailer not found"}
	}
	if err != nil {
		return model.Trailer{}, fmt.Errorf("trailer repository: %w", err)
	}
	return trailer, nil
}

func storageError(movieID uint32, err error) error {
	if errors.Is(err, ErrObjectNotFound) {
		log.Printf("trailer object missing: movie_id=%d", movieID)
		return TrailerUnavailableError{}
	}
	log.Printf("trailer storage error: movie_id=%d: %v", movieID, err)
	return fmt.Errorf("trailer storage: %w", err)
}

func removeSpool(spool *os.File) {
	if err := spool.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		log.Printf("close trailer spool file %s: %v", spool.Name(), err)
	}
	if err := os.Remove(spool.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("remove trailer spool file %s: %v", spool.Name(), err)
	}
}

// spoolTrailer copies the upload to a temporary file, enforcing the size
// limit and the MP4 signature (an "ftyp" box at offset 4).
func spoolTrailer(file io.Reader) (*os.File, int64, error) {
	spool, err := os.CreateTemp("", "trailer-*.mp4")
	if err != nil {
		return nil, 0, fmt.Errorf("create trailer spool file: %w", err)
	}
	size, err := io.Copy(spool, io.LimitReader(file, MaxTrailerSize+1))
	if err != nil {
		return spool, 0, fmt.Errorf("read trailer upload: %w", err)
	}
	if size > MaxTrailerSize {
		return spool, 0, PayloadTooLargeError{}
	}
	if size == 0 {
		return spool, 0, ValidationError{Message: "Trailer file must not be empty"}
	}
	header := make([]byte, 8)
	if _, err := spool.ReadAt(header, 0); err != nil || !bytes.Equal(header[4:8], []byte("ftyp")) {
		return spool, 0, UnsupportedMediaTypeError{}
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return spool, 0, fmt.Errorf("rewind trailer spool file: %w", err)
	}
	return spool, size, nil
}

// ParseByteRange resolves a single "bytes=" range against size. It returns nil
// for a missing, malformed or multi-range header, and RangeNotSatisfiableError
// when the range lies outside the file.
func ParseByteRange(header string, size int64) (*ByteRange, error) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return nil, nil
	}
	startText, endText, ok := strings.Cut(strings.TrimSpace(spec), "-")
	if !ok {
		return nil, nil
	}
	if startText == "" {
		suffix, ok := parseRangeNumber(endText)
		if !ok {
			return nil, nil
		}
		if suffix == 0 {
			return nil, RangeNotSatisfiableError{Size: size}
		}
		return &ByteRange{Start: max(size-suffix, 0), End: size - 1}, nil
	}
	start, ok := parseRangeNumber(startText)
	if !ok {
		return nil, nil
	}
	end := size - 1
	if endText != "" {
		end, ok = parseRangeNumber(endText)
		if !ok || end < start {
			return nil, nil
		}
	}
	if start >= size {
		return nil, RangeNotSatisfiableError{Size: size}
	}
	return &ByteRange{Start: start, End: min(end, size-1)}, nil
}

// parseRangeNumber accepts only ASCII digits that fit in an int64 (no sign or spaces).
func parseRangeNumber(text string) (int64, bool) {
	if text == "" {
		return 0, false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseInt(text, 10, 64)
	return value, err == nil
}
