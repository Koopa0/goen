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

// TestEveryRoleCanRunItsOwnQueries asks the direction nothing asked.
//
// # What was missing
//
// [TestNoRoleHoldsAWriteItsQueriesNeverMake] computes two sets — what a role's
// queries write, and what it is granted — and errors only when the grant is
// WIDER than the need. The narrower case was never tested, with both sets
// already in hand. And [TestEveryRoleCanReadWhatItsQueriesRead], despite its
// name, was seven hand-written statements over views.
//
// So a privilege model tightened too far failed nowhere. Two live defects
// shipped behind that gap, both in background workers where nobody watches:
// the media sweeper could not DELETE `media_objects` and the access-grant
// retention sweep could not DELETE `order_access_grants`, so uploaded images
// were never reclaimed and a bearer credential the code's own comment calls
// "a live bearer credential for nobody" was kept forever. Both were found by a
// reviewer running this check by hand.
//
// # Why this is not "structurally impossible", which is what the last
// # acceptance prompt claimed
//
// That prompt told a reviewer this could only be found by connecting as
// store_svc and clicking through the whole application, because "every suite
// connects as the schema OWNER, who is subject to no missing grant". That was
// wrong, and being wrong sent a reviewer on a much longer errand than necessary.
//
// SET ROLE binds ACLs even for a superuser, and EXPLAIN (GENERIC_PLAN) makes
// PostgreSQL plan a statement — resolving every table, column and function
// privilege — WITHOUT executing it and without bound parameters. So the whole
// question is answerable in a test, against every generated query, in seconds.
// A guard that is coarse and true beats one that is precise and unwritten; a
// guard somebody talked themselves out of writing beats neither.
func TestEveryRoleCanRunItsOwnQueries(t *testing.T) {
	ctx := t.Context()
	byQuery := queryWrites(t) // presence check only; the SQL comes from generatedSQL
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
				// 42501 is insufficient_privilege and the only failure this test
				// is about. Anything else — a statement GENERIC_PLAN cannot plan,
				// a type it cannot infer — is reported separately rather than
				// counted as a privilege finding, because a guard that reports
				// one thing as another is how the media sweeper's permission
				// denial got logged as "the guard working".
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

// runExemptions is a query a package calls that one of its roles cannot run,
// with the reason that is correct. Keyed role.Query so an exemption cannot
// spread to another role.
//
// Every entry here exists because a PACKAGE spans two pools. internal/media is
// constructed on the storefront pool to serve images and on the admin pool for
// the back office and the sweeper; internal/newsletter has had two halves on two
// pools since it was built. The pool map is per package because that is the unit
// cmd/goen wires, and these are the queries where that granularity is too coarse.
//
// [TestNoStaleRunExemption] refuses an entry whose role CAN now run the query,
// so the list cannot quietly describe a world that has moved on.
var runExemptions = map[string]string{
	// The media sweeper runs on the ADMIN pool: reclaiming an unreferenced image
	// is the shop's own housekeeping, not something a request does. store serves
	// images and must never delete one.
	"store.DeleteMedia": "the sweeper runs on the admin pool; store serves images and never deletes one",
	"store.PutMedia":    "only the back office uploads; store serves what is already stored",
	// record_audit_event is the ONE door to audit_events and only the back office
	// goes through it. A storefront request has no actor to attribute.
	"store.RecordNewsletterSend": "the back office sends and audits; store only subscribes",
	// The back office composes and sends; the storefront subscribes. Two halves,
	// two pools, since internal/newsletter was written.
	"store.CreateNewsletterIssue":   "the back office composes; store only subscribes",
	"store.MarkNewsletterIssueSent": "the back office sends; store only subscribes",
	// The outbox worker DELIVERS on the storefront pool and the back office only
	// READS the queue at /admin/health. The retention sweep is the admin half.
	// The four halves of double opt-in that only a VISITOR performs. The back
	// office deliberately holds no write on either table — that is what double
	// opt-in MEANS, and leaving it to a convention is how a back office grows an
	// "add subscriber" form. These four are the model working, not a gap.
	"admin.RequestNewsletterConfirm":    "only a visitor asks to join; admin holds no write on newsletter_confirmations by design",
	"admin.SpendNewsletterConfirmation": "only the mailbox owner confirms",
	"admin.AddNewsletterSubscriber":     "the shop cannot put an address on its own list",
	"admin.UnsubscribeNewsletter":       "only the address owner leaves",
}

// TestNoStaleRunExemption refuses an entry the schema has outgrown.
//
// By IDENTITY rather than by count: comparing totals is what let an entry naming
// a query that no longer existed pass in TestEveryCategoryNameIsLocalized, and it
// immediately found two entries added on a guess.
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

// packagesOn is the feature packages whose stores are constructed on a role's
// pool. It reuses the one pool map, so a package added to that map is covered
// here by existing.
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

// sortedStrings is a map's keys in a stable order, so a failing run names things
// in the same sequence every time.
func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
