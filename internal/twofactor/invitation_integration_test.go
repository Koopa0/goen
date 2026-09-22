//go:build integration

package twofactor_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/twofactor"
)

func TestStaffInvitationCommitsWithANewGrantWithoutRecipientPII(t *testing.T) {
	actor, _ := staff(t)
	s := twofactor.NewStore(twofactorRolePool(t, "staff-invitation", "admin"), testKey)
	address := "invitation-" + uuid.NewString() + "@example.com"
	if _, err := s.AddStaff(t.Context(), address, "Invited colleague", "staff", actor); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM users WHERE email=$1`, address).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := pool.QueryRow(t.Context(), `SELECT payload FROM outbox_messages WHERE topic=$1 AND dedupe_key=$2`, outbox.TopicStaffInvitation, "staff-invite:"+id.String()).Scan(&payload); err != nil {
		t.Fatalf("successful staff grant queued no invitation: %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["user_id"] != id.String() || fields["locale"] == "" || strings.Contains(string(payload), address) || strings.Contains(string(payload), "Invited colleague") {
		t.Fatalf("invitation payload retains unexpected data: %s", payload)
	}
	if _, err := s.AddStaff(t.Context(), address, "Duplicate", "staff", actor); !errors.Is(err, twofactor.ErrAlreadyStaff) {
		t.Fatalf("duplicate add=%v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1 AND dedupe_key=$2`, outbox.TopicStaffInvitation, "staff-invite:"+id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate grant queued %d invitations", count)
	}
	got, _, err := s.InvitationRecipient(t.Context(), id.String())
	if err != nil || got != address {
		t.Fatalf("active invite recipient=%q %v", got, err)
	}
	next := "changed-" + address
	if _, err := pool.Exec(t.Context(), `UPDATE users SET email=$2 WHERE id=$1`, id, next); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.InvitationRecipient(t.Context(), id.String())
	if err != nil || got != next {
		t.Fatalf("invite did not follow current account address: %q %v", got, err)
	}
	if err := s.RevokeStaff(t.Context(), id.String(), actor); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.InvitationRecipient(t.Context(), id.String())
	if err != nil || got != "" {
		t.Fatalf("revoked invite still has recipient=%q %v", got, err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.InvitationRecipient(t.Context(), id.String())
	if err != nil || got != "" {
		t.Fatalf("erased invite still has recipient=%q %v", got, err)
	}
}

func TestFailedStaffAuditLeavesNoInvitation(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	var before, after int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1`, outbox.TopicStaffInvitation).Scan(&before); err != nil {
		t.Fatal(err)
	}
	address := "rollback-invite-" + uuid.NewString() + "@example.com"
	if _, err := s.AddStaff(t.Context(), address, "Uncommitted", "staff", uuid.NewString()); err == nil {
		t.Fatal("missing audit actor accepted")
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_messages WHERE topic=$1`, outbox.TopicStaffInvitation).Scan(&after); err != nil {
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
