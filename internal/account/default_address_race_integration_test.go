//go:build integration

package account_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/user"
)

func TestConcurrentDefaultAddressSelectionsBothSave(t *testing.T) {
	runDefaultAddressRace(t, "selection", "selection")
}

func TestConcurrentDefaultAddressAdditionAndSelectionBothSave(t *testing.T) {
	runDefaultAddressRace(t, "addition", "selection")
}

func TestConcurrentDefaultAddressAdditionsBothSave(t *testing.T) {
	runDefaultAddressRace(t, "addition", "addition")
}

func runDefaultAddressRace(t *testing.T, firstKind, secondKind string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ownerStore := account.NewStore(pool)
	u := register(t, ownerStore, "default-race-"+uuid.NewString()+"@goen.invalid")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, u.ID); err != nil {
			t.Errorf("remove default-address fixture: %v", err)
		}
	})
	firstID := addAddress(t, ownerStore, u.ID, "first saved", false)
	secondID := addAddress(t, ownerStore, u.ID, "second saved", false)
	if n := countDefaults(t, u.ID); n != 0 {
		t.Fatalf("initial defaults = %d, want zero", n)
	}

	firstBlock, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	defer pgtx.Rollback(ctx, firstBlock)
	holdDefaultAddressWriter(t, ctx, firstBlock, u.ID, firstID, firstKind)
	var firstPID int
	if err := firstBlock.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&firstPID); err != nil {
		t.Fatal(err)
	}
	var secondBlock pgx.Tx
	var secondPID int
	if secondKind == "selection" {
		secondBlock, beginErr = pool.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer pgtx.Rollback(ctx, secondBlock)
		holdDefaultAddressWriter(t, ctx, secondBlock, u.ID, secondID, secondKind)
		if err := secondBlock.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&secondPID); err != nil {
			t.Fatal(err)
		}
	}

	prefix := "default-address-" + uuid.NewString()
	firstPool := accountStorePool(t, prefix+"-first")
	secondPool := accountStorePool(t, prefix+"-second")
	for _, p := range []*pgxpool.Pool{firstPool, secondPool} {
		var role string
		if err := p.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
			t.Fatalf("request database role = %q, want store: %v", role, err)
		}
	}
	var workers sync.WaitGroup
	t.Cleanup(func() { cancel(); workers.Wait() })
	firstHandler := account.NewHandler(account.NewStore(firstPool), nil, slog.New(slog.DiscardHandler), false, nil)
	secondHandler := account.NewHandler(account.NewStore(secondPool), nil, slog.New(slog.DiscardHandler), false, nil)
	firstDone := startDefaultAddressRequest(ctx, &workers, firstHandler, u, firstKind, firstID, "first added")
	// A selected row is held before its UPDATE; an addition is held at its user FK.
	waitForDefaultAddressBlock(t, ctx, prefix+"-first", firstPID, firstDone)
	var firstWriterPID int
	if err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE application_name=$1`, prefix+"-first").Scan(&firstWriterPID); err != nil {
		t.Fatal(err)
	}
	secondDone := startDefaultAddressRequest(ctx, &workers, secondHandler, u, secondKind, secondID, "second added")
	if secondKind == "addition" {
		// The second insert must reach the first insert's unique-index transaction.
		secondPID = firstWriterPID
	}
	waitForDefaultAddressBlock(t, ctx, prefix+"-second", secondPID, secondDone)
	if err := firstBlock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertDefaultAddressSaved(t, ctx, firstDone)
	if secondBlock != nil {
		if err := secondBlock.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertDefaultAddressSaved(t, ctx, secondDone)

	type savedAddress struct {
		Label   string
		Default bool
	}
	rows, err := pool.Query(ctx, `SELECT label,is_default FROM addresses WHERE user_id=$1 ORDER BY label`, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[savedAddress])
	if err != nil {
		t.Fatal(err)
	}
	want := []savedAddress{{Label: "first saved"}, {Label: "second saved", Default: true}}
	if firstKind == "addition" {
		want = append([]savedAddress{{Label: "first added"}}, want...)
	}
	if secondKind == "addition" {
		want = []savedAddress{{Label: "first added"}, {Label: "first saved"}, {Label: "second added", Default: true}, {Label: "second saved"}}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("saved address book (-want +got):\n%s", diff)
	}
	if n := countDefaults(t, u.ID); n != 1 {
		t.Errorf("final defaults = %d, want one", n)
	}
}

func holdDefaultAddressWriter(t *testing.T, ctx context.Context, tx pgx.Tx, userID, addressID, kind string) {
	t.Helper()
	var err error
	if kind == "selection" {
		_, err = tx.Exec(ctx, `SELECT id FROM addresses WHERE id=$1 FOR UPDATE`, addressID)
	} else {
		_, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func startDefaultAddressRequest(ctx context.Context, workers *sync.WaitGroup, h *account.Handler, u user.User, kind, addressID, label string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	workers.Go(func() {
		form := url.Values{"address": {addressID}}
		action := h.MakeDefaultAddress
		if kind == "addition" {
			form = url.Values{"label": {label}, "name": {"Recipient"}, "phone": {"0912345678"}, "postal_code": {"110"}, "city": {"Taipei"}, "district": {"Xinyi"}, "street": {"1 Test Road"}, "default": {"1"}}
			action = h.AddAddress
		}
		r := httptest.NewRequestWithContext(user.NewContext(ctx, u), http.MethodPost, "/account/address", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		action(response, r)
		done <- response
	})
	return done
}

func waitForDefaultAddressBlock(t *testing.T, ctx context.Context, application string, blocker int, done <-chan *httptest.ResponseRecorder) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND $2=ANY(pg_blocking_pids(pid)))`, application, blocker).Scan(&blocked)
		if err != nil {
			t.Fatalf("observe address writer lock: %v", err)
		}
		if blocked {
			return
		}
		select {
		case response := <-done:
			t.Fatalf("writer finished before its intended database lock: %d %s", response.Code, response.Body.String())
		case <-ctx.Done():
			t.Fatalf("writer never reached its intended database lock: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertDefaultAddressSaved(t *testing.T, ctx context.Context, done <-chan *httptest.ResponseRecorder) {
	t.Helper()
	select {
	case response := <-done:
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/account?saved=1" {
			t.Errorf("address write = %d %q, want 303 /account?saved=1; body %s", response.Code, response.Header().Get("Location"), response.Body.String())
		}
	case <-ctx.Done():
		t.Fatalf("address writer did not finish: %v", ctx.Err())
	}
}

func TestDefaultAddressWritesPreserveUnrelatedFailureAndCancellation(t *testing.T) {
	s := account.NewStore(pool)
	a := &account.Address{Name: "Recipient", Phone: "0912345678", PostalCode: "110", City: "Taipei", District: "Xinyi", Street: "1 Test Road", Default: true}
	err := s.AddAddress(t.Context(), uuid.NewString(), a)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23503" {
		t.Errorf("missing address owner error = %v, want original foreign-key refusal", err)
	}
	for _, write := range []func(context.Context) error{
		func(ctx context.Context) error { return s.AddAddress(ctx, uuid.NewString(), a) },
		func(ctx context.Context) error { return s.MakeDefaultAddress(ctx, uuid.NewString(), uuid.NewString()) },
	} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := write(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled address write = %v, want context cancellation", err)
		}
	}
}
