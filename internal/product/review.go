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

// Review-specific sentinel errors, each a distinct decision for the handler.
var (
	// ErrAlreadyReviewed is a second review on one product from one person.
	// product_reviews_author_key refuses it underneath; this makes the refusal
	// a message rather than a 500.
	ErrAlreadyReviewed = errors.New("product: already reviewed")
	// ErrReviewInvalid is a form goen refused before the database saw it.
	ErrReviewInvalid = errors.New("product: invalid review")
)

// Review length bounds, counted in RUNES.
//
// A Traditional Chinese review is three bytes a character, so a byte limit
// would give a Chinese reviewer a third of the room an English one gets — and
// could cut a character in half.
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

// Validate refuses what the schema would.
//
// It returns message KEYS, not sentences: this runs from a handler and from a
// test, and the words belong to whoever knows which locale is reading. The
// caller renders them.
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
		// Newlines and tabs are allowed: a review is a paragraph. Everything
		// else in the control range is invisible and only useful for smuggling.
		if unicode.IsControl(c) && c != '\n' && c != '\t' && c != '\r' {
			errs["body"] = i18n.KeyReviewBodyUnprintable
			break
		}
	}
	return errs
}

// CanReview reports whether this customer may leave a review, and whether it
// would carry the verified badge.
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
//
// Anyone signed in may review; only a committed purchase earns the badge. That
// is a deliberate split: a shop that only lets buyers speak hides the opinions
// of people who returned something, and a badge that anyone can claim is worth
// nothing. product_reviews_verified_is_real refuses a false claim underneath,
// so a bug here becomes a refusal rather than an unearned badge.
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
