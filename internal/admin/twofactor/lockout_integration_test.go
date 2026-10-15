//go:build integration

package twofactor_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/admin/twofactor"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/user"
)

// wrongCode is six digits the authenticator is not showing at any step the
// skew window accepts.
func wrongCode(secret []byte) string {
	now := twofactor.StepAt(time.Now())
	for _, c := range []string{"000000", "111111", "222222", "333333"} {
		taken := false
		for step := now - twofactor.Skew - 1; step <= now+twofactor.Skew+1; step++ {
			taken = taken || twofactor.Code(secret, step) == c
		}
		if !taken {
			return c
		}
	}
	panic("four fixed codes all valid at once")
}

// postCode submits a code through the handler as the staff member and returns
// where it redirects.
func postCode(t *testing.T, h *twofactor.Handler, userID, address, code string) string {
	t.Helper()
	ctx := user.NewContext(t.Context(), user.User{ID: userID, Email: address, Role: user.RoleAdmin})
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/verify",
		strings.NewReader(url.Values{"code": {code}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	h.Verify(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("Verify = %d, want 303; body=%s", res.Code, res.Body.String())
	}
	return res.Header().Get("Location")
}

// factorAudit counts userID's audit rows of action.
func factorAudit(t *testing.T, userID string, action audit.Action) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit_events
		WHERE action = $1 AND entity_table = 'users' AND entity_id = $2 AND actor_user_id = $2`,
		string(action), uuid.MustParse(userID)).Scan(&n); err != nil {
		t.Fatalf("count %s rows: %v", action, err)
	}
	return n
}

// factorLetters is whether each warning queued to address, oldest first, was
// the locking one.
func factorLetters(t *testing.T, address string) []bool {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT (payload->>'locked')::boolean FROM outbox_messages
		WHERE topic = $1 AND payload->>'email' = $2 AND (payload->>'limit')::int = $3
		ORDER BY id`,
		outbox.TopicStaffFactorFailure.Name(), address, twofactor.MaxFailures)
	if err != nil {
		t.Fatalf("read warnings: %v", err)
	}
	defer rows.Close()
	var locked []bool
	for rows.Next() {
		var l bool
		if err := rows.Scan(&l); err != nil {
			t.Fatalf("scan warning: %v", err)
		}
		locked = append(locked, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read warnings: %v", err)
	}
	return locked
}

func sameLetters(got, want []bool) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestWrongCodesLockTheFactorAcrossARestart is the guess the in-memory
// throttle alone allowed: a burst, a restart that refills it, another burst.
// The count is in the database, so the restart does not reset it, and once it
// is reached the right code is refused too. It runs on the back office's own
// role, so a grant the charge needs and the owner has is missing here.
func TestWrongCodesLockTheFactorAcrossARestart(t *testing.T) {
	admin := admintest.AdminRolePool(t, pool)
	userID, address := admintest.AdminUser(t, pool)
	secret := enrol(t, twofactor.NewStore(admin, testKey, nil), userID, address)
	wrong := wrongCode(secret)
	quiet := slog.New(slog.DiscardHandler)

	before := twofactor.NewHandler(twofactor.NewStore(admin, testKey, nil), quiet)
	for i := 1; i < twofactor.MaxFailures; i++ {
		if got := postCode(t, before, userID, address, wrong); got != "/admin/verify?bad=1" {
			t.Fatalf("wrong code %d redirected to %q, want the wrong-code notice", i, got)
		}
	}

	// A new handler is a restart: its throttle is full again.
	after := twofactor.NewHandler(twofactor.NewStore(admin, testKey, nil), quiet)
	if got := postCode(t, after, userID, address, wrong); got != "/admin/verify?locked=1" {
		t.Fatalf("wrong code %d after a restart redirected to %q, want the lock notice", twofactor.MaxFailures, got)
	}
	right := twofactor.Code(secret, twofactor.StepAt(time.Now())+1)
	if got := postCode(t, after, userID, address, right); got != "/admin/verify?locked=1" {
		t.Fatalf("the right code on a locked factor redirected to %q, want the lock notice", got)
	}

	if n := factorAudit(t, userID, audit.ActionFailStaffFactor); n != twofactor.MaxFailures-1 {
		t.Errorf("%d staff.factor.fail rows, want %d", n, twofactor.MaxFailures-1)
	}
	if n := factorAudit(t, userID, audit.ActionLockStaffFactor); n != 1 {
		t.Errorf("%d staff.factor.lock rows, want 1", n)
	}
	// The first wrong code and the locking one; nothing for the refused right
	// code, which was never judged.
	if got := factorLetters(t, address); !sameLetters(got, []bool{false, true}) {
		t.Errorf("warnings queued (locked?) = %v, want [false true]", got)
	}
}

// TestAnAcceptedCodeClearsTheWrongOnes: the count is of wrong codes IN A ROW,
// so the holder's own typos never add up to a lock, and the next wrong code
// after a right one warns the account again.
func TestAnAcceptedCodeClearsTheWrongOnes(t *testing.T) {
	admin := admintest.AdminRolePool(t, pool)
	s := twofactor.NewStore(admin, testKey, nil)
	userID, address := admintest.AdminUser(t, pool)
	secret := enrol(t, s, userID, address)
	ctx := asActor(t.Context(), userID)
	wrong := wrongCode(secret)

	for i := 1; i < twofactor.MaxFailures; i++ {
		if err := s.Verify(ctx, userID, wrong); !errors.Is(err, twofactor.ErrBadCode) {
			t.Fatalf("wrong code %d = %v, want ErrBadCode", i, err)
		}
	}
	if err := s.Verify(ctx, userID, twofactor.Code(secret, twofactor.StepAt(time.Now())+1)); err != nil {
		t.Fatalf("the right code after %d wrong ones = %v, want accepted", twofactor.MaxFailures-1, err)
	}
	for i := 1; i < twofactor.MaxFailures; i++ {
		if err := s.Verify(ctx, userID, wrong); !errors.Is(err, twofactor.ErrBadCode) {
			t.Fatalf("wrong code %d after an accepted one = %v, want ErrBadCode: the accepted code did not clear the count", i, err)
		}
	}
	if got := factorLetters(t, address); !sameLetters(got, []bool{false, false}) {
		t.Errorf("warnings queued (locked?) = %v, want one per run of wrong codes, [false false]", got)
	}
}

// TestTheCapHoldsUnderConcurrentWrongCodes: the count is charged by the
// statement that reads the credential, so attempts racing past a count read
// separately cannot each be judged. Exactly MaxFailures codes are judged
// however many arrive at once.
func TestTheCapHoldsUnderConcurrentWrongCodes(t *testing.T) {
	cfg := pool.Config().Copy()
	cfg.MaxConns = 3 * twofactor.MaxFailures
	wide, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wide.Close)
	s := twofactor.NewStore(wide, testKey, nil)
	userID, address := admintest.AdminUser(t, pool)
	secret := enrol(t, s, userID, address)
	ctx := asActor(t.Context(), userID)
	wrong := wrongCode(secret)

	const racers = 3 * twofactor.MaxFailures
	errs := make(chan error, racers)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for range racers {
		done.Go(func() {
			start.Wait()
			errs <- s.Verify(ctx, userID, wrong)
		})
	}
	start.Done()
	done.Wait()
	close(errs)

	judged := 0
	for err := range errs {
		switch {
		case errors.Is(err, twofactor.ErrBadCode):
			judged++
		case errors.Is(err, twofactor.ErrLocked):
		default:
			t.Fatalf("a concurrent wrong code = %v, want ErrBadCode or ErrLocked", err)
		}
	}
	rows := factorAudit(t, userID, audit.ActionFailStaffFactor) + factorAudit(t, userID, audit.ActionLockStaffFactor)
	if judged != twofactor.MaxFailures-1 || rows != twofactor.MaxFailures {
		t.Errorf("%d of %d concurrent wrong codes judged wrong and %d recorded, want %d and %d",
			judged, racers, rows, twofactor.MaxFailures-1, twofactor.MaxFailures)
	}
}

// TestRemovingALockedFactorLetsItsHolderEnrolAgain is the recovery the lock
// notice names: another admin removes the factor at /admin/staff, and its
// holder enrols afresh with no count carried over.
func TestRemovingALockedFactorLetsItsHolderEnrolAgain(t *testing.T) {
	admin := admintest.AdminRolePool(t, pool)
	s := twofactor.NewStore(admin, testKey, nil)
	userID, address := admintest.AdminUser(t, pool)
	secret := enrol(t, s, userID, address)
	wrong := wrongCode(secret)
	for range twofactor.MaxFailures {
		_ = s.Verify(asActor(t.Context(), userID), userID, wrong)
	}
	if err := s.Verify(asActor(t.Context(), userID), userID, wrong); !errors.Is(err, twofactor.ErrLocked) {
		t.Fatalf("Verify after %d wrong codes = %v, want ErrLocked", twofactor.MaxFailures, err)
	}

	colleague, _ := admintest.AdminUser(t, pool)
	if err := staff.NewStore(admin).RemoveFactor(asActor(t.Context(), colleague), userID); err != nil {
		t.Fatalf("remove the locked factor: %v", err)
	}
	fresh := enrol(t, s, userID, address)
	if err := s.Verify(asActor(t.Context(), userID), userID, twofactor.Code(fresh, twofactor.StepAt(time.Now())+1)); err != nil {
		t.Errorf("Verify on the re-enrolled factor = %v, want accepted", err)
	}
}
