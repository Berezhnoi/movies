package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"

	"homework/internal/model"
	"homework/internal/repository"
)

type stubTrailerStorage struct {
	puts      []string
	deleteErr error
}

func (storage *stubTrailerStorage) Put(_ context.Context, key string, body io.ReadSeeker, _ int64, _ string) error {
	storage.puts = append(storage.puts, key)
	_, err := io.Copy(io.Discard, body)
	return err
}

func (*stubTrailerStorage) Head(context.Context, string) (int64, error) { return 0, ErrObjectNotFound }

func (*stubTrailerStorage) Get(context.Context, string, *ByteRange) (StoredObject, error) {
	return StoredObject{}, ErrObjectNotFound
}

func (storage *stubTrailerStorage) Delete(context.Context, string) error { return storage.deleteErr }

type failingTrailerRepository struct {
	*repository.InMemoryTrailerRepository
	err error
}

func (repository failingTrailerRepository) Upsert(context.Context, model.Trailer) (bool, error) {
	return false, repository.err
}

func captureServiceLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

func TestTrailerUploadRepositoryFailsAfterPut(t *testing.T) {
	dbErr := errors.New("database is down")
	storage := &stubTrailerStorage{}
	trailerService := NewTrailerService(repository.NewInMemoryMovieRepositoryAdapter(),
		failingTrailerRepository{repository.NewInMemoryTrailerRepository(), dbErr}, storage)
	logs := captureServiceLog(t)

	file := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p'}, make([]byte, 92)...)
	_, _, err := trailerService.Upload(context.Background(), 7, bytes.NewReader(file), 1)
	if !errors.Is(err, dbErr) {
		t.Fatalf("Upload() error = %v, want it to wrap the repository error", err)
	}
	// An untyped error is what apperr.WriteError maps to 500 internal_server_error.
	var notFound NotFoundError
	var validation ValidationError
	if errors.As(err, &notFound) || errors.As(err, &validation) || errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("Upload() error = %v, want an untyped (500) error", err)
	}
	if len(storage.puts) != 1 || storage.puts[0] != "trailers/7.mp4" {
		t.Errorf("puts = %v, want one put of trailers/7.mp4", storage.puts)
	}
	if !strings.Contains(logs.String(), "movie_id=7") {
		t.Errorf("log = %q, want the movie id", logs)
	}
}

func TestTrailerRemoveMovieTrailerOnlyLogs(t *testing.T) {
	trailers := repository.NewInMemoryTrailerRepository()
	if _, err := trailers.Upsert(context.Background(), model.Trailer{MovieID: 3, ObjectKey: TrailerObjectKey(3)}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	storage := &stubTrailerStorage{deleteErr: ErrStorageUnavailable}
	trailerService := NewTrailerService(repository.NewInMemoryMovieRepositoryAdapter(), trailers, storage)
	logs := captureServiceLog(t)

	trailerService.RemoveMovieTrailer(context.Background(), 3)
	if trailers.Len() != 0 {
		t.Errorf("rows = %d, want 0", trailers.Len())
	}
	if !strings.Contains(logs.String(), "trailer object not removed: movie_id=3") {
		t.Errorf("log = %q, want an object cleanup failure with movie_id=3", logs)
	}

	storage.deleteErr = ErrObjectNotFound
	logs.Reset()
	trailerService.RemoveMovieTrailer(context.Background(), 3)
	if logs.Len() != 0 {
		t.Errorf("log = %q, want nothing for an already missing object", logs)
	}
}

func TestParseByteRange(t *testing.T) {
	tests := []struct {
		name   string
		header string
		size   int64
		want   *ByteRange
		err    bool
	}{
		{name: "no header", header: "", size: 1000},
		{name: "first bytes", header: "bytes=0-99", size: 1000, want: &ByteRange{Start: 0, End: 99}},
		{name: "open end", header: "bytes=900-", size: 1000, want: &ByteRange{Start: 900, End: 999}},
		{name: "suffix", header: "bytes=-50", size: 1000, want: &ByteRange{Start: 950, End: 999}},
		{name: "suffix longer than file", header: "bytes=-5000", size: 1000, want: &ByteRange{Start: 0, End: 999}},
		{name: "end clamped", header: "bytes=990-5000", size: 1000, want: &ByteRange{Start: 990, End: 999}},
		{name: "single byte", header: "bytes=10-10", size: 1000, want: &ByteRange{Start: 10, End: 10}},
		{name: "spaces around spec", header: " bytes= 1-2 ", size: 1000, want: &ByteRange{Start: 1, End: 2}},
		{name: "1-byte file whole", header: "bytes=0-0", size: 1, want: &ByteRange{Start: 0, End: 0}},
		{name: "1-byte file open end", header: "bytes=0-", size: 1, want: &ByteRange{Start: 0, End: 0}},
		{name: "1-byte file suffix", header: "bytes=-1", size: 1, want: &ByteRange{Start: 0, End: 0}},
		{name: "1-byte file past end", header: "bytes=1-", size: 1, err: true},
		{name: "start past end", header: "bytes=1000-1100", size: 1000, err: true},
		{name: "zero suffix", header: "bytes=-0", size: 1000, err: true},
		{name: "empty spec", header: "bytes=", size: 1000},
		{name: "dash only", header: "bytes=-", size: 1000},
		{name: "plus sign start", header: "bytes=+5-9", size: 1000},
		{name: "plus sign end", header: "bytes=5-+9", size: 1000},
		{name: "plus sign suffix", header: "bytes=-+5", size: 1000},
		{name: "negative start", header: "bytes=-5-9", size: 1000},
		{name: "start overflows int64", header: "bytes=9223372036854775808-", size: 1000},
		{name: "end overflows int64", header: "bytes=0-99999999999999999999", size: 1000},
		{name: "suffix overflows int64", header: "bytes=-99999999999999999999", size: 1000},
		{name: "inverted", header: "bytes=9-0", size: 1000},
		{name: "multi-range", header: "bytes=0-1,5-9", size: 1000},
		{name: "other unit", header: "items=0-9", size: 1000},
		{name: "no dash", header: "bytes=5", size: 1000},
		{name: "letters", header: "bytes=a-b", size: 1000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseByteRange(test.header, test.size)
			if test.err {
				var rangeError RangeNotSatisfiableError
				if !errors.As(err, &rangeError) || rangeError.Size != test.size {
					t.Fatalf("ParseByteRange(%q) error = %v, want RangeNotSatisfiableError{Size: %d}", test.header, err, test.size)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseByteRange(%q) error = %v", test.header, err)
			}
			if (got == nil) != (test.want == nil) || (got != nil && *got != *test.want) {
				t.Errorf("ParseByteRange(%q) = %+v, want %+v", test.header, got, test.want)
			}
		})
	}
}
