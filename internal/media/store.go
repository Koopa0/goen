package media

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

// MaxPickerRows bounds the back office's image list.
const MaxPickerRows = 60

// Store keeps images in PostgreSQL.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("media: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

// Put normalises an upload and stores it, returning what it became. An image
// goen already holds is the same Object and not a second row.
func (s *Store) Put(ctx context.Context, r io.Reader) (Object, error) {
	obj, data, err := Normalise(r)
	if err != nil {
		return Object{}, err
	}
	if err := s.q.PutMedia(ctx, db.PutMediaParams{
		Digest: obj.Digest, ContentType: obj.ContentType, Bytes: data,
		Width: obj.Width, Height: obj.Height, ByteSize: obj.ByteSize,
	}); err != nil {
		return Object{}, fmt.Errorf("store image %s: %w", obj.Digest, err)
	}
	return obj, nil
}

// Bytes reads an image out for serving.
func (s *Store) Bytes(ctx context.Context, digest string) (contentType string, data []byte, err error) {
	row, err := s.q.MediaBytes(ctx, digest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrNotFound
		}
		return "", nil, fmt.Errorf("read image %s: %w", digest, err)
	}
	return row.ContentType, row.Bytes, nil
}

// Object reads one stored image's metadata, including the dimensions a caller
// attaching it needs.
func (s *Store) Object(ctx context.Context, digest string) (Object, error) {
	row, err := s.q.MediaObject(ctx, digest)
	if err != nil {
		return Object{}, fmt.Errorf("read media %s: %w", digest, err)
	}
	return Object{
		Digest: row.Digest, ContentType: row.ContentType,
		Width: row.Width, Height: row.Height, ByteSize: row.ByteSize,
	}, nil
}

// Recent is the back office's picker.
func (s *Store) Recent(ctx context.Context) ([]Object, error) {
	rows, err := s.q.RecentMedia(ctx, MaxPickerRows)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	out := make([]Object, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, Object{
			Digest: r.Digest, ContentType: r.ContentType,
			Width: r.Width, Height: r.Height, ByteSize: r.ByteSize,
		})
	}
	return out, nil
}
