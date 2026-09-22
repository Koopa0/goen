package oracle

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type evidenceKind string

const (
	evidenceStart     evidenceKind = "start"
	evidenceAnchor    evidenceKind = "anchor"
	evidencePlacement evidenceKind = "placement"
	evidenceRejected  evidenceKind = "rejected"
	evidenceReplay    evidenceKind = "replay"
)

type checkoutEvidence struct {
	Kind        evidenceKind `json:"kind"`
	RunID       string       `json:"run_id"`
	StartedAt   time.Time    `json:"started_at"`
	Buyers      int          `json:"buyers"`
	Replays     int          `json:"replays"`
	Key         string       `json:"key"`
	Email       string       `json:"email"`
	Order       string       `json:"order"`
	VariantID   string       `json:"variant_id"`
	Quantity    int          `json:"quantity"`
	TotalCents  int          `json:"total_cents"`
	BodyHash    string       `json:"body_hash"`
	CookieHash  string       `json:"cookie_hash"`
	ReplayIndex int          `json:"replay_index"`
}

// StockRun contains the identities observed by one real HTTP checkout run.
type StockRun struct {
	start      checkoutEvidence
	anchor     checkoutEvidence
	placements []checkoutEvidence
	attempts   map[string]bool
	replays    map[int]bool
}

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,80}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var attemptPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
var orderPattern = regexp.MustCompile(`^GO-[0-9]{6}-[0-9]{6}$`)

// ReadStockRun rejects incomplete, mixed-run or identity-changing evidence.
func ReadStockRun(reader io.Reader, runID string) (StockRun, error) {
	run := StockRun{attempts: make(map[string]bool), replays: make(map[int]bool)}
	if !runIDPattern.MatchString(runID) {
		return run, errors.New("oracle: valid LOAD_RUN_ID is required")
	}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var event checkoutEvidence
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return run, fmt.Errorf("oracle: decode checkout evidence: %w", err)
		}
		if event.RunID != runID {
			return run, errors.New("oracle: checkout evidence belongs to another run")
		}
		if err := run.add(event); err != nil {
			return run, err
		}
	}
	if err := scanner.Err(); err != nil {
		return run, fmt.Errorf("oracle: read checkout evidence: %w", err)
	}
	if run.anchor.Order == "" || len(run.placements) < 2 || len(run.attempts) != run.start.Buyers {
		return run, errors.New("oracle: missing successful anchor, competing placement or buyer attempts")
	}
	if len(run.replays) != 6 {
		return run, fmt.Errorf("oracle: replay evidence count %d, want 6", len(run.replays))
	}
	return run, nil
}

func (run *StockRun) add(event checkoutEvidence) error {
	if event.Kind == evidenceStart {
		return run.begin(event)
	}
	if run.start.StartedAt.IsZero() {
		return errors.New("oracle: missing run start")
	}
	if err := event.validateIdentity(); err != nil {
		return err
	}
	switch event.Kind {
	case evidenceAnchor:
		return run.addAnchor(event)
	case evidencePlacement, evidenceRejected:
		return run.addBuyer(event)
	case evidenceReplay:
		return run.addReplay(event)
	default:
		return fmt.Errorf("oracle: unknown checkout evidence kind %q", event.Kind)
	}
}

func (run *StockRun) begin(event checkoutEvidence) error {
	if !run.start.StartedAt.IsZero() || event.StartedAt.IsZero() || event.Buyers < 2 || event.Buyers > 40 || event.Replays != 6 {
		return errors.New("oracle: invalid or repeated run start")
	}
	run.start = event
	return nil
}

func (run *StockRun) addAnchor(event checkoutEvidence) error {
	if run.anchor.Order != "" || event.Email != "load-"+event.RunID+"-replay@goen.invalid" {
		return errors.New("oracle: invalid or repeated anchor")
	}
	run.anchor = event
	run.placements = append(run.placements, event)
	return nil
}

func (run *StockRun) addBuyer(event checkoutEvidence) error {
	if run.anchor.Order == "" || run.attempts[event.Email] || !run.buyerEmail(event.Email) || event.Key == run.anchor.Key {
		return errors.New("oracle: invalid or repeated buyer")
	}
	run.attempts[event.Email] = true
	if event.Kind == evidencePlacement {
		run.placements = append(run.placements, event)
	}
	return nil
}

func (run *StockRun) addReplay(event checkoutEvidence) error {
	if !event.sameCheckout(run.anchor) || event.ReplayIndex < 0 || event.ReplayIndex >= 6 || run.replays[event.ReplayIndex] {
		return errors.New("oracle: replay changed checkout identity or repeated an index")
	}
	run.replays[event.ReplayIndex] = true
	return nil
}

func (run *StockRun) buyerEmail(email string) bool {
	for i := range run.start.Buyers {
		if email == fmt.Sprintf("load-%s-%d@goen.invalid", run.start.RunID, i) {
			return true
		}
	}
	return false
}

func (event checkoutEvidence) validateIdentity() error {
	if !attemptPattern.MatchString(event.Key) || !digestPattern.MatchString(event.BodyHash) || !digestPattern.MatchString(event.CookieHash) || event.VariantID != FlashSaleVariantID.String() || event.Quantity != 1 || event.TotalCents != 107000 {
		return errors.New("oracle: invalid checkout identity")
	}
	if event.Kind == evidenceRejected {
		if event.Order != "" {
			return errors.New("oracle: rejected checkout contains an order")
		}
	} else if !orderPattern.MatchString(event.Order) {
		return errors.New("oracle: checkout has no valid order number")
	}
	return nil
}

func (event checkoutEvidence) sameCheckout(other checkoutEvidence) bool {
	return event.Key == other.Key && event.Email == other.Email && event.Order == other.Order && event.BodyHash == other.BodyHash && event.CookieHash == other.CookieHash
}

// CheckStockRun ties each observed success to fresh database effects. Pending
// checkout must have exactly one hold and no provider or credit posting.
func CheckStockRun(ctx context.Context, pool *pgxpool.Pool, run StockRun) error {
	if pool == nil || len(run.placements) < 2 || len(run.replays) != 6 {
		return errors.New("oracle: complete stock run and database are required")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_private_data WHERE email LIKE $1`, "load-"+run.start.RunID+"-%@goen.invalid").Scan(&count); err != nil {
		return fmt.Errorf("oracle: count run orders: %w", err)
	}
	if count != len(run.placements) {
		return fmt.Errorf("oracle: run has %d orders, want %d observed placements", count, len(run.placements))
	}
	orders, keys := make(map[string]bool), make(map[string]bool)
	for _, event := range run.placements {
		if orders[event.Order] || keys[event.Key] {
			return errors.New("oracle: distinct placements reused an order or key")
		}
		orders[event.Order], keys[event.Key] = true, true
		if err := checkPlacement(ctx, pool, run.start.StartedAt, event); err != nil {
			return err
		}
	}
	return nil
}

func checkPlacement(ctx context.Context, pool *pgxpool.Pool, started time.Time, event checkoutEvidence) error {
	var valid bool
	err := pool.QueryRow(ctx, `SELECT
  o.placed_at >= $4 AND o.fulfillment_status = 'pending' AND o.currency = 'TWD'
  AND (SELECT count(*) FROM checkout_attempts a WHERE a.order_id = o.id) = 1
  AND EXISTS (SELECT 1 FROM checkout_attempts a WHERE a.order_id = o.id AND a.idempotency_key = $2 AND a.created_at >= $4)
  AND (SELECT count(*) FROM order_private_data d WHERE d.order_id = o.id AND d.email = $3) = 1
  AND (SELECT count(*) FROM order_lines l WHERE l.order_id = o.id) = 1
  AND EXISTS (SELECT 1 FROM order_lines l WHERE l.order_id = o.id AND l.sku = 'LOAD-FLASH-001' AND l.quantity = 1 AND l.unit_price_cents = 99000)
  AND (SELECT sum(l.unit_price_cents * l.quantity) FROM order_lines l WHERE l.order_id = o.id) + o.shipping_cents + o.tax_cents - o.discount_cents = 107000
  AND (SELECT count(*) FROM inventory_reservations r WHERE r.order_id = o.id) = 1
  AND EXISTS (SELECT 1 FROM inventory_reservations r WHERE r.order_id = o.id AND r.variant_id = $5 AND r.quantity = 1 AND r.state = 'held' AND r.expires_at > now())
  AND (SELECT count(*) FROM inventory_movements m WHERE m.source_type = 'order' AND m.source_id = o.id) = 1
  AND EXISTS (SELECT 1 FROM inventory_movements m WHERE m.source_type = 'order' AND m.source_id = o.id AND m.variant_id = $5 AND m.reason = 'hold' AND m.delta = -1)
  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id)
  AND NOT EXISTS (SELECT 1 FROM store_credit_entries c WHERE c.order_id = o.id)
 FROM orders o WHERE o.order_number = $1`, event.Order, event.Key, event.Email, started, FlashSaleVariantID).Scan(&valid)
	if err != nil {
		return fmt.Errorf("oracle: check order %s: %w", event.Order, err)
	}
	if !valid {
		return fmt.Errorf("oracle: order %s has stale, duplicate or incorrect checkout effects", event.Order)
	}
	return nil
}
