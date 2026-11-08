package repository

import (
	"context"
	"sync"

	"homework/internal/model"
)

// TrailerRepository stores trailer metadata, at most one row per movie.
type TrailerRepository interface {
	Get(ctx context.Context, movieID uint32) (model.Trailer, error)
	// Upsert inserts or replaces the movie's trailer and reports whether it was new.
	Upsert(ctx context.Context, trailer model.Trailer) (bool, error)
}

type InMemoryTrailerRepository struct {
	mu    sync.RWMutex
	items map[uint32]model.Trailer
}

var _ TrailerRepository = (*InMemoryTrailerRepository)(nil)

func NewInMemoryTrailerRepository() *InMemoryTrailerRepository {
	return &InMemoryTrailerRepository{items: make(map[uint32]model.Trailer)}
}

func (repository *InMemoryTrailerRepository) Get(ctx context.Context, movieID uint32) (model.Trailer, error) {
	if err := ctx.Err(); err != nil {
		return model.Trailer{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	trailer, ok := repository.items[movieID]
	if !ok {
		return model.Trailer{}, ErrNotFound
	}
	return copyTrailer(trailer), nil
}

func (repository *InMemoryTrailerRepository) Upsert(ctx context.Context, trailer model.Trailer) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	_, exists := repository.items[trailer.MovieID]
	repository.items[trailer.MovieID] = copyTrailer(trailer)
	return !exists, nil
}

func copyTrailer(trailer model.Trailer) model.Trailer {
	if trailer.UploadedBy != nil {
		uploadedBy := *trailer.UploadedBy
		trailer.UploadedBy = &uploadedBy
	}
	return trailer
}
