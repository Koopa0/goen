package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"

	"github.com/koopa0/goen/internal/i18n"
)

var (
	// ErrAlreadyReviewed is a second review on one product from one person.
	ErrAlreadyReviewed = errors.New("product: already reviewed")
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
	for _, c := range r.Body + r.Title {
		if unicode.IsControl(c) && c != '\n' && c != '\t' && c != '\r' {
			errs["body"] = i18n.KeyReviewBodyUnprintable
			break
		}
	}
	return errs
}

// CanReview reports whether this customer may review, and whether it would
// carry the verified badge.
func (s *Store) CanReview(ctx context.Context, slug, userID string) (allowed, verified bool, err error) {
	id, parseErr := uuid.Parse(userID)
	if parseErr != nil {
		return false, false, nil //nolint:nilerr // not signed in is not an error
	}
	owner := uuid.NullUUID{UUID: id, Valid: true}

	reviewed, err := s.q.HasReviewed(ctx, db.HasReviewedParams{UserID: owner, Slug: slug})
	if err != nil {
		return false, false, fmt.Errorf("check existing review: %w", err)
	}
	if reviewed {
		return false, false, nil
	}
	bought, err := s.q.HasBoughtProduct(ctx, db.HasBoughtProductParams{UserID: owner, Slug: slug})
	if err != nil {
		return false, false, fmt.Errorf("check purchase: %w", err)
	}
	return true, bought, nil
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

	allowed, verified, err := s.CanReview(ctx, slug, userID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrAlreadyReviewed
	}

	if err := s.q.CreateReview(ctx, db.CreateReviewParams{
		Slug: slug, UserID: owner, Rating: r.Rating,
		Title: r.Title, Body: r.Body, Verified: verified,
	}); err != nil {
		if strings.Contains(err.Error(), "product_reviews_author_key") {
			return nil, ErrAlreadyReviewed
		}
		return nil, fmt.Errorf("create review: %w", err)
	}
	return nil, nil
}
