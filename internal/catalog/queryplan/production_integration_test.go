//go:build integration

package queryplan_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/catalog/queryplan"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
)

func TestHarnessUsesProductionSearchSQL(t *testing.T) {
	ctx := t.Context()
	pool, stop, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if loadErr := queryplan.LoadSeed(ctx, pool, queryplan.ScaleLarge); loadErr != nil {
		t.Fatal(loadErr)
	}

	queries := queryplan.Queries(queryplan.ScaleLarge, nil, uuid.Nil)

	var searchBrand queryplan.Query
	for _, q := range queries {
		if q.Route == queryplan.RouteSearchBrand && !q.CountRead {
			searchBrand = q
			break
		}
	}
	if searchBrand.SQL == "" {
		t.Fatal("missing search_brand list query")
	}
	if searchBrand.SQL != db.HarnessCatalogueSQL.SearchProducts {
		t.Fatal("harness search SQL diverged from sqlc production text")
	}
	wantArgs := []any{string(i18n.Default), "%Meridian%", int32(0), int32(catalog.PageSize)}
	if len(searchBrand.Args) != len(wantArgs) {
		t.Fatalf("search args len = %d, want %d", len(searchBrand.Args), len(wantArgs))
	}
	for i := range wantArgs {
		if searchBrand.Args[i] != wantArgs[i] {
			t.Fatalf("search arg[%d] = %v, want %v", i, searchBrand.Args[i], wantArgs[i])
		}
	}

	r, err := queryplan.Measure(ctx, pool, searchBrand, queryplan.ScaleLarge, false, 0, "harness-test")
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.ToLower(string(r.PlanJSON))
	if !strings.Contains(plan, "product_images") {
		t.Fatal("production search plan missing product_images")
	}
	if !strings.Contains(plan, "review") {
		t.Fatal("production search plan missing review aggregation")
	}
	if !strings.Contains(db.HarnessCatalogueSQL.SearchProducts, "product_specs") {
		t.Fatal("production search SQL missing product_specs predicate")
	}

	var searchCount queryplan.Query
	for _, q := range queries {
		if q.Route == queryplan.RouteSearchBrand && q.CountRead {
			searchCount = q
			break
		}
	}
	if searchCount.SQL != db.HarnessCatalogueSQL.SearchProductsCount {
		t.Fatal("harness search count SQL diverged from sqlc production text")
	}
	if !strings.Contains(db.HarnessCatalogueSQL.SearchProductsCount, "product_specs") {
		t.Fatal("production search count predicate missing product_specs")
	}
	assertCategoryProductionPages(t, pool)
	assertSearchPageEnrichment(t, r.PlanJSON)
	t.Run("page semantics", func(t *testing.T) {
		assertSearchPageSemantics(t, pool)
	})
}
