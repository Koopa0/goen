package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

const maxSKURunes = 60

// VariantForm is a new variant.
type VariantForm struct {
	SKU          string
	PriceCents   int64
	CompareCents int64
	SafetyStock  int32
	// Zero is UNMEASURED and stored as NULL, so it refuses no shipping method.
	ParcelLongestMM int32
	ParcelSumMM     int32
	ParcelWeightG   int32
	OptionValues    []string
}

// Validate refuses what the schema would.
func (f *VariantForm) Validate(ctx context.Context) map[string]string {
	f.SKU = strings.ToUpper(strings.TrimSpace(f.SKU))

	errs := map[string]string{}
	if f.SKU == "" || utf8.RuneCountInString(f.SKU) > maxSKURunes {
		errs["sku"] = i18n.T(ctx, i18n.KeyFormSKURequired)
	}
	if f.PriceCents <= 0 || f.PriceCents > MaxPriceCents {
		errs["price"] = i18n.T(ctx, i18n.KeyFormPricePositive)
	}
	if f.CompareCents != 0 && f.CompareCents <= f.PriceCents {
		errs["compare"] = i18n.T(ctx, i18n.KeyFormCompareHigher)
	}
	if f.SafetyStock < 0 {
		errs["safety"] = i18n.T(ctx, i18n.KeyFormSafetyStock)
	}
	return errs
}

// AddVariant adds a variant at zero stock.
func (s *Store) AddVariant(ctx context.Context, slug string, f *VariantForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	chosen, errs := s.chosenOptionValues(ctx, slug, f.OptionValues)
	if len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionCreateVariant, Table: "product_variants", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"product": slug, "sku": f.SKU, "price_cents": f.PriceCents},
	},
		func(ctx context.Context, q *db.Queries) error {
			if createErr := q.CreateVariant(ctx, db.CreateVariantParams{
				Slug: slug, SKU: f.SKU,
				PriceCents: f.PriceCents, CompareAtPriceCents: f.CompareCents,
				SafetyStock:     f.SafetyStock,
				ParcelLongestMm: f.ParcelLongestMM,
				ParcelSumMm:     f.ParcelSumMM,
				ParcelWeightG:   f.ParcelWeightG,
			}); createErr != nil {
				return createErr
			}
			for _, valueID := range chosen {
				n, linkErr := q.SetVariantOptionValue(ctx, db.SetVariantOptionValueParams{
					SKU: f.SKU, OptionValueID: valueID,
				})
				if linkErr != nil {
					return linkErr
				}
				if n == 0 {
					return ErrNotFound
				}
			}
			return nil
		}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "product_variants_sku_key" {
			return map[string]string{"sku": i18n.T(ctx, i18n.KeyFormSKUTaken)}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsInvalid)}, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

func (s *Store) chosenOptionValues(ctx context.Context, slug string, raw []string) (
	chosen []uuid.UUID, fieldErrs map[string]string,
) {
	options, err := s.q.ProductOptionCount(ctx, slug)
	if err != nil {
		return nil, map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsUnreadable)}
	}
	chosen = make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		if value == "" {
			continue
		}
		id, parseErr := uuid.Parse(value)
		if parseErr != nil {
			return nil, map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsInvalid)}
		}
		chosen = append(chosen, id)
	}
	if int64(len(chosen)) != options {
		return nil, map[string]string{
			"options": i18n.T(ctx, i18n.KeyFormVariantNeedsEveryOption),
		}
	}
	return chosen, nil
}
