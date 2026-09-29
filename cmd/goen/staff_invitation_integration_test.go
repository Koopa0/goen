//go:build integration

package main

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/twofactor"
)

func TestStaffInvitationRegrantSurvivesRetainedDelivery(t *testing.T) {
	for _, revokeBeforeDelivery := range []bool{true, false} {
		name := "delivered-before-revoke"
		if revokeBeforeDelivery {
			name = "revoked-before-delivery"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			var actor uuid.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO users(email,role) VALUES($1,'admin') RETURNING id`, "actor-"+uuid.NewString()+"@example.com").Scan(&actor); err != nil {
				t.Fatal(err)
			}
			adminPool, err := openAdminPool(ctx, pool.Config().ConnString())
			if err != nil {
				t.Fatal(err)
			}
			defer adminPool.Close()
			storePool, err := openPool(ctx, pool.Config().ConnString())
			if err != nil {
				t.Fatal(err)
			}
			defer storePool.Close()
			staff := twofactor.NewStore(adminPool, nil)
			address := "regrant-" + uuid.NewString() + "@example.com"
			if _, err := staff.AddStaff(ctx, address, "Colleague", "staff", actor.String()); err != nil {
				t.Fatal(err)
			}
			var target uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, address).Scan(&target); err != nil {
				t.Fatal(err)
			}
			revoke := func() {
				t.Helper()
				if err := staff.RevokeStaff(ctx, target.String(), actor.String()); err != nil {
					t.Fatal(err)
				}
			}
			sender := &countingSender{}
			worker := outbox.NewStore(storePool, slog.Default())
			worker.HandleJSON[email.StaffInvitation](outbox.TopicStaffInvitation, staffInvitationHandler(staff, email.New(sender, "https://goen.test", "", "")))
			drain := func() {
				t.Helper()
				if _, _, err := worker.DrainAll(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if revokeBeforeDelivery {
				revoke()
			}
			drain()
			wantSent := 1
			if revokeBeforeDelivery {
				wantSent = 0
			}
			if sender.sent != wantSent {
				t.Fatalf("first invitation sent=%d, want %d", sender.sent, wantSent)
			}
			var retained int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'user_id'=$2 AND delivered_at IS NOT NULL`, outbox.TopicStaffInvitation, target.String()).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if retained != 1 {
				t.Fatalf("worker retained %d consumed invitations, want 1", retained)
			}
			if !revokeBeforeDelivery {
				revoke()
			}
			if _, err := staff.AddStaff(ctx, address, "Colleague", "staff", actor.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := staff.AddStaff(ctx, address, "Duplicate", "staff", actor.String()); !errors.Is(err, twofactor.ErrAlreadyStaff) {
				t.Fatalf("active staff duplicate=%v", err)
			}
			var total int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'user_id'=$2`, outbox.TopicStaffInvitation, target.String()).Scan(&total); err != nil {
				t.Fatal(err)
			}
			if total != 2 {
				t.Fatalf("re-grant queued %d invitations, want 2", total)
			}
			drain()
			if sender.sent != wantSent+1 {
				t.Fatalf("re-grant sent=%d, want %d", sender.sent, wantSent+1)
			}
		})
	}
}
