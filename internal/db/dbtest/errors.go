//go:build integration

package dbtest

import (
	"errors"
	"fmt"
	"os"
)

var (
	errNoMigrations = errors.New("dbtest: migrations directory holds no *.up.sql files")
	errNoCaller     = errors.New("dbtest: cannot locate this source file")
)

type migrationError struct {
	file string
	err  error
}

func (e migrationError) Error() string { return fmt.Sprintf("apply %s: %v", e.file, e.err) }
func (e migrationError) Unwrap() error { return e.err }

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is derived from this repo's migrations/ glob
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(b), nil
}
