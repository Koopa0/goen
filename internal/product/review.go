package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

var (
	ErrAlreadyReviewed = errors.New("product: already reviewed")
	ErrNotDelivered    = errors.New("product: no delivered order for this product")
	ErrReviewInvalid   = errors.New("product: invalid review")
)

const (
	MaxReviewTitleRunes = 80
	MaxReviewBodyRunes  = 2000
	MinReviewBodyRunes  = 5
)

type Review struct {
	Rating int16
	Title  string
	Body   string
}

func (r *Review) Validate() map[string]i18n.Key {
	r.Title = strings.TrimSpace(r.Title)
	r.Body = strings.TrimSpace(r.Body)

	errs := map[string]i18n.Key{}
	if r.Rating < 1 || r.Rating > 5 {
		errs["rating"] = i18n.KeyRatingOutOfRange
	}
	if utf8.RuneCountInString(r.Title) > MaxReviewTitleRunes {
		errs["title"] = i18n.KeyReviewTitleTooLong
	}
	n := utf8.RuneCountInString(r.Body)
	if n < MinReviewBodyRunes || n > MaxReviewBodyRunes {
		errs["body"] = i18n.KeyReviewBodyLength
	}
	if hasUnprintableReviewControl(r.Title) {
		errs["title"] = i18n.KeyReviewBodyUnprintable
	}
	if hasUnprintableReviewControl(r.Body) {
		errs["body"] = i18n.KeyReviewBodyUnprintable
	}
	return errs
}

func hasUnprintableReviewControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r'
	})
}

// ReviewStanding answers for a signed-in customer; a visitor is
// ReviewSignedOut.
func (s *Store) ReviewStanding(ctx context.Context, slug, userID string) (pages.ReviewStanding, error) {
	id, parseErr := uuid.Parse(userID)
	if parseErr != nil {
		return pages.ReviewSignedOut, nil //nolint:nilerr // not signed in is not an error
	}
	owner := uuid.NullUUID{UUID: id, Valid: true}

	productID, err := s.q.ActiveProductForReview(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.ReviewSignedOut, ErrNotFound
		}
		return pages.ReviewSignedOut, fmt.Errorf("find review product: %w", err)
	}
	reviewed, err := s.q.HasReviewed(ctx, db.HasReviewedParams{
		UserID: owner, ProductID: productID,
	})
	if err != nil {
		return pages.ReviewSignedOut, fmt.Errorf("check existing review: %w", err)
	}
	if reviewed {
		return pages.ReviewAlreadyWritten, nil
	}
	delivered, err := s.q.HasDeliveredProduct(ctx, db.HasDeliveredProductParams{
		UserID: owner, ProductID: uuid.NullUUID{UUID: productID, Valid: true},
	})
	if err != nil {
		return pages.ReviewSignedOut, fmt.Errorf("check delivery: %w", err)
	}
	if !delivered {
		return pages.ReviewNotDelivered, nil
	}
	return pages.ReviewOpen, nil
}

func (s *Store) AddReview(ctx context.Context, slug, userID string, r *Review) (map[string]i18n.Key, error) {
	if errs := r.Validate(); len(errs) > 0 {
		return errs, nil
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrReviewInvalid
	}
	owner := uuid.NullUUID{UUID: id, Valid: true}

	standing, err := s.ReviewStanding(ctx, slug, userID)
	if err != nil {
		return nil, err
	}
	switch standing {
	case pages.ReviewOpen:
	case pages.ReviewAlreadyWritten:
		return nil, ErrAlreadyReviewed
	case pages.ReviewNotDelivered:
		return nil, ErrNotDelivered
	case pages.ReviewSignedOut:
		return nil, ErrReviewInvalid
	}

	n, err := s.q.CreateReview(ctx, db.CreateReviewParams{
		Slug: slug, UserID: owner, Rating: r.Rating,
		Title: r.Title, Body: r.Body,
	})
	if err != nil {
		// Bound to the CONSTRAINT name, never the message: PostgreSQL names the
		// index in a unique violation, but the message is prose that
		// lc_messages localizes and releases reword. ReviewStanding answers
		// first in the ordinary case, so only two racing submissions reach this
		// branch.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "product_reviews_author_key" {
			return nil, ErrAlreadyReviewed
		}
		return nil, fmt.Errorf("create review: %w", err)
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return nil, nil
}
