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
		success, err := oracle.ParseRequiredCount("LOAD_ORACLE_SUCCESS_COUNT", os.Getenv("LOAD_ORACLE_SUCCESS_COUNT"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		total, err := oracle.ParseRequiredCount("LOAD_ORACLE_TOTAL_COUNT", os.Getenv("LOAD_ORACLE_TOTAL_COUNT"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		if err := oracle.DegradedWork(success, total, minSuccess); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
	}

	slog.Info("load oracle passed", "profile", profile)
	return 0
}
