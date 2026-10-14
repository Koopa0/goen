package newsletter_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/newsletter"
)

// The error of a refused request is logged by the handler. It names the step
// and never echoes the address the visitor typed, which may be a real one with
// a slip in it.
func TestRequestRefusalDoesNotCarryTheAddress(t *testing.T) {
	t.Parallel()
	const typed = "jane.doe.private@@example.test"
	// A zero Store is enough: the address is judged before the database is.
	_, err := (&newsletter.Store{}).Request(t.Context(), typed)
	if err == nil {
		t.Fatal("Request: nil error for an unusable address")
	}
	if !strings.Contains(err.Error(), "not a usable address") {
		t.Errorf("error %q does not say why the request was refused", err)
	}
	for _, secret := range []string{typed, "jane.doe", "example.test"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q carries %q", err, secret)
		}
	}
}
