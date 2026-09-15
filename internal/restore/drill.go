//go:build integration

package restore

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SnapshotArtifacts are the immutable oracle rows read inside one exported
// snapshot before it is released. Row counts and the business manifest must both
// come from this artifact, never from a later read of the live source.
type SnapshotArtifacts struct {
	SnapshotID string
	RowCounts  []string
	Manifest   []string
}

// RowCountsSQL matches the restore-drill Makefile oracle.
const RowCountsSQL = `
SELECT c.relname||' '||(xpath('/row/c/text()', query_to_xml(
	format('select count(*) as c from public.%I', c.relname), false, true, '')))[1]::text::bigint
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname='public' AND c.relkind='r'
 ORDER BY 1`

// DumpCopy exports a snapshot of sourceURL, dumps it, restores into copyName on
// the same cluster, and returns the snapshot artifacts collected before the
// transaction commits.
func DumpCopy(ctx context.Context, sourceURL, copyName string) (copyURL string, artifacts SnapshotArtifacts, cleanup func(), err error) {
	work, err := os.MkdirTemp("", "goen-restore-drill-")
	if err != nil {
		return "", SnapshotArtifacts{}, nil, err
	}
	cleanup = func() {
		if dropErr := dropDatabase(context.WithoutCancel(ctx), sourceURL, copyName); dropErr != nil {
			return
		}
		if rmErr := os.RemoveAll(work); rmErr != nil {
			return
		}
	}

	copyURL, err = siblingDatabaseURL(sourceURL, copyName)
	if err != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, err
	}

	conn, err := pgx.Connect(ctx, sourceURL)
	if err != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, err
	}
	defer closeConn(ctx, conn)

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil {
			return
		}
	}()

	if scanErr := tx.QueryRow(ctx, "SELECT pg_export_snapshot()").Scan(&artifacts.SnapshotID); scanErr != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, scanErr
	}

	dumpPath := filepath.Join(work, "goen.dump")
	dump := exec.CommandContext(ctx, "pg_dump", sourceURL, //nolint:gosec // G204: drill URLs come from the test harness or operator shell
		"--snapshot="+artifacts.SnapshotID, "-Fc", "-f", dumpPath)
	if dumpErr := dump.Run(); dumpErr != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, fmt.Errorf("pg_dump: %w", dumpErr)
	}

	artifacts.RowCounts, err = queryStringLines(ctx, tx, RowCountsSQL)
	if err != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, err
	}
	artifacts.Manifest, err = queryStringLines(ctx, tx, manifestSQL)
	if err != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, err
	}
	sort.Strings(artifacts.Manifest)

	if commitErr := tx.Commit(ctx); commitErr != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, commitErr
	}

	if createErr := createDatabase(ctx, sourceURL, copyName); createErr != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, createErr
	}

	restore := exec.CommandContext(ctx, "pg_restore", //nolint:gosec // G204: drill URLs come from the test harness or operator shell
		"-d", copyURL, "--no-owner", "--exit-on-error", dumpPath)
	if restoreErr := restore.Run(); restoreErr != nil {
		cleanup()
		return "", SnapshotArtifacts{}, nil, fmt.Errorf("pg_restore: %w", restoreErr)
	}

	return copyURL, artifacts, cleanup, nil
}

// RolePoolURL builds a login URL for one application role against dbName on the
// same host as templateURL.
func RolePoolURL(templateURL, role, password, dbName string) (string, error) {
	u, err := url.Parse(templateURL)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(role, password)
	u.Path = "/" + dbName
	return u.String(), nil
}

// CreateDrillRoles provisions ephemeral LOGIN roles for one restored copy.
func CreateDrillRoles(ctx context.Context, adminURL, password string, names ...string) error {
	if len(names) != 3 {
		return errors.New("need store, admin and maintenance drill role names")
	}
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer closeConn(ctx, conn)

	roles := []struct {
		name string
		in   string
	}{
		{names[0], "store"},
		{names[1], "admin"},
		{names[2], "maintenance"},
	}
	for _, role := range roles {
		sql := fmt.Sprintf(
			"CREATE ROLE %s LOGIN NOSUPERUSER PASSWORD %s IN ROLE %s",
			pgx.Identifier{role.name}.Sanitize(),
			quoteLiteral(password),
			pgx.Identifier{role.in}.Sanitize(),
		)
		if _, execErr := conn.Exec(ctx, sql); execErr != nil {
			return execErr
		}
	}
	return nil
}

// DropDrillRoles removes ephemeral LOGIN roles created for a restore drill.
func DropDrillRoles(ctx context.Context, adminURL string, names ...string) {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return
	}
	defer closeConn(ctx, conn)
	for _, name := range names {
		if _, dropErr := conn.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize()); dropErr != nil {
			return
		}
	}
}

func closeConn(ctx context.Context, conn *pgx.Conn) {
	if err := conn.Close(ctx); err != nil {
		return
	}
}

func createDatabase(ctx context.Context, sourceURL, copyName string) error {
	adminURL, err := maintenanceDatabaseURL(sourceURL)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect for create database: %w", err)
	}
	defer closeConn(ctx, conn)
	_, execErr := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{copyName}.Sanitize())
	if execErr != nil {
		return fmt.Errorf("create database %s: %w", copyName, execErr)
	}
	return nil
}

func dropDatabase(ctx context.Context, sourceURL, copyName string) error {
	adminURL, err := maintenanceDatabaseURL(sourceURL)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer closeConn(ctx, conn)
	_, execErr := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{copyName}.Sanitize()+" WITH (FORCE)")
	return execErr
}

func maintenanceDatabaseURL(sourceURL string) (string, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return "", err
	}
	u.Path = "/postgres"
	return u.String(), nil
}

func siblingDatabaseURL(sourceURL, copyName string) (string, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + copyName
	return u.String(), nil
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func queryStringLines(ctx context.Context, tx pgx.Tx, sql string) ([]string, error) {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if scanErr := rows.Scan(&line); scanErr != nil {
			return nil, scanErr
		}
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, rows.Err()
}
