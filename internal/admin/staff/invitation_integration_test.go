//go:build integration

package staff_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/outbox"
)

func TestStaffInvitationCommitsWithANewGrantWithoutRecipientPII(t *testing.T) {
	actor, _ := admintest.AdminUser(t, pool)
	s := staff.NewStore(rolePool(t, "staff-invitation", "admin"))
	address := "invitation-" + uuid.NewString() + "@example.com"
	if _, err := s.AddStaff(asActor(t.Context(), actor), address, "Invited colleague", "staff"); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM users WHERE email=$1`, address).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := pool.QueryRow(t.Context(), `SELECT payload FROM outbox_messages WHERE topic=$1 AND payload->>'user_id'=$2`, outbox.TopicStaffInvitation.Name(), id.String()).Scan(&payload); err != nil {
		t.Fatalf("successful staff grant queued no invitation: %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["user_id"] != id.String() || fields["locale"] == "" || strings.Contains(string(payload), address) || strings.Contains(string(payload), "Invited colleague") {
		t.Fatalf("invitation payload retains unexpected data: %s", payload)
	}
	if _, err := s.AddStaff(asActor(t.Context(), actor), address, "Duplicate", "staff"); !errors.Is(err, staff.ErrAlreadyStaff) {
		t.Fatalf("duplicate add=%v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND payload->>'user_id'=$2`, outbox.TopicStaffInvitation.Name(), id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate grant queued %d invitations", count)
	}
	got, _, recipientErr := s.InvitationRecipient(t.Context(), id.String())
	if recipientErr != nil || got != address {
		t.Fatalf("active invite recipient=%q %v", got, recipientErr)
	}
	next := "changed-" + address
	if _, err := pool.Exec(t.Context(), `UPDATE users SET email=$2 WHERE id=$1`, id, next); err != nil {
		t.Fatal(err)
	}
	got, _, recipientErr = s.InvitationRecipient(t.Context(), id.String())
	if recipientErr != nil || got != next {
		t.Fatalf("invite did not follow current account address: %q %v", got, recipientErr)
	}
	if err := s.RevokeStaff(asActor(t.Context(), actor), id.String()); err != nil {
		t.Fatal(err)
	}
	got, _, recipientErr = s.InvitationRecipient(t.Context(), id.String())
	if recipientErr != nil || got != "" {
		t.Fatalf("revoked invite still has recipient=%q %v", got, recipientErr)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	got, _, recipientErr = s.InvitationRecipient(t.Context(), id.String())
	if recipientErr != nil || got != "" {
		t.Fatalf("erased invite still has recipient=%q %v", got, recipientErr)
	}
}

func TestFailedStaffAuditLeavesNoInvitation(t *testing.T) {
	s := staff.NewStore(pool)
	var before, after int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1`, outbox.TopicStaffInvitation.Name()).Scan(&before); err != nil {
		t.Fatal(err)
	}
	address := "rollback-invite-" + uuid.NewString() + "@example.com"
	if _, err := s.AddStaff(asActor(t.Context(), uuid.NewString()), address, "Uncommitted", "staff"); err == nil {
		t.Fatal("missing audit actor accepted")
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1`, outbox.TopicStaffInvitation.Name()).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("failed staff grant left an invitation queued")
	}
	var users int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE email=$1`, address).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Fatal("failed staff grant left a user")
	}
}
