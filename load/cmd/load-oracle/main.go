package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

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

	variantID, err := oracle.MustParseUUID(variant)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	if err := oracle.CheckNoDuplicateOrders(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	switch profile {
	case "stock-contention", "mixed-ops", "":
		if err := oracle.CheckNoOversell(ctx, pool, variantID); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
	case "dependency-failure":
		if floor := envInt64("LOAD_ORACLE_MIN_SUCCESS", minSuccess); floor > 0 {
			success := envInt64("LOAD_ORACLE_SUCCESS_COUNT", floor)
			total := envInt64("LOAD_ORACLE_TOTAL_COUNT", success)
			if err := oracle.DegradedWork(success, total, floor); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				return 1
			}
		}
	}

	slog.Info("load oracle passed", "profile", profile)
	return 0
}

func envInt64(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	var v int64
	if _, err := fmt.Sscan(raw, &v); err != nil {
		return fallback
	}
	return v
}
