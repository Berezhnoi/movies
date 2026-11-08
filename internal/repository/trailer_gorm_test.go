package repository

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"homework/internal/model"
)

type trailerFixture struct {
	repository TrailerRepository
	movieIDs   [2]uint32
	userID     uint64
}

func testTrailer(movieID uint32, size int64, uploadedBy *uint64) model.Trailer {
	return model.Trailer{
		MovieID:     movieID,
		ObjectKey:   "trailers/test.mp4",
		ContentType: model.TrailerContentType,
		SizeBytes:   size,
		UploadedBy:  uploadedBy,
		UploadedAt:  time.Date(2026, 11, 8, 12, 0, 0, 123456000, time.UTC),
	}
}

// runTrailerRepositoryContract runs the same behaviour against every TrailerRepository implementation.
func runTrailerRepositoryContract(t *testing.T, setup func(t *testing.T) trailerFixture) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, fixture trailerFixture)
	}{
		{"get missing returns not found", func(t *testing.T, fixture trailerFixture) {
			if _, err := fixture.repository.Get(ctx, fixture.movieIDs[0]); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get() error = %v, want ErrNotFound", err)
			}
		}},
		{"upsert inserts then get returns it", func(t *testing.T, fixture trailerFixture) {
			want := testTrailer(fixture.movieIDs[0], 100, &fixture.userID)
			created, err := fixture.repository.Upsert(ctx, want)
			if err != nil || !created {
				t.Fatalf("Upsert() = (%v, %v), want (true, nil)", created, err)
			}
			got, err := fixture.repository.Get(ctx, want.MovieID)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Get() = %#v, want %#v", got, want)
			}
		}},
		{"upsert replaces existing row", func(t *testing.T, fixture trailerFixture) {
			movieID := fixture.movieIDs[0]
			if _, err := fixture.repository.Upsert(ctx, testTrailer(movieID, 100, &fixture.userID)); err != nil {
				t.Fatalf("first Upsert() error = %v", err)
			}
			replacement := testTrailer(movieID, 200, nil)
			replacement.UploadedAt = replacement.UploadedAt.Add(time.Hour)
			created, err := fixture.repository.Upsert(ctx, replacement)
			if err != nil || created {
				t.Fatalf("second Upsert() = (%v, %v), want (false, nil)", created, err)
			}
			got, err := fixture.repository.Get(ctx, movieID)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if !reflect.DeepEqual(got, replacement) {
				t.Fatalf("Get() = %#v, want %#v", got, replacement)
			}
		}},
		{"delete removes row", func(t *testing.T, fixture trailerFixture) {
			movieID := fixture.movieIDs[0]
			if _, err := fixture.repository.Upsert(ctx, testTrailer(movieID, 100, nil)); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			if err := fixture.repository.Delete(ctx, movieID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := fixture.repository.Get(ctx, movieID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
			}
		}},
		{"delete missing row is a no-op", func(t *testing.T, fixture trailerFixture) {
			if err := fixture.repository.Delete(ctx, fixture.movieIDs[1]); err != nil {
				t.Fatalf("Delete() error = %v, want nil", err)
			}
		}},
		{"exists for reports only movies with a row", func(t *testing.T, fixture trailerFixture) {
			if _, err := fixture.repository.Upsert(ctx, testTrailer(fixture.movieIDs[0], 100, nil)); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			got, err := fixture.repository.ExistsFor(ctx, []uint32{fixture.movieIDs[0], fixture.movieIDs[1]})
			if err != nil {
				t.Fatalf("ExistsFor() error = %v", err)
			}
			if want := map[uint32]bool{fixture.movieIDs[0]: true}; !reflect.DeepEqual(got, want) {
				t.Fatalf("ExistsFor() = %v, want %v", got, want)
			}
			empty, err := fixture.repository.ExistsFor(ctx, nil)
			if err != nil || empty == nil || len(empty) != 0 {
				t.Fatalf("ExistsFor(nil) = (%v, %v), want empty non-nil map", empty, err)
			}
		}},
		{"cancelled context returns error", func(t *testing.T, fixture trailerFixture) {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := fixture.repository.Get(cancelled, fixture.movieIDs[0]); err == nil {
				t.Error("Get() error = nil, want context error")
			}
			if _, err := fixture.repository.Upsert(cancelled, testTrailer(fixture.movieIDs[0], 100, nil)); err == nil {
				t.Error("Upsert() error = nil, want context error")
			}
			if err := fixture.repository.Delete(cancelled, fixture.movieIDs[0]); err == nil {
				t.Error("Delete() error = nil, want context error")
			}
			if _, err := fixture.repository.ExistsFor(cancelled, []uint32{fixture.movieIDs[0]}); err == nil {
				t.Error("ExistsFor() error = nil, want context error")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.run(t, setup(t))
		})
	}
}

func TestInMemoryTrailerRepositoryContract(t *testing.T) {
	runTrailerRepositoryContract(t, func(*testing.T) trailerFixture {
		return trailerFixture{repository: NewInMemoryTrailerRepository(), movieIDs: [2]uint32{1, 2}, userID: 7}
	})
}

func TestGormTrailerRepositoryIntegration(t *testing.T) {
	db := openIntegrationDB(t)
	movies := NewGormMovieRepository(db)
	users := NewGormUserRepository(db)
	ctx := context.Background()
	t.Cleanup(func() {
		if err := db.Exec(trailerTruncateSQL).Error; err != nil {
			t.Errorf("clean test database: %v", err)
		}
	})
	setup := func(t *testing.T) trailerFixture {
		t.Helper()
		if err := db.WithContext(ctx).Exec(trailerTruncateSQL).Error; err != nil {
			t.Fatalf("truncate test tables: %v", err)
		}
		fixture := trailerFixture{repository: NewGormTrailerRepository(db)}
		for i, title := range []string{"First", "Second"} {
			movie, err := movies.Create(ctx, testMovie(title, "Trailer Director", "Trailer Genre"))
			if err != nil {
				t.Fatalf("create movie: %v", err)
			}
			fixture.movieIDs[i] = movie.ID
		}
		user := model.User{Email: "editor@example.com", Name: "Editor", PasswordHash: "test-hash", Role: model.RoleEditor}
		if err := users.Create(ctx, &user); err != nil {
			t.Fatalf("create user: %v", err)
		}
		fixture.userID = user.ID
		return fixture
	}

	runTrailerRepositoryContract(t, setup)

	t.Run("movie delete cascades to trailer row", func(t *testing.T) {
		fixture := setup(t)
		movieID := fixture.movieIDs[0]
		if _, err := fixture.repository.Upsert(ctx, testTrailer(movieID, 100, nil)); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
		if !movies.Delete(ctx, movieID) {
			t.Fatal("delete movie failed")
		}
		if _, err := fixture.repository.Get(ctx, movieID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get() after movie delete error = %v, want ErrNotFound", err)
		}
	})

	t.Run("upsert for missing movie returns not found", func(t *testing.T) {
		fixture := setup(t)
		if _, err := fixture.repository.Upsert(ctx, testTrailer(fixture.movieIDs[1]+100, 100, nil)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Upsert() error = %v, want ErrNotFound", err)
		}
	})
}
