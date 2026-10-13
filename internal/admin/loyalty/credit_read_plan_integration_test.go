//go:build integration

package loyalty_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/web"
)

type creditQueryTrace struct {
	sql  string
	args []any
}

func (q *creditQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(strings.ToLower(data.SQL), "store_credit_entries") && !strings.HasPrefix(strings.TrimSpace(data.SQL), "EXPLAIN") {
		q.sql = data.SQL
		q.args = append([]any(nil), data.Args...)
	}
	return ctx
}

func (*creditQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type creditPlan struct {
	RelationName              string       `json:"Relation Name"`
	IndexName                 string       `json:"Index Name"`
	ActualRows                float64      `json:"Actual Rows"`
	ActualLoops               float64      `json:"Actual Loops"`
	RowsRemovedByFilter       float64      `json:"Rows Removed by Filter"`
	RowsRemovedByIndexRecheck float64      `json:"Rows Removed by Index Recheck"`
	Plans                     []creditPlan `json:"Plans"`
}

func TestRecentCreditBoundsItsGlobalPageAndReadsOnlySelectedAccountHistory(t *testing.T) {
	ctx := t.Context()
	owner := dbtest.Pool(t)
	const accounts = 300
	const entriesPerAccount = 20
	prefix := uuid.NewString()
	for i := range accounts {
		var customer uuid.UUID
		if err := owner.QueryRow(ctx, `INSERT INTO users(email) VALUES ($1) RETURNING id`, prefix+"-"+strconv.Itoa(i)+"@example.com").Scan(&customer); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, `SELECT post_store_credit($1,100,'Index fixture',NULL,'plan:'||$1::text||':'||n::text,NULL) FROM generate_series(1,$2) n`, customer, entriesPerAccount); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"store_credit_entries", "store_credit_accounts", "users"} {
		if _, err := owner.Exec(ctx, "ANALYZE "+table); err != nil {
			t.Fatal(err)
		}
	}
	trace := &creditQueryTrace{}
	config := owner.Config().Copy()
	config.MaxConns = 2
	config.ConnConfig.Tracer = trace
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE admin`)
		return err
	}
	reader, poolErr := pgxpool.NewWithConfig(ctx, config)
	if poolErr != nil {
		t.Fatal(poolErr)
	}
	t.Cleanup(reader.Close)
	var role string
	if err := reader.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("reader role=%q,%v,want admin", role, err)
	}
	view, readErr := loyalty.NewStore(reader).Credit(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(view.Rows) != web.PageSize || view.Next == "" {
		t.Fatalf("recent page has %d rows,next=%q", len(view.Rows), view.Next)
	}
	if trace.sql == "" {
		t.Fatal("the actual RecentCredit query was not observed")
	}
	var encoded []byte
	if err := reader.QueryRow(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+trace.sql, trace.args...).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var explained []struct{ Plan creditPlan }
	if err := json.Unmarshal(encoded, &explained); err != nil {
		t.Fatal(err)
	}
	if len(explained) != 1 {
		t.Fatalf("EXPLAIN produced %d plans,want 1", len(explained))
	}
	var ledgerRows float64
	globalIndex := false
	var visit func(creditPlan)
	visit = func(node creditPlan) {
		if node.IndexName == "store_credit_entries_created_idx" {
			globalIndex = true
			if node.ActualRows > float64(web.PageLimit) || node.ActualLoops != 1 {
				t.Errorf("global keyset index read %d rows in %d loops,want at most %d once", int(node.ActualRows), int(node.ActualLoops), web.PageLimit)
			}
		}
		if node.RelationName == "store_credit_entries" {
			ledgerRows += (node.ActualRows + node.RowsRemovedByFilter + node.RowsRemovedByIndexRecheck) * node.ActualLoops
		}
		for _, child := range node.Plans {
			visit(child)
		}
	}
	visit(explained[0].Plan)
	if !globalIndex {
		t.Errorf("global keyset page did not use its ordering index: %s", encoded)
	}
	if ledgerRows >= accounts*entriesPerAccount {
		t.Errorf("recent page traversed %.0f ledger rows against %d fixture rows: %s", ledgerRows, accounts*entriesPerAccount, encoded)
	}
	t.Logf("RecentCredit plan: global keyset index=%t; ledger rows traversed=%.0f of %d", globalIndex, ledgerRows, accounts*entriesPerAccount)
}
