package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/outbox"
)

// The error of a refused verification request is logged by the handler. It names
// the user and never echoes the address that was submitted.
func TestVerificationRefusalCarriesTheUserNotTheAddress(t *testing.T) {
	t.Parallel()
	const typed = "jane.doe.private@@example.test"
	user := uuid.New()
	// A zero Store is enough: the address is judged before the database is.
	err := (&Store{}).queueVerification(t.Context(), user.String(), typed, outbox.AddressVerify{})
	if err == nil {
		t.Fatal("queueVerification: nil error for an unusable address")
	}
	if !strings.Contains(err.Error(), user.String()) {
		t.Errorf("error %q does not name the user", err)
	}
	for _, secret := range []string{typed, "jane.doe", "example.test"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q carries %q", err, secret)
		}
	}
}
