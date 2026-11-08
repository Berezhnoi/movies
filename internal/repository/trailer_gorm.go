package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"homework/internal/model"
)

type trailerEntity struct {
	MovieID     uint32 `gorm:"primaryKey;autoIncrement:false"`
	ObjectKey   string `gorm:"not null"`
	ContentType string `gorm:"not null"`
	SizeBytes   int64  `gorm:"not null"`
	UploadedBy  *uint64
	UploadedAt  time.Time `gorm:"autoCreateTime:false"`
}

func (trailerEntity) TableName() string { return "movie_trailers" }

type gormTrailerRepository struct {
	db *gorm.DB
}

var _ TrailerRepository = (*gormTrailerRepository)(nil)

func NewGormTrailerRepository(db *gorm.DB) TrailerRepository {
	return &gormTrailerRepository{db: db}
}

func (repository *gormTrailerRepository) Get(ctx context.Context, movieID uint32) (model.Trailer, error) {
	var entity trailerEntity
	if err := repository.db.WithContext(ctx).First(&entity, "movie_id = ?", movieID).Error; err != nil {
		return model.Trailer{}, wrapDatabaseError("find trailer", err)
	}
	return trailerEntityToDomain(entity), nil
}

// upsertTrailerSQL replaces the movie's row in one statement; xmax = 0 only for a freshly inserted row.
const upsertTrailerSQL = `INSERT INTO movie_trailers (movie_id, object_key, content_type, size_bytes, uploaded_by, uploaded_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (movie_id) DO UPDATE SET
	object_key = EXCLUDED.object_key,
	content_type = EXCLUDED.content_type,
	size_bytes = EXCLUDED.size_bytes,
	uploaded_by = EXCLUDED.uploaded_by,
	uploaded_at = EXCLUDED.uploaded_at
RETURNING (xmax = 0) AS inserted`

func (repository *gormTrailerRepository) Upsert(ctx context.Context, trailer model.Trailer) (bool, error) {
	var inserted bool
	err := repository.db.WithContext(ctx).Raw(upsertTrailerSQL,
		trailer.MovieID, trailer.ObjectKey, trailer.ContentType, trailer.SizeBytes,
		trailer.UploadedBy, trailer.UploadedAt.UTC().Truncate(time.Microsecond),
	).Scan(&inserted).Error
	if err != nil {
		// Raw().Scan() skips TranslateError, so translate explicitly.
		if translator, ok := repository.db.Dialector.(gorm.ErrorTranslator); ok {
			err = translator.Translate(err)
		}
		// The movie was deleted after the service checked it.
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return false, ErrNotFound
		}
		return false, wrapDatabaseError("upsert trailer", err)
	}
	return inserted, nil
}

func (repository *gormTrailerRepository) Delete(ctx context.Context, movieID uint32) error {
	if err := repository.db.WithContext(ctx).Delete(&trailerEntity{}, "movie_id = ?", movieID).Error; err != nil {
		return wrapDatabaseError("delete trailer", err)
	}
	return nil
}

func (repository *gormTrailerRepository) ExistsFor(ctx context.Context, movieIDs []uint32) (map[uint32]bool, error) {
	exists := make(map[uint32]bool, len(movieIDs))
	if len(movieIDs) == 0 {
		return exists, ctx.Err()
	}
	var found []uint32
	if err := repository.db.WithContext(ctx).Model(&trailerEntity{}).
		Where("movie_id IN ?", movieIDs).
		Pluck("movie_id", &found).Error; err != nil {
		return nil, wrapDatabaseError("find trailers", err)
	}
	for _, movieID := range found {
		exists[movieID] = true
	}
	return exists, nil
}

func trailerEntityToDomain(entity trailerEntity) model.Trailer {
	return model.Trailer{
		MovieID:     entity.MovieID,
		ObjectKey:   entity.ObjectKey,
		ContentType: entity.ContentType,
		SizeBytes:   entity.SizeBytes,
		UploadedBy:  entity.UploadedBy,
		UploadedAt:  entity.UploadedAt.UTC(),
	}
}
