//go:build integration

package db_test

import (
	"errors"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestEveryRoleCanRunItsOwnQueries refuses a role that cannot run a query its own code calls.
func TestEveryRoleCanRunItsOwnQueries(t *testing.T) {
	ctx := t.Context()
	byQuery := queryWrites(t)
	sql := generatedSQL(t)
	if len(sql) != len(byQuery) {
		t.Fatalf("parsed %d query bodies and %d write sets — the two parsers disagree",
			len(sql), len(byQuery))
	}

	conn, err := schemaPool(t).Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire a connection: %v", err)
	}
	defer conn.Release()

	checked := 0
	for _, role := range []string{"store", "admin", "maintenance"} {
		for _, pkg := range packagesOn(role) {
			for _, method := range calledQueries(t, pkg) {
				key := role + "." + method
				if _, ok := runExemptions[key]; ok {
					continue
				}
				body, ok := sql[method]
				if !ok {
					continue
				}
				checked++

				if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
					t.Fatalf("assume %s: %v", role, err)
				}
				_, planErr := conn.Exec(ctx, "EXPLAIN (GENERIC_PLAN) "+body)
				if _, resetErr := conn.Exec(ctx, "RESET ROLE"); resetErr != nil {
					t.Fatalf("reset role: %v", resetErr)
				}
				if planErr == nil {
					continue
				}
				// 42501 is insufficient_privilege, the only failure this test is about.
				pgErr, isPg := errors.AsType[*pgconn.PgError](planErr)
				if !isPg || pgErr.Code != "42501" {
					t.Errorf("%s: EXPLAIN of %s failed for a reason that is not a "+
						"privilege: %v\n  If GENERIC_PLAN cannot plan this statement, "+
						"name it in runExemptions with that reason.", role, method, planErr)
					continue
				}
				t.Errorf("%s CANNOT run %s: %s\n"+
					"  A role that cannot run a query its own code calls is a feature "+
					"that fails at runtime and nowhere else — and when the caller is a "+
					"background worker, nobody is watching. Either grant it, or run that "+
					"code on the pool whose role can, or name it in runExemptions.",
					role, method, pgErr.Message)
			}
		}
	}
	if checked < 200 {
		t.Fatalf("only %d (role, query) pairs were checked, want far more — the "+
			"pool map or the query parser is wrong", checked)
	}
	t.Logf("checked %d (role, query) pairs", checked)
}

// runExemptions is a query a package calls that one of its roles cannot run, keyed role.Query.
var runExemptions = map[string]string{
	"store.DeleteMedia":                  "the sweeper runs on the admin pool; store serves images and never deletes one",
	"store.AttributeCompletePaymentPaid": "payment owns the capture invariants and side effects, but manual paid attribution is called only with an audited admin transaction; store must not hold that privilege",
	"store.PutMedia":                     "only the back office uploads; store serves what is already stored",
	"store.RecordNewsletterSend":         "the back office sends and audits; store only subscribes",
	"store.CreateNewsletterIssue":        "the back office composes; store only subscribes",
	"store.MarkNewsletterIssueSent":      "the back office sends; store only subscribes",
	"admin.RequestNewsletterConfirm":     "only a visitor asks to join; admin holds no write on newsletter_confirmations by design",
	"admin.SpendNewsletterConfirmation":  "only the mailbox owner confirms",
	"admin.AddNewsletterSubscriber":      "the shop cannot put an address on its own list",
	"admin.UnsubscribeNewsletter":        "only the address owner leaves",
}

// TestNoStaleRunExemption refuses a runExemptions entry the schema has outgrown, by identity.
func TestNoStaleRunExemption(t *testing.T) {
	ctx := t.Context()
	sql := generatedSQL(t)

	conn, err := schemaPool(t).Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire a connection: %v", err)
	}
	defer conn.Release()

	for _, key := range sortedStrings(runExemptions) {
		role, method, ok := strings.Cut(key, ".")
		if !ok {
			t.Errorf("runExemptions key %q is not role.Query", key)
			continue
		}
		body, known := sql[method]
		if !known {
			t.Errorf("runExemptions has %q, and no generated query is called %s — "+
				"the entry names something that no longer exists", key, method)
			continue
		}
		if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
			t.Fatalf("assume %s: %v", role, err)
		}
		_, planErr := conn.Exec(ctx, "EXPLAIN (GENERIC_PLAN) "+body)
		if _, resetErr := conn.Exec(ctx, "RESET ROLE"); resetErr != nil {
			t.Fatalf("reset role: %v", resetErr)
		}
		pgErr, isPg := errors.AsType[*pgconn.PgError](planErr)
		if planErr == nil || !isPg || pgErr.Code != "42501" {
			t.Errorf("runExemptions has %q (%s), but %s CAN run %s now. The entry "+
				"claims a gap that is closed, and an exemption nobody revisits is how "+
				"a list stops describing the schema.", key, runExemptions[key], role, method)
		}
	}
}

// generatedSQL is each generated query method mapped to its SQL body.
func generatedSQL(t *testing.T) map[string]string {
	t.Helper()
	src, err := os.ReadFile("query.sql.go")
	if err != nil {
		t.Fatalf("read the generated queries: %v", err)
	}
	out := map[string]string{}
	for _, m := range generatedQuery.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = m[2]
	}
	if len(out) < 100 {
		t.Fatalf("parsed %d generated queries, want far more — the pattern is wrong",
			len(out))
	}
	return out
}

// packagesOn is the feature packages whose stores are constructed on a role's pool.
func packagesOn(role string) []string {
	switch role {
	case "store":
		return storefrontPackages
	case "admin":
		return backOfficePackages
	case "maintenance":
		return maintenancePackages
	default:
		panic("db: unknown role in the pool map: " + role)
	}
}

// sortedStrings is a map's keys in a stable order.
func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
