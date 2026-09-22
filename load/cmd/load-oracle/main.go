package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/load/oracle"
)

func main() {
	profile := flag.String("profile", "", "load profile name")
	variant := flag.String("variant", oracle.FlashSaleVariantID.String(), "variant UUID for stock oracle")
	minSuccess := flag.Int64("min-success", 1, "minimum successful checks during degraded runs")
	flag.Parse()

	dbURL := os.Getenv("GOEN_LOAD_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("GOEN_DATABASE_URL")
	}
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "GOEN_LOAD_DATABASE_URL is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	code := run(ctx, dbURL, *profile, *variant, *minSuccess)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, dbURL, profile, variant string, minSuccess int64) int {
	pool, stop, err := oracle.OpenPool(ctx, dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	defer stop()

	if _, err := oracle.MustParseUUID(variant); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	if err := checkProfile(ctx, pool, profile, variant, minSuccess); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	slog.Info("load oracle passed", "profile", profile)
	return 0
}

func checkStockEvidence(ctx context.Context, pool *pgxpool.Pool) error {
	file, err := os.Open(os.Getenv("LOAD_STOCK_EVIDENCE")) //nolint:gosec // G304: explicit local load-run evidence input
	if err != nil {
		return fmt.Errorf("open stock evidence: %w", err)
	}
	defer file.Close()
	run, err := oracle.ReadStockRun(file, os.Getenv("LOAD_RUN_ID"))
	if err != nil {
		return err
	}
	return oracle.CheckStockRun(ctx, pool, run)
}

func checkProfile(ctx context.Context, pool *pgxpool.Pool, profile, variant string, minSuccess int64) error {
	variantID, err := oracle.MustParseUUID(variant)
	if err != nil {
		return err
	}
	if err := oracle.CheckNoDuplicateOrders(ctx, pool); err != nil {
		return err
	}
	switch profile {
	case "stock-contention":
		if err := checkStockEvidence(ctx, pool); err != nil {
			return err
		}
		return oracle.CheckNoOversell(ctx, pool, variantID)
	case "mixed-ops", "":
		return oracle.CheckNoOversell(ctx, pool, variantID)
	case "dependency-failure":
		return checkDegradedWork(minSuccess)
	default:
		return nil
	}
}

func checkDegradedWork(minSuccess int64) error {
	success, err := oracle.ParseRequiredCount("LOAD_ORACLE_SUCCESS_COUNT", os.Getenv("LOAD_ORACLE_SUCCESS_COUNT"))
	if err != nil {
		return err
	}
	total, err := oracle.ParseRequiredCount("LOAD_ORACLE_TOTAL_COUNT", os.Getenv("LOAD_ORACLE_TOTAL_COUNT"))
	if err != nil {
		return err
	}
	return oracle.DegradedWork(success, total, minSuccess)
}
