//go:build integration

package payment_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/payment"
)

func TestExpiredPaymentTombstoneIsIdempotentAndNeverRegressesCapturedMoney(t *testing.T) {
	ctx := t.Context()

	t.Run("an uncertain committed open converges to one cancelled row", func(t *testing.T) {
		number, id := order(t, 99000)
		session := "cs_expired_existing_" + uuid.NewString()[:12]
		if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 99000); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		for range 2 {
			if _, err := pool.Exec(ctx,
				`SELECT record_expired_payment($1, $2, 99000)`, id, session); err != nil {
				t.Fatalf("record expired payment: %v", err)
			}
		}
		var rows int
		var status string
		if err := pool.QueryRow(ctx, `
			SELECT count(*), max(status) FROM payments WHERE provider_ref = $1`, session).
			Scan(&rows, &status); err != nil {
			t.Fatalf("read expired tombstone: %v", err)
		}
		if rows != 1 || status != "cancelled" {
			t.Errorf("expired tombstone = %d rows/status %q, want 1/cancelled", rows, status)
		}
	})

	t.Run("captured money is never downgraded", func(t *testing.T) {
		number, id := order(t, 99000)
		session := "cs_expired_captured_" + uuid.NewString()[:12]
		if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 99000); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`SELECT capture_payment($1, 99000, 'visa', '4242')`, session); err != nil {
			t.Fatalf("capture payment: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`SELECT record_expired_payment($1, $2, 99000)`, id, session); err != nil {
			t.Fatalf("record impossible late expiry: %v", err)
		}
		var status string
		var captured int64
		if err := pool.QueryRow(ctx, `
			SELECT status, captured_amount_cents FROM payments WHERE provider_ref = $1`, session).
			Scan(&status, &captured); err != nil {
			t.Fatalf("read captured payment after expiry record: %v", err)
		}
		if status != "succeeded" || captured != 99000 {
			t.Errorf("captured payment became %q/%d, want succeeded/99000", status, captured)
		}
	})
}

// TestAnExpiredRejectedSessionConsumesItsIdempotencyGeneration covers the
// distributed boundary where Stripe creates a Session but open_payment refuses
// it. Even if persisting the first expiry fails, a retry must observe Stripe's
// current expired state, cancel the local row it just recovered, and advance to
// a new idempotency generation rather than redirecting to the dead Session.
func TestAnExpiredRejectedSessionConsumesItsIdempotencyGeneration(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	const amount = int64(130000)
	number, orderID := holdableOrder(t, amount)
	hold(t, orderID, 0, 60*time.Minute, "generation:"+number)

	oldSession := "cs_generation_old_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, oldSession, amount); err != nil {
		t.Fatalf("open old payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, oldSession); err != nil {
		t.Fatalf("cancel old payment: %v", err)
	}

	firstSession := "cs_generation_first_" + uuid.NewString()[:12]
	secondSession := "cs_generation_second_" + uuid.NewString()[:12]
	eventID := "evt_generation_" + uuid.NewString()[:12]

	// Fail exactly the first cancelled INSERT made by record_expired_payment.
	// This models a database error after Stripe accepted the expiration; the
	// second request must still recover from Stripe's cached Session response.
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reject_expired_tombstone_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reject_expired_tombstone_insert_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.provider_ref = '%s' AND NEW.status = 'cancelled' THEN
				RAISE EXCEPTION 'forced expired tombstone failure'
					USING ERRCODE = 'check_violation',
					      CONSTRAINT = 'test_expired_tombstone_failure';
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON payments
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, firstSession, triggerName, functionName)); err != nil {
		t.Fatalf("install tombstone failure: %v", err)
	}
	rejectorInstalled := true
	dropRejector := func() {
		t.Helper()
		if !rejectorInstalled {
			return
		}
		if _, err := pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			`DROP TRIGGER %s ON payments; DROP FUNCTION %s()`, triggerName, functionName)); err != nil {
			t.Fatalf("remove tombstone failure: %v", err)
		}
		rejectorInstalled = false
	}
	defer dropRejector()

	type remoteSession struct {
		id     string
		status string
	}
	var remote struct {
		mu          sync.Mutex
		byKey       map[string]*remoteSession
		createKeys  []string
		markerAdded bool
		markerErr   error
	}
	remote.byKey = make(map[string]*remoteSession)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			key := r.Header.Get("Idempotency-Key")
			remote.mu.Lock()
			session := remote.byKey[key]
			if session == nil {
				id := firstSession
				if len(remote.byKey) > 0 {
					id = secondSession
				}
				session = &remoteSession{id: id, status: "open"}
				remote.byKey[key] = session
			}
			remote.createKeys = append(remote.createKeys, key)
			addMarker := !remote.markerAdded
			remote.markerAdded = true
			remote.mu.Unlock()

			if addMarker {
				_, markerErr := pool.Exec(r.Context(), `
					INSERT INTO payment_webhook_events
						(provider, event_id, type, object_ref, payload, processed_at, unreconciled)
					VALUES ('stripe', $1, 'checkout.session.completed', $2,
					        '{}'::jsonb, now(), 'unreadable_event: generation test')`,
					eventID, oldSession)
				remote.mu.Lock()
				remote.markerErr = markerErr
				remote.mu.Unlock()
				if markerErr != nil {
					http.Error(w, markerErr.Error(), http.StatusInternalServerError)
					return
				}
			}
			// Stripe replays the original create body for the same key, even
			// after the underlying resource has become expired.
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`,
				session.id, session.id)

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/expire"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/"), "/expire")
			remote.mu.Lock()
			knownID := ""
			for _, session := range remote.byKey {
				if session.id == id {
					session.status = "expired"
					knownID = session.id
				}
			}
			remote.mu.Unlock()
			if knownID == "" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": knownID, "object": "checkout.session", "status": "expired",
			})

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")
			remote.mu.Lock()
			var status string
			knownID := ""
			for _, session := range remote.byKey {
				if session.id == id {
					status = session.status
					knownID = session.id
					break
				}
			}
			remote.mu.Unlock()
			if status == "" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": knownID, "object": "checkout.session", "status": status,
				"url": "https://checkout.stripe.com/c/pay/" + knownID,
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	gateway := gatewayAt(t, srv.URL)
	h := payment.NewHandler(s, gateway, access.NewStore(pool, false), slog.New(slog.DiscardHandler))
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		req.SetPathValue("number", number)
		placedBy(t, req, number)
		res := httptest.NewRecorder()
		h.Start(res, req)
		return res
	}

	if res := post(); res.Code != http.StatusInternalServerError {
		t.Fatalf("first Start status = %d, want 500 after tombstone persistence failed", res.Code)
	}
	remote.mu.Lock()
	markerErr := remote.markerErr
	firstKeys := append([]string(nil), remote.createKeys...)
	remote.mu.Unlock()
	if markerErr != nil {
		t.Fatalf("install concurrent reconciliation marker: %v", markerErr)
	}
	if diff := cmp.Diff([]string{payment.SessionKey(orderID, amount, 1)}, firstKeys); diff != "" {
		t.Errorf("first create keys (-want +got):\n%s", diff)
	}
	var firstRows int
	if countErr := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE provider_ref = $1`, firstSession).Scan(&firstRows); countErr != nil {
		t.Fatalf("count failed tombstone rows: %v", countErr)
	}
	if firstRows != 0 {
		t.Fatalf("failed tombstone left %d rows, want 0 for the recovery path", firstRows)
	}

	dropRejector()
	var reconciled bool
	if reconcileErr := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).Scan(&reconciled); reconcileErr != nil || !reconciled {
		t.Fatalf("reconcile generation marker = %v, %v; want true", reconciled, reconcileErr)
	}

	if res := post(); res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("recovered expired Start = %d %q, want 303 back to order",
			res.Code, res.Header().Get("Location"))
	}
	var recoveredStatus string
	if statusErr := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, firstSession).Scan(&recoveredStatus); statusErr != nil {
		t.Fatalf("read recovered expired payment: %v", statusErr)
	}
	if recoveredStatus != "cancelled" {
		t.Fatalf("recovered expired payment status = %q, want cancelled", recoveredStatus)
	}

	if res := post(); res.Code != http.StatusSeeOther ||
		!strings.HasPrefix(res.Header().Get("Location"), "https://checkout.stripe.com/") {
		t.Fatalf("replacement Start = %d %q, want 303 to a fresh Stripe Session",
			res.Code, res.Header().Get("Location"))
	}
	remote.mu.Lock()
	keys := append([]string(nil), remote.createKeys...)
	remote.mu.Unlock()
	wantKeys := []string{
		payment.SessionKey(orderID, amount, 1),
		payment.SessionKey(orderID, amount, 1),
		payment.SessionKey(orderID, amount, 2),
	}
	if diff := cmp.Diff(wantKeys, keys); diff != "" {
		t.Errorf("create idempotency generations (-want +got):\n%s", diff)
	}

	var statuses []string
	rows, err := pool.Query(ctx,
		`SELECT status FROM payments WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatalf("read payment generations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatalf("scan payment generation: %v", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk payment generations: %v", err)
	}
	if diff := cmp.Diff([]string{"cancelled", "cancelled", "requires_payment"}, statuses); diff != "" {
		t.Errorf("payment generations (-want +got):\n%s", diff)
	}
}

// TestObsoleteSessionCleanupConvergesAfterALocalWriteFailure proves that a
// successful provider expiry is not followed by an endless sequence of
// "already expired" errors when the first local cancellation fails. The retry
// retrieves current provider state, records cancelled, and only then opens the
// replacement amount.
func TestObsoleteSessionCleanupConvergesAfterALocalWriteFailure(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := holdableOrder(t, 100000)
	hold(t, orderID, 0, 60*time.Minute, "obsolete-cleanup:"+number)
	oldSession := "cs_obsolete_cleanup_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, oldSession, 100000); err != nil {
		t.Fatalf("open old amount: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET shipping_cents = 30000 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("change amount owed: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reject_expired_update_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reject_expired_update_once_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.provider_ref = '%s' AND NEW.status = 'cancelled' THEN
				RAISE EXCEPTION 'forced local cancellation failure'
					USING ERRCODE = 'check_violation',
					      CONSTRAINT = 'test_local_cancellation_failure';
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE UPDATE ON payments
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, oldSession, triggerName, functionName)); err != nil {
		t.Fatalf("install local cancellation failure: %v", err)
	}
	rejectorInstalled := true
	dropRejector := func() {
		t.Helper()
		if !rejectorInstalled {
			return
		}
		if _, err := pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			`DROP TRIGGER %s ON payments; DROP FUNCTION %s()`, triggerName, functionName)); err != nil {
			t.Fatalf("remove local cancellation failure: %v", err)
		}
		rejectorInstalled = false
	}
	defer dropRejector()

	replacement := "cs_obsolete_replacement_" + uuid.NewString()[:12]
	var remote struct {
		mu          sync.Mutex
		oldStatus   string
		expireCalls int
		createKeys  []string
	}
	remote.oldStatus = "open"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+oldSession:
			remote.mu.Lock()
			status := remote.oldStatus
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":%q,`+
				`"url":"https://checkout.stripe.com/c/pay/%s"}`, oldSession, status, oldSession)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/"+oldSession+"/expire":
			remote.mu.Lock()
			remote.oldStatus = "expired"
			remote.expireCalls++
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"expired"}`, oldSession)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			remote.mu.Lock()
			remote.createKeys = append(remote.createKeys, r.Header.Get("Idempotency-Key"))
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+replacement:
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	gateway := gatewayAt(t, srv.URL)
	h := payment.NewHandler(s, gateway, access.NewStore(pool, false), slog.New(slog.DiscardHandler))
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		req.SetPathValue("number", number)
		placedBy(t, req, number)
		res := httptest.NewRecorder()
		h.Start(res, req)
		return res
	}

	if res := post(); res.Code != http.StatusInternalServerError {
		t.Fatalf("first obsolete cleanup status = %d, want 500", res.Code)
	}
	dropRejector()
	if res := post(); res.Code != http.StatusSeeOther ||
		!strings.HasPrefix(res.Header().Get("Location"), "https://checkout.stripe.com/") {
		t.Fatalf("retry after local cleanup failure = %d %q, want fresh checkout",
			res.Code, res.Header().Get("Location"))
	}

	remote.mu.Lock()
	expireCalls := remote.expireCalls
	createKeys := append([]string(nil), remote.createKeys...)
	remote.mu.Unlock()
	if expireCalls != 1 {
		t.Errorf("Stripe expire calls = %d, want 1; retry should consume known expired state", expireCalls)
	}
	if diff := cmp.Diff([]string{payment.SessionKey(orderID, 130000, 1)}, createKeys); diff != "" {
		t.Errorf("replacement create keys (-want +got):\n%s", diff)
	}

	var statuses []string
	rows, err := pool.Query(ctx,
		`SELECT status FROM payments WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		t.Fatalf("read obsolete cleanup attempts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatalf("scan obsolete cleanup attempt: %v", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk obsolete cleanup attempts: %v", err)
	}
	if diff := cmp.Diff([]string{"cancelled", "requires_payment"}, statuses); diff != "" {
		t.Errorf("obsolete cleanup statuses (-want +got):\n%s", diff)
	}
}

// TestObsoleteSessionCleanupSurvivesAClientDisconnect holds the local record of
// a retirement against a browser that leaves while Stripe is closing the
// session. Stripe has already expired it by then, so a cancelled local write
// leaves goen offering a checkout that can never take money.
func TestObsoleteSessionCleanupSurvivesAClientDisconnect(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := holdableOrder(t, 100000)
	hold(t, orderID, 0, 60*time.Minute, "obsolete-disconnect:"+number)
	oldSession := "cs_obsolete_disconnect_" + uuid.NewString()[:12]
	if err := s.OpenPayment(ctx, number, oldSession, 100000); err != nil {
		t.Fatalf("open old amount: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET shipping_cents = 30000 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("change amount owed: %v", err)
	}

	// The disconnect lands after Stripe has applied the expiry and before its
	// reply is written, which is the only window in which the two records can
	// disagree.
	atExpire := make(chan struct{})
	proceed := make(chan struct{})
	var remote struct {
		mu          sync.Mutex
		status      string
		expireCalls int
	}
	remote.status = "open"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+oldSession:
			remote.mu.Lock()
			status := remote.status
			remote.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":%q,`+
				`"url":"https://checkout.stripe.com/c/pay/%s"}`, oldSession, status, oldSession)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/"+oldSession+"/expire":
			remote.mu.Lock()
			remote.status = "expired"
			remote.expireCalls++
			remote.mu.Unlock()
			atExpire <- struct{}{}
			<-proceed
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"expired"}`, oldSession)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	gateway := gatewayAt(t, srv.URL)

	h := payment.NewHandler(s, gateway, access.NewStore(pool, false), slog.New(slog.DiscardHandler))
	reqCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	req := httptest.NewRequestWithContext(reqCtx, http.MethodPost,
		"/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	placedBy(t, req, number)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		h.Start(httptest.NewRecorder(), req)
	}()

	<-atExpire
	disconnect()
	close(proceed)
	<-returned

	remote.mu.Lock()
	expireCalls := remote.expireCalls
	remote.mu.Unlock()
	if expireCalls != 1 {
		t.Fatalf("Stripe expire calls = %d, want 1", expireCalls)
	}

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, oldSession).Scan(&status); err != nil {
		t.Fatalf("read the retired attempt: %v", err)
	}
	if status != "cancelled" {
		t.Errorf("retired attempt status = %q, want %q; Stripe has already expired the session",
			status, "cancelled")
	}
}
