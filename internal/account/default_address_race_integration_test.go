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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/user"
)

func TestConcurrentDefaultAddressSelectionsBothSave(t *testing.T) {
	runDefaultAddressRace(t, addressSelection, addressSelection)
}

func TestConcurrentDefaultAddressAdditionAndSelectionBothSave(t *testing.T) {
	runDefaultAddressRace(t, addressAddition, addressSelection)
}

func TestConcurrentDefaultAddressAdditionsBothSave(t *testing.T) {
	runDefaultAddressRace(t, addressAddition, addressAddition)
}

type addressWrite string

const (
	addressSelection addressWrite = "selection"
	addressAddition  addressWrite = "addition"
)

func runDefaultAddressRace(t *testing.T, firstKind, secondKind addressWrite) {
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
	if secondKind == addressSelection {
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
	// A selection holds the account lock before its address UPDATE; an addition
	// waits at the held account row before clearing any default.
	waitForDefaultAddressBlock(t, ctx, prefix+"-first", []int{firstPID}, firstDone)
	var firstWriterPID int
	if err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE application_name=$1`, prefix+"-first").Scan(&firstWriterPID); err != nil {
		t.Fatal(err)
	}
	secondDone := startDefaultAddressRequest(ctx, &workers, secondHandler, u, secondKind, secondID, "second added")
	// The serialized writer waits on the first writer or the held account.
	// The address and unique-entry blockers retain the failing interleaving
	// when the account lock is removed for the mutation proof.
	waitForDefaultAddressBlock(t, ctx, prefix+"-second", []int{firstPID, firstWriterPID, secondPID}, secondDone)
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
	if firstKind == addressAddition {
		want = append([]savedAddress{{Label: "first added"}}, want...)
	}
	if secondKind == addressAddition {
		want = []savedAddress{{Label: "first added"}, {Label: "first saved"}, {Label: "second added", Default: true}, {Label: "second saved"}}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("saved address book (-want +got):\n%s", diff)
	}
	if n := countDefaults(t, u.ID); n != 1 {
		t.Errorf("final defaults = %d, want one", n)
	}
}

func holdDefaultAddressWriter(t *testing.T, ctx context.Context, tx pgx.Tx, userID, addressID string, kind addressWrite) {
	t.Helper()
	var err error
	switch kind {
	case addressSelection:
		_, err = tx.Exec(ctx, `SELECT id FROM addresses WHERE id=$1 FOR UPDATE`, addressID)
	case addressAddition:
		_, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID)
	default:
		panic("account: unknown address write: " + string(kind))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func startDefaultAddressRequest(ctx context.Context, workers *sync.WaitGroup, h *account.Handler, u user.User, kind addressWrite, addressID, label string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	workers.Go(func() {
		form := url.Values{"address": {addressID}}
		action := h.MakeDefaultAddress
		if kind == addressAddition {
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

func waitForDefaultAddressBlock(t *testing.T, ctx context.Context, application string, blockers []int, done <-chan *httptest.ResponseRecorder) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND pg_blocking_pids(pid) && $2::integer[])`, application, blockers).Scan(&blocked)
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

func TestDefaultAddressWritesRefuseMissingAccounts(t *testing.T) {
	s := account.NewStore(pool)
	a := &account.Address{Name: "Recipient", Phone: "0912345678", PostalCode: "110", City: "Taipei", District: "Xinyi", Street: "1 Test Road", Default: true}
	err := s.AddAddress(t.Context(), uuid.NewString(), a)
	if err == nil || errors.Is(err, account.ErrNotFound) || errors.Is(err, account.ErrInvalidInput) {
		t.Errorf("AddAddress(missing account) = %v, want non-refusal storage error", err)
	}
	if err := s.MakeDefaultAddress(t.Context(), uuid.NewString(), uuid.NewString()); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("MakeDefaultAddress(missing account) = %v, want not found", err)
	}
}
