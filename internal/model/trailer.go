package model

import "time"

const TrailerContentType = "video/mp4"

// Trailer is the metadata of a movie's trailer; the video itself is in object storage.
type Trailer struct {
	MovieID     uint32    `json:"movie_id" example:"1"`
	ObjectKey   string    `json:"-"`
	ContentType string    `json:"content_type" example:"video/mp4"`
	SizeBytes   int64     `json:"size_bytes" example:"1048576"`
	UploadedBy  *uint64   `json:"-"`
	UploadedAt  time.Time `json:"uploaded_at" example:"2026-10-05T12:00:00.123456Z"`
}
