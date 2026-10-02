//go:build integration

package loyalty_test

import (
	"errors"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/loyalty"
)

func TestTwoTiersCannotShareAThreshold(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := loyalty.NewStore(pool)

	if err := s.CreateTier(ctx, "band_a", "甲", "Band A", 90000, 120); err != nil {
		t.Fatalf("create the first band: %v", err)
	}
	err := s.CreateTier(ctx, "band_b", "乙", "", 90000, 130)
	if !errors.Is(err, loyalty.ErrRefused) {
		t.Errorf("a second band at the same threshold answered %v, want loyalty.ErrRefused", err)
	}
}

func TestATierCannotEarnLessThanNoTier(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := loyalty.NewStore(pool)

	if err := s.CreateTier(ctx, "worse", "倒扣", "", 80000, 90); !errors.Is(err, loyalty.ErrInvalid) {
		t.Errorf("a band below the base rate answered %v, want loyalty.ErrInvalid", err)
	}
	if err := s.CreateTier(ctx, "worse", "基本", "Base", 80000, 100); err != nil {
		t.Errorf("a band at the base rate was refused: %v", err)
	}
}
