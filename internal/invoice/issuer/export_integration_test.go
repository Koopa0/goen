//go:build integration

package issuer

import "github.com/jackc/pgx/v5/pgxpool"

// SuitePool is the database TestMain started, for the external test files
// (package issuer_test): a test that places an order through checkout cannot be
// internal, because checkout imports this package.
func SuitePool() *pgxpool.Pool { return pool }
