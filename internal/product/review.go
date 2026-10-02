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
)

var (
	// ErrAlreadyReviewed is a second review on one product from one person.
	ErrAlreadyReviewed = errors.New("product: already reviewed")
	ErrNotDelivered    = errors.New("product: no delivered order for this product")
	// ErrReviewInvalid is a form goen refused before the database saw it.
	ErrReviewInvalid = errors.New("product: invalid review")
)

// Review length bounds, counted in runes.
const (
	MaxReviewTitleRunes = 80
	MaxReviewBodyRunes  = 2000
	MinReviewBodyRunes  = 5
)

// Review is what a customer is submitting.
type Review struct {
	Rating int16
	Title  string
	Body   string
}

// Validate refuses what the schema would, returning keys the caller renders in
// the reader's locale.
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

type ReviewRight int

const (
	MayReview ReviewRight = iota
	// HasReviewed counts a hidden review too: it still holds the unique index.
	HasReviewed
	AwaitsDelivery
)

// CanReview answers for a signed-in customer; a visitor gets AwaitsDelivery.
func (s *Store) CanReview(ctx context.Context, slug, userID string) (ReviewRight, error) {
	id, parseErr := uuid.Parse(userID)
	if parseErr != nil {
		return AwaitsDelivery, nil //nolint:nilerr // not signed in is not an error
	}
	owner := uuid.NullUUID{UUID: id, Valid: true}

	productID, err := s.q.ActiveProductForReview(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AwaitsDelivery, ErrNotFound
		}
		return AwaitsDelivery, fmt.Errorf("find review product: %w", err)
	}
	reviewed, err := s.q.HasReviewed(ctx, db.HasReviewedParams{
		UserID: owner, ProductID: productID,
	})
	if err != nil {
		return AwaitsDelivery, fmt.Errorf("check existing review: %w", err)
	}
	if reviewed {
		return HasReviewed, nil
	}
	received, err := s.q.HasReceivedProduct(ctx, db.HasReceivedProductParams{
		UserID: owner, ProductID: uuid.NullUUID{UUID: productID, Valid: true},
	})
	if err != nil {
		return AwaitsDelivery, fmt.Errorf("check delivery: %w", err)
	}
	if !received {
		return AwaitsDelivery, nil
	}
	return MayReview, nil
}

// AddReview records a review.
func (s *Store) AddReview(ctx context.Context, slug, userID string, r *Review) (map[string]i18n.Key, error) {
	if errs := r.Validate(); len(errs) > 0 {
		return errs, nil
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, ErrReviewInvalid
	}
	owner := uuid.NullUUID{UUID: id, Valid: true}

	right, err := s.CanReview(ctx, slug, userID)
	if err != nil {
		return nil, err
	}
	switch right {
	case MayReview:
	case HasReviewed:
		return nil, ErrAlreadyReviewed
	case AwaitsDelivery:
		return nil, ErrNotDelivered
	}

	n, err := s.q.CreateReview(ctx, db.CreateReviewParams{
		Slug: slug, UserID: owner, Rating: r.Rating,
		Title: r.Title, Body: r.Body,
	})
	if err != nil {
		// Bound to the CONSTRAINT name, never to the message text: PostgreSQL
		// happens to name the index in a unique violation, but the message is
		// prose that lc_messages localizes and releases reword, while the name
		// is a field. CanReview answers first in the ordinary case, so only two
		// racing submissions reach this branch.
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
