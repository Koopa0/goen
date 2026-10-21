//go:build integration

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/letter"
	"github.com/koopa0/goen/internal/order/notice"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping/storemap"
)

// capturingSender keeps the letters a worker sends.
type capturingSender struct{ sent []*letter.Message }

func (c *capturingSender) Send(_ context.Context, m *letter.Message) error {
	c.sent = append(c.sent, m)
	return nil
}

// The order.part_refunded letter is read at delivery on the store pool, from
// the order and the settled refund: the outbox row names neither address nor
// amount, so an erasure before delivery leaves nothing to send and erase_user
// needs no change.
func TestThePartRefundWorkerReadsTheLetterAtDelivery(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	o := admintest.PartlyShippedOrder(t, owner, 0)
	desk := refunds.NewStore(admintest.AdminRolePool(t, owner), admintest.Refunder{}, nil)
	if err := desk.RefundUnsent(ctx, o.Number, &refunds.UnsentRequest{
		Units: map[uuid.UUID]int32{o.LineB: 2}, Reason: string(returns.UnsentDamaged), Note: "外盒破損",
	}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	var payload []byte
	if err := owner.QueryRow(ctx, `SELECT payload FROM outbox_messages WHERE topic = $1`,
		outbox.TopicOrderPartRefunded.Name()).Scan(&payload); err != nil {
		t.Fatalf("read the queued letter: %v", err)
	}
	var m outbox.OrderPartRefunded
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatal(err)
	}

	storePool, err := openPool(ctx, owner.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatal(err)
	}
	defer storePool.Close()
	sender := &capturingSender{}
	deliver := partRefundHandler(notice.NewRecipients(storePool), letter.New(sender, "https://goen.test", "", ""))
	if err := deliver(ctx, &m); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("%d letters sent, want one", len(sender.sent))
	}
	body := sender.sent[0].Body
	for _, want := range []string{o.Number, "點線筆記本 × 2", "NT$926 已退還：購物金 NT$926"} {
		if !strings.Contains(body, want) {
			t.Errorf("letter lacks %q:\n%s", want, body)
		}
	}
	if !strings.HasPrefix(sender.sent[0].To, "unsent-") {
		t.Errorf("letter went to %q, want the order's address", sender.sent[0].To)
	}

	if _, err := owner.Exec(ctx, `UPDATE order_private_data SET email = NULL, recipient_name = NULL, phone = NULL,
		postal_code = NULL, city = NULL, district = NULL, street = NULL, erased_at = now() WHERE order_id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := deliver(ctx, &m); err != nil {
		t.Fatalf("deliver after erasure: %v — nothing to send is not a failure", err)
	}
	if len(sender.sent) != 1 {
		t.Error("the worker wrote to an erased recipient")
	}
}

// The refund door is behind goen's cross-site defence: a cross-site browser
// post is refused before anything reads the session, while the same post from
// the shop's own pages reaches the back office's guard.
func TestTheUnsentRefundDoorRefusesACrossSitePost(t *testing.T) {
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build disabled payment gateway: %v", err)
	}
	storeMap, err := storemap.New("1000001", string(storemap.ModeB2C), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: pool, Payments: gateway, BaseURL: "https://goen.test", StoreMap: storeMap},
		BackOffice: BackOfficeConfig{AdminPool: pool, Payments: gateway, Refunder: gateway, StoreMap: storeMap},
	}, slog.New(slog.DiscardHandler))
	for site, want := range map[string]int{"cross-site": http.StatusForbidden, "same-origin": http.StatusNotFound} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/admin/orders/GO-261015-ABCDE1/refund-unsent", strings.NewReader("reason=damaged"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", site)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != want {
			t.Errorf("a %s post to the unsent refund door = %d, want %d", site, res.Code, want)
		}
	}
}
