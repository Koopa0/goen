//go:build integration

package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/ordernotice"
)

func TestTerminalWorkerSkipsAnAddressErasedBeforeDelivery(t *testing.T) {
	ctx := t.Context()
	var method, version, id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO shipping_methods(code) VALUES($1) RETURNING id`, "notice_"+uuid.NewString()[:8]).Scan(&method); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO shipping_method_versions(method_id,name,fee_cents) VALUES($1,'Delivery',0) RETURNING id`, method).Scan(&version); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := tx.QueryRow(ctx, `INSERT INTO orders(order_number,shipping_version_id,shipping_method_code,shipping_method_name) SELECT next_order_number(),$1,code,'Delivery' FROM shipping_methods WHERE id=$2 RETURNING id`, version, method).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_private_data(order_id,email,recipient_name,phone,postal_code,city,district,street) VALUES($1,'reader@example.com','Reader','0912345678','110','City','District','Street')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_lines(order_id,sku,product_name,unit_price_cents,quantity) VALUES($1,'NOTICE-TEST','Notice product',50000,1)`, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	storePool, err := openPool(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer storePool.Close()
	sender := &countingSender{}
	deliver := terminalOrderHandler(db.New(storePool), email.New(sender, "https://goen.test", "", ""))
	msg := &ordernotice.Message{OrderID: id, Kind: ordernotice.CancelledByCustomer}
	if err := deliver(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if sender.sent != 1 {
		t.Fatalf("active recipient received %d messages", sender.sent)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_private_data SET email=NULL,recipient_name=NULL,phone=NULL,postal_code=NULL,city=NULL,district=NULL,street=NULL,erased_at=now() WHERE order_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := deliver(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := deliver(ctx, &ordernotice.Message{OrderID: uuid.New(), Kind: ordernotice.Delivered}); err != nil {
		t.Fatal(err)
	}
	if sender.sent != 1 {
		t.Fatal("worker restored an erased or missing recipient")
	}
}
