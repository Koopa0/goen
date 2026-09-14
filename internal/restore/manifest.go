//go:build integration

// Package restore holds the business manifest oracle used by restore drills.
package restore

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed manifest.sql
var manifestSQL string

// Collect reads the business manifest from one database connection.
func Collect(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, manifestSQL)
	if err != nil {
		return nil, fmt.Errorf("collect business manifest: %w", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if scanErr := rows.Scan(&line); scanErr != nil {
			return nil, fmt.Errorf("scan business manifest line: %w", scanErr)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate business manifest: %w", err)
	}
	return lines, nil
}

// Equal reports whether two manifests match line for line.
func Equal(live, restored []string) bool {
	return strings.Join(live, "\n") == strings.Join(restored, "\n")
}

// Diff returns a unified diff when the manifests disagree.
func Diff(live, restored []string) string {
	return diffLines("live", live, "restored", restored)
}

func diffLines(liveLabel string, live []string, restoredLabel string, restored []string) string {
	var b strings.Builder
	i, j := 0, 0
	for i < len(live) || j < len(restored) {
		switch {
		case i < len(live) && j < len(restored) && live[i] == restored[j]:
			i++
			j++
		case j >= len(restored) || i < len(live) && (j >= len(restored) || live[i] < restored[j]):
			b.WriteString("- ")
			b.WriteString(liveLabel)
			b.WriteByte('\t')
			b.WriteString(live[i])
			b.WriteByte('\n')
			i++
		default:
			b.WriteString("+ ")
			b.WriteString(restoredLabel)
			b.WriteByte('\t')
			b.WriteString(restored[j])
			b.WriteByte('\n')
			j++
		}
	}
	return b.String()
}
