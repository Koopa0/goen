//go:build integration

package admin_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
)

func TestTerminalRecipientNeverRecoversErasedAddress(t *testing.T) {
	_, id, _ := admintest.PendingOrderHoldingStock(t, pool)
	q := db.New(pool)
	if _, err := q.TerminalOrderRecipient(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE order_private_data SET email=NULL,recipient_name=NULL,phone=NULL,postal_code=NULL,city=NULL,district=NULL,street=NULL,erased_at=now() WHERE order_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.TerminalOrderRecipient(t.Context(), id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("erased recipient resolved: %v", err)
	}
}
