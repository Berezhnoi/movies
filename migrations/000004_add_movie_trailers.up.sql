-- One trailer per movie. The video lives in S3; this row is only its metadata,
-- so the movie table and its JSON contract stay unchanged.
CREATE TABLE movie_trailers (
    movie_id BIGINT NOT NULL,
    object_key TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    uploaded_by BIGINT,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_movie_trailers PRIMARY KEY (movie_id),
    CONSTRAINT fk_movie_trailers_movie
        FOREIGN KEY (movie_id) REFERENCES movies (id) ON DELETE CASCADE,
    CONSTRAINT fk_movie_trailers_uploaded_by
        FOREIGN KEY (uploaded_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT chk_movie_trailers_size CHECK (size_bytes > 0)
);
