package pagestatus

import (
	"net/http"
	"testing"
)

func TestFrom(t *testing.T) {
	t.Parallel()
	if got := From(t.Context()); got != 0 {
		t.Errorf("From with no status = %d, want 0", got)
	}
	if got := From(With(t.Context(), http.StatusUnprocessableEntity)); got != http.StatusUnprocessableEntity {
		t.Errorf("From(With(422)) = %d, want 422", got)
	}
}
