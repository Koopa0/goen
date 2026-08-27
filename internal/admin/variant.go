package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

const (
	maxSKURunes = 60
	// The parcel ceilings mirror the product_variants parcel CHECKs.
	parcelLongestCeilingMM = 5000
	parcelSumCeilingMM     = 15000
	parcelWeightCeilingG   = 200000
	// safetyStockCeiling is an application anti-typo bound, not a schema mirror.
	safetyStockCeiling = 1_000_000
)

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
	f.validateFulfilment(ctx, errs)
	return errs
}

func (f *VariantForm) validateFulfilment(ctx context.Context, errs map[string]string) {
	if f.SafetyStock < 0 || f.SafetyStock > safetyStockCeiling {
		errs["safety"] = i18n.T(ctx, i18n.KeyFormSafetyStock)
	}
	if f.ParcelLongestMM < 0 || f.ParcelLongestMM > parcelLongestCeilingMM {
		errs["parcel_longest"] = fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormParcelMeasurement), parcelLongestCeilingMM)
	}
	if f.ParcelSumMM < 0 || f.ParcelSumMM > parcelSumCeilingMM {
		errs["parcel_sum"] = fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormParcelMeasurement), parcelSumCeilingMM)
	}
	if f.ParcelWeightG < 0 || f.ParcelWeightG > parcelWeightCeilingG {
		errs["parcel_weight"] = fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormParcelMeasurement), parcelWeightCeilingG)
	}
	if f.ParcelLongestMM != 0 && f.ParcelSumMM != 0 && f.ParcelSumMM < f.ParcelLongestMM {
		errs["parcel_sum"] = i18n.T(ctx, i18n.KeyFormParcelSumShort)
	}
}

// AddVariant adds a variant at zero stock.
func (s *Store) AddVariant(ctx context.Context, slug string, f *VariantForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	chosen, errs, err := s.chosenOptionValues(ctx, slug, f.OptionValues)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return errs, nil
	}

	if err := s.insertVariant(ctx, slug, f, chosen); err != nil {
		return variantWriteError(ctx, slug, err)
	}
	return nil, nil
}

func (s *Store) insertVariant(
	ctx context.Context, slug string, f *VariantForm, chosen []uuid.UUID,
) error {
	return s.audited(ctx, Event{
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
		})
}

func variantWriteError(ctx context.Context, slug string, err error) (map[string]string, error) {
	if hasConstraint(err, "product_variants_sku_key") {
		return map[string]string{"sku": i18n.T(ctx, i18n.KeyFormSKUTaken)}, nil
	}
	if hasConstraint(err, "product_variants_safety_stock_non_negative") {
		return map[string]string{"safety": i18n.T(ctx, i18n.KeyFormSafetyStock)}, nil
	}
	if hasConstraint(err, "product_variants_parcel_longest_sane") {
		return map[string]string{"parcel_longest": fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormParcelMeasurement), parcelLongestCeilingMM)}, nil
	}
	if hasConstraint(err, "product_variants_parcel_sum_sane") {
		return map[string]string{"parcel_sum": fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormParcelMeasurement), parcelSumCeilingMM)}, nil
	}
	if hasConstraint(err, "product_variants_parcel_weight_sane") {
		return map[string]string{"parcel_weight": fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormParcelMeasurement), parcelWeightCeilingG)}, nil
	}
	if hasConstraint(err, "product_variants_parcel_sum_covers_longest") {
		return map[string]string{"parcel_sum": i18n.T(ctx, i18n.KeyFormParcelSumShort)}, nil
	}
	if errors.Is(err, ErrNotFound) {
		return map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsInvalid)}, nil
	}
	return nil, fmt.Errorf("add variant to product %s: %w", slug, err)
}

func (s *Store) chosenOptionValues(ctx context.Context, slug string, raw []string) (
	chosen []uuid.UUID, fieldErrs map[string]string, err error,
) {
	options, err := s.q.ProductOptionCount(ctx, slug)
	if err != nil {
		return nil, nil, fmt.Errorf("count options for product %s: %w", slug, err)
	}
	chosen = make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		if value == "" {
			continue
		}
		id, parseErr := uuid.Parse(value)
		if parseErr != nil {
			return nil, map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsInvalid)}, nil
		}
		chosen = append(chosen, id)
	}
	if int64(len(chosen)) != options {
		return nil, map[string]string{
			"options": i18n.T(ctx, i18n.KeyFormVariantNeedsEveryOption),
		}, nil
	}
	return chosen, nil, nil
}
