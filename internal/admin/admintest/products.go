//go:build integration

package admintest

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func AnyProductSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}
